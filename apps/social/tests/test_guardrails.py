"""F7 — guardian-set participation guardrails: enforcement in the can_join gate.

The guardrail only ever NARROWS a CHILD ward's access. These tests drive the real join
chokepoint (can_join / request_to_join) so both the web and DRF surfaces are covered.
"""

import zoneinfo
from datetime import datetime

import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import (
    apply_assurance,
    link_guardian,
    revoke_guardian,
    set_guardian_guardrail,
)
from apps.places.models import Place
from apps.social.services import NotEligible, can_join, create_activity, request_to_join
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

TZ = zoneinfo.ZoneInfo("Europe/Bucharest")


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child_activity(owner, *, local_hour=10, guardian_accompanied=False, slug="g"):
    place = Place.objects.create(
        name=f"Hall-{slug}", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"cat-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"at-{slug}", name="Football", category=cat)
    starts_at = datetime(2026, 6, 15, local_hour, 0, tzinfo=TZ)  # local Bucharest hour
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Kids football",
        starts_at=starts_at,
        guardian_accompanied=guardian_accompanied,
    )


def test_no_guardrail_allows_join():
    owner = _child("o0")
    activity = _child_activity(owner, slug="base")
    joiner = _child("j0")
    assert can_join(joiner, activity) is True


def test_supervised_only_blocks_unaccompanied():
    owner = _child("o1")
    unaccompanied = _child_activity(owner, guardian_accompanied=False, slug="unsup")
    accompanied = _child_activity(owner, guardian_accompanied=True, slug="sup")
    joiner = _child("j1")
    guardian = _adult("p1")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, supervised_only=True)
    assert can_join(joiner, unaccompanied) is False
    assert can_join(joiner, accompanied) is True


def test_latest_start_hour_blocks_late_meetup():
    owner = _child("o2")
    early = _child_activity(owner, local_hour=9, slug="early")
    late = _child_activity(owner, local_hour=21, slug="late")
    joiner = _child("j2")
    guardian = _adult("p2")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, latest_start_hour="12")
    assert can_join(joiner, early) is True  # 09:00 local <= 12
    assert can_join(joiner, late) is False  # 21:00 local > 12


def test_latest_start_hour_uses_local_time_not_utc():
    # 13:00 Bucharest (== 10:00 UTC in June). A cap of 12 must block it on LOCAL hour (13), not
    # pass it on the UTC hour (10).
    owner = _child("o2b")
    activity = _child_activity(owner, local_hour=13, slug="tz")
    joiner = _child("j2b")
    guardian = _adult("p2b")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, latest_start_hour="12")
    assert can_join(joiner, activity) is False


def test_max_open_joins_caps_concurrent_meetups():
    owner = _child("o3")
    a1 = _child_activity(owner, slug="cap1")
    a2 = _child_activity(owner, slug="cap2")
    a3 = _child_activity(owner, slug="cap3")
    joiner = _child("j3")
    # Join two while unlimited.
    request_to_join(joiner, a1)
    request_to_join(joiner, a2)
    guardian = _adult("p3")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, max_open_joins="2")
    # Already in two open meetups -> a third is blocked.
    assert can_join(joiner, a3) is False
    with pytest.raises(NotEligible):
        request_to_join(joiner, a3)


def test_max_open_joins_allows_up_to_cap():
    owner = _child("o3b")
    a1 = _child_activity(owner, slug="capb1")
    a2 = _child_activity(owner, slug="capb2")
    joiner = _child("j3b")
    request_to_join(joiner, a1)
    guardian = _adult("p3b")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, max_open_joins="2")
    assert can_join(joiner, a2) is True  # in one, cap two -> a second is fine


def test_strictest_across_two_guardians_enforced():
    owner = _child("o4")
    late = _child_activity(owner, local_hour=19, slug="g4late")
    joiner = _child("j4")
    g1 = _adult("p4a")
    g2 = _adult("p4b")
    link_guardian(g1, joiner)
    link_guardian(g2, joiner)
    set_guardian_guardrail(g1, joiner, latest_start_hour="20")  # lax
    set_guardian_guardrail(g2, joiner, latest_start_hour="18")  # strict
    # 19:00 passes g1 (<=20) but fails g2 (<=18) -> the strictest wins, blocked.
    assert can_join(joiner, late) is False


