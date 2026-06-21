"""DSA Art.17 redress: reachable statement of reasons + the internal appeal (file / resolve).

The pre-auth web surface is covered in apps/web/tests/test_account_restricted.py; here we cover the
services (self-scope, idempotency, reversal) and the logged-in DRF surface.
"""

import datetime as dt

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.models import ModerationAction, ModerationAppeal, ReasonCode
from apps.safety.services import (
    AppealError,
    file_appeal,
    resolve_appeal,
    restriction_statement_for,
    safety_record_for,
    take_action,
)
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, staff=False, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name, is_staff=staff)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="ap-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="ap-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at=timezone.now()
    )


# --- statement of reasons -----------------------------------------------------------------


def test_statement_for_suspended_user_is_self_scoped():
    mod, user = _user("ap_mod", staff=True), _user("ap_user")
    future = timezone.now() + dt.timedelta(days=3)
    action = take_action(
        mod, user, ModerationAction.Action.SUSPEND, ReasonCode.HARASSMENT, expires_at=future
    )
    st = restriction_statement_for(user)
    assert st is not None
    assert st["action_id"] == action.id
    assert st["is_lifetime"] is False
    assert st["lifts_at"] == future
    assert st["can_appeal"] is True
    assert "ap_mod" not in str(st)  # no moderator identity


def test_statement_none_for_active_user():
    assert restriction_statement_for(_user("ap_active")) is None


def test_statement_none_for_self_deactivated_account():
    # is_active=False but NOT from moderation → reveal no (false) moderation detail.
    user = _user("ap_selfoff")
    user.is_active = False
    user.save(update_fields=["is_active"])
    assert restriction_statement_for(user) is None


