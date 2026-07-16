"""Batch derivation for the ADR-0029 plural sentiment system — appreciation footers, the
adult-only dissent line, the restorative concern ladder, and the moderator-facing sensors.

This lives OUTSIDE ``services.py`` on purpose: the per-request write path (``toggle_reaction`` /
``toggle_dissent`` / ``record_concern``) must stay lean and never derive an aggregate live (a live
flip is a small-roster timing leak — ADR-0029). Everything here runs from ``run_due_jobs`` via the
three thin management commands (``recompute_post_sentiment``, ``evaluate_concerns``,
``purge_stale_reaction_rows``) and produces NO count, percentage, who-list, or per-user reliability
history at any surface — only the denormalized, re-derivable ``PostSentimentFooter`` cache and the
incident-scoped ``ConcernReview`` queue.

Invariants upheld here (see ADR-0029 / CLAUDE.md):
  * no counts/identities ever leave this module — footers hold latched slugs + a boolean, reviews
    hold incident facts (post ids / window / set size), never flagger identities;
  * aggregates are RE-DERIVED from surviving rows each run, so a GDPR erasure (CASCADE) cascades
    honestly and a disappearance lands on a batch boundary (non-attributable);
  * no automated corrective delivery to a minor — TEEN concern only ever reaches a human queue,
    CHILD threads never accrue a footer or a concern at all;
  * every author-directed note is capped (≤1 per post lifetime, ≤1 per author / cooldown window)
    and suppressed while a protective pile-on review is open.
"""

from __future__ import annotations

import logging
from collections import defaultdict
from datetime import date, timedelta

from django.conf import settings
from django.db import transaction
from django.db.models import Count
from django.utils import timezone

from apps.accounts.models import Cohort
from apps.notifications.models import Notification
from apps.notifications.services import notify, notify_moderators
from apps.ops.models import JobMarker
from apps.safety.models import ConcernReview
from apps.safety.services import record_audit

from .models import (
    Group,
    Post,
    PostConcern,
    PostConcernState,
    PostDissent,
    PostReaction,
    PostSentimentFooter,
)
from .services import REACTION_FACETS, eligible_audience_count

logger = logging.getLogger("apps.social.sentiment")

# Verbatim restorative copy (ADR-0029 — do NOT paraphrase). The single formative note an adult
# author may receive once per post; a friendly heads-up, never an allegation.
FORMATIVE_NOTE_TITLE = "A quiet note about one of your posts"
FORMATIVE_NOTE_BODY = (
    "A few members felt one of your recent posts didn't quite fit the spirit of this group. "
    "No one has reported anything, nothing is hidden, and no names or numbers are attached — "
    "this is just a friendly heads-up so you can take another look if you'd like. If you edit "
    "the post, this note won't come back for it. You're a valued part of this group; this is "
    "about one post, not about you."
)


# --- cadence gates ---------------------------------------------------------------------------


def _claim_daily(name, *, now, min_interval=timedelta(hours=24)) -> bool:
    """Self-gate a daily/periodic job on its JobMarker: return True (and stamp ``last_run_at=now``)
    only when at least ``min_interval`` has passed since the last run — otherwise False.
    ``run_due_jobs`` ticks with no per-job cadence, so this is how a daily job avoids doing real
    work every tick. The read+stamp is serialized under ``select_for_update`` so two concurrent
    ticks can't both claim.

    Daily markers deliberately CLAIM-BEFORE-WORK (stamp up front): the stamp's job is to prevent
    two overlapping ticks doing the same daily work. That is safe now that every per-post body is
    wrapped in its own try/except (see the jobs below), so a single bad post can no longer abort a
    whole run and strand the day's work behind an already-claimed marker — a full-run abort is
    effectively impossible."""
    with transaction.atomic():
        marker, _created = JobMarker.objects.select_for_update().get_or_create(name=name)
        if marker.last_run_at is not None and now - marker.last_run_at < min_interval:
            return False
        marker.last_run_at = now
        marker.save(update_fields=["last_run_at"])
        return True


