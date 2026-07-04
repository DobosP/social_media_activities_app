from datetime import timedelta
from io import BytesIO

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from PIL import Image
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media.services import upload_activity_cover
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


@pytest.fixture(autouse=True)
def _media_root(tmp_path, settings):
    settings.MEDIA_ROOT = tmp_path / "media"


def _png(color=(10, 120, 200), size=(16, 12)) -> bytes:
    out = BytesIO()
    Image.new("RGB", size, color).save(out, format="PNG")
    return out.getvalue()


def _user(name, *, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _type(slug="deck-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="deck-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _place(name="Deck Court", lon=23.6, lat=46.77):
    return Place.objects.create(
        name=name, location=Point(lon, lat, srid=4326), source=Place.Source.OSM
    )


def _activity(owner, title, *, starts_in=1, place=None, beginners=False):
    return social.create_activity(
        owner,
        place=place or _place(),
        activity_type=_type(),
        title=title,
        description=f"Description for {title}",
        starts_at=timezone.now() + timedelta(days=starts_in),
        beginners_welcome=beginners,
    )


def _client(user):
    client = APIClient()
    client.force_authenticate(user)
    return client


def test_activity_deck_requires_auth_and_returns_visuals_and_actions():
    owner = _user("deck-owner")
    viewer = _user("deck-viewer")
    with_cover = _activity(owner, "Covered hoops", beginners=True)
    upload_activity_cover(owner, with_cover, _png(), alt_text="Hoop court")
    _activity(owner, "Fallback chess")

    assert APIClient().get("/api/v1/discovery/activity-deck/").status_code == 401

    resp = _client(viewer).get("/api/v1/discovery/activity-deck/", {"seed": "abc", "limit": 12})
    assert resp.status_code == 200, resp.content
    body = resp.json()
    assert body["deck_seed"] == "abc"
    assert set(body) == {"deck_seed", "next_cursor", "items"}
    by_title = {item["title"]: item for item in body["items"]}
    assert by_title["Covered hoops"]["visual"]["kind"] == "activity_cover_photo"
    assert by_title["Covered hoops"]["visual"]["url"].startswith("/api/media/activity-cover-file/")
    assert by_title["Covered hoops"]["visual"]["alt"] == "Hoop court"
    assert by_title["Fallback chess"]["visual"] == {"kind": "generated_accent"}
    assert by_title["Covered hoops"]["actions"] == {
        "detail_url": f"/api/v1/social/activities/{with_cover.id}/",
        "web_url": f"/activities/{with_cover.id}/",
    }
    assert all(
        "like" not in item and "swipe" not in item and "pass" not in item for item in body["items"]
    )


def test_activity_deck_seed_cursor_and_limit_are_bounded_and_deterministic():
    owner = _user("deck-seed-owner")
    viewer = _user("deck-seed-viewer")
    for i in range(30):
        _activity(owner, f"Deck {i}", starts_in=i + 1)

    client = _client(viewer)
    first = client.get("/api/v1/discovery/activity-deck/", {"seed": "stable", "limit": 5}).json()
    again = client.get("/api/v1/discovery/activity-deck/", {"seed": "stable", "limit": 5}).json()
    assert [i["id"] for i in first["items"]] == [i["id"] for i in again["items"]]
    assert first["next_cursor"]

    second = client.get(
        "/api/v1/discovery/activity-deck/",
        {"seed": "stable", "limit": 5, "cursor": first["next_cursor"]},
    ).json()
    assert {i["id"] for i in first["items"]}.isdisjoint({i["id"] for i in second["items"]})

    clamped = client.get(
        "/api/v1/discovery/activity-deck/", {"seed": "stable", "limit": 999}
    ).json()
    assert len(clamped["items"]) == 24


def test_activity_deck_filters_visibility_future_open_activity_and_beginner_flag():
    owner = _user("deck-gates-owner")
    viewer = _user("deck-gates-viewer")
    teen = _user("deck-gates-teen", band=AgeBand.AGE_16_17)
    visible = _activity(owner, "Visible beginner", beginners=True)
    not_beginner = _activity(owner, "Visible regular", beginners=False)
    cancelled = _activity(owner, "Cancelled")
    cancelled.status = Activity.Status.CANCELLED
    cancelled.save(update_fields=["status"])
    hidden = _activity(owner, "Hidden")
    hidden.is_hidden = True
    hidden.save(update_fields=["is_hidden"])
    past = _activity(owner, "Past")
    past.starts_at = timezone.now() - timedelta(days=1)
    past.save(update_fields=["starts_at"])

    adult_data = (
        _client(viewer)
        .get("/api/v1/discovery/activity-deck/", {"seed": "gates", "beginners": "true"})
        .json()
    )
    titles = {item["title"] for item in adult_data["items"]}
    assert titles == {visible.title}

    all_items = (
        _client(viewer).get("/api/v1/discovery/activity-deck/", {"seed": "gates"}).json()["items"]
    )
    all_titles = {item["title"] for item in all_items}
    assert visible.title in all_titles and not_beginner.title in all_titles
    assert "Cancelled" not in all_titles
    assert "Hidden" not in all_titles
    assert "Past" not in all_titles

    teen_data = _client(teen).get("/api/v1/discovery/activity-deck/", {"seed": "gates"}).json()
    assert teen_data["items"] == []


def test_activity_deck_proximity_uses_request_only_coordinates():
    owner = _user("deck-near-owner")
    viewer = _user("deck-near-viewer")
    near = _activity(owner, "Near", place=_place("Near Park", 23.6, 46.77))
    _activity(owner, "Far", place=_place("Far Park", 26.1, 44.43))

    data = (
        _client(viewer)
        .get(
            "/api/v1/discovery/activity-deck/",
            {"seed": "near", "near_lon": 23.6, "near_lat": 46.77, "radius_m": 5000},
        )
        .json()
    )

    assert [item["title"] for item in data["items"]] == [near.title]
    assert data["items"][0]["distance_m"] is not None
