"""F22 — Co-organizer seat + graceful ownership handoff.

An owner can grant a current adult member co-organiser rights (the *operational* owner-powers:
edit / cancel / admit / announce) and can cleanly hand the activity over so a thriving meetup
survives the organiser stepping down (and an owner can leave before a GDPR erasure CASCADE
destroys an evidence-bearing thread). The *meta*-powers — grant / revoke / transfer — stay
owner-only, so a co-organiser can never lock the owner out. The whole feature is structurally
impossible on CHILD/TEEN cohorts (no peer organiser handoff on a minor activity).
"""

from datetime import timedelta

import pytest
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.safety.models import AuditLog
from apps.social import services as social
from apps.social.models import Activity, Membership
from apps.social.services import (
    InvalidState,
    NotAMember,
    cancel_activity,
    create_activity,
    grant_co_organizer,
    is_organizer,
    owner_admit,
    post_announcement,
    request_to_join,
    revoke_co_organizer,
    transfer_ownership,
    update_activity,
)

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, starts_at, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Pickup game",
        starts_at=starts_at,
        **kw,
    )


def _member(activity, user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER):
    return activity.memberships.create(user=user, role=role, state=state)


def _web(user):
    c = Client()
    c.force_login(user)
    return c


# --- is_organizer gate ------------------------------------------------------------------


def test_is_organizer_true_for_owner_and_co_org_false_for_member(
    adult, adult2, place, activity_type, now
):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    assert is_organizer(adult, activity) is True
    assert is_organizer(adult2, activity) is False
    grant_co_organizer(adult, activity, adult2)
    assert is_organizer(adult2, activity) is True