def _weekly_due(name, *, now) -> bool:
    """Whether the weekly dissent step is due — the current ISO week differs from the week of the
    last STAMPED run — WITHOUT stamping. The marker read is serialized under ``select_for_update``.

    Unlike the daily claim, the weekly stamp lands AFTER the loop finishes (see
    ``recompute_post_sentiment``), so a mid-loop crash leaves the marker un-advanced and the step
    re-runs next tick, resuming where it left off (each post is skipped once its footer's
    ``dissent_window_key`` already equals the current week). The residual race — two ticks both
    seeing the step due before either stamps — is harmless precisely because of that per-post
    window_key skip: whichever tick processes a post first stamps its window_key, and the other
    skips it."""
    week = _iso_week_key(now)
    with transaction.atomic():
        marker, _created = JobMarker.objects.select_for_update().get_or_create(name=name)
        return marker.last_run_at is None or _iso_week_key(marker.last_run_at) != week


def _stamp_weekly(name, *, now) -> None:
    """Stamp the weekly marker AFTER the dissent loop has completed (crash-resume: the marker only
    advances once the whole week's work is done)."""
    with transaction.atomic():
        marker, _created = JobMarker.objects.select_for_update().get_or_create(name=name)
        marker.last_run_at = now
        marker.save(update_fields=["last_run_at"])


def _iso_week_key(dt) -> str:
    iso = dt.isocalendar()
    return f"{iso[0]}-W{iso[1]:02d}"


def _thread_url(owner_obj) -> str:
    """The web thread URL a notification links to (mirrors the announcement fan-out)."""
    if isinstance(owner_obj, Group):
        return f"/groups/{owner_obj.id}/"
    return f"/activities/{owner_obj.id}/"


def _candidate_posts():
    """Posts that could carry a footer this run: any with a reaction/dissent row OR an existing
    footer (so a lapsed aggregate can be re-derived down to silence). Owner objects are prefetched
    so the per-post cohort/audience reads don't re-query the thread owner. Hidden posts are
    EXCLUDED — a moderator REMOVE sets ``is_hidden`` without bumping ``updated_at``, so a hidden
    post would otherwise keep accruing/latching a footer even though the render already refuses to
    show it (ADR-0029): don't do the work, and don't latch, on a hidden post."""
    ids = set(PostReaction.objects.values_list("post_id", flat=True))
    ids |= set(PostDissent.objects.values_list("post_id", flat=True))
    ids |= set(PostSentimentFooter.objects.values_list("post_id", flat=True))
    if not ids:
        return []
    return list(
        Post.objects.filter(id__in=ids, is_hidden=False).select_related(
            "thread__activity", "thread__group"
        )
    )


def _audience(owner_obj, cache) -> int:
    """Memoized ``eligible_audience_count`` for one batch run: several posts share a thread owner,
    so the (guardian- and blocked-vs-owner-excluding) audience is counted once per owner per run,
    keyed by (owner type, owner id)."""
    key = (type(owner_obj).__name__, owner_obj.id)
    if key not in cache:
        cache[key] = eligible_audience_count(owner_obj)
    return cache[key]


# --- job 1: recompute_post_sentiment (daily appreciation + weekly dissent) --------------------


