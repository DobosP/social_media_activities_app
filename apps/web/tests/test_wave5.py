"""Web tests for wave-5: F8 (what-to-expect), F17 (why-recommended + beginners), F40 (prefill)."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.events.models import Event
from apps.places.models import Place
from apps.recommendations.services import set_interests
from apps.social.models import Activity
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _type(slug="w5-bball", name="Basketball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="w5-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(slug=slug, defaults={"name": name, "category": cat})
    return t


def _place():
    return Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _activity(owner, atype=None, **kw):
    return create_activity(
        owner,
        place=_place(),
        activity_type=atype or _type(),
        title=kw.pop("title", "Game"),
        starts_at=timezone.now() + timedelta(days=1),
        **kw,
    )


# --- F8: what-to-expect ----------------------------------------------------------------


def test_web_create_omitting_cost_band_stores_unspecified():
    owner = _user("f8c")
    atype = _type()
    resp = _client(owner).post(
        "/activities/new/",
        {
            "place": _place().id,
            "activity_type": atype.id,
            "title": "Pickup",
            "starts_at": (timezone.now() + timedelta(days=1)).strftime("%Y-%m-%dT%H:%M"),
            # cost_band / difficulty deliberately omitted
        },
    )
    assert resp.status_code == 302, resp.content
    a = Activity.objects.get(title="Pickup")
    assert a.cost_band == "unspecified"  # coerced, never ""
    assert a.difficulty == "unspecified"


def test_detail_shows_chips_and_accessibility():
    owner = _user("f8d")
    a = _activity(
        owner,
        cost_band=Activity.CostBand.PAID,
        difficulty=Activity.Difficulty.EASY,
        accessibility_notes="Step-free access throughout.",
    )
    body = _client(owner).get(f"/activities/{a.id}/").content.decode()
    assert "Paid" in body
    assert "Easy" in body
    assert "Accessibility:" in body
    assert "Step-free access throughout." in body


# --- F17: beginners filter + honest recommendation reason ------------------------------


def test_beginners_filter():
    owner, viewer = _user("f17o"), _user("f17v")
    atype = _type()
    _activity(owner, atype, title="For all")
    _activity(owner, atype, title="Newbies ok", beginners_welcome=True)
    c = _client(viewer)
    all_titles = {a.title for a in c.get("/activities/").context["activities"]}
    assert {"For all", "Newbies ok"} <= all_titles
    only = {a.title for a in c.get("/activities/?beginners=true").context["activities"]}
    assert only == {"Newbies ok"}


def test_recommendation_reason_cold_start():
    owner, viewer = _user("f17co"), _user("f17cv")  # viewer has no interests → cold start
    a = _activity(owner, _type())
    recommended = _client(viewer).get("/").context["recommended"]
    match = next(r for r in recommended if r.id == a.id)
    assert match.rec_reason == "soonest first"


def test_recommendation_reason_matches_declared_interest():
    owner, viewer = _user("f17io"), _user("f17iv")
    atype = _type(slug="w5-running", name="Running")
    set_interests(viewer, [atype.slug])  # viewer declares the interest
    a = _activity(owner, atype)
    recommended = _client(viewer).get("/").context["recommended"]
    match = next(r for r in recommended if r.id == a.id)
    assert match.rec_reason == "matches your interest in Running"


# --- F40: prefill from an event --------------------------------------------------------


def test_event_link_carries_prefill_params():
    user = _user("f40u")
    atype = _type()
    place = _place()
    event = Event.objects.create(
        title="Chess night", place=place, activity_type=atype, starts_at=timezone.now()
    )
    body = _client(user).get(f"/events/{event.id}/").content.decode()
    assert f"activity_type={atype.pk}" in body
    assert "starts_at=" in body


def test_activity_create_prefills_from_valid_params():
    user = _user("f40v")
    atype = _type()
    place = _place()
    resp = _client(user).get(
        f"/activities/new/?place={place.id}&activity_type={atype.id}&starts_at=2031-06-15T12:30"
    )
    assert resp.status_code == 200
    initial = resp.context["form"].initial
    assert initial.get("activity_type") == str(atype.id)
    assert "starts_at" in initial


def test_activity_create_ignores_crafted_bad_params():
    user = _user("f40b")
    resp = _client(user).get("/activities/new/?activity_type=999999&starts_at=not-a-date")
    assert resp.status_code == 200  # never 500
    initial = resp.context["form"].initial
    assert "activity_type" not in initial  # nonexistent id dropped
    assert "starts_at" not in initial  # unparseable dropped
