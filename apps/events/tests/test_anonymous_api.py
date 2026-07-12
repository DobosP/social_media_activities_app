from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.events.models import Event
from apps.events.serializers import EventSerializer
from apps.places.models import Place
from apps.social.models import UserPlaceProposal

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _osm_place(name="City Library", lon=23.6, lat=46.77, city=""):
    return Place.objects.create(
        name=name,
        location=Point(lon, lat, srid=4326),
        source=Place.Source.OSM,
        address_city=city,
    )


def _upcoming(**kwargs):
    kwargs.setdefault("title", "Upcoming happening")
    kwargs.setdefault("starts_at", timezone.now() + timedelta(days=2))
    kwargs.setdefault("source", Event.Source.MANUAL)
    return Event.objects.create(**kwargs)


def test_anonymous_list_returns_upcoming_events():
    _upcoming(title="Chess club night", place=_osm_place())

    resp = APIClient().get("/api/v1/events/")

    assert resp.status_code == 200
    titles = [e["title"] for e in resp.json()["results"]]
    assert "Chess club night" in titles


def test_anonymous_detail_returns_200():
    event = _upcoming(title="Reading circle", place=_osm_place())

    resp = APIClient().get(f"/api/v1/events/{event.id}/")

    assert resp.status_code == 200
    assert resp.json()["title"] == "Reading circle"


def test_anonymous_list_hides_event_at_unpublished_user_place():
    # A USER-source place stays hidden until its co-creation proposal is PUBLISHED, so an
    # event pinned to a still-PENDING proposal must not leak that place through the API.
    pending_place = Place.objects.create(
        name="Pending backyard court",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.USER,
    )
    UserPlaceProposal.objects.create(
        place=pending_place,
        proposer=_user("proposer"),
        status=UserPlaceProposal.Status.PENDING,
    )
    _upcoming(title="At pending place", place=pending_place)
    _upcoming(title="At public place", place=_osm_place())

    titles = [e["title"] for e in APIClient().get("/api/v1/events/").json()["results"]]
    assert "At public place" in titles
    assert "At pending place" not in titles


# NOTE: lifecycle/tombstone gating tests (cancelled events hidden, retracted events out of
# include_past) live with the v_2 scraper-lane schema and return when it lands on main.


def test_anonymous_include_past_returns_past_events():
    _upcoming(title="Upcoming", place=_osm_place())
    Event.objects.create(
        title="Old one",
        starts_at=timezone.now() - timedelta(days=1),
        source=Event.Source.MANUAL,
    )

    default = APIClient().get("/api/v1/events/").json()
    history = APIClient().get("/api/v1/events/?include_past=true").json()

    assert "Old one" not in [e["title"] for e in default["results"]]
    assert "Old one" in [e["title"] for e in history["results"]]


def test_anonymous_response_exposes_no_user_or_pii_fields():
    _upcoming(title="Public event", place=_osm_place())

    item = APIClient().get("/api/v1/events/").json()["results"][0]

    # The serialized keys must be a subset of EventSerializer's declared fields — no user,
    # reporter, proposer, or other PII/relational field can leak through the anon surface.
    allowed = set(EventSerializer().fields.keys())
    assert set(item.keys()) <= allowed


# --- agent queryability: from/to, q, city, near --------------------------------------------


def _titles(resp):
    assert resp.status_code == 200, resp.content
    return [e["title"] for e in resp.json()["results"]]


def test_from_to_window_filters_by_start():
    place = _osm_place()
    now = timezone.now()
    _upcoming(title="Tomorrow", starts_at=now + timedelta(days=1), place=place)
    _upcoming(title="Next week", starts_at=now + timedelta(days=6), place=place)
    _upcoming(title="Far out", starts_at=now + timedelta(days=20), place=place)

    frm = (now + timedelta(days=3)).date().isoformat()
    to = (now + timedelta(days=10)).date().isoformat()
    titles = _titles(APIClient().get(f"/api/v1/events/?from={frm}&to={to}"))
    assert titles == ["Next week"]


def test_to_bare_date_includes_that_whole_day():
    place = _osm_place()
    evening = (timezone.now() + timedelta(days=4)).replace(hour=18, minute=0)
    _upcoming(title="Evening session", starts_at=evening, place=place)

    to = evening.date().isoformat()
    assert "Evening session" in _titles(APIClient().get(f"/api/v1/events/?to={to}"))


@pytest.mark.parametrize(
    "raw",
    [
        "next-friday",  # not ISO at all
        "2026-13-01",  # month 13: parse_date RAISES ValueError (never returns None)
        "2026-02-30",  # Feb 30
        "2026-07-16T25:00",  # hour 25: parse_datetime raises too
        "2026-07-16T12:60",  # minute 60
    ],
)
def test_invalid_date_bound_is_a_clear_400(raw):
    # A typo'd or out-of-range date must not silently widen the requested window — and
    # must be a 400 with the param named, never a 500 (Django 5's fromisoformat parsers
    # raise ValueError on out-of-range components instead of returning None).
    resp = APIClient().get(f"/api/v1/events/?from={raw}")
    assert resp.status_code == 400
    assert "from" in resp.json()


def test_invalid_date_bound_is_a_clear_400_on_public_activities_too():
    resp = APIClient().get("/api/v1/discovery/public/activities/?to=2026-13-01")
    assert resp.status_code == 400


def test_q_matches_title_description_and_venue_name():
    _upcoming(title="Open practice", description="bring a chess clock", place=_osm_place())
    _upcoming(title="Quiet reading", place=_osm_place("Chess Palace"))
    _upcoming(title="Unrelated", place=_osm_place("Gym"))

    titles = _titles(APIClient().get("/api/v1/events/?q=chess"))
    assert set(titles) == {"Open practice", "Quiet reading"}
    # A one-character q is ignored (same >=2 contract as services.search_events).
    assert len(_titles(APIClient().get("/api/v1/events/?q=c"))) == 3


def test_city_filter_is_case_insensitive():
    _upcoming(title="In Cluj", place=_osm_place(city="Cluj-Napoca"))
    _upcoming(title="Elsewhere", place=_osm_place("Other hall", city="Bucharest"))

    assert _titles(APIClient().get("/api/v1/events/?city=cluj-napoca")) == ["In Cluj"]


def test_near_orders_nearest_first_and_radius_filters():
    # ~0.0099° latitude ≈ 1.1 km; the far venue sits ~11 km north.
    near_place = _osm_place("Near venue", lon=23.6236, lat=46.7712)
    far_place = _osm_place("Far venue", lon=23.6236, lat=46.8702)
    now = timezone.now()
    _upcoming(title="Far event", starts_at=now + timedelta(days=1), place=far_place)
    _upcoming(title="Near event", starts_at=now + timedelta(days=2), place=near_place)

    base = "/api/v1/events/?near_lat=46.7712&near_lon=23.6236"
    assert _titles(APIClient().get(base)) == ["Near event", "Far event"]
    assert _titles(APIClient().get(f"{base}&radius_m=2000")) == ["Near event"]


def test_bad_near_coordinates_and_radius_are_clear_400s():
    # Malformed geo params must not silently return an empty page (bad near) or a
    # silently-unbounded radius (bad radius_m) — agents need the explicit error.
    _upcoming(title="Somewhere", place=_osm_place())
    assert APIClient().get("/api/v1/events/?near_lat=abc&near_lon=23.6").status_code == 400
    resp = APIClient().get("/api/v1/events/?near_lat=46.77&near_lon=23.6&radius_m=abc")
    assert resp.status_code == 400
    assert "radius_m" in resp.json()