def recompute_post_sentiment(now=None) -> dict:
    """Re-derive every post's appreciation footer from surviving reaction rows (daily), and advance
    the adult-only dissent window (weekly). Idempotent and self-gated: a second run the same day is
    a no-op for the daily step; the weekly step advances only on an ISO-week change."""
    now = now or timezone.now()
    summary = {"skipped": False, "latched": 0, "unlatched": 0, "permanent": 0, "dissent": None}
    if not _claim_daily("post_sentiment_daily", now=now):
        summary["skipped"] = True
        return summary

    posts = _candidate_posts()
    ids = [p.id for p in posts]
    facet_counts = _facet_reactor_counts(ids)
    audience_cache: dict = {}  # (owner type, owner id) -> eligible audience, per-run memo (R5)

    latched = unlatched = permanent = 0
    for post in posts:
        # Per-post isolation: one malformed post/owner must never abort the whole run and strand
        # the rest of the day's footers (mirrors run_reverify_sweep).
        try:
            owner_obj = post.thread.owner_object
            if owner_obj is None or getattr(owner_obj, "is_hidden", False):
                continue
            cohort = getattr(owner_obj, "cohort", None)
            if cohort == Cohort.CHILD or cohort not in (Cohort.ADULT, Cohort.TEEN):
                # CHILD threads never carry a footer (inv.3); an UNASSIGNED thread has no cohort k.
                # Delete any stray footer so a mis-created row can't linger and leak.
                PostSentimentFooter.objects.filter(post=post).delete()
                continue
            k = settings.SENTIMENT_K_ADULT if cohort == Cohort.ADULT else settings.SENTIMENT_K_TEEN
            audience = _audience(owner_obj, audience_cache)
            footer, _created = PostSentimentFooter.objects.get_or_create(post=post)
            d_latched, d_unlatched, d_permanent = _recompute_appreciation(
                footer, facet_counts.get(post.id, {}), k=k, audience=audience, now=now
            )
            latched += d_latched
            unlatched += d_unlatched
            permanent += d_permanent
        except Exception:  # noqa: BLE001 — one bad post must not starve the rest of the run
            logger.exception("recompute_post_sentiment: skipping post %s after an error", post.id)

    summary.update(latched=latched, unlatched=unlatched, permanent=permanent)

    if _weekly_due("post_sentiment_weekly", now=now):
        # Due-check first WITHOUT stamping; the marker advances only after the loop completes so a
        # mid-week crash resumes (per-post window_key skip inside makes the re-run idempotent).
        summary["dissent"] = _recompute_dissent_window(
            posts, now=now, audience_cache=audience_cache
        )
        _stamp_weekly("post_sentiment_weekly", now=now)

    record_audit(
        "post_sentiment.recomputed",
        latched=latched,
        unlatched=unlatched,
        permanent=permanent,
        posts=len(posts),
    )
    return summary


def _facet_reactor_counts(post_ids) -> dict:
    """{post_id: {facet_slug: distinct_reactor_count}} in one query. The (post,user,emoji) unique
    constraint means one row per (user, facet), so a row count IS the distinct-reactor count."""
    out: dict = defaultdict(dict)
    if not post_ids:
        return out
    rows = (
        PostReaction.objects.filter(post_id__in=post_ids)
        .values("post_id", "emoji")
        .annotate(n=Count("user", distinct=True))
    )
    for row in rows:
        out[row["post_id"]][row["emoji"]] = row["n"]
    return out


