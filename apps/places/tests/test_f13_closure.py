"""W3-F13 — 'this venue is gone' crowd closure overlay (ingest-safe).

Mirrors the F28 open-now overlay: counts-only, read-time decay, NEVER written to Place. A quorum of
recent reports hides the venue from public_places() — so discovery drops it AND the create_activity
write-gate refuses it. A still-mapped venue self-heals once reports age out or on a staff reset.
"""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place, PlaceClosureReport
from apps.places.services import (
    NotEligible,
    clear_closure_reports,
    file_closure_report,
    place_is_closed,
    public_places,
)
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)


def _user(name, *, verified=True, staff=False):
    u = User.objects.create_user(username=name, password="pw", display_name=name, is_staff=staff)
    if verified:
        apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _place(name="Gone Hall"):
    return Place.objects.create(name=name, location=PT, source=Place.Source.OSM)


def _report(place, n, *, prefix="r"):
    for i in range(n):
        file_closure_report(_user(f"{prefix}-{place.id}-{i}"), place)


def test_file_requires_can_participate():
    with pytest.raises(NotEligible):
        file_closure_report(_user("unv", verified=False), _place())


def test_file_is_idempotent_per_window():
    place, reporter = _place(), _user("rep")
    assert file_closure_report(reporter, place) is not None
    assert file_closure_report(reporter, place) is None  # same window -> deduped
    assert PlaceClosureReport.objects.filter(place=place, reporter=reporter).count() == 1


def test_rate_limited_across_venues(settings):
    settings.CLOSURE_REPORT_RATE_LIMIT = 1
    settings.CLOSURE_REPORT_RATE_WINDOW_SECONDS = 3600
    reporter = _user("rl")
    assert file_closure_report(reporter, _place("A")) is not None
    assert file_closure_report(reporter, _place("B")) is None  # over the cross-venue rate limit


def test_place_is_closed_only_at_quorum(settings):
    settings.CLOSURE_REPORT_THRESHOLD = 3
    place = _place()
    _report(place, 2)
    assert place_is_closed(place) is False  # below quorum
    _report(place, 1, prefix="more")
    assert place_is_closed(place) is True  # at quorum


def test_decayed_reports_self_heal(settings):
    settings.CLOSURE_REPORT_THRESHOLD = 1
    settings.CLOSURE_REPORT_DECAY_SECONDS = 3600
    place = _place()
    file_closure_report(_user("old"), place)
    assert place_is_closed(place) is True
    PlaceClosureReport.objects.filter(place=place).update(
        created_at=timezone.now() - timedelta(hours=2)  # age it past the decay window
    )
    assert place_is_closed(place) is False


def test_public_places_hides_a_closed_venue(settings):
    settings.CLOSURE_REPORT_THRESHOLD = 2
    place = _place()
    assert public_places().filter(pk=place.pk).exists()  # visible before
    _report(place, 2)
    assert not public_places().filter(pk=place.pk).exists()  # hidden at quorum


def test_create_activity_blocked_at_closed_venue(settings):
    from apps.social.services import InvalidState, create_activity

    settings.CLOSURE_REPORT_THRESHOLD = 2
    owner, place = _user("owner"), _place()
    cat, _ = ActivityCategory.objects.get_or_create(slug="f13-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="f13-bball", defaults={"name": "Basketball", "category": cat}
    )
    _report(place, 1)  # below quorum -> still creatable
    a = create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Game",
        starts_at=timezone.now() + timedelta(days=1),
    )
    assert a.place_id == place.id
    _report(place, 1, prefix="more")  # reaches quorum -> the write-gate refuses it
    with pytest.raises(InvalidState):
        create_activity(
            owner,
            place=place,
            activity_type=atype,
            title="Game2",
            starts_at=timezone.now() + timedelta(days=1),
        )


def test_pending_below_quorum_does_not_hide_or_block(settings):
    settings.CLOSURE_REPORT_THRESHOLD = 3
    place = _place()
    _report(place, 2)  # one short of quorum
    assert place_is_closed(place) is False
    assert public_places().filter(pk=place.pk).exists()