def test_statement_lifetime_ban_has_no_lift_date():
    mod, user = _user("ap_mod2", staff=True), _user("ap_banned")
    take_action(mod, user, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    st = restriction_statement_for(user)
    assert st["is_lifetime"] is True
    assert st["lifts_at"] is None


# --- file_appeal --------------------------------------------------------------------------


def test_file_appeal_happy_path_trims_and_pends():
    mod, user = _user("fa_mod", staff=True), _user("fa_user")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    appeal = file_appeal(user, action, "  I did nothing wrong  ")
    assert appeal.status == ModerationAppeal.Status.PENDING
    assert appeal.statement == "I did nothing wrong"
    assert appeal.appellant == user


def test_file_appeal_rejects_empty_statement():
    mod, user = _user("fa_mod2", staff=True), _user("fa_user2")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    with pytest.raises(AppealError):
        file_appeal(user, action, "   ")
    assert ModerationAppeal.objects.count() == 0


def test_file_appeal_rejects_other_users_action():
    mod, user, other = _user("fa_mod3", staff=True), _user("fa_user3"), _user("fa_other3")
    action = take_action(mod, other, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    with pytest.raises(AppealError):
        file_appeal(user, action, "let me appeal someone else's sanction")
    assert ModerationAppeal.objects.count() == 0


def test_file_appeal_is_one_per_action():
    mod, user = _user("fa_mod4", staff=True), _user("fa_user4")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    file_appeal(user, action, "first")
    with pytest.raises(AppealError):
        file_appeal(user, action, "second")
    assert ModerationAppeal.objects.filter(action=action).count() == 1


# --- resolve_appeal -----------------------------------------------------------------------


def test_overturn_reactivates_account():
    mod, user = _user("re_mod", staff=True), _user("re_user")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    user.refresh_from_db()
    assert user.is_active is False
    appeal = file_appeal(user, action, "please review")
    resolve_appeal(mod, appeal, grant=True)
    user.refresh_from_db()
    action.refresh_from_db()
    appeal.refresh_from_db()
    assert user.is_active is True
    assert action.lifted_at is not None
    assert appeal.status == ModerationAppeal.Status.OVERTURNED


def test_uphold_keeps_restriction():
    mod, user = _user("re_mod2", staff=True), _user("re_user2")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    appeal = file_appeal(user, action, "please")
    resolve_appeal(mod, appeal, grant=False, notes="decision stands")
    user.refresh_from_db()
    appeal.refresh_from_db()
    assert user.is_active is False
    assert appeal.status == ModerationAppeal.Status.UPHELD


def test_overturn_does_not_reactivate_when_a_separate_ban_still_applies():
    mod, user = _user("re_mod3", staff=True), _user("re_user3")
    suspend = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    take_action(mod, user, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    appeal = file_appeal(user, suspend, "appeal the suspension")
    resolve_appeal(mod, appeal, grant=True)
    user.refresh_from_db()
    assert user.is_active is False  # the independent lifetime BAN still deactivates the account


def test_overturn_unhides_removed_content():
    mod, owner = _user("re_mod5", staff=True), _user("re_owner5")
    activity = _activity(owner)
    take_action(mod, activity, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    activity.refresh_from_db()
    assert activity.is_hidden is True
    action = ModerationAction.objects.get(action=ModerationAction.Action.REMOVE)
    appeal = file_appeal(owner, action, "this activity was fine")
    resolve_appeal(mod, appeal, grant=True)
    activity.refresh_from_db()
    assert activity.is_hidden is False


def test_resolve_is_idempotent():
    mod, user = _user("re_mod4", staff=True), _user("re_user4")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    appeal = file_appeal(user, action, "x")
    resolve_appeal(mod, appeal, grant=False)
    with pytest.raises(AppealError):
        resolve_appeal(mod, appeal, grant=True)


def test_safety_record_shows_appeal_status_and_action_id():
    mod, user = _user("sr_mod", staff=True), _user("sr_user")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    rec = safety_record_for(user)
    d = rec["decisions"][0]
    assert d["action_id"] == action.id
    assert d["can_appeal"] is True
    file_appeal(user, action, "contest")
    d2 = safety_record_for(user)["decisions"][0]
    assert d2["can_appeal"] is False
    assert d2["appeal_status_label"]


# --- DRF surface (logged-in) --------------------------------------------------------------


def test_drf_user_files_appeal_against_own_action():
    mod, user = _user("dr_mod", staff=True), _user("dr_user")
    # A WARN keeps the account active, so the user has a normal API session to contest with.
    action = take_action(mod, user, ModerationAction.Action.WARN, ReasonCode.SPAM)
    client = APIClient()
    client.force_authenticate(user)
    resp = client.post(
        "/api/safety/appeals/", {"action_id": action.id, "statement": "not spam"}, format="json"
    )
    assert resp.status_code == 201
    assert ModerationAppeal.objects.filter(action=action, appellant=user).exists()
    listing = client.get("/api/safety/appeals/")
    assert listing.status_code == 200
    assert len(listing.json()) == 1


def test_drf_cannot_appeal_anothers_action_404():
    mod, user, other = _user("dr_mod2", staff=True), _user("dr_user2"), _user("dr_other2")
    action = take_action(mod, other, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    client = APIClient()
    client.force_authenticate(user)
    resp = client.post(
        "/api/safety/appeals/", {"action_id": action.id, "statement": "x"}, format="json"
    )
    assert resp.status_code == 404
    assert ModerationAppeal.objects.count() == 0


def test_drf_resolve_requires_moderator():
    mod, user = _user("dr_mod3", staff=True), _user("dr_user3")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    appeal = file_appeal(user, action, "please")
    plain = APIClient()
    plain.force_authenticate(user)
    plain_resp = plain.post(f"/api/safety/moderation/appeals/{appeal.id}/resolve/", {"grant": True})
    assert plain_resp.status_code == 403
    staff = APIClient()
    staff.force_authenticate(mod)
    resp = staff.post(
        f"/api/safety/moderation/appeals/{appeal.id}/resolve/", {"grant": True}, format="json"
    )
    assert resp.status_code == 200
    user.refresh_from_db()
    assert user.is_active is True


def test_drf_moderator_queue_lists_appeals():
    mod, user = _user("dr_mod4", staff=True), _user("dr_user4")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    file_appeal(user, action, "please")
    staff = APIClient()
    staff.force_authenticate(mod)
    resp = staff.get("/api/safety/moderation/appeals/?status=pending")
    assert resp.status_code == 200
    assert len(resp.json()) == 1
    # The plain appellant must not reach the staff queue.
    plain = APIClient()
    plain.force_authenticate(user)
    assert plain.get("/api/safety/moderation/appeals/").status_code == 403
