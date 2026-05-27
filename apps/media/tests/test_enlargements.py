from io import BytesIO

import pytest
from django.contrib.gis.geos import Point
from django.test import override_settings
from PIL import Image
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media.models import Photo
from apps.media.processing import validate_and_strip
from apps.media.services import NotAuthorized, delete_photo, thread_photos, upload_photo
from apps.places.models import Place
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _png(size=(8, 8)) -> bytes:
    out = BytesIO()
    Image.new("RGB", size, (10, 120, 200)).save(out, format="PNG")
    return out.getvalue()


def _activity(owner):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug="m6e", name="Sport")
    atype = ActivityType.objects.create(slug="bball6e", name="Basketball", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2026-06-01T10:00Z"
    )


def test_downscale_caps_longest_side():
    big = _png(size=(4000, 2000))
    clean, fmt, (w, h) = validate_and_strip(big, max_bytes=10_000_000, max_dimension=2048)
    assert max(w, h) == 2048
    assert fmt == "PNG"


def test_delete_photo_by_uploader_and_authorization():
    owner = _user("dl1")
    photo = upload_photo(owner, Photo.Kind.PROFILE, _png())
    other = _user("dl2")
    with pytest.raises(NotAuthorized):
        delete_photo(other, photo)
    delete_photo(owner, photo)
    assert not Photo.objects.filter(id=photo.id).exists()


def test_thread_gallery_members_only():
    owner = _user("g1")
    activity = _activity(owner)
    upload_photo(owner, Photo.Kind.THREAD, _png(), thread=activity.thread)

    assert thread_photos(owner, activity.thread).count() == 1
    outsider = _user("g2")
    with pytest.raises(NotAuthorized):
        thread_photos(outsider, activity.thread)


def test_thread_gallery_api():
    owner = _user("g3")
    activity = _activity(owner)
    upload_photo(owner, Photo.Kind.THREAD, _png(), thread=activity.thread)
    client = APIClient()
    client.force_authenticate(owner)
    resp = client.get(f"/api/media/threads/{activity.thread.id}/photos/")
    assert resp.status_code == 200
    body = resp.json()
    assert len(body) == 1
    assert body[0]["url"]


@override_settings(MEDIA_MAX_DIMENSION=64)
def test_upload_applies_downscale():
    owner = _user("g4")
    photo = upload_photo(owner, Photo.Kind.PROFILE, _png(size=(500, 250)))
    assert max(photo.width, photo.height) == 64