def test_staff_clear_self_heals(settings):
    settings.CLOSURE_REPORT_THRESHOLD = 2
    staff, place = _user("mod", staff=True), _place()
    _report(place, 2)
    assert place_is_closed(place) is True
    assert clear_closure_reports(place, moderator=staff) == 2
    assert place_is_closed(place) is False
    assert public_places().filter(pk=place.pk).exists()  # back in discovery


def test_closure_is_never_written_to_place(settings):
    # Counts-only overlay: the hiding is derived from the report table, not a Place flag, so a
    # re-ingest (which rewrites the Place row) can never clobber it.
    settings.CLOSURE_REPORT_THRESHOLD = 1
    place = _place()
    file_closure_report(_user("rep"), place)
    place.refresh_from_db()
    assert place_is_closed(place) is True
    assert PlaceClosureReport.objects.filter(place=place).count() == 1


def test_web_endpoint_files_and_staff_resets(settings):
    settings.CLOSURE_REPORT_THRESHOLD = 1
    place = _place()
    reporter = _user("webrep")
    rc = Client()
    rc.force_login(reporter)
    rc.post(f"/places/{place.pk}/closed/")
    assert place_is_closed(place) is True

    staff = _user("webstaff", staff=True)
    sc = Client()
    sc.force_login(staff)
    sc.post(f"/places/{place.pk}/closed-reset/")
    assert place_is_closed(place) is False


def test_web_closure_reset_is_staff_only(settings):
    place = _place()
    plain = _user("plain")
    c = Client()
    c.force_login(plain)
    assert c.post(f"/places/{place.pk}/closed-reset/").status_code == 404  # non-staff -> 404


def test_place_is_closed_honors_annotation():
    # The recent_closure_n fast-path is reserved for a future per-row list surface; pin it so the
    # branch isn't dead/untested (hiding itself happens upstream in public_places()).
    place = _place()
    place.recent_closure_n = 5
    assert place_is_closed(place) is True
    place.recent_closure_n = 0
    assert place_is_closed(place) is False


def test_staff_clear_is_audited(settings):
    from apps.safety.models import AuditLog

    settings.CLOSURE_REPORT_THRESHOLD = 1
    staff, place = _user("auditmod", staff=True), _place()
    file_closure_report(_user("rep"), place)
    clear_closure_reports(place, moderator=staff)
    assert AuditLog.objects.filter(event="place.closure_reports_cleared").count() == 1
    # A reset with no moderator writes no audit row (accountability rides on the moderator arg).
    other = _place("Other")
    file_closure_report(_user("rep2"), other)
    clear_closure_reports(other, moderator=None)
    assert AuditLog.objects.filter(event="place.closure_reports_cleared").count() == 1


def test_closed_place_detail_404s_for_member_but_staff_can_view(settings):
    # The intended UX of hiding: a quorum-closed venue's detail page 404s for a regular member
    # (it's gone from discovery), but staff can still reach it to reset.
    settings.CLOSURE_REPORT_THRESHOLD = 2
    place = _place()
    _report(place, 2)
    mc = Client()
    mc.force_login(_user("viewer"))
    assert mc.get(f"/places/{place.pk}/").status_code == 404
    sc = Client()
    sc.force_login(_user("vstaff", staff=True))
    assert sc.get(f"/places/{place.pk}/").status_code == 200


def test_api_representation_exposes_no_closure_field():
    # inv.2: a closed place vanishes from the public API (routed through public_places); a VISIBLE
    # place's serialized row carries no closure count or reporter identity.
    from rest_framework.test import APIClient

    place = _place("Visible")
    data = APIClient().get("/api/places/").json()
    # The Place API is a GeoJSON FeatureCollection (GeoFeatureModelSerializer +
    # CappedGeoJsonPagination): rows live under "features", model fields under "properties".
    feature = next(f for f in data["features"] if f["id"] == place.id)
    assert not any(("closure" in k) or ("closed" in k) for k in feature["properties"])
