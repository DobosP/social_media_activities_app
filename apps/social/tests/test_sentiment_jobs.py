"""ADR-0029 batch jobs (apps.social.sentiment): the daily appreciation footer recompute + weekly
dissent window, the restorative concern ladder + anti-bully sensors, and the 90-day purge.

These test the DERIVATION only — no count/who-list/identity ever leaves the jobs; the
render/exposure guarantees live in the service + web tests (B6/B7 extend). Time is controlled by
passing an explicit ``now`` into the job functions (and backdating row ``created_at`` where a
window matters), so the cadence gates and rolling windows are deterministic without freezegun."""

from datetime import datetime, timedelta

import pytest
from django.utils import timezone

from apps.accounts.models import AgeBand, Cohort
from apps.notifications.models import Notification
from apps.notifications.services import set_muted_kinds
from apps.safety.models import AuditLog, ConcernReview
from apps.social import sentiment
from apps.social import services as social
from apps.social.models import (
    Activity,
    Membership,
    Post,
    PostConcern,
    PostConcernState,
    PostDissent,
    PostReaction,
    PostSentimentFooter,
)

from .conftest import make_user

FACETS = list(social.REACTION_FACETS)


def _lines(post, viewer):
    """The rendered footer for a FRESHLY re-fetched post. A reverse OneToOne
    (``post.sentiment_footer``) caches on first access, so re-reading the same in-memory ``post``
    after a batch recompute would mask it — a request loads posts fresh, so this mirrors that."""
    from apps.social.models import Post

    return social.sentiment_footer_for(Post.objects.get(pk=post.pk), viewer)


def _activity(place, activity_type, *, cohort=Cohort.ADULT, n_members=0, tag="a"):
    """An ADULT activity (owner auto-admitted) with ``n_members`` extra members, optionally
    re-pinned to another cohort for routing tests (bypassing the minor apparatus)."""
    owner = make_user(f"{tag}_owner", AgeBand.ADULT)
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    if cohort != Cohort.ADULT:
        Activity.objects.filter(pk=activity.pk).update(cohort=cohort)
        activity.refresh_from_db()
    members = []
    for i in range(n_members):
        u = make_user(f"{tag}_m{i}", AgeBand.ADULT)
        Membership.objects.create(
            activity=activity, user=u, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
        )
        members.append(u)
    return owner, activity, members


def _react(post, users, facet):
    for u in users:
        PostReaction.objects.create(post=post, user=u, emoji=facet)


def _dissent(post, users):
    for u in users:
        PostDissent.objects.create(post=post, user=u)


def _concern(post, users, *, when=None):
    for u in users:
        c = PostConcern.objects.create(post=post, user=u)
        if when is not None:
            PostConcern.objects.filter(pk=c.pk).update(created_at=when)


# --- cadence gate ----------------------------------------------------------------------------


@pytest.mark.django_db
def test_daily_gate_second_run_same_day_is_noop(place, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 2
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="g")
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:2], FACETS[0])
    now = timezone.now()
    first = sentiment.recompute_post_sentiment(now=now)
    assert first["skipped"] is False
    second = sentiment.recompute_post_sentiment(now=now)
    assert second["skipped"] is True


# --- appreciation latch / floor edges --------------------------------------------------------


@pytest.mark.django_db
def test_k_minus_one_does_not_latch(place, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 2  # need >=2 reactors AND audience >=4
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="k1")
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:1], FACETS[0])  # only 1 reactor (k-1)
    sentiment.recompute_post_sentiment(now=timezone.now())
    assert _lines(post, owner) == []


@pytest.mark.django_db
def test_audience_below_two_k_does_not_latch(place, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 2  # audience floor = 4
    owner, activity, members = _activity(place, activity_type, n_members=2, tag="au")  # audience=3
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:2], FACETS[0])  # count=2 (>=k) but audience 3 < 4
    sentiment.recompute_post_sentiment(now=timezone.now())
    assert _lines(post, owner) == []


@pytest.mark.django_db
def test_k_with_two_k_audience_latches(place, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 2
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="lt")  # audience=4
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:2], FACETS[0])
    sentiment.recompute_post_sentiment(now=timezone.now())
    assert _lines(post, owner) == [social.REACTION_FACETS[FACETS[0]][2]]