def _recompute_appreciation(footer, counts, *, k, audience, now) -> tuple[int, int, int]:
    """Re-derive one footer's latched appreciation slugs from the surviving per-facet counts.

    Latch a slug when ``count >= k AND audience >= 2k``; keep a latched slug while ``count >= k``
    (maintenance needs no audience re-check); UNLATCH (erasure honesty) when it drops below k
    unless it has graduated to permanent; graduate a slug held continuously past
    ``REACTION_ROW_RETENTION_DAYS`` into ``appreciation_permanent`` (kept even after the rows
    purge). ``appreciation_slugs`` stores ``[slug, "YYYY-MM-DD"]`` pairs (latched-since);
    ``appreciation_permanent`` a flat slug list."""
    retention = settings.REACTION_ROW_RETENTION_DAYS
    today = now.date().isoformat()
    existing = {pair[0]: pair[1] for pair in (footer.appreciation_slugs or []) if pair}
    permanent = set(footer.appreciation_permanent or [])

    d_latched = d_unlatched = d_permanent = 0
    new_pairs: dict = {}
    for slug in REACTION_FACETS:  # fixed catalog order — never popularity-sorted
        count = counts.get(slug, 0)
        was_latched = slug in existing
        if was_latched:
            if count >= k:  # maintenance floor: still meets threshold -> stays latched
                new_pairs[slug] = existing[slug]
            elif slug not in permanent:
                # Surviving rows fell below k. Normally an honest disappearance — BUT if the slug
                # has been latched continuously past the retention window, its supporting rows have
                # aged into the 90-day purge and the sentence is now a non-personal aggregate
                # (ADR-0029): GRADUATE it to permanent rather than unlatch, so a graduated
                # appreciation survives the purge instead of vanishing at the boundary. (An erasure
                # BEFORE 90d still drops below k while young, so it unlatches honestly.)
                try:
                    since_date = date.fromisoformat(existing[slug])
                except (TypeError, ValueError):
                    since_date = now.date()
                if (now.date() - since_date).days >= retention:
                    permanent.add(slug)
                    d_permanent += 1
                else:
                    d_unlatched += 1
            # (a permanent slug is kept regardless; it isn't re-latched here — it lives in perm.)
        elif count >= k and audience >= 2 * k:
            new_pairs[slug] = today
            d_latched += 1

    # Graduate slugs held continuously past the retention window into permanent.
    for slug, since in list(new_pairs.items()):
        try:
            since_date = date.fromisoformat(since)
        except (TypeError, ValueError):
            since_date = now.date()
            new_pairs[slug] = today
        if (now.date() - since_date).days >= retention and slug not in permanent:
            permanent.add(slug)
            del new_pairs[slug]
            d_permanent += 1

    footer.appreciation_slugs = [[slug, since] for slug, since in new_pairs.items()]
    footer.appreciation_permanent = sorted(permanent)
    footer.save(update_fields=["appreciation_slugs", "appreciation_permanent", "computed_at"])
    return d_latched, d_unlatched, d_permanent


def _recompute_dissent_window(posts, *, now, audience_cache=None) -> dict:
    """The WEEKLY dissent step (ADR-0029 rung 1): for ADULT threads only, non-announcement posts,
    advance the sustained-window latch/lapse of the "Some see this differently." line. Never for
    TEEN/CHILD (their tallies feed only the sensors), never on an announcement.

    PER-POST IDEMPOTENT + crash-resumable: a post whose footer already carries this ISO week's
    ``dissent_window_key`` is skipped, so a re-run after a mid-loop crash (or a harmless double
    tick) never advances the same post's window twice."""
    to_latch = settings.DISSENT_WINDOWS_TO_LATCH
    to_lapse = settings.DISSENT_WINDOWS_TO_LAPSE
    k = settings.DISSENT_K
    floor = settings.DISSENT_AUDIENCE_FLOOR
    week = _iso_week_key(now)
    if audience_cache is None:
        audience_cache = {}
    ids = [p.id for p in posts]
    dissent_counts = dict(
        PostDissent.objects.filter(post_id__in=ids)
        .values("post_id")
        .annotate(n=Count("user", distinct=True))
        .values_list("post_id", "n")
    )

    latched = lapsed = 0
    for post in posts:
        try:
            owner_obj = post.thread.owner_object
            if owner_obj is None or getattr(owner_obj, "is_hidden", False):
                continue
            if getattr(owner_obj, "cohort", None) != Cohort.ADULT or post.is_announcement:
                continue
            footer, _created = PostSentimentFooter.objects.get_or_create(post=post)
            if footer.dissent_window_key == week:
                continue  # already advanced this week — idempotent skip (crash-resume)
            audience = _audience(owner_obj, audience_cache)
            hit = dissent_counts.get(post.id, 0) >= k and audience >= floor
            if hit:
                footer.dissent_consecutive_hits += 1
                footer.dissent_consecutive_misses = 0
            else:
                footer.dissent_consecutive_misses += 1
                footer.dissent_consecutive_hits = 0
            was_active = footer.dissent_active
            if footer.dissent_consecutive_hits >= to_latch:
                footer.dissent_active = True
            elif footer.dissent_consecutive_misses >= to_lapse:
                footer.dissent_active = False
            if footer.dissent_active and not was_active:
                latched += 1
            elif was_active and not footer.dissent_active:
                lapsed += 1
            footer.dissent_window_key = week
            footer.save(
                update_fields=[
                    "dissent_active",
                    "dissent_consecutive_hits",
                    "dissent_consecutive_misses",
                    "dissent_window_key",
                    "computed_at",
                ]
            )
        except Exception:  # noqa: BLE001 — one bad post must not starve the weekly step
            logger.exception("dissent_window: skipping post %s after an error", post.id)
    return {"week": week, "latched": latched, "lapsed": lapsed}


