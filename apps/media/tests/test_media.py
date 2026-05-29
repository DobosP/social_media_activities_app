import hashlib
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
from apps.media.services import (
    MediaRejected,
    NotAuthorized,
    can_view_photo,
    resolve_signed_token,
    signed_url,
    upload_photo,
)
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import create_activity, request_to_join
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _png(color=(10, 120, 200), size=(8, 8)) -> bytes:
    img = Image.new("RGB", size, color)
    out = BytesIO()
    img.save(out, format="PNG")
    return out.getvalue()


def _jpeg_with_exif() -> bytes:
    img = Image.new("RGB", (8, 8), (200, 50, 50))
    exif = img.getexif()
    exif[0x0131] = "leak-me"  # Software tag — stand-in for metadata that must be stripped
    exif[0x0110] = "SecretCam"  # Model
    out = BytesIO()
    img.save(out, format="JPEG", exif=exif)
    return out.getvalue()


def _activity(owner):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug="m6", name="Sport")
    atype = ActivityType.objects.create(slug="bball6", name="Basketball", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2026-06-01T10:00Z"
    )


def test_profile_upload_strips_metadata():
    user = _user("p1")
    original = _jpeg_with_exif()
    assert "exif" in Image.open(BytesIO(original)).info  # original carries metadata

    photo = upload_photo(user, Photo.Kind.PROFILE, original)
    assert photo.scan_status == Photo.ScanStatus.CLEAN
    assert photo.exif_stripped is True

    from apps.media.storage import get_storage

    stored = get_storage().open(photo.storage_key)
    reopened = Image.open(BytesIO(stored))
    assert "exif" not in reopened.info  # no EXIF/GPS survives
    assert dict(reopened.getexif()) == {}


def test_only_one_profile_picture_replaces_previous():
    user = _user("p2")
    first = upload_photo(user, Photo.Kind.PROFILE, _png())
    second = upload_photo(user, Photo.Kind.PROFILE, _png(color=(1, 2, 3)))
    assert Photo.objects.filter(uploader=user, kind=Photo.Kind.PROFILE).count() == 1
    assert not Photo.objects.filter(id=first.id).exists()
    assert Photo.objects.filter(id=second.id).exists()


def test_thread_photo_requires_membership():
    owner = _user("owner6")
    activity = _activity(owner)
    outsider = _user("out6")
    with pytest.raises(NotAuthorized):
        upload_photo(outsider, Photo.Kind.THREAD, _png(), thread=activity.thread)

    photo = upload_photo(owner, Photo.Kind.THREAD, _png(), thread=activity.thread)
    assert photo.kind == Photo.Kind.THREAD


def test_thread_photo_visible_only_to_members():
    owner = _user("owner7")
    activity = _activity(owner)
    photo = upload_photo(owner, Photo.Kind.THREAD, _png(), thread=activity.thread)

    outsider = _user("out7")
    assert can_view_photo(outsider, photo) is False
    assert can_view_photo(owner, photo) is True

    # Once admitted as a member, the peer can see it.
    member = _user("mem7")
    m = request_to_join(member, activity)
    m.state = Membership.State.MEMBER
    m.save(update_fields=["state"])
    assert can_view_photo(member, photo) is True


def test_blocked_scan_rejects_and_does_not_store():
    user = _user("p3")
    data = _png(color=(9, 9, 9))
    # The scanner matches the ORIGINAL uploaded bytes — what a real CSAM hash set knows —
    # not the metadata-stripped Pillow re-encode (whose hash a known-bad file never has).
    digest = hashlib.sha256(data).hexdigest()
    with override_settings(MEDIA_CSAM_HASH_BLOCKLIST=[digest]):
        with pytest.raises(MediaRejected):
            upload_photo(user, Photo.Kind.PROFILE, data)
    assert Photo.objects.filter(uploader=user).count() == 0


def test_upload_fails_closed_when_no_scanner_configured():
    """On a children's platform an empty/ineffective scanner must REJECT uploads, not
    silently accept them (UPLOAD-1). MEDIA_REQUIRE_SCANNER defaults True in prod."""
    user = _user("p3b")
    with override_settings(MEDIA_REQUIRE_SCANNER=True, MEDIA_CSAM_HASH_BLOCKLIST=[]):
        with pytest.raises(MediaRejected):
            upload_photo(user, Photo.Kind.PROFILE, _png())
    assert Photo.objects.filter(uploader=user).count() == 0


def test_oversize_upload_rejected():
    user = _user("p4")
    with override_settings(MEDIA_MAX_UPLOAD_BYTES=10):
        with pytest.raises(ValueError):
            upload_photo(user, Photo.Kind.PROFILE, _png())


def test_non_image_rejected():
    user = _user("p5")
    with pytest.raises(ValueError):
        upload_photo(user, Photo.Kind.PROFILE, b"not an image")


def test_signed_url_roundtrip_and_scope():
    owner = _user("p6")
    photo = upload_photo(owner, Photo.Kind.PROFILE, _png())
    url = signed_url(photo, owner)
    token = url.rsplit("/", 2)[1]
    assert resolve_signed_token(token, owner).id == photo.id

    # A token is bound to its viewer; a different cohort user can't reuse it.
    other = _user("p7", band=AgeBand.UNDER_16)
    with pytest.raises(NotAuthorized):
        resolve_signed_token(token, other)


def test_upload_api_and_file_serve():
    user = _user("api6")
    client = APIClient()
    client.force_authenticate(user)
    resp = client.post(
        "/api/media/photos/",
        {"kind": "profile", "file": BytesIO(_png())},
        format="multipart",
    )
    assert resp.status_code == 201, resp.content
    file_url = resp.json()["url"]
    assert file_url

    served = client.get(file_url)
    assert served.status_code == 200
    assert served["Content-Type"].startswith("image/")