@pytest.mark.django_db
def test_teen_k_eight_audience_sixteen_latches(place, activity_type):
    # Default settings (SENTIMENT_K_TEEN=8): a TEEN thread needs 8 distinct reactors AND an
    # audience >= 16 -- a stricter floor than ADULT, appreciation-only (no dissent on TEEN).
    owner, activity, members = _activity(
        place, activity_type, cohort=Cohort.TEEN, n_members=16, tag="tk8"
    )  # audience = owner + 16 = 17 >= 16
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:8], FACETS[0])
    sentiment.recompute_post_sentiment(now=timezone.now())
    assert _lines(post, owner) == [social.REACTION_FACETS[FACETS[0]][2]]


@pytest.mark.django_db
def test_teen_k_minus_one_does_not_latch(place, activity_type):
    owner, activity, members = _activity(
        place, activity_type, cohort=Cohort.TEEN, n_members=16, tag="tk7"
    )
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:7], FACETS[0])  # 7 < k=8
    sentiment.recompute_post_sentiment(now=timezone.now())
    assert _lines(post, owner) == []


@pytest.mark.django_db
def test_author_parity_byte_identical(place, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 2
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="par")
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:2], FACETS[0])
    sentiment.recompute_post_sentiment(now=timezone.now())
    assert _lines(post, owner) == _lines(post, members[0])


@pytest.mark.django_db
def test_erasure_unlatches_at_next_run(place, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 2
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="er")
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:2], FACETS[0])
    sentiment.recompute_post_sentiment(now=timezone.now())
    assert _lines(post, owner)  # latched
    # A reactor erases their row -> surviving count drops below k -> next run unlatches (honesty).
    PostReaction.objects.filter(post=post, user=members[0]).delete()
    sentiment.recompute_post_sentiment(now=timezone.now() + timedelta(hours=25))
    assert _lines(post, owner) == []


@pytest.mark.django_db
def test_permanence_survives_row_purge(place, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 2
    settings.REACTION_ROW_RETENTION_DAYS = 90
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="pm")
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:2], FACETS[0])
    sentiment.recompute_post_sentiment(now=timezone.now())
    footer = PostSentimentFooter.objects.get(post=post)
    # Backdate the latched-since so the next run graduates the slug to permanent.
    old = (timezone.now() - timedelta(days=200)).date().isoformat()
    footer.appreciation_slugs = [[FACETS[0], old]]
    footer.save(update_fields=["appreciation_slugs"])
    sentiment.recompute_post_sentiment(now=timezone.now() + timedelta(hours=25))
    footer.refresh_from_db()
    assert footer.appreciation_permanent == [FACETS[0]]
    # Purge all rows: the permanent slug (and its sentence) survive even with zero surviving rows.
    PostReaction.objects.filter(post=post).update(created_at=timezone.now() - timedelta(days=200))
    sentiment.purge_stale_reaction_rows(now=timezone.now())
    assert not PostReaction.objects.filter(post=post).exists()
    assert _lines(post, owner) == [social.REACTION_FACETS[FACETS[0]][2]]


# --- weekly dissent window -------------------------------------------------------------------


def _dissent_settings(settings):
    settings.DISSENT_K = 2
    settings.DISSENT_AUDIENCE_FLOOR = 3
    settings.DISSENT_WINDOWS_TO_LATCH = 2
    settings.DISSENT_WINDOWS_TO_LAPSE = 2


@pytest.mark.django_db
def test_dissent_latches_after_two_windows_then_lapses(place, activity_type, settings):
    _dissent_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="di")
    post = social.post_to_thread(owner, activity, "hi")
    _dissent(post, members[:2])
    base = timezone.now()
    sentiment.recompute_post_sentiment(now=base)  # window 1: hit, not yet latched
    assert _lines(post, owner) == []
    sentiment.recompute_post_sentiment(now=base + timedelta(days=7))  # window 2: latch
    assert _lines(post, owner) == [social.DISSENT_SENTENCE]
    # Remove dissents: two consecutive below-threshold windows lapse the line (no permanent mark).
    PostDissent.objects.filter(post=post).delete()
    sentiment.recompute_post_sentiment(now=base + timedelta(days=14))
    sentiment.recompute_post_sentiment(now=base + timedelta(days=21))
    assert _lines(post, owner) == []


