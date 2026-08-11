"""GDPR Art. 20 (data portability) export for a single user.

Produces a structured, machine-readable (JSON) snapshot of the personal data we hold for
one account: profile, the proven age band / cohort (never a birthdate — see data
minimisation in docs/COMPLIANCE.md), consent metadata, the activities they own/joined, and
a donations summary. Cross-app data is gathered through ORM relations only; no payment-card
data is ever stored, so the donations section is an aggregate-plus-references summary.

This is the portability counterpart to account erasure: it discloses ONLY the requesting
user's (or, via the guardian variant, their ward's) own data, never other members' PII.
"""

from django.utils import timezone

# Export schema version, so consumers can detect format changes over time.
# v5 (DSA Art.17 provenance/scope fix): thread_posts is now {"items", "total", "truncated"} instead
# of a bare list (F4 — the old ascending order_by()[:cap] silently kept the OLDEST 5000 posts and
# dropped the newest for a prolific author); an author-self-deleted post's own body is released to
# ITS OWNER (the data subject on the self path only — the guardian ward-export keeps "[removed]",
# see ``for_self``) instead of "[removed]" unless a standing platform REMOVE also holds it, and each
# row gains a "status" token of visible|deleted_by_you|removed (F3); safety_record gains additive
# decisions_total/decisions_truncated/reports_total/reports_truncated keys (the safety-record half
# of F4, added in apps/safety/services.py).
#
# Two truncation idioms coexist in v5 BY DESIGN: thread_posts is reshaped in this version anyway,
# so it uses the self-describing {"items", "total", "truncated"} envelope; safety_record's lists
# predate v5 and stay flat, with truncation arriving as ADDITIVE sibling keys (*_total/*_truncated)
# so existing v4 consumers of those lists keep parsing. New list sections should use the envelope.
EXPORT_SCHEMA_VERSION = 5


def build_user_export(user, *, for_self: bool = True) -> dict:
    """Return a JSON-serialisable dict of all personal data held for ``user``.

    Self-contained and side-effect free: callers (the export views) decide how to deliver
    it. The shape is intentionally explicit (not a blind model dump) so we never leak a
    field we did not mean to disclose.

    ``for_self`` says who READS the document: the data subject themselves (MeExportView, the
    web download — the default) or their guardian (WardExportView passes False). Exactly one
    section differs: thread_posts releases the author's own self-deleted bodies only on the
    self path — see ``_thread_posts`` for the rationale."""
    return {
        "schema_version": EXPORT_SCHEMA_VERSION,
        "generated_at": timezone.now().isoformat(),
        "profile": _profile(user),
        "age_assurance": _age_assurance(user),
        "consents": _consents(user),
        "guardianships": _guardianships(user),
        "memberships": _memberships(user),
        "owned_activities": _owned_activities(user),
        "owned_groups": _owned_groups(user),
        "group_memberships": _group_memberships(user),
        "thread_posts": _thread_posts(user, for_self=for_self),
        "donations": _donations_summary(user),
        "api_access": _api_access(user),
        # W4-F22: the user's OWN DSA Art.16/17 record + block list + notification/access settings —
        # data the platform holds + shows on-screen, now portable so "download my data" is complete.
        "safety_record": _safety_record(user),
        "blocks": _blocks(user),
        "privacy_settings": _privacy_settings(user),
        # ADR-0029: the user's OWN plural-sentiment rows. SAR posture: a dissent/concern row is the
        # FLAGGER's personal data (a record of their own action), not the post author's — Art.15(4)
        # rights-of-others redaction means an AUTHOR's export never gets this data about someone
        # else's flag. This section is scoped to `user` throughout (never another member's rows,
        # never a count/aggregate — just the plain list of the user's own toggles).
        "own_sentiment_actions": _own_sentiment_actions(user),
    }


def _api_access(user) -> dict:
    """W10 disclosure: whether an API token exists for this account and when it was
    issued — METADATA only, never the key itself (Art. 15 transparency without turning
    the export into a credential leak)."""
    from rest_framework.authtoken.models import Token

    token = Token.objects.filter(user=user).first()
    return {
        "api_token_issued": token is not None,
        "issued_at": token.created.isoformat() if token else None,
    }


