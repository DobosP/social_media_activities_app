"""ADR-0029 rung 1/2 write gates: ``toggle_dissent`` ("I see this differently") and
``record_concern`` ("This doesn't seem to fit here") share ``_thread_write_gate`` with
``toggle_reaction``/``post_to_thread``, so neither can become a side door weaker than posting.
These tests pin GATE PARITY (non-member, guardian, unconsented minor, hidden post, frozen thread,
blocked-vs-owner, rate limit — mirroring test_reactions.py's structure) plus the CHILD-flagger
rejection unique to dissent/concern (the appreciation picker stays open to a child; dissent/concern
do not), and the same gate holding identically on a GROUP thread owner object."""

import pytest
from django.utils import timezone

from apps.accounts.models import AgeBand, Cohort
from apps.communities.models import Area
from apps.safety.services import block_user
from apps.social import services as social
from apps.social.models import Activity, Membership, PostConcern, PostDissent

from .conftest import make_user


def _setup(place, activity_type):
    owner = make_user("dc_owner")
    member = make_user("dc_member")
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return owner, member, activity


ACTIONS = ["toggle_dissent", "record_concern"]


# --- toggle semantics -------------------------------------------------------------------------


@pytest.mark.django_db
def test_dissent_toggle_adds_then_removes(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    assert social.toggle_dissent(member, post) is True
    assert PostDissent.objects.filter(post=post, user=member).exists()
    assert social.toggle_dissent(member, post) is False
    assert not PostDissent.objects.filter(post=post, user=member).exists()


@pytest.mark.django_db
def test_concern_toggle_adds_then_removes(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    assert social.record_concern(member, post) is True
    assert PostConcern.objects.filter(post=post, user=member).exists()
    assert social.record_concern(member, post) is False
    assert not PostConcern.objects.filter(post=post, user=member).exists()


@pytest.mark.django_db
def test_dissent_and_concern_rows_never_public_no_read_helper():
    # Neither model exposes a public/serialized read path in this module — the ONLY read surface
    # is the batched sentiment_footer_for output (asserted in test_sentiment_jobs.py); this is a
    # documentation-anchor test so a future PR can't accidentally bolt on a per-row read helper
    # without a reviewer noticing the assertion break.
    assert not hasattr(social, "post_dissents_for")
    assert not hasattr(social, "post_concerns_for")


# --- gate parity: non-member / guardian / unconsented minor / hidden / frozen / blocked --------


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_gate_non_member_cannot_write(place, activity_type, action):
    owner, member, activity = _setup(place, activity_type)
    outsider = make_user("dc_out")
    post = social.post_to_thread(owner, activity, "hi")
    fn = getattr(social, action)
    with pytest.raises(social.NotAMember):
        fn(outsider, post)


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_gate_guardian_cannot_write(place, activity_type, action):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    guardian = make_user("dc_grd")
    Membership.objects.create(
        activity=activity,
        user=guardian,
        role=Membership.Role.GUARDIAN,
        state=Membership.State.MEMBER,
    )
    fn = getattr(social, action)
    with pytest.raises(social.NotEligible):
        fn(guardian, post)


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_gate_unconsented_minor_cannot_write(place, activity_type, action):
    # An UNDER_16 member with no ACTIVE ParentalConsent fails can_participate() inside the SHARED
    # gate before either function's own CHILD-cohort check is ever reached.
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    no_consent = make_user("dc_noconsent", AgeBand.UNDER_16, consented=False)
    Membership.objects.create(
        activity=activity,
        user=no_consent,
        role=Membership.Role.MEMBER,
        state=Membership.State.MEMBER,
    )
    fn = getattr(social, action)
    with pytest.raises(social.NotEligible):
        fn(no_consent, post)


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_gate_hidden_post_cannot_write(place, activity_type, action):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "secret")
    social.delete_own_post(owner, post)
    fn = getattr(social, action)
    with pytest.raises(social.InvalidState):
        fn(member, post)


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_gate_frozen_thread_cannot_write(place, activity_type, action):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    activity.status = Activity.Status.CANCELLED
    activity.save(update_fields=["status"])
    fn = getattr(social, action)
    with pytest.raises(social.InvalidState):
        fn(member, post)


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_gate_blocked_vs_owner_cannot_write(place, activity_type, action):
    # A block leaves Membership intact, so the gate must re-check it (parity with post_to_thread /
    # toggle_reaction) — otherwise a blocked member's dissent/concern would still reach the owner.
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    block_user(member, owner)
    fn = getattr(social, action)
    with pytest.raises(social.InvalidState):
        fn(member, post)
    assert not PostDissent.objects.filter(post=post, user=member).exists()
    assert not PostConcern.objects.filter(post=post, user=member).exists()


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_gate_rate_limit_shared_with_reactions(place, activity_type, action, settings):
    # Dissent/concern share the "thread_react" rate budget with toggle_reaction (ADR-0029: a
    # shared budget is simpler and strictly more conservative than a separate one).
    settings.THREAD_REACT_RATE_LIMIT = 1
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    fn = getattr(social, action)
    assert fn(member, post) is True
    with pytest.raises(social.InvalidState):
        fn(member, post)


# --- CHILD flagger: unique to dissent/concern (the reaction picker stays available) -------------


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_child_flagger_rejected_but_reaction_picker_stays_open(place, activity_type, action):
    owner = make_user("dc_cowner", AgeBand.ADULT)
    child = make_user("dc_child", AgeBand.UNDER_16, consented=True)
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Kids", starts_at=timezone.now()
    )
    Activity.objects.filter(pk=activity.pk).update(cohort=Cohort.CHILD)
    activity.refresh_from_db()
    Membership.objects.create(
        activity=activity, user=child, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    post = social.post_to_thread(owner, activity, "hi")
    fn = getattr(social, action)
    with pytest.raises(social.NotEligible):
        fn(child, post)
    # ...but a CHILD can still appreciate — only the disapproval affordances are walled off.
    e = social.allowed_reactions()[0]
    assert social.toggle_reaction(child, post, e) is True


# --- group-thread variants: the same gate holds on a Group owner object ------------------------


@pytest.fixture
def area(db):
    return Area.objects.create(city="Cluj-Napoca", slug="dc-grp", name="Cluj-Napoca")


def _staff(username):
    u = make_user(username, AgeBand.ADULT)
    u.is_staff = True
    u.save(update_fields=["is_staff"])
    return u


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_group_thread_gate_parity(area, activity_type, action):
    staff = _staff("dc_gowner")
    group = social.create_group(staff, area=area, title="Cluj Group", activity_type=activity_type)
    member = make_user("dc_gmember", AgeBand.ADULT)
    social.join_group(member, group.id)
    non_member = make_user("dc_gnon", AgeBand.ADULT)
    post = social.post_to_thread(member, group, "hi")
    fn = getattr(social, action)
    # toggle semantics hold identically on a Group owner object.
    assert fn(member, post) is True
    assert fn(member, post) is False
    with pytest.raises(social.NotAMember):
        fn(non_member, post)
    # archiving a group freezes its thread exactly like a cancelled Activity.
    social.archive_group(staff, group)
    group.refresh_from_db()
    with pytest.raises(social.InvalidState):
        fn(member, post)


@pytest.mark.django_db
@pytest.mark.parametrize("action", ACTIONS)
def test_group_thread_blocked_vs_owner_cannot_write(area, activity_type, action):
    staff = _staff("dc_gbowner")
    group = social.create_group(staff, area=area, title="Cluj Group 2", activity_type=activity_type)
    member = make_user("dc_gbmember", AgeBand.ADULT)
    social.join_group(member, group.id)
    post = social.post_to_thread(member, group, "hi")
    block_user(member, staff)
    fn = getattr(social, action)
    with pytest.raises(social.InvalidState):
        fn(member, post)


# --- author parity + eligible_audience_count sanity ---------------------------------------------


@pytest.mark.django_db
def test_eligible_audience_excludes_guardians_activity_only(place, activity_type):
    owner = make_user("dc_aud_owner")
    member = make_user("dc_aud_member")
    guardian = make_user("dc_aud_guardian")
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    Membership.objects.create(
        activity=activity,
        user=guardian,
        role=Membership.Role.GUARDIAN,
        state=Membership.State.MEMBER,
    )
    # owner + member counted; the supervisory guardian is excluded.
    assert social.eligible_audience_count(activity) == 2


@pytest.mark.django_db
def test_eligible_audience_excludes_member_blocked_vs_owner(place, activity_type):
    # The 2k floor is an anonymity denominator, so it must count only members who could ACTUALLY
    # react — a member in a block with the owner is refused by _thread_write_gate, so they must not
    # pad the floor (R5).
    owner = make_user("dc_blk_owner")
    m1 = make_user("dc_blk_m1")
    m2 = make_user("dc_blk_m2")
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    for u in (m1, m2):
        Membership.objects.create(
            activity=activity, user=u, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
        )
    assert social.eligible_audience_count(activity) == 3  # owner + m1 + m2
    # A member the owner blocked (or who blocked the owner) drops out of the denominator.
    block_user(owner, m1)
    assert social.eligible_audience_count(activity) == 2


@pytest.mark.django_db
def test_eligible_audience_group_is_peer_only(area, activity_type):
    staff = _staff("dc_gaud_owner")
    group = social.create_group(staff, area=area, title="Cluj Group 3", activity_type=activity_type)
    member = make_user("dc_gaud_member", AgeBand.ADULT)
    social.join_group(member, group.id)
    # GroupMembership has no GUARDIAN role -- nothing to exclude, so this is a plain member count.
    assert social.eligible_audience_count(group) == 2