@pytest.mark.django_db
def test_dissent_exempts_announcements(place, activity_type, settings):
    _dissent_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="an")
    post = social.post_to_thread(owner, activity, "hi")
    post.is_announcement = True
    post.save(update_fields=["is_announcement"])
    _dissent(post, members[:2])
    base = timezone.now()
    sentiment.recompute_post_sentiment(now=base)
    sentiment.recompute_post_sentiment(now=base + timedelta(days=7))
    assert _lines(post, owner) == []


@pytest.mark.django_db
def test_dissent_never_on_teen_thread(place, activity_type, settings):
    _dissent_settings(settings)
    owner, activity, members = _activity(
        place, activity_type, cohort=Cohort.TEEN, n_members=3, tag="tn"
    )
    post = social.post_to_thread(owner, activity, "hi")
    _dissent(post, members[:2])
    base = timezone.now()
    sentiment.recompute_post_sentiment(now=base)
    sentiment.recompute_post_sentiment(now=base + timedelta(days=7))
    footer = PostSentimentFooter.objects.filter(post=post).first()
    assert footer is None or footer.dissent_active is False


# --- concern ladder --------------------------------------------------------------------------


def _concern_settings(settings):
    settings.CONCERN_K1 = 2
    settings.CONCERN_K2 = 4
    settings.CONCERN_AUDIENCE_FLOOR = 3
    settings.CONCERN_TEEN_K = 3
    settings.FORMATIVE_NOTE_COOLDOWN_DAYS = 14


@pytest.mark.django_db
def test_concerns_daily_gate_second_run_same_day_is_noop(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=1, tag="cg")
    now = timezone.now()
    first = sentiment.evaluate_concerns(now=now)
    assert first["skipped"] is False
    second = sentiment.evaluate_concerns(now=now)
    assert second["skipped"] is True


@pytest.mark.django_db
def test_k1_sends_one_formative_note(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=4, tag="n1")
    post = social.post_to_thread(owner, activity, "hi")
    _concern(post, members[:2], when=timezone.now())
    sentiment.evaluate_concerns(now=timezone.now())
    notes = Notification.objects.filter(recipient=owner, kind=Notification.Kind.FORMATIVE_NOTE)
    assert notes.count() == 1
    assert PostConcernState.objects.get(post=post).note_sent_at is not None
    # Idempotent: a second run does not send a second note (≤1 per post lifetime).
    sentiment.evaluate_concerns(now=timezone.now() + timedelta(hours=25))
    assert notes.count() == 1


@pytest.mark.django_db
def test_cooldown_one_note_across_two_posts(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=4, tag="cd")
    p1 = social.post_to_thread(owner, activity, "one")
    p2 = social.post_to_thread(owner, activity, "two")
    _concern(p1, members[:2], when=timezone.now())
    _concern(p2, members[:2], when=timezone.now())
    sentiment.evaluate_concerns(now=timezone.now())
    assert (
        Notification.objects.filter(recipient=owner, kind=Notification.Kind.FORMATIVE_NOTE).count()
        == 1
    )


@pytest.mark.django_db
def test_edit_bars_repeat_and_recross_queues(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=4, tag="ed")
    post = social.post_to_thread(owner, activity, "hi")
    _concern(post, members[:2], when=timezone.now())
    sentiment.evaluate_concerns(now=timezone.now())
    # The author edits the post -> accrual resets + repeat auto-note is barred.
    post.body = "edited"
    post.save(update_fields=["body", "updated_at"])
    PostConcern.objects.filter(post=post).delete()
    later = timezone.now() + timedelta(days=1)
    _concern(post, members[:2], when=later)  # re-cross after edit
    sentiment.evaluate_concerns(now=later + timedelta(hours=1))
    state = PostConcernState.objects.get(post=post)
    assert state.note_barred is True
    # A barred re-cross routes to the moderator queue, not the author.
    assert ConcernReview.objects.filter(
        kind=ConcernReview.Kind.CONCERN_ESCALATED, post=post, status=ConcernReview.Status.OPEN
    ).exists()
    assert (
        Notification.objects.filter(recipient=owner, kind=Notification.Kind.FORMATIVE_NOTE).count()
        == 1
    )