def test_is_organizer_false_for_non_member_and_anonymous(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    assert is_organizer(adult2, activity) is False  # not a member at all
    assert is_organizer(None, activity) is False  # anonymous / no user


# --- grant ------------------------------------------------------------------------------


def test_grant_co_organizer_sets_role_notifies_and_audits(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    m = grant_co_organizer(adult, activity, adult2)
    assert m.role == Membership.Role.CO_ORGANIZER
    note = Notification.objects.get(recipient=adult2, kind=Notification.Kind.ORGANIZER_ROLE)
    assert "co-organiser" in note.title.lower()
    assert AuditLog.objects.filter(event="activity.co_organizer_granted").exists()


def test_grant_is_idempotent_and_does_not_renotify(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    grant_co_organizer(adult, activity, adult2)
    grant_co_organizer(adult, activity, adult2)  # second grant is a no-op
    assert (
        Notification.objects.filter(recipient=adult2, kind=Notification.Kind.ORGANIZER_ROLE).count()
        == 1
    )


def test_grant_rejected_for_non_owner(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    with pytest.raises(NotAMember):
        grant_co_organizer(adult2, activity, adult2)


def test_co_organizer_cannot_grant_or_transfer(adult, adult2, place, activity_type, now):
    """A co-organiser holds the operational powers but NEVER the meta-powers — it can never
    create more co-organisers or seize ownership (the anti-lockout invariant)."""
    third = make_user("adult3", AgeBand.ADULT)
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    _member(activity, third)
    grant_co_organizer(adult, activity, adult2)
    with pytest.raises(NotAMember):
        grant_co_organizer(adult2, activity, third)
    with pytest.raises(NotAMember):
        transfer_ownership(adult2, activity, third)
    with pytest.raises(NotAMember):
        revoke_co_organizer(adult2, activity, adult2)


def test_grant_rejected_for_non_member(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    with pytest.raises(NotAMember):
        grant_co_organizer(adult, activity, adult2)


def test_grant_rejected_for_guardian(adult, place, activity_type, now):
    """A supervisory guardian can never become a co-organiser (excluded like in voting_members)."""
    child_owner = make_user("childown", AgeBand.UNDER_16, consented=True)
    from apps.accounts.services import link_guardian

    guardian = make_user("guard", AgeBand.ADULT)
    link_guardian(guardian, child_owner)
    activity = _activity(
        child_owner, place, activity_type, now + timedelta(hours=2), guardian_accompanied=True
    )
    social.add_guardian(child_owner, activity, guardian)
    # Even setting cohort aside, a CHILD activity refuses all peer organiser handoff:
    with pytest.raises(InvalidState):
        grant_co_organizer(child_owner, activity, guardian)


# --- co-organiser inherits the operational owner powers ---------------------------------


def test_co_organizer_can_cancel_edit_admit_announce(adult, adult2, place, activity_type, now):
    third = make_user("joiner", AgeBand.ADULT)
    activity = _activity(adult, place, activity_type, now + timedelta(hours=3))
    activity.owner_can_override = True
    activity.save(update_fields=["owner_can_override"])
    _member(activity, adult2)
    grant_co_organizer(adult, activity, adult2)

    # edit (logistics) as co-organiser
    update_activity(adult2, activity, organizer_note="Bring a bib")
    activity.refresh_from_db()
    assert activity.organizer_note == "Bring a bib"

    # announce as co-organiser
    post = post_announcement(adult2, activity, "Meet at the north gate")
    assert post.is_announcement

    # admit a requested joiner as co-organiser
    req = request_to_join(third, activity)
    owner_admit(adult2, req)
    req.refresh_from_db()
    assert req.state == Membership.State.MEMBER

    # cancel as co-organiser
    cancel_activity(adult2, activity)
    activity.refresh_from_db()
    assert activity.status == Activity.Status.CANCELLED


# --- revoke -----------------------------------------------------------------------------


def test_revoke_restores_plain_member_and_notifies(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    grant_co_organizer(adult, activity, adult2)
    m = revoke_co_organizer(adult, activity, adult2)
    assert m.role == Membership.Role.MEMBER
    assert is_organizer(adult2, activity) is False
    assert (
        Notification.objects.filter(recipient=adult2, kind=Notification.Kind.ORGANIZER_ROLE).count()
        == 2  # granted, then removed
    )


def test_revoke_rejected_when_target_not_co_organizer(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)  # plain member, never a co-organiser
    with pytest.raises(NotAMember):
        revoke_co_organizer(adult, activity, adult2)


# --- transfer ---------------------------------------------------------------------------


def test_transfer_ownership_swaps_roles_and_owner_fk(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    transfer_ownership(adult, activity, adult2)
    activity.refresh_from_db()
    assert activity.owner_id == adult2.id
    assert activity.memberships.get(user=adult2).role == Membership.Role.OWNER
    assert activity.memberships.get(user=adult).role == Membership.Role.MEMBER
    # the new owner is an organiser; the old owner is now just a member
    assert is_organizer(adult2, activity) is True
    assert is_organizer(adult, activity) is False
    note = Notification.objects.get(recipient=adult2, kind=Notification.Kind.ORGANIZER_ROLE)
    assert "organiser" in note.title.lower()
    assert AuditLog.objects.filter(event="activity.ownership_transferred").exists()


def test_transfer_promotes_an_existing_co_organizer(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    grant_co_organizer(adult, activity, adult2)
    transfer_ownership(adult, activity, adult2)
    activity.refresh_from_db()
    assert activity.owner_id == adult2.id
    assert activity.memberships.get(user=adult2).role == Membership.Role.OWNER


def test_transfer_rejected_for_non_owner(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    with pytest.raises(NotAMember):
        transfer_ownership(adult2, activity, adult2)


def test_transfer_rejected_to_non_member(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    with pytest.raises(NotAMember):
        transfer_ownership(adult, activity, adult2)


def test_transfer_works_on_completed_activity(adult, adult2, place, activity_type, now):
    """The GDPR motivation: an owner must be able to hand off an evidence-bearing thread even
    after the meetup is over, so erase_user no longer CASCADE-destroys it. Transfer is NOT
    gated on OPEN."""
    activity = _activity(adult, place, activity_type, now - timedelta(hours=2))
    _member(activity, adult2)
    activity.status = Activity.Status.COMPLETED
    activity.save(update_fields=["status"])
    transfer_ownership(adult, activity, adult2)
    activity.refresh_from_db()
    assert activity.owner_id == adult2.id


# --- child-safety invariant: structurally impossible on minor cohorts -------------------


def test_minor_cohort_has_no_co_organizer_or_transfer_path(place, activity_type, now):
    """A same-cohort minor MEMBER is never staff, so peer co-org/transfer must be refused on
    CHILD and TEEN activities — there is no adult<->minor organiser path, ever."""
    for username, band in (("c1", AgeBand.UNDER_16), ("t1", AgeBand.AGE_16_17)):
        owner = make_user(f"{username}o", band, consented=band == AgeBand.UNDER_16)
        peer = make_user(f"{username}p", band, consented=band == AgeBand.UNDER_16)
        activity = _activity(owner, place, activity_type, now + timedelta(hours=2))
        _member(activity, peer)
        with pytest.raises(InvalidState):
            grant_co_organizer(owner, activity, peer)
        with pytest.raises(InvalidState):
            transfer_ownership(owner, activity, peer)


# --- web surface ------------------------------------------------------------------------


def test_web_owner_sees_manage_panel_but_co_organizer_does_not(
    adult, adult2, place, activity_type, now
):
    activity = _activity(adult, place, activity_type, now + timedelta(hours=2))
    _member(activity, adult2)
    grant_co_organizer(adult, activity, adult2)
    owner_page = _web(adult).get(f"/activities/{activity.id}/").content.decode()
    assert "Make co-organiser" in owner_page or "Remove co-organiser" in owner_page
    assert "Hand over" in owner_page
    co_org_page = _web(adult2).get(f"/activities/{activity.id}/").content.decode()
    # the co-organiser sees the operational tools but never the owner-only management panel
    assert "Hand over" not in co_org_page
    assert "Organiser tools" in co_org_page  # edit/cancel card is theirs


def test_web_grant_then_revoke_then_transfer(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now + timedelta(hours=2))
    _member(activity, adult2)
    owner = _web(adult)
    owner.post(f"/activities/{activity.id}/co-org/grant/", {"user_id": adult2.id})
    assert activity.memberships.get(user=adult2).role == Membership.Role.CO_ORGANIZER
    owner.post(f"/activities/{activity.id}/co-org/revoke/", {"user_id": adult2.id})
    assert activity.memberships.get(user=adult2).role == Membership.Role.MEMBER
    owner.post(f"/activities/{activity.id}/transfer/", {"user_id": adult2.id})
    activity.refresh_from_db()
    assert activity.owner_id == adult2.id


def test_web_non_owner_cannot_grant(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now + timedelta(hours=2))
    _member(activity, adult2)
    _web(adult2).post(f"/activities/{activity.id}/co-org/grant/", {"user_id": adult2.id})
    assert activity.memberships.get(user=adult2).role == Membership.Role.MEMBER  # unchanged


# --- DRF surface ------------------------------------------------------------------------


def test_api_grant_revoke_transfer(adult, adult2, place, activity_type, now):
    from rest_framework.test import APIClient

    activity = _activity(adult, place, activity_type, now + timedelta(hours=2))
    _member(activity, adult2)
    client = APIClient()
    client.force_authenticate(adult)
    base = f"/api/social/activities/{activity.id}"

    r = client.post(f"{base}/grant_organizer/", {"user_id": adult2.id}, format="json")
    assert r.status_code == 200, r.content
    assert activity.memberships.get(user=adult2).role == Membership.Role.CO_ORGANIZER

    r = client.post(f"{base}/revoke_organizer/", {"user_id": adult2.id}, format="json")
    assert r.status_code == 200, r.content
    assert activity.memberships.get(user=adult2).role == Membership.Role.MEMBER

    r = client.post(f"{base}/transfer/", {"user_id": adult2.id}, format="json")
    assert r.status_code == 200, r.content
    activity.refresh_from_db()
    assert activity.owner_id == adult2.id


def test_api_non_owner_grant_is_forbidden(adult, adult2, place, activity_type, now):
    from rest_framework.test import APIClient

    activity = _activity(adult, place, activity_type, now + timedelta(hours=2))
    _member(activity, adult2)
    client = APIClient()
    client.force_authenticate(adult2)
    r = client.post(
        f"/api/social/activities/{activity.id}/grant_organizer/",
        {"user_id": adult2.id},
        format="json",
    )
    assert r.status_code == 403


def test_api_non_numeric_user_id_is_400_not_500(adult, adult2, place, activity_type, now):
    """A malformed user_id must degrade to a clean 400, never reach the ORM as an invalid literal
    and surface as a 500 (the web surface already guards this with isdigit)."""
    from rest_framework.test import APIClient

    activity = _activity(adult, place, activity_type, now + timedelta(hours=2))
    _member(activity, adult2)
    client = APIClient()
    client.force_authenticate(adult)
    for verb in ("grant_organizer", "revoke_organizer", "transfer"):
        r = client.post(
            f"/api/social/activities/{activity.id}/{verb}/",
            {"user_id": "not-a-number"},
            format="json",
        )
        assert r.status_code == 400, (verb, r.status_code, r.content)


# --- review fix: live-cohort re-check (no adult<->minor organiser path via a downgraded seat) ---


def test_grant_and_transfer_reject_a_member_downgraded_to_a_minor_cohort(
    adult, adult2, place, activity_type, now
):
    """The cohort wall is enforced at READ time, so a member re-verified ADULT->minor after joining
    keeps a stale MEMBER row on the (immutable-cohort) ADULT activity. Promotion to organiser/owner
    must re-check the LIVE cohort so a downgraded seat can never open an adult<->minor organiser."""
    activity = _activity(adult, place, activity_type, now + timedelta(hours=2))
    _member(activity, adult2)
    apply_assurance(adult2, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    adult2.refresh_from_db()
    assert adult2.cohort != activity.cohort  # now TEEN; the activity stays ADULT
    with pytest.raises(NotAMember):
        grant_co_organizer(adult, activity, adult2)
    with pytest.raises(NotAMember):
        transfer_ownership(adult, activity, adult2)


# --- review fix: single-owner invariant under a stale-owner / concurrent transfer ----------------


def test_transfer_keeps_one_owner_and_refuses_a_stale_owner_retry(
    adult, adult2, place, activity_type, now
):
    """After a hand-off there must be exactly one OWNER membership, and the former owner — who still
    holds a stale in-memory Activity (owner_id unchanged on the caller's object) — is re-checked
    against the locked row, so a second transfer can't mint a split-brain second OWNER row."""
    third = make_user("adult3b", AgeBand.ADULT)
    activity = _activity(adult, place, activity_type, now + timedelta(hours=2))
    _member(activity, adult2)
    _member(activity, third)
    transfer_ownership(
        adult, activity, adult2
    )  # `activity` object is now stale (owner_id == adult)
    fresh = Activity.objects.get(pk=activity.pk)
    assert fresh.memberships.filter(role=Membership.Role.OWNER).count() == 1
    assert fresh.owner_id == adult2.id
    # the former owner retries with the stale handle: the locked re-read refuses it
    with pytest.raises(NotAMember):
        transfer_ownership(adult, activity, third)
    assert (
        Activity.objects.get(pk=activity.pk).memberships.filter(role=Membership.Role.OWNER).count()
        == 1
    )
