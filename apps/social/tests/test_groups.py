"""Public Groups: persistent, cohort-pinned standing groups that REUSE the one hardened thread
stack. These tests pin the design's MUST-FIX blockers and the headline rule:

  - the roster-less-for-minors rule (no roster/count to a minor or a non-member, anywhere);
  - the count-leak fix (no serialized member count on activities OR groups, any cohort);
  - gate parity (a group thread enforces the SAME union gate as an activity thread);
  - cohort isolation on read/write/join + the DRF queryset (no id-guessing across cohorts);
  - GroupMembership has no GUARDIAN role; the private-contact wall holds (no can_connect via group);
  - cohort-change AND consent-revocation eviction; creation/curation (staff-only for minors);
  - minor group threads are announcement-only; archive freezes the thread; Community->Group linkage.
"""

import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand, Cohort
from apps.accounts.services import revoke_parental_consent
from apps.communities.models import Area, Community
from apps.safety.services import block_user
from apps.social import services as social
from apps.social.models import Group, GroupMembership, Thread
from apps.social.serializers import GroupSerializer

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _staff(username):
    u = make_user(username, AgeBand.ADULT)
    u.is_staff = True
    u.save(update_fields=["is_staff"])
    return u


@pytest.fixture
def area():
    return Area.objects.create(city="Cluj-Napoca", slug="cluj-grp", name="Cluj-Napoca")


def _adult_group(owner, area, activity_type, **kw):
    """Create an ADULT group. The owner is staff (staff may always create), so creation does not
    depend on the GROUPS_ALLOW_USER_CREATED flag."""
    return social.create_group(
        owner, area=area, title="Cluj Basketball", activity_type=activity_type, **kw
    )


# --- model shape: GroupMembership has NO GUARDIAN role; Thread.group is OneToOne + XOR ----------


def test_group_membership_role_has_no_guardian():
    # H14/Fix-1: a standing group thread is peer-only — there is no guardian-observer seat, so the
    # GUARDIAN role must not exist on GroupMembership (the post_to_thread role check stays as
    # belt-and-suspenders, but a GUARDIAN can never be minted here).
    assert "guardian" not in {v for v, _ in GroupMembership.Role.choices}
    assert set(GroupMembership.Role.values) == {"owner", "member"}


def test_group_thread_is_onetoone_and_owner_object_resolves(area, activity_type):
    staff = _staff("g_o2o")
    group = _adult_group(staff, area, activity_type)
    # group.thread is the Thread object (OneToOne reverse), not a manager.
    assert isinstance(group.thread, Thread)
    assert group.thread.activity is None
    assert group.thread.owner_object == group


def test_thread_xor_constraint_rejects_two_owners(area, activity_type):
    from django.db import IntegrityError, transaction

    staff = _staff("g_xor")
    group = _adult_group(staff, area, activity_type)
    activity = social.create_activity(
        staff, place=_place(), activity_type=activity_type, title="A", starts_at=timezone.now()
    )
    with pytest.raises(IntegrityError), transaction.atomic():
        Thread.objects.create(activity=activity, group=group)  # both owners -> XOR violation


def _place():
    from django.contrib.gis.geos import Point

    from apps.places.models import Place

    return Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


# --- cohort isolation on read / join / DRF ------------------------------------------------------


def test_cohort_wall_on_thread_read(area, activity_type):
    staff = _staff("g_read_owner")
    group = _adult_group(staff, area, activity_type)
    member = make_user("g_read_member", AgeBand.ADULT)
    social.join_group(member, group.id)
    non_member = make_user("g_read_nonmember", AgeBand.ADULT)
    child = make_user("g_read_child", AgeBand.UNDER_16, consented=True)

    assert social.can_read_thread(member, group) is True
    assert social.can_read_thread(non_member, group) is False  # not a member
    assert social.can_read_thread(child, group) is False  # cross-cohort (CHILD vs ADULT)


def test_join_is_cohort_walled_and_idempotent(area, activity_type):
    staff = _staff("g_join_owner")
    group = _adult_group(staff, area, activity_type)
    child = make_user("g_join_child", AgeBand.UNDER_16, consented=True)
    # A cross-cohort user can't even resolve the group (visible_groups is cohort-walled) -> 404-ish.
    with pytest.raises(social.NotAMember):
        social.join_group(child, group.id)

    member = make_user("g_join_member", AgeBand.ADULT)
    m1 = social.join_group(member, group.id)
    m2 = social.join_group(member, group.id)  # idempotent: same row, no duplicate
    assert m1.pk == m2.pk
    assert group.memberships.filter(user=member, state=GroupMembership.State.MEMBER).count() == 1