# --- job 2: evaluate_concerns (restorative ladder + sensors) ----------------------------------


def evaluate_concerns(now=None) -> dict:
    """The daily conduct-concern ladder + the anti-bully sensors (ADR-0029 rung 2 / sensor
    inversion). NEVER auto-delivers to a minor; caps the adult formative note; converts flagger
    coordination into a moderator-facing detection, not a delivery. Self-gated daily."""
    now = now or timezone.now()
    summary = {
        "skipped": False,
        "notes": 0,
        "escalated": 0,
        "teen": 0,
        "coordinated": 0,
        "pileon": 0,
    }
    if not _claim_daily("concerns_daily", now=now):
        summary["skipped"] = True
        return summary

    window14 = now - timedelta(days=settings.FORMATIVE_NOTE_COOLDOWN_DAYS)
    downweighted = _downweighted_flaggers(window14)

    # Protective sensors run FIRST so a pile-on target is shielded from a note in the same pass.
    # NOTE: the sensors (pile-on / coordinated / down-weighting) read the raw concern & dissent
    # rows DIRECTLY, so they intentionally keep counting rows on hidden posts — bullying a hidden
    # post is still bullying, and coordinated-flag detection must see it. Only the author-directed
    # LADDER below skips hidden posts (a hidden post must never accrue or deliver a note).
    protected = _sensor_pileon(now=now)
    summary["pileon"] = protected["created"]
    protected_authors = protected["authors"]
    summary["coordinated"] = _sensor_coordinated(now=now)

    noted_this_run: set = set()
    audience_cache: dict = {}  # per-run eligible-audience memo (R5)
    post_ids = set(PostConcern.objects.values_list("post_id", flat=True))
    posts = Post.objects.filter(id__in=post_ids).select_related(
        "thread__activity", "thread__group", "author"
    )
    for post in posts:
        # Per-post isolation: one bad post must not abort the whole ladder pass.
        try:
            if post.is_hidden:
                # A moderator REMOVE (is_hidden, no updated_at bump) must not let the ladder keep
                # accruing/delivering a note on a hidden post; the sensors above already saw its
                # rows. Skip the LADDER only.
                continue
            owner_obj = post.thread.owner_object
            if owner_obj is None or getattr(owner_obj, "is_hidden", False):
                continue
            cohort = getattr(owner_obj, "cohort", None)
            # Distinct eligible flaggers whose concern POST-DATES the last edit (an edit resets
            # accrual), minus the down-weighted many-target flaggers. Only IDs — never persisted.
            flaggers = set(
                PostConcern.objects.filter(post=post, created_at__gt=post.updated_at).values_list(
                    "user_id", flat=True
                )
            )
            flaggers -= downweighted
            count = len(flaggers)

            if cohort == Cohort.CHILD:
                # The service rejects child flaggers and guardians are barred, so a CHILD thread
                # can't accrue concern rows. Assert defensively (log, never crash the cron), skip.
                if count:
                    logger.warning("concern rows on a CHILD thread post %s — skipping", post.id)
                continue
            if cohort == Cohort.TEEN:
                if count >= settings.CONCERN_TEEN_K and _open_review(
                    ConcernReview.Kind.TEEN_CONCERN,
                    post=post,
                    subject_user=post.author,
                    payload={"window_days": settings.FORMATIVE_NOTE_COOLDOWN_DAYS},
                    dedupe={"post": post},
                ):
                    # NEVER auto-delivered to the minor — a human relays via the moderation UI.
                    notify_moderators(
                        Notification.Kind.MOD_ALERT,
                        "A teen post may need a gentle human relay",
                        body="A few members felt a teen's post might be off-topic. See the queue.",
                        url="/moderation/",
                    )
                    summary["teen"] += 1
                continue
            if cohort != Cohort.ADULT:
                continue

            summary["notes"] += _adult_ladder(
                post,
                owner_obj,
                count=count,
                now=now,
                protected_authors=protected_authors,
                noted_this_run=noted_this_run,
                audience_cache=audience_cache,
            )
            if _adult_escalation(post, count=count):
                summary["escalated"] += 1
        except Exception:  # noqa: BLE001 — one bad post must not starve the ladder pass
            logger.exception("evaluate_concerns: skipping post %s after an error", post.id)

    record_audit(
        "concerns.evaluated",
        notes=summary["notes"],
        escalated=summary["escalated"],
        teen=summary["teen"],
        coordinated=summary["coordinated"],
        pileon=summary["pileon"],
    )
    return summary