def _profile(user) -> dict:
    return {
        "public_id": str(user.public_id),
        "username": user.username,
        "display_name": user.display_name,
        "age_band": user.age_band,
        "cohort": user.cohort,
        "role": user.role,
        "is_identity_verified": user.is_identity_verified,
        "identity_verified_at": _iso(user.identity_verified_at),
        "is_active": user.is_active,
        "date_joined": _iso(user.date_joined),
    }


def _age_assurance(user) -> list[dict]:
    """Age-assurance events — the proven band and provenance, never identity data."""
    return [
        {
            "provider": a.provider,
            "method": a.method,
            "age_band": a.age_band,
            "verified_at": _iso(a.verified_at),
            "expires_at": _iso(a.expires_at),
            # `raw` holds only the over-threshold booleans / format markers (no PII).
            "evidence": a.raw,
        }
        for a in user.age_assurances.all().order_by("verified_at")
    ]


def _consents(user) -> dict:
    """Parental-consent metadata: consents held *as the minor*, plus references to
    consents this user granted *as a guardian* (identified by guardian public id, not
    free-form personal data)."""
    as_minor = [
        {
            "status": c.status,
            "scope": c.scope,
            "guardian_identifier": c.guardian_identifier,
            "granted_at": _iso(c.granted_at),
            "expires_at": _iso(c.expires_at),
            "revoked_at": _iso(c.revoked_at),
            "created_at": _iso(c.created_at),
        }
        for c in user.parental_consents.all().order_by("created_at")
    ]
    return {"as_minor": as_minor}


def _guardianships(user) -> dict:
    """Guardianship links in both directions (account-level), by public id only."""
    wards = [
        {
            "ward_public_id": str(link.ward.public_id),
            "relationship": link.relationship,
            "status": link.status,
            "created_at": _iso(link.created_at),
        }
        for link in user.wards.select_related("ward").order_by("created_at")
    ]
    guardians = [
        {
            "guardian_public_id": str(link.guardian.public_id),
            "relationship": link.relationship,
            "status": link.status,
            "created_at": _iso(link.created_at),
        }
        for link in user.guardians.select_related("guardian").order_by("created_at")
    ]
    return {"as_guardian_of": wards, "guarded_by": guardians}


def _memberships(user) -> list[dict]:
    return [
        {
            "activity_id": m.activity_id,
            "activity_title": m.activity.title,
            "role": m.role,
            "state": m.state,
            "created_at": _iso(m.created_at),
            "decided_at": _iso(m.decided_at),
        }
        for m in user.memberships.select_related("activity").order_by("created_at")
    ]


def _owned_activities(user) -> list[dict]:
    return [
        {
            "id": a.id,
            "title": a.title,
            "status": a.status,
            "cohort": a.cohort,
            "starts_at": _iso(a.starts_at),
            "created_at": _iso(a.created_at),
        }
        for a in user.owned_activities.all().order_by("created_at")
    ]


def _owned_groups(user) -> list[dict]:
    """Standing groups this user owns (a group is content like an activity)."""
    return [
        {
            "id": g.id,
            "title": g.title,
            "status": g.status,
            "cohort": g.cohort,
            "area": g.area.name,
            "is_staff_curated": g.is_staff_curated,
            "created_at": _iso(g.created_at),
        }
        for g in user.owned_groups.select_related("area").order_by("created_at")
    ]


def _group_memberships(user) -> list[dict]:
    """Standing-group memberships (role/state only — a group keeps no per-user history)."""
    return [
        {
            "group_id": m.group_id,
            "group_title": m.group.title,
            "role": m.role,
            "state": m.state,
            "joined_at": _iso(m.joined_at),
        }
        for m in user.group_memberships.select_related("group").order_by("joined_at")
    ]


# F4: bounded so a runaway export can't blow memory/timeout; monkeypatchable for tests.
_THREAD_POSTS_CAP = 5000