def test_drf_groupviewset_is_cohort_walled_no_id_guessing(area, activity_type):
    # C18: a CHILD GET on an ADULT group by id must 404 (get_queryset sources from visible_groups,
    # never Group.objects.all()).
    staff = _staff("g_drf_owner")
    group = _adult_group(staff, area, activity_type)
    child = make_user("g_drf_child", AgeBand.UNDER_16, consented=True)
    client = APIClient()
    client.force_authenticate(child)
    assert client.get(f"/api/social/groups/{group.id}/").status_code == 404
    assert client.get("/api/social/groups/").json() == []  # list is empty for the child cohort
    # roster action is also gated (not a member / wrong cohort) -> 403/404, never a count.
    assert client.get(f"/api/social/groups/{group.id}/roster/").status_code in (403, 404)


def test_group_non_integer_pk_404s_not_500(adult):
    # A non-numeric pk must 404 at routing (lookup_value_regex), never reach group_by_id's
    # .filter(pk="abc") and 500.
    client = APIClient()
    client.force_authenticate(adult)
    assert client.get("/api/social/groups/not-a-number/").status_code == 404


# --- gate parity: a group thread enforces the same union as an activity thread ------------------


def test_group_thread_post_gate_parity(area, activity_type):
    staff = _staff("g_gate_owner")
    group = _adult_group(staff, area, activity_type)
    member = make_user("g_gate_member", AgeBand.ADULT)
    social.join_group(member, group.id)
    non_member = make_user("g_gate_nonmember", AgeBand.ADULT)

    # member can post
    post = social.post_to_thread(member, group, "hello group")
    assert post.thread.group_id == group.id
    # non-member cannot post (same NotAMember gate as an activity)
    with pytest.raises(social.NotAMember):
        social.post_to_thread(non_member, group, "let me in")
    # blocked-vs-owner cannot post
    block_user(member, staff)
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, group, "still here?")


def test_archived_group_thread_is_frozen(area, activity_type):
    staff = _staff("g_arch_owner")
    group = _adult_group(staff, area, activity_type)
    member = make_user("g_arch_member", AgeBand.ADULT)
    social.join_group(member, group.id)
    social.post_to_thread(member, group, "before archive")
    social.archive_group(staff, group)
    group.refresh_from_db()
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, group, "after archive")  # frozen
    # ...and the archived group drops out of discovery.
    assert social.visible_groups(member).filter(pk=group.id).exists() is False


# --- the headline rule: roster / count visibility per cohort ------------------------------------


def test_roster_is_none_for_minors_and_nonmembers_list_for_adult_member(area, activity_type):
    staff = _staff("g_roster_owner")
    group = _adult_group(staff, area, activity_type)
    member = make_user("g_roster_member", AgeBand.ADULT)
    social.join_group(member, group.id)
    non_member = make_user("g_roster_nonmember", AgeBand.ADULT)

    # ADULT member: a list (owner + member), and the count is its length.
    roster = social.group_roster(group, member)
    assert roster is not None
    assert member in roster and staff in roster
    assert social.group_member_count(group, member) == len(roster)

    # ADULT non-member: None (member-gated, not just cohort-gated).
    assert social.group_roster(group, non_member) is None
    assert social.group_member_count(group, non_member) is None


