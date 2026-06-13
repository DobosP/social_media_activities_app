"""F21 (web + discovery) — event accuracy report flow + Happening-feed demotion."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.events.models import Event, EventReport
from apps.events.services import file_event_report
from apps.places.models import Place

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)
PW = "sup3r-secret-pw"
K = EventReport.Kind


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _event(title="Chess night"):
    place = Place.objects.create(name="Hall", location=PT, source=Place.Source.OSM)
    return Event.objects.create(
        title=title, place=place, starts_at=timezone.now() + timedelta(days=3)
    )


def test_member_can_report_event():
    event = _event()
    resp = _client(_user("e1")).post(f"/events/{event.pk}/report/", {"kind": K.CANCELLED})
    assert resp.status_code == 302
    assert EventReport.objects.filter(event=event, kind="cancelled").exists()


def test_detail_shows_report_form_and_flag():
    event = _event()
    body = _client(_user("e2")).get(f"/events/{event.pk}/").content.decode()
    assert "Is this event wrong?" in body
    # Once flagged, the detail shows the "may have changed" notice.
    for i in range(3):
        file_event_report(_user(f"e2r{i}"), event, K.CANCELLED)
    body2 = _client(_user("e2v")).get(f"/events/{event.pk}/").content.decode()
    assert "may have changed" in body2


def test_flagged_event_drops_from_happening_feed():
    event = _event("Disappearing")
    client = APIClient()  # AllowAny discovery feed
    before = client.get("/api/discovery/happening/")
    assert any(
        f["properties"].get("title") == "Disappearing"
        if "properties" in f
        else f.get("title") == "Disappearing"
        for f in _rows(before.json())
    )
    for i in range(3):
        file_event_report(_user(f"hf{i}"), event, K.CANCELLED)
    after = client.get("/api/discovery/happening/")
    titles = [
        (f["properties"].get("title") if "properties" in f else f.get("title"))
        for f in _rows(after.json())
    ]
    assert "Disappearing" not in titles  # flagged -> demoted out of Happening


def _rows(data):
    # EventCardSerializer may return a plain list or a GeoJSON FeatureCollection.
    if isinstance(data, dict) and "features" in data:
        return data["features"]
    return data if isinstance(data, list) else data.get("results", [])


def test_non_staff_cannot_reset():
    event = _event()
    resp = _client(_user("ns")).post(f"/events/{event.pk}/report-reset/")
    assert resp.status_code == 404


def test_report_form_hidden_for_unverified_user():
    event = _event()
    unverified = User.objects.create_user(username="unverif", password=PW)  # no apply_assurance
    body = _client(unverified).get(f"/events/{event.pk}/").content.decode()
    assert "Is this event wrong?" not in body  # fail-closed UX — no form they can't use


def test_pending_place_event_gated_for_view_and_report():
    # F25 + F21 compose: an event at a still-pending USER place can't be viewed OR reported by a
    # non-proposer, and the report endpoint is gated even though the proposer can view the page.
    from apps.social.models import UserPlaceProposal

    proposer = _user("ppx")
    place = Place.objects.create(name="Pending Hall", location=PT, source=Place.Source.USER)
    UserPlaceProposal.objects.create(
        place=place, proposer=proposer, status=UserPlaceProposal.Status.PENDING
    )
    event = Event.objects.create(
        title="Hidden", place=place, starts_at=timezone.now() + timedelta(days=3)
    )
    stranger = _user("strangerx")
    assert _client(stranger).get(f"/events/{event.pk}/").status_code == 404
    assert (
        _client(stranger).post(f"/events/{event.pk}/report/", {"kind": K.CANCELLED}).status_code
        == 404
    )
    assert not EventReport.objects.filter(event=event).exists()  # no report-on-invisible
    # The proposer CAN view their pending event, but the report form is hidden (not yet public).
    body = _client(proposer).get(f"/events/{event.pk}/").content.decode()
    assert "Is this event wrong?" not in body
