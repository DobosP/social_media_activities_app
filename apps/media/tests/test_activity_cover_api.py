from datetime import timedelta
from io import BytesIO

import pytest
from django.contrib.gis.geos import Point
from django.core.files.uploadedfile import SimpleUploadedFile
from django.utils import timezone
from PIL import Image
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media import services as media
from apps.media.models import ActivityCover
from apps.places.models import Place
from apps.social import services as social
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


def _activity(owner, title="Cover API Game"):
    cat, _ = ActivityCategory.objects.get_or_create(
        slug="cover-api-sport", defaults={"name": "Sport"}
    )
    atype, _ = ActivityType.objects.get_or_create(
        slug="cover-api-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="API Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return social.create_activity(
        owner,
        place=place,
        activity_type=atype,
        title=title,
        starts_at=timezone.now() + timedelta(days=1),
    )


def _client(user=None):
    c = APIClient()
    if user is not None:
        c.force_authenticate(user)
    return c


def _upload(name, data):
    return SimpleUploadedFile(name, data, content_type="image/png")


def test_activity_cover_put_get_delete_and_file_serve():
    owner = _user("cover-api-owner")
    activity = _activity(owner)
    client = _client(owner)

    resp = client.put(
        f"/api/v1/media/activity-covers/{activity.id}/",
        {"file": _upload("cover.png", _png()), "alt_text": "Court gate"},
        format="multipart",
    )

    assert resp.status_code == 200, resp.content
    data = resp.json()
    assert data["activity"] == activity.id
    assert data["alt_text"] == "Court gate"
    assert data["url"].startswith("/api/media/activity-cover-file/")
    assert "storage_key" not in data and "sha256" not in data and "uploader" not in data

    served = client.get(data["url"])
    assert served.status_code == 200
    assert served["Content-Type"].startswith("image/")

    fetched = client.get(f"/api/v1/media/activity-covers/{activity.id}/")
    assert fetched.status_code == 200
    assert fetched.json()["url"]

    deleted = client.delete(f"/api/v1/media/activity-covers/{activity.id}/")
    assert deleted.status_code == 204
    assert not ActivityCover.objects.filter(activity=activity).exists()


def test_activity_cover_api_requires_organizer_for_writes():
    owner = _user("cover-api-owner2")
    other = _user("cover-api-other2")
    activity = _activity(owner)

    unauth = APIClient().put(
        f"/api/v1/media/activity-covers/{activity.id}/",
        {"file": _upload("cover.png", _png())},
        format="multipart",
    )
    assert unauth.status_code == 401

    denied = _client(other).put(
        f"/api/v1/media/activity-covers/{activity.id}/",
        {"file": _upload("cover.png", _png())},
        format="multipart",
    )
    assert denied.status_code == 403
    assert not ActivityCover.objects.filter(activity=activity).exists()

    cover = media.upload_activity_cover(owner, activity, _png())
    denied_delete = _client(other).delete(f"/api/v1/media/activity-covers/{activity.id}/")
    assert denied_delete.status_code == 403
    assert ActivityCover.objects.filter(id=cover.id).exists()


def test_anonymous_get_only_serves_public_adult_activity_cover():
    owner = _user("cover-api-public")
    activity = _activity(owner, title="Public cover")
    cover = media.upload_activity_cover(owner, activity, _png())

    hidden = APIClient().get(f"/api/v1/media/activity-covers/{activity.id}/")
    assert hidden.status_code == 403

    social.set_public_listing(owner, activity, True)
    public = APIClient().get(f"/api/v1/media/activity-covers/{activity.id}/")
    assert public.status_code == 200
    url = public.json()["url"]
    assert url
    assert APIClient().get(url).status_code == 200

    activity.is_publicly_listed = False
    activity.save(update_fields=["is_publicly_listed"])
    token = url.rsplit("/", 2)[1]
    with pytest.raises(media.NotAuthorized):
        media.resolve_activity_cover_token(token, None)
    cover.refresh_from_db()
    assert cover.activity_id == activity.id