@pytest.mark.django_db
def test_k2_queues_and_dedupes(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=5, tag="k2")
    post = social.post_to_thread(owner, activity, "hi")
    _concern(post, members[:4], when=timezone.now())  # k2 = 4
    sentiment.evaluate_concerns(now=timezone.now())
    q = ConcernReview.objects.filter(
        kind=ConcernReview.Kind.CONCERN_ESCALATED, post=post, status=ConcernReview.Status.OPEN
    )
    assert q.count() == 1
    sentiment.evaluate_concerns(now=timezone.now() + timedelta(hours=25))
    assert q.count() == 1  # deduped against the existing OPEN row


@pytest.mark.django_db
def test_teen_never_notifies_author_only_queues(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(
        place, activity_type, cohort=Cohort.TEEN, n_members=4, tag="te"
    )
    post = social.post_to_thread(owner, activity, "hi")
    _concern(post, members[:3], when=timezone.now())  # teen k = 3
    sentiment.evaluate_concerns(now=timezone.now())
    assert not Notification.objects.filter(
        recipient=owner, kind=Notification.Kind.FORMATIVE_NOTE
    ).exists()
    assert ConcernReview.objects.filter(kind=ConcernReview.Kind.TEEN_CONCERN, post=post).exists()


# --- sensors ---------------------------------------------------------------------------------


@pytest.mark.django_db
def test_coordinated_sensor_fires_on_overlap_not_disjoint(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=6, tag="co")
    posts = [social.post_to_thread(owner, activity, f"p{i}") for i in range(3)]
    # Same two flaggers concern all three of the author's posts -> coordinated.
    for p in posts:
        _concern(p, members[:2], when=timezone.now())
    fired = sentiment.evaluate_concerns(now=timezone.now())
    assert fired["coordinated"] == 1
    assert (
        ConcernReview.objects.filter(
            kind=ConcernReview.Kind.SENSOR_COORDINATED, status=ConcernReview.Status.OPEN
        ).count()
        == 1
    )


@pytest.mark.django_db
def test_coordinated_sensor_silent_on_disjoint_flaggers(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=6, tag="dj")
    posts = [social.post_to_thread(owner, activity, f"p{i}") for i in range(3)]
    # A different flagger per post -> no overlapping pair across 3 posts.
    for i, p in enumerate(posts):
        _concern(p, [members[i]], when=timezone.now())
    fired = sentiment.evaluate_concerns(now=timezone.now())
    assert fired["coordinated"] == 0


@pytest.mark.django_db
def test_pileon_flags_target_and_suppresses_note(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=5, tag="po")
    posts = [social.post_to_thread(owner, activity, f"p{i}") for i in range(3)]
    for p in posts:
        _concern(p, members[:2], when=timezone.now())  # each post also crosses k1
    sentiment.evaluate_concerns(now=timezone.now())
    assert ConcernReview.objects.filter(
        kind=ConcernReview.Kind.SENSOR_PILEON,
        subject_user=owner,
        status=ConcernReview.Status.OPEN,
    ).exists()
    # Protective suppression: the pile-on target gets no formative note this run.
    assert not Notification.objects.filter(
        recipient=owner, kind=Notification.Kind.FORMATIVE_NOTE
    ).exists()


@pytest.mark.django_db
def test_downweighted_flagger_excluded(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=4, tag="dw")
    post = social.post_to_thread(owner, activity, "hi")
    # members[0] is a many-target flagger (concerns 5 distinct authors' posts) -> down-weighted.
    heavy = members[0]
    for i in range(5):
        other_owner, other_act, _ = _activity(place, activity_type, tag=f"dw_o{i}")
        other_post = social.post_to_thread(other_owner, other_act, "x")
        _concern(other_post, [heavy], when=timezone.now())
    # On THIS post only members[0] (heavy) + members[1] flag: heavy is discounted -> count=1 < k1.
    _concern(post, [heavy, members[1]], when=timezone.now())
    sentiment.evaluate_concerns(now=timezone.now())
    assert not Notification.objects.filter(
        recipient=owner, kind=Notification.Kind.FORMATIVE_NOTE
    ).exists()


# --- child defensive + purge -----------------------------------------------------------------


@pytest.mark.django_db
def test_child_thread_never_gets_footer(place, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 2
    settings.SENTIMENT_K_TEEN = 2
    owner, activity, members = _activity(
        place, activity_type, cohort=Cohort.CHILD, n_members=3, tag="ch"
    )
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:2], FACETS[0])
    sentiment.recompute_post_sentiment(now=timezone.now())
    assert not PostSentimentFooter.objects.filter(post=post).exists()
    assert _lines(post, owner) == []