def test_drf_join_surface_enforces_guardrail():
    # The guardrail must hold on the DRF join action too (it routes through request_to_join ->
    # can_join). A supervised_only ward gets 403 on an unaccompanied meetup, 201 on an
    # accompanied one.
    owner = _child("o_api")
    unaccompanied = _child_activity(owner, guardian_accompanied=False, slug="apiun")
    accompanied = _child_activity(owner, guardian_accompanied=True, slug="apiacc")
    joiner = _child("j_api")
    guardian = _adult("p_api")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, supervised_only=True)
    client = APIClient()
    client.force_authenticate(joiner)
    blocked = client.post(f"/api/social/activities/{unaccompanied.id}/join/")
    assert blocked.status_code == 403, blocked.content
    ok = client.post(f"/api/social/activities/{accompanied.id}/join/")
    assert ok.status_code == 201, ok.content


# --- W3-F1: family-calendar window enforcement (allowed weekdays + earliest hour) ---


def _local_weekday(activity):
    from django.utils import timezone

    return timezone.localtime(activity.starts_at).isoweekday()


def test_allowed_weekdays_blocks_other_days():
    owner = _child("o_wd")
    activity = _child_activity(owner, local_hour=10, slug="wd")
    wd = _local_weekday(activity)
    joiner = _child("j_wd")
    guardian = _adult("p_wd")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, allowed_weekdays=str(wd % 7 + 1))  # a DIFFERENT day
    assert can_join(joiner, activity) is False
    set_guardian_guardrail(guardian, joiner, allowed_weekdays=str(wd))  # the activity's own day
    assert can_join(joiner, activity) is True


def test_empty_weekdays_allow_any_day():
    owner = _child("o_wd0")
    activity = _child_activity(owner, slug="wd0")
    joiner = _child("j_wd0")
    guardian = _adult("p_wd0")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, allowed_weekdays=[])  # no weekday restriction
    assert can_join(joiner, activity) is True


def test_disjoint_weekday_allowlists_block_all():
    owner = _child("o_wdx")
    activity = _child_activity(owner, local_hour=10, slug="wdx")
    wd = _local_weekday(activity)
    joiner = _child("j_wdx")
    g1, g2 = _adult("p_wdx1"), _adult("p_wdx2")
    link_guardian(g1, joiner)
    link_guardian(g2, joiner)
    set_guardian_guardrail(g1, joiner, allowed_weekdays=str(wd))  # allows the activity's day
    set_guardian_guardrail(g2, joiner, allowed_weekdays=str(wd % 7 + 1))  # allows a different day
    assert can_join(joiner, activity) is False  # empty intersection -> nothing passes


def test_earliest_start_hour_blocks_early_meetup():
    owner = _child("o_eh")
    early = _child_activity(owner, local_hour=7, slug="eh_e")
    ok = _child_activity(owner, local_hour=10, slug="eh_o")
    joiner = _child("j_eh")
    guardian = _adult("p_eh")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, earliest_start_hour="9")
    assert can_join(joiner, early) is False  # 07:00 local < 9
    assert can_join(joiner, ok) is True  # 10:00 local >= 9


def test_revoked_guardian_guardrail_no_longer_blocks():
    owner = _child("o5")
    late = _child_activity(owner, local_hour=22, slug="g5")
    joiner = _child("j5")
    guardian = _adult("p5")
    link_guardian(guardian, joiner)
    set_guardian_guardrail(guardian, joiner, latest_start_hour="12")
    assert can_join(joiner, late) is False
    revoke_guardian(guardian, joiner)
    # NOTE: revoke_guardian also revokes that guardian's consent. Give the ward a standing
    # consent from another identifier so can_participate stays True and we isolate the guardrail.
    ParentalConsent.objects.create(
        minor=joiner, guardian_identifier="other", status=ParentalConsent.Status.ACTIVE
    )
    assert can_join(joiner, late) is True
