"""W3-F14 — crowd-correctable opening hours (quorum edit, not just a wrong-hours flag).

Extends the F20 PlaceCorrection overlay with a HOURS field: validated via parse_opening_hours on
PROPOSE, re-parsed on READ (Place.display_opening_hours returns the DICT is_open_at consumes), and
a published HOURS correction clears the F28 open-now reports (they were about the superseded hours).
"""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Role, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place, PlaceCorrection
from apps.places.services import (
    PlacesError,
    confirm_place_correction,
    file_open_now_report,
    open_now_status,
    propose_place_correction,
    staff_publish_correction,
)

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)
F = PlaceCorrection.Field


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _staff(name):
    u = User.objects.create_user(
        username=name, password="pw", display_name=name, role=Role.ADMIN, is_staff=True
    )
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _place(**kw):
    kw.setdefault("name", "Venue")
    kw.setdefault("source", Place.Source.OSM)
    return Place.objects.create(location=PT, **kw)


def test_field_choices_includes_hours():
    assert ("hours", "Opening hours") in PlaceCorrection.Field.choices


def test_display_opening_hours_falls_back_to_raw_when_no_correction():
    place = _place(opening_hours={"mo": [[540, 1080]]})
    assert place.display_opening_hours == {"mo": [[540, 1080]]}
    blank = _place()
    assert blank.display_opening_hours is None  # no correction, no raw hours


def test_invalid_hours_rejected_on_propose():
    place = _place()
    with pytest.raises(PlacesError):
        propose_place_correction(
            _user("badh"), place, field=F.HOURS, proposed_value="definitely not hours"
        )


def test_hours_correction_applies_as_parsed_dict_at_read_time():
    place = _place()
    proposer = _user("hc")
    c = propose_place_correction(proposer, place, field=F.HOURS, proposed_value="24/7")
    assert c.proposed_value == "24/7"  # the RAW string is stored
    for i in range(3):
        confirm_place_correction(_user(f"hcc-{i}"), c)
    c.refresh_from_db()
    assert c.status == PlaceCorrection.Status.PUBLISHED
    place.refresh_from_db()
    sched = place.display_opening_hours
    assert isinstance(sched, dict)  # re-parsed to the dict is_open_at consumes, not a raw string
    assert all(v == [[0, 1440]] for v in sched.values())  # 24/7 -> every day all-day


def test_open_now_status_uses_corrected_hours():
    place = _place()  # no raw hours -> unknown
    assert open_now_status(place) is None
    c = propose_place_correction(_user("oc"), place, field=F.HOURS, proposed_value="24/7")
    staff_publish_correction(_staff("modo"), c)
    place.refresh_from_db()
    assert open_now_status(place) is True  # 24/7 -> always open (no reports -> reliable)


def test_hours_publish_clears_open_now_reports(settings):
    settings.OPEN_NOW_REPORT_THRESHOLD = 1
    place = _place(opening_hours={"mo": [[540, 1080]]})
    file_open_now_report(_user("r1"), place)
    assert place.open_now_reports.count() == 1
    # A published HOURS correction supersedes the posted hours, so the old 'wrong-hours' reports
    # are cleared (else the freshly-corrected venue would read 'unverified' at the same time).
    c = propose_place_correction(_user("hp"), place, field=F.HOURS, proposed_value="24/7")
    staff_publish_correction(_staff("modh"), c)
    assert place.open_now_reports.count() == 0


def test_name_publish_does_not_clear_open_now_reports():
    place = _place(opening_hours={"mo": [[540, 1080]]})
    file_open_now_report(_user("r2"), place)
    # A NAME correction doesn't affect open_now_status, so it must NOT touch the reports.
    c = propose_place_correction(_user("np"), place, field=F.NAME, proposed_value="New Name")
    staff_publish_correction(_staff("modn"), c)
    assert place.open_now_reports.count() == 1