@pytest.mark.django_db
def test_purge_deletes_old_rows_keeps_recent(place, activity_type, settings):
    settings.REACTION_ROW_RETENTION_DAYS = 90
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="pu")
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, [members[0]], FACETS[0])
    _react(post, [members[1]], FACETS[1])
    _dissent(post, [members[2]])
    # Age the first reaction + the dissent past retention; keep the second reaction recent.
    PostReaction.objects.filter(post=post, user=members[0]).update(
        created_at=timezone.now() - timedelta(days=91)
    )
    PostDissent.objects.filter(post=post).update(created_at=timezone.now() - timedelta(days=91))
    summary = sentiment.purge_stale_reaction_rows(now=timezone.now())
    assert summary["reactions"] == 1
    assert summary["dissents"] == 1
    assert PostReaction.objects.filter(post=post, user=members[1]).exists()
    assert not PostReaction.objects.filter(post=post, user=members[0]).exists()


@pytest.mark.django_db
def test_purge_daily_gate_skips_second_run(place, activity_type, settings):
    owner, activity, members = _activity(place, activity_type, n_members=1, tag="pg")
    now = timezone.now()
    assert sentiment.purge_stale_reaction_rows(now=now)["skipped"] is False
    assert sentiment.purge_stale_reaction_rows(now=now)["skipped"] is True


# --- R1: hidden posts leave the ladder + footer, sensors still count --------------------------


@pytest.mark.django_db
def test_hidden_post_skips_footer_and_ladder(place, activity_type, settings):
    # A moderator REMOVE sets is_hidden WITHOUT bumping updated_at, so a hidden post must not accrue
    # a footer or an author-directed note (the render already hides it — don't do the work either).
    settings.SENTIMENT_K_ADULT = 2
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=4, tag="hid")
    post = social.post_to_thread(owner, activity, "hi")
    Post.objects.filter(pk=post.pk).update(is_hidden=True)  # REMOVE-style hide (no updated_at bump)
    _react(post, members[:2], FACETS[0])
    _concern(post, members[:2], when=timezone.now())
    sentiment.recompute_post_sentiment(now=timezone.now())
    sentiment.evaluate_concerns(now=timezone.now())
    assert not PostSentimentFooter.objects.filter(post=post).exists()  # excluded from candidates
    assert not Notification.objects.filter(
        recipient=owner, kind=Notification.Kind.FORMATIVE_NOTE
    ).exists()
    assert not ConcernReview.objects.filter(
        post=post, kind=ConcernReview.Kind.CONCERN_ESCALATED
    ).exists()


@pytest.mark.django_db
def test_hidden_posts_skip_ladder_but_sensors_still_count(place, activity_type, settings):
    # Only the LADDER skips a hidden post; the anti-bully sensors read the raw rows directly, so
    # bullying a hidden post is still detected (pile-on across 3 hidden posts still protects them).
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=5, tag="hs")
    posts = [social.post_to_thread(owner, activity, f"p{i}") for i in range(3)]
    for p in posts:
        _concern(p, members[:2], when=timezone.now())
        Post.objects.filter(pk=p.pk).update(is_hidden=True)
    sentiment.evaluate_concerns(now=timezone.now())
    assert ConcernReview.objects.filter(
        kind=ConcernReview.Kind.SENSOR_PILEON, subject_user=owner, status=ConcernReview.Status.OPEN
    ).exists()
    assert not Notification.objects.filter(
        recipient=owner, kind=Notification.Kind.FORMATIVE_NOTE
    ).exists()


# --- R2: weekly dissent idempotency + crash-resume --------------------------------------------


@pytest.mark.django_db
def test_weekly_dissent_second_run_same_week_is_noop(place, activity_type, settings):
    _dissent_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="dd")
    post = social.post_to_thread(owner, activity, "hi")
    _dissent(post, members[:2])
    base = timezone.make_aware(datetime(2026, 7, 13, 9, 0))  # a Monday
    sentiment.recompute_post_sentiment(now=base)
    footer = PostSentimentFooter.objects.get(post=post)
    assert footer.dissent_consecutive_hits == 1
    # A later run the SAME ISO week (daily gate passes, weekly does not) leaves the window put.
    sentiment.recompute_post_sentiment(now=base + timedelta(hours=25))
    footer.refresh_from_db()
    assert footer.dissent_consecutive_hits == 1