def _adult_ladder(
    post, owner_obj, *, count, now, protected_authors, noted_this_run, audience_cache=None
) -> int:
    """The adult author's restorative rung: at k1 (+ audience floor) send the ONE lifetime formative
    note, capped by the rolling cooldown and suppressed under pile-on protection. Also maintains the
    edit-bar. Returns 1 if a note was DELIVERED, else 0.

    Muting is an honored opt-out but does NOT reset the caps: if the recipient muted the kind,
    ``notify`` returns None and the note is not delivered, yet the attempt is still CONSUMED —
    ``note_sent_at`` is stamped and the per-run/rolling caps count it — so a determined dyad can't
    bypass the ≤1-per-author / ≤1-per-post-lifetime caps by the author having muted the kind. The
    audit records the muted attempt distinctly (``concern.formative_note_muted``)."""
    if audience_cache is None:
        audience_cache = {}
    state, _created = PostConcernState.objects.get_or_create(post=post)

    # Edit-bar: a note was sent, then the post was edited -> permanently bar a repeat auto-note
    # (a re-cross after edit routes to the moderator queue instead — see _adult_escalation).
    if state.note_sent_at and post.updated_at > state.note_sent_at and not state.note_barred:
        state.note_barred = True
        state.save(update_fields=["note_barred"])

    if (
        count >= settings.CONCERN_K1
        and _audience(owner_obj, audience_cache) >= settings.CONCERN_AUDIENCE_FLOOR
        and not state.note_sent_at
        and not state.note_barred
        and post.author_id not in protected_authors
        and post.author_id not in noted_this_run
        and not _recent_formative_note(post.author_id, now=now)
    ):
        note = notify(
            post.author,
            Notification.Kind.FORMATIVE_NOTE,
            FORMATIVE_NOTE_TITLE,
            body=FORMATIVE_NOTE_BODY,
            url=_thread_url(owner_obj),
        )
        # Consume the attempt regardless of delivery (mute-independent caps — see docstring).
        state.note_sent_at = now
        state.save(update_fields=["note_sent_at"])
        noted_this_run.add(post.author_id)
        if note is None:
            record_audit("concern.formative_note_muted", target=post)
            return 0
        record_audit("concern.formative_note_sent", target=post)
        return 1
    return 0


def _adult_escalation(post, *, count) -> bool:
    """The k2 (or barred-re-cross) rung: raise ONE OPEN moderator-queue item + alert. Returns True
    if a review was newly created this run."""
    state = PostConcernState.objects.filter(post=post).first()
    barred_recross = bool(state and state.note_barred and count >= settings.CONCERN_K1)
    if not (count >= settings.CONCERN_K2 or barred_recross):
        return False
    created = _open_review(
        ConcernReview.Kind.CONCERN_ESCALATED,
        post=post,
        subject_user=post.author,
        payload={"barred_recross": barred_recross},
        dedupe={"post": post},
    )
    if created:
        notify_moderators(
            Notification.Kind.MOD_ALERT,
            "A concern needs a human look",
            body="A post drew repeated concern flags. Review it in the moderation queue.",
            url="/moderation/",
        )
    return created