def test_minor_member_never_sees_a_roster_or_count(area, activity_type):
    # A CHILD group (staff-curated), with a CHILD member: the member sees NO roster and NO count.
    staff = _staff("g_minor_owner")
    group = social.create_group(
        staff, area=area, title="Kids Basketball", activity_type=activity_type, cohort=Cohort.CHILD
    )
    child = make_user("g_minor_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)
    assert social.group_roster(group, child) is None
    assert social.group_member_count(group, child) is None


def test_roster_excludes_blocked_offcohort_and_ineligible_members(area, activity_type):
    # C7/H6 defence-in-depth: the roster excludes blocked pairs, members whose cohort drifted off
    # group.cohort, and ineligible (inactive/consent-lapsed) members — even if eviction was missed.
    staff = _staff("g_def_owner")
    group = _adult_group(staff, area, activity_type)
    viewer = make_user("g_def_viewer", AgeBand.ADULT)
    blocked = make_user("g_def_blocked", AgeBand.ADULT)
    drifted = make_user("g_def_drifted", AgeBand.ADULT)
    inactive = make_user("g_def_inactive", AgeBand.ADULT)
    for u in (viewer, blocked, drifted, inactive):
        social.join_group(u, group.id)

    block_user(viewer, blocked)
    # Simulate a MISSED eviction: flip a member's cohort directly without the eviction sweep.
    drifted.cohort = Cohort.TEEN
    drifted.save(update_fields=["cohort"])
    inactive.is_active = False
    inactive.save(update_fields=["is_active"])

    roster = social.group_roster(group, viewer)
    assert blocked not in roster  # block-filtered both ways
    assert drifted not in roster  # off-cohort row never surfaces (symmetric with can_read_thread)
    assert inactive not in roster  # ineligible filtered
    assert viewer in roster and staff in roster


def test_group_serializer_emits_no_count_or_roster_for_any_cohort(area, activity_type):
    staff = _staff("g_ser_owner")
    group = _adult_group(staff, area, activity_type)
    keys = set(GroupSerializer(group).data.keys())
    forbidden = {
        "member_count",
        "members",
        "roster",
        "participants",
        "who_else",
        "member_n",
        "participant_n",
        "open_positions",
        "count",
        "n",
    }
    assert keys.isdisjoint(forbidden), (
        f"GroupSerializer leaks a count/roster key: {keys & forbidden}"
    )
    # No field name ends in _count / _n either.
    assert not any(k.endswith("_count") or k.endswith("_n") for k in keys)


def test_activity_serializer_no_longer_emits_member_count(area, activity_type):
    # C12: the pre-existing per-activity member_count vanity surface is removed for ALL cohorts.
    from apps.social.serializers import ActivitySerializer

    assert "member_count" not in ActivitySerializer().fields
    assert "open_positions" in ActivitySerializer().fields  # functional capacity info stays


# --- minor group threads are announcement-only (C2) ---------------------------------------------


def test_minor_group_thread_is_announcement_only(area, activity_type):
    staff = _staff("g_anno_owner")
    group = social.create_group(
        staff, area=area, title="Kids Reading", activity_type=activity_type, cohort=Cohort.CHILD
    )
    child = make_user("g_anno_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)
    # A minor member cannot peer-post (announcement-only) — collapses the enumeration surface.
    with pytest.raises(social.NotEligible):
        social.post_to_thread(child, group, "hi everyone")
    # ...but the staff owner can broadcast an announcement (the only write into a minor thread).
    post = social.post_announcement(staff, group, "Welcome — meet at the library.")
    assert post.is_announcement is True


# --- the private-contact wall: a group can never unlock can_connect -----------------------------


def test_group_co_membership_does_not_enable_can_connect(area, activity_type, settings):
    from apps.connections import services as connections

    settings.CONNECTIONS_ALLOWED_COHORTS = ["adult"]
    staff = _staff("g_conn_owner")
    group = _adult_group(staff, area, activity_type)
    a = make_user("g_conn_a", AgeBand.ADULT)
    b = make_user("g_conn_b", AgeBand.ADULT)
    social.join_group(a, group.id)
    social.join_group(b, group.id)
    # Co-membership in a Group is NOT a shared PEER activity (shares_activity queries Membership
    # only), so it never satisfies can_connect — joining a group can't open a private DM.
    assert connections.shares_activity(a, b) is False
    assert connections.can_connect(a, b) is False


# --- eviction: cohort change AND consent revocation ---------------------------------------------


def test_cohort_change_evicts_from_groups(area, activity_type):
    from apps.accounts.identity.base import AssuranceResult
    from apps.accounts.services import apply_assurance

    staff = _staff("g_evict_owner")
    group = _adult_group(staff, area, activity_type)
    member = make_user("g_evict_member", AgeBand.ADULT)
    social.join_group(member, group.id)
    assert group.memberships.filter(user=member, state=GroupMembership.State.MEMBER).exists()
    # Re-verify the member into a younger band -> cohort change -> eviction sweep.
    apply_assurance(member, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    member.refresh_from_db()
    m = group.memberships.get(user=member)
    assert m.state == GroupMembership.State.REMOVED


def test_consent_revocation_evicts_from_groups(area, activity_type):
    # H6: consent revocation does NOT change cohort, so it must wire group eviction separately.
    from apps.accounts.services import grant_parental_consent, link_guardian

    staff = _staff("g_consent_owner")
    # Build a CHILD member with an active guardian/consent so revocation is meaningful.
    guardian = _staff("g_consent_guardian")  # an adult guardian
    child = make_user("g_consent_child", AgeBand.UNDER_16, consented=False)
    link_guardian(guardian, child)
    grant_parental_consent(guardian, child)
    child.refresh_from_db()
    group = social.create_group(
        staff, area=area, title="Kids Chess", activity_type=activity_type, cohort=Cohort.CHILD
    )
    social.join_group(child, group.id)
    assert group.memberships.filter(user=child, state=GroupMembership.State.MEMBER).exists()
    revoke_parental_consent(guardian, child)
    assert group.memberships.get(user=child).state == GroupMembership.State.REMOVED


# --- creation / curation rules ------------------------------------------------------------------


def test_non_staff_adult_cannot_create_without_flag(area, activity_type, settings):
    settings.GROUPS_ALLOW_USER_CREATED = False
    adult = make_user("g_create_adult", AgeBand.ADULT)
    with pytest.raises(social.NotEligible):
        social.create_group(adult, area=area, title="Mine", activity_type=activity_type)


def test_non_staff_adult_can_create_with_flag(area, activity_type, settings):
    settings.GROUPS_ALLOW_USER_CREATED = True
    adult = make_user("g_create_adult2", AgeBand.ADULT)
    group = social.create_group(adult, area=area, title="Mine", activity_type=activity_type)
    assert group.cohort == Cohort.ADULT
    assert group.owner_id == adult.id
    assert group.memberships.get(user=adult).role == GroupMembership.Role.OWNER


def test_minor_cannot_own_a_group_and_minor_groups_are_staff_only(area, activity_type, settings):
    settings.GROUPS_ALLOW_USER_CREATED = True  # even with self-creation on...
    child = make_user("g_create_child", AgeBand.UNDER_16, consented=True)
    with pytest.raises(social.NotEligible):
        social.create_group(child, area=area, title="Kids", activity_type=activity_type)
    # A non-staff adult cannot create a MINOR group either (cross-cohort creation blocked).
    adult = make_user("g_create_adult3", AgeBand.ADULT)
    with pytest.raises(social.NotEligible):
        social.create_group(
            adult, area=area, title="Kids", activity_type=activity_type, cohort=Cohort.CHILD
        )


def test_visible_groups_excludes_anon_unassigned_hidden(area, activity_type):
    from django.contrib.auth.models import AnonymousUser

    staff = _staff("g_vis_owner")
    group = _adult_group(staff, area, activity_type)
    assert social.visible_groups(AnonymousUser()).count() == 0
    unassigned = make_user("g_vis_unassigned", AgeBand.ADULT)
    unassigned.cohort = Cohort.UNASSIGNED
    unassigned.save(update_fields=["cohort"])
    assert social.visible_groups(unassigned).count() == 0
    # A moderator REMOVE (is_hidden) drops the group from discovery.
    group.is_hidden = True
    group.save(update_fields=["is_hidden"])
    member = make_user("g_vis_member", AgeBand.ADULT)
    assert social.visible_groups(member).filter(pk=group.id).exists() is False


# --- moderation reuse + Community linkage -------------------------------------------------------


def test_take_action_remove_hides_group_and_resolves_owner(area, activity_type):
    from apps.safety.models import ModerationAction, ReasonCode
    from apps.safety.services import _affected_user, take_action

    staff = _staff("g_mod_owner")
    group = _adult_group(staff, area, activity_type)
    assert _affected_user(group) == staff  # owner resolves for the DSA Art.17 notice
    moderator = _staff("g_mod_moderator")
    take_action(moderator, group, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    group.refresh_from_db()
    assert group.is_hidden is True


def test_community_group_linkage_is_cohort_walled(area, activity_type):
    # M8: the "join the standing group" link is sourced from visible_groups, so a CHILD viewing a
    # CHILD community whose coordinate ALSO has an ADULT group sees no link to the adult group.
    staff = _staff("g_link_owner")
    adult_group = _adult_group(staff, area, activity_type)
    # A published CHILD community on the same coordinate.
    child_comm = Community.objects.create(
        cohort=Cohort.CHILD,
        area=area,
        category=activity_type.category,
        activity_type=activity_type,
        tier=Community.Tier.TYPE,
        slug="kids-bball-link",
        name="Kids Basketball",
        is_published=True,
    )
    child = make_user("g_link_child", AgeBand.UNDER_16, consented=True)
    # The child viewer must NOT discover the adult group via the community card.
    assert social.linked_group_for_community(child_comm, child) is None
    # An adult viewing an adult community on the same coordinate DOES see the link.
    adult_comm = Community.objects.create(
        cohort=Cohort.ADULT,
        area=area,
        category=activity_type.category,
        activity_type=activity_type,
        tier=Community.Tier.TYPE,
        slug="adult-bball-link",
        name="Cluj Basketball",
        is_published=True,
    )
    adult = make_user("g_link_adult", AgeBand.ADULT)
    assert social.linked_group_for_community(adult_comm, adult) == adult_group


# --- review-remediation regressions -------------------------------------------------------------


def test_staff_curator_can_manage_their_minor_group(area, activity_type):
    # Findings 2/3/6: the adult staff curator of a CHILD group must be able to RETRIEVE, ANNOUNCE,
    # and ARCHIVE it, even though the cohort wall keeps them out of ordinary discovery.
    staff = _staff("g_curate_owner")
    group = social.create_group(
        staff, area=area, title="Kids Football", activity_type=activity_type, cohort=Cohort.CHILD
    )
    # group_by_id gives staff a curation bypass (discovery stays cohort-walled).
    assert social.group_by_id(group.id, staff) == group
    # ...but a NON-staff adult still can't reach a CHILD group by id.
    adult = make_user("g_curate_adult", AgeBand.ADULT)
    assert social.group_by_id(group.id, adult) is None
    # The curator can announce (the only write into a minor announcement-only thread) and archive.
    social.post_announcement(staff, group, "Welcome — meet at the gate.")
    social.archive_group(staff, group)
    group.refresh_from_db()
    assert group.status == Group.Status.ARCHIVED
    # The web detail view 200s for the curator (staff read bypass), and a DRF retrieve too.
    client = APIClient()
    client.force_authenticate(staff)
    assert client.get(f"/api/social/groups/{group.id}/").status_code == 200


def test_evicted_owner_cannot_announce(area, activity_type):
    # Finding 5: an owner whose membership was evicted can no longer broadcast.
    staff = _staff("g_evictann_owner")
    group = _adult_group(staff, area, activity_type)
    social.remove_user_from_groups(staff, reason="cohort_changed")
    assert group.memberships.get(user=staff).state == GroupMembership.State.REMOVED
    with pytest.raises(social.NotAMember):
        social.post_announcement(staff, group, "I should not be able to broadcast")


def test_minor_can_report_a_group_post_via_web(area, activity_type, client):
    # Finding 7: a minor in a group thread has a working WEB report path for a post author.
    from apps.safety.models import ReasonCode, Report

    staff = _staff("g_report_owner")
    group = social.create_group(
        staff, area=area, title="Kids Chess Club", activity_type=activity_type, cohort=Cohort.CHILD
    )
    child = make_user("g_report_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)
    announcement = social.post_announcement(staff, group, "Meet at the library at 4pm.")
    client.force_login(child)
    resp = client.post(
        "/report/",
        {"type": "post", "id": announcement.id, "reason": ReasonCode.OTHER, "detail": "x"},
    )
    assert resp.status_code in (200, 302)
    assert Report.objects.filter(reporter=child).exists()


def test_erase_user_audits_owned_group_destruction(area, activity_type):
    # Finding 1: CASCADE deletes an owned group on owner erase, but the destruction is AUDITED.
    from apps.accounts.services import erase_user
    from apps.safety.models import AuditLog

    staff = _staff("g_erase_owner")
    group = social.create_group(
        staff, area=area, title="Kids Drama", activity_type=activity_type, cohort=Cohort.CHILD
    )
    gid = group.id
    erase_user(staff, staff)
    assert Group.objects.filter(pk=gid).exists() is False  # CASCADE removed it
    assert AuditLog.objects.filter(event="group.owner_erased").exists()  # ...but it was recorded


def test_gdpr_export_includes_groups(area, activity_type):
    # Finding 4: the data export discloses owned groups and group memberships.
    from apps.accounts.export import build_user_export

    staff = _staff("g_export_owner")
    group = _adult_group(staff, area, activity_type)
    member = make_user("g_export_member", AgeBand.ADULT)
    social.join_group(member, group.id)

    owner_export = build_user_export(staff)
    assert any(g["title"] == "Cluj Basketball" for g in owner_export["owned_groups"])
    member_export = build_user_export(member)
    titles = {m["group_title"] for m in member_export["group_memberships"]}
    assert "Cluj Basketball" in titles