@pytest.mark.django_db
def test_weekly_dissent_crash_resume_skips_already_windowed(place, activity_type, settings):
    # Simulate a crash mid-loop: post A was advanced this week before the crash (its footer carries
    # this week's key), the weekly marker was NOT yet stamped (stamp lands after the loop). On the
    # resumed run, A is skipped (window_key match) and only B is freshly evaluated.
    _dissent_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="cr")
    pa = social.post_to_thread(owner, activity, "a")
    pb = social.post_to_thread(owner, activity, "b")
    _dissent(pa, members[:2])
    _dissent(pb, members[:2])
    base = timezone.make_aware(datetime(2026, 7, 13, 9, 0))
    week = sentiment._iso_week_key(base)
    PostSentimentFooter.objects.create(post=pa, dissent_window_key=week, dissent_consecutive_hits=1)
    sentiment.recompute_post_sentiment(now=base)
    fa = PostSentimentFooter.objects.get(post=pa)
    fb = PostSentimentFooter.objects.get(post=pb)
    assert fa.dissent_consecutive_hits == 1  # skipped — not double-counted
    assert fb.dissent_consecutive_hits == 1  # freshly evaluated on resume


# --- R6: graduation survives the 90-day purge -------------------------------------------------


@pytest.mark.django_db
def test_below_k_after_retention_graduates_not_unlatch(place, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 2
    settings.REACTION_ROW_RETENTION_DAYS = 90
    owner, activity, members = _activity(place, activity_type, n_members=3, tag="grad")
    post = social.post_to_thread(owner, activity, "hi")
    _react(post, members[:2], FACETS[0])
    sentiment.recompute_post_sentiment(now=timezone.now())
    footer = PostSentimentFooter.objects.get(post=post)
    # Backdate the latched-since past the retention window, then drop below k (a reactor erases).
    old = (timezone.now() - timedelta(days=100)).date().isoformat()
    footer.appreciation_slugs = [[FACETS[0], old]]
    footer.save(update_fields=["appreciation_slugs"])
    PostReaction.objects.filter(post=post, user=members[0]).delete()  # count=1 < k=2
    sentiment.recompute_post_sentiment(now=timezone.now() + timedelta(hours=25))
    footer.refresh_from_db()
    # Its supporting rows have aged into the purge window -> graduate rather than unlatch.
    assert footer.appreciation_permanent == [FACETS[0]]
    assert footer.appreciation_slugs == []
    assert _lines(post, owner) == [social.REACTION_FACETS[FACETS[0]][2]]


# --- R7: muted author consumes the cap (mute-independent) --------------------------------------


@pytest.mark.django_db
def test_muted_author_consumes_cap_no_delivery(place, activity_type, settings):
    _concern_settings(settings)
    owner, activity, members = _activity(place, activity_type, n_members=4, tag="mu")
    set_muted_kinds(owner, [Notification.Kind.FORMATIVE_NOTE])  # author opts out of the note kind
    post = social.post_to_thread(owner, activity, "hi")
    _concern(post, members[:2], when=timezone.now())
    sentiment.evaluate_concerns(now=timezone.now())
    # No note delivered (muted), but the attempt is CONSUMED: the state is stamped and the muted
    # attempt is audited distinctly.
    assert not Notification.objects.filter(
        recipient=owner, kind=Notification.Kind.FORMATIVE_NOTE
    ).exists()
    state = PostConcernState.objects.get(post=post)
    assert state.note_sent_at is not None
    assert AuditLog.objects.filter(event="concern.formative_note_muted").exists()
    assert not AuditLog.objects.filter(event="concern.formative_note_sent").exists()
    # The rolling cross-post cap still holds (mute-independent): a second post can't re-trigger.
    p2 = social.post_to_thread(owner, activity, "two")
    _concern(p2, members[:2], when=timezone.now())
    sentiment.evaluate_concerns(now=timezone.now() + timedelta(hours=25))
    assert not PostConcernState.objects.filter(post=p2, note_sent_at__isnull=False).exists()