def _thread_posts(user, *, for_self: bool) -> dict:
    """W2-F32: the user's OWN authored thread posts + announcements, so their actual words travel
    with them (GDPR Art.20), not just metadata. STRICT allowlist — only fields the user authored
    or that describe their own post:

    * body — their own text. Provenance-aware (DSA Art.17 fix, see canonical model in
      apps.safety.services.targets_with_unlifted_remove): a post the AUTHOR deleted themselves
      is their own withdrawn personal data, still on disk, and is released here as
      "deleted_by_you" UNLESS a standing (un-lifted) platform REMOVE also holds it. While that
      REMOVE stands the body stays "[removed]" — not for moderator anonymity (no moderator data
      is in this projection on any path) but as fail-closed evidence-integrity precedence: the
      platform still holds the content as the record of a live decision, the same precedence F5
      applies to media retention. A hidden post WITHOUT author-delete provenance (a REMOVE the
      author never followed with their own delete, or the PostAdmin manual hide) is likewise
      "[removed]". An admin manual hide FOLLOWED by the author's own delete is byte-identical in
      data to a plain self-delete (is_hidden + is_author_deleted, zero ModerationAction rows),
      so it necessarily releases as "deleted_by_you" — no code could tell the two apart; a test
      pins that;
    * status — one of "visible" | "deleted_by_you" | "removed", so a consumer can tell a live
      post from the two ways it can be hidden without inferring it from the body text;
    * created_at, edited (derived live from updated_at > created_at — there is no stored flag),
      is_announcement, had_attachment (boolean only — never attachment bytes);
    * the parent thread's title + id (via the activity-XOR-group bridge).

    HARD EXCLUSIONS (another member's data / not the user's words): never the reply_to parent's
    body or the derived reply snippet, never a shared activity/place/event target's content.

    ``for_self`` — the released-body rule above applies ONLY when the reader is the data
    subject. On the guardian path (WardExportView passes False) every hidden body stays
    "[removed]": the guardian is an explicit read-only observer (docs/SAFETY.md) and the
    Art.17 statement of reasons already deliberately excludes guardians, so a ward's
    affirmative withdrawal of their own words gets the most protective reading. The status
    token (named from the data subject's perspective) still flows, so the guardian learns a
    post existed and was withdrawn — never the withdrawn text. The owner may later widen
    this; the point is the policy is explicit and tested, not silent.

    F4: keeps the NEWEST ``_THREAD_POSTS_CAP`` posts, not the oldest — ``Post.Meta.ordering`` is
    ``["created_at"]``, so a naive ascending head-slice silently dropped a prolific author's most
    recent words from their own portability export. Fetched descending (with ``-id`` as a
    deterministic tiebreak) and reversed back to chronological order in Python, so the on-screen
    shape is unchanged. Returns ``{"items", "total", "truncated"}`` so a truncated export is
    signalled rather than silently claiming completeness. Attachments are prefetched so the
    had_attachment flag costs no extra query."""
    from apps.safety.services import targets_with_unlifted_remove
    from apps.social.models import Post

    total = Post.objects.filter(author=user).count()
    posts = list(
        Post.objects.filter(author=user)
        .select_related("thread__activity", "thread__group")
        .prefetch_related("attachments")
        .order_by("-created_at", "-id")[:_THREAD_POSTS_CAP]
    )
    posts.reverse()  # back to chronological (oldest-first) order for display

    # One bounded query: which of THIS user's own author-deleted posts still sit under a standing
    # platform REMOVE (so the moderator's decision, not the author's own act, is what's blocking
    # disclosure)? Empty in the common case (no author-deleted posts among the fetched page).
    removed_ids = targets_with_unlifted_remove(
        Post, [p.id for p in posts if p.is_hidden and p.is_author_deleted]
    )

    rows = []
    for p in posts:
        owner = p.thread.owner_object  # an Activity XOR a Group
        if not p.is_hidden:
            status, body = "visible", p.body
        elif p.is_author_deleted and p.id not in removed_ids:
            # Withdrawn words go to their author alone; a guardian still gets the status token
            # (a post existed and was withdrawn), never the withdrawn text.
            status, body = "deleted_by_you", (p.body if for_self else "[removed]")
        else:
            status, body = "removed", "[removed]"
        rows.append(
            {
                "thread_kind": "group" if p.thread.group_id else "activity",
                "thread_id": getattr(owner, "id", None),
                "thread_title": getattr(owner, "title", None) or getattr(owner, "name", None),
                "body": body,
                "status": status,
                "is_announcement": p.is_announcement,
                "edited": p.updated_at > p.created_at,
                "had_attachment": bool(p.attachments.all()),
                "created_at": _iso(p.created_at),
            }
        )
    return {"items": rows, "total": total, "truncated": total > len(rows)}