def _recent_formative_note(author_id, *, now) -> bool:
    """Whether the author already had a formative-note ATTEMPT within the rolling cooldown window
    (the ≤1-per-author cap across all posts). Reads ``PostConcernState.note_sent_at`` (stamped on
    every attempt, delivered or muted) rather than the Notification table, so the cap is
    MUTE-INDEPENDENT: a muted recipient still consumes the window, and the cap can't be reset by the
    author having muted the kind (ADR-0029)."""
    cutoff = now - timedelta(days=settings.FORMATIVE_NOTE_COOLDOWN_DAYS)
    return PostConcernState.objects.filter(
        post__author_id=author_id,
        note_sent_at__gte=cutoff,
    ).exists()


def _downweighted_flaggers(window_start) -> set:
    """Flaggers to DISCOUNT this run: anyone who has concerned posts by ≥5 distinct authors in the
    window (a many-target flagger is noise, not signal — sensor inversion). Returns a set of user
    ids; never stored, never surfaced, not a reliability history."""
    authors_by_user: dict = defaultdict(set)
    for row in PostConcern.objects.filter(created_at__gte=window_start).values(
        "user_id", "post__author_id"
    ):
        authors_by_user[row["user_id"]].add(row["post__author_id"])
    return {uid for uid, authors in authors_by_user.items() if len(authors) >= 5}


def _sensor_pileon(now) -> dict:
    """Pile-on protection: an author drawing concern rows across ≥3 distinct posts within 7 days is
    flagged for PROTECTIVE review and shielded from further auto-notes this run (and while an OPEN
    SENSOR_PILEON exists). Returns {"authors": set, "created": int}."""
    window7 = now - timedelta(days=7)
    posts_by_author: dict = defaultdict(set)
    for row in PostConcern.objects.filter(created_at__gte=window7).values(
        "post__author_id", "post_id"
    ):
        posts_by_author[row["post__author_id"]].add(row["post_id"])

    # Already-protected authors (an OPEN pile-on review keeps the shield up across runs).
    protected = set(
        ConcernReview.objects.filter(
            kind=ConcernReview.Kind.SENSOR_PILEON,
            status=ConcernReview.Status.OPEN,
            subject_user__isnull=False,
        ).values_list("subject_user_id", flat=True)
    )
    created = 0
    for author_id, posts in posts_by_author.items():
        if len(posts) < 3:
            continue
        protected.add(author_id)
        if _open_review(
            ConcernReview.Kind.SENSOR_PILEON,
            subject_user_id=author_id,
            payload={"post_ids": sorted(posts), "window_days": 7},
            dedupe={"subject_user_id": author_id},
        ):
            created += 1
            notify_moderators(
                Notification.Kind.MOD_ALERT,
                "A member may need protective review",
                body="One member is drawing concern flags across several posts. Please look.",
                url="/moderation/",
            )
    return {"authors": protected, "created": created}