def _donations_summary(user) -> dict:
    """Donations the user made. No card/payment data is stored (the provider handles it);
    we keep only amount, status, provider and an opaque reference (see donations model)."""
    from django.db.models import Sum

    from apps.donations.models import Donation

    donations = user.donations.all().order_by("created_at")
    completed = donations.filter(status=Donation.Status.COMPLETED)
    return {
        "count": donations.count(),
        "completed_count": completed.count(),
        "completed_total_cents": completed.aggregate(s=Sum("amount_cents"))["s"] or 0,
        "items": [
            {
                "amount_cents": d.amount_cents,
                "currency": d.currency,
                "recurring": d.recurring,
                "campaign": d.campaign.title if d.campaign else None,
                "provider": d.provider,
                "status": d.status,
                "external_ref": d.external_ref,
                "created_at": _iso(d.created_at),
                "completed_at": _iso(d.completed_at),
            }
            for d in donations
        ],
    }


def _own_sentiment_actions(user) -> dict:
    """ADR-0029: the user's OWN appreciation-facet reactions, "I see this differently" dissent
    tallies, and "doesn't seem to fit here" concern flags — never another member's, never a count
    (this is the flat list of their own rows, not a derived aggregate). ``post_id`` lets the export
    line up with ``thread_posts``/other own-authored data; there is no post body here (a reaction
    row carries no content of its own)."""
    from apps.social.models import PostConcern, PostDissent, PostReaction

    return {
        "reactions": [
            {"post_id": r.post_id, "facet": r.emoji, "created_at": _iso(r.created_at)}
            for r in PostReaction.objects.filter(user=user).order_by("created_at")
        ],
        "dissents": [
            {"post_id": d.post_id, "created_at": _iso(d.created_at)}
            for d in PostDissent.objects.filter(user=user).order_by("created_at")
        ],
        "concerns": [
            {"post_id": c.post_id, "created_at": _iso(c.created_at)}
            for c in PostConcern.objects.filter(user=user).order_by("created_at")
        ],
    }


def _json_safe(value):
    """Recursively coerce datetimes to ISO strings so a dict returned by a hardened service stays
    serialisable by the plain-json export (account_export uses json.dumps, not the DRF encoder)."""
    if isinstance(value, dict):
        return {k: _json_safe(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [_json_safe(v) for v in value]
    if hasattr(value, "isoformat"):
        return value.isoformat()
    return value


def _safety_record(user) -> dict:
    """W4-F22: the user's OWN DSA Art.16/17 record — moderation decisions about their account/
    content and the reports they filed — via the hardened, self-scoped ``safety_record_for``, which
    already strips the moderator identity, other users, and who-reported. Routing through it (never
    re-querying the raw FK rows) means the export can never widen exposure beyond what
    /my-safety-record/ already shows on-screen."""
    from apps.safety.services import safety_record_for

    return _json_safe(safety_record_for(user))


def _blocks(user) -> list:
    """W4-F22: the user's OWN block actions — who they blocked and when. Mirrors exactly what the
    /blocks page shows the blocker (the blocked user's display name + stable public id), never the
    blocked user's other PII."""
    from apps.safety.models import Block

    return [
        {
            "blocked": b.blocked.display_name or b.blocked.username,
            "blocked_public_id": str(b.blocked.public_id),
            "created_at": _iso(b.created_at),
        }
        for b in Block.objects.filter(blocker=user).select_related("blocked").order_by("created_at")
    ]


def _privacy_settings(user) -> dict:
    """W4-F22: the user's OWN notification mutes (F31) + stated accessibility preferences (F15) —
    settings they chose and already see on-screen, now portable. No inferred / behavioural data."""
    from apps.notifications.services import get_muted_kinds
    from apps.places.models import AccessPreference

    pref = AccessPreference.objects.filter(user=user).first()
    from apps.accounts.signature import avatar_style_info

    style = avatar_style_info(user)
    return {
        "muted_notification_kinds": sorted(get_muted_kinds(user)),
        "access_preferences": {
            "needs_step_free": pref.needs_step_free,
            "needs_accessible_toilet": pref.needs_accessible_toilet,
            "needs_hearing_loop": pref.needs_hearing_loop,
            "prefers_quiet": pref.prefers_quiet,
        }
        if pref is not None
        else None,
        # ADR-0027: the chosen avatar generation is user preference data the UI shows, so it is
        # portable (Art. 15/20) — the generation only, never the internal fingerprint/salt/dates.
        "avatar_style": {
            "generation": style["generation"],
            "name": style["generation_name"],
        },
    }


def _iso(value):
    return value.isoformat() if value else None