def _sensor_coordinated(now) -> int:
    """Coordinated-flagging detection: when an overlapping set of ≥2 flaggers hits the same author
    across ≥3 posts in 14 days, alert about the FLAGGERS (never the author). Payload carries the
    post ids + window + set SIZE only — no flagger identities, no long-term accretion. Returns the
    number of reviews newly created."""
    window14 = now - timedelta(days=14)
    # author -> {flagger_id -> set(post_ids)} across concern OR dissent rows.
    by_author: dict = defaultdict(lambda: defaultdict(set))
    for row in PostConcern.objects.filter(created_at__gte=window14).values(
        "user_id", "post__author_id", "post_id"
    ):
        by_author[row["post__author_id"]][row["user_id"]].add(row["post_id"])
    for row in PostDissent.objects.filter(created_at__gte=window14).values(
        "user_id", "post__author_id", "post_id"
    ):
        by_author[row["post__author_id"]][row["user_id"]].add(row["post_id"])

    created = 0
    for author_id, flagger_posts in by_author.items():
        # Heavy flaggers hit ≥3 of the author's posts; a coordinated pair shares ≥3 common posts.
        heavy = {f: posts for f, posts in flagger_posts.items() if len(posts) >= 3}
        heavy_ids = list(heavy)
        common_posts: set = set()
        for i in range(len(heavy_ids)):
            for j in range(i + 1, len(heavy_ids)):
                shared = heavy[heavy_ids[i]] & heavy[heavy_ids[j]]
                if len(shared) >= 3:
                    common_posts |= shared
        if not common_posts:
            continue
        # The full overlapping set: every flagger covering all of the shared posts.
        overlap = {f for f, posts in flagger_posts.items() if common_posts <= posts}
        if len(overlap) < 2:
            continue
        if _open_review(
            ConcernReview.Kind.SENSOR_COORDINATED,
            payload={
                "author_id": author_id,
                "post_ids": sorted(common_posts),
                "window_days": 14,
                "flagger_set_size": len(overlap),
            },
            dedupe={"payload__author_id": author_id},
        ):
            created += 1
            notify_moderators(
                Notification.Kind.MOD_ALERT,
                "Possible coordinated flagging",
                body="The same members are flagging one author across several posts. Review it.",
                url="/moderation/",
            )
    return created


def _open_review(kind, *, dedupe, payload, post=None, subject_user=None, subject_user_id=None):
    """Create ONE OPEN ConcernReview of ``kind`` unless a matching OPEN row already exists
    (``dedupe`` is the extra filter, e.g. ``{"post": post}`` or ``{"subject_user_id": id}``).
    Returns True if a row was created. Incident-scoped: ``payload`` holds facts only — never an
    accreting per-user history (inv.2)."""
    if ConcernReview.objects.filter(kind=kind, status=ConcernReview.Status.OPEN, **dedupe).exists():
        return False
    kwargs = {"kind": kind, "post": post, "payload": payload}
    if subject_user_id is not None:
        kwargs["subject_user_id"] = subject_user_id
    else:
        kwargs["subject_user"] = subject_user
    ConcernReview.objects.create(**kwargs)
    return True


# --- job 3: purge_stale_reaction_rows (90-day hard delete) ------------------------------------


def purge_stale_reaction_rows(now=None) -> dict:
    """Hard-delete reaction / dissent / concern rows older than REACTION_ROW_RETENTION_DAYS
    (ADR-0029: delete beats anonymize). The derived footers keep their permanent slugs, so a
    long-standing appreciation survives the purge while the raw, re-identifiable rows do not.
    Batched, self-gated daily."""
    now = now or timezone.now()
    summary = {"skipped": False, "reactions": 0, "dissents": 0, "concerns": 0}
    if not _claim_daily("reaction_purge_daily", now=now):
        summary["skipped"] = True
        return summary
    cutoff = now - timedelta(days=settings.REACTION_ROW_RETENTION_DAYS)
    summary["reactions"] = _batched_delete(PostReaction.objects.filter(created_at__lt=cutoff))
    summary["dissents"] = _batched_delete(PostDissent.objects.filter(created_at__lt=cutoff))
    summary["concerns"] = _batched_delete(PostConcern.objects.filter(created_at__lt=cutoff))
    record_audit(
        "reaction.rows_purged",
        reactions=summary["reactions"],
        dissents=summary["dissents"],
        concerns=summary["concerns"],
    )
    return summary


def _batched_delete(qs, *, batch=1000) -> int:
    """Delete a queryset in bounded id-batches so a large purge never holds one giant lock. These
    rows have no cascade children, so the count is exact."""
    model = qs.model
    total = 0
    while True:
        ids = list(qs.values_list("id", flat=True)[:batch])
        if not ids:
            break
        deleted, _ = model.objects.filter(id__in=ids).delete()
        total += deleted
    return total
