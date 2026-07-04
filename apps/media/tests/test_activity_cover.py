import hashlib
from datetime import timedelta
from io import BytesIO

import pytest
from django.contrib.gis.geos import Point
from django.test import override_settings
from django.utils import timezone
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media import services as media
from apps.media.models import ActivityCover
from apps.media.storage import get_storage
from apps.ops.tasks import run_pending_tasks
from apps.places.models import Place
from apps.safety.services import block_user
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


def _jpeg_with_exif() -> bytes:
    img = Image.new("RGB", (16, 12), (200, 50, 50))
    exif = img.getexif()
    exif[0x0131] = "leak-me"
    exif[0x0110] = "SecretCam"
    out = BytesIO()
    img.save(out, format="JPEG", exif=exif)
    return out.getvalue()


def _user(name, *, band=AgeBand.ADULT, staff=False):
    u = User.objects.create_user(username=name, password="pw", display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if staff:
        u.is_staff = True
        u.save(update_fields=["is_staff"])
    return u


def _type(slug="cover-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="cover-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _activity(owner, *, starts_at=None, title="Game"):
    place = Place.objects.create(
        name="Cover Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return social.create_activity(
        owner,
        place=place,
        activity_type=_type(),
        title=title,
        starts_at=starts_at or timezone.now() + timedelta(days=1),
        description="Bring water",
    )


def test_upload_activity_cover_strips_metadata_and_signs_url():
    owner = _user("cover-owner")
    activity = _activity(owner)
    original = _jpeg_with_exif()
    assert "exif" in Image.open(BytesIO(original)).info

    cover = media.upload_activity_cover(owner, activity, original, alt_text="Court entrance")

    assert cover.activity == activity
    assert cover.uploader == owner
    assert cover.exif_stripped is True
    assert cover.alt_text == "Court entrance"
    assert cover.content_type.startswith("image/")
    stored = get_storage().open(cover.storage_key)
    reopened = Image.open(BytesIO(stored))
    assert "exif" not in reopened.info
    assert dict(reopened.getexif()) == {}
    url = media.activity_cover_signed_url(cover, owner)
    token = url.rsplit("/", 2)[1]
    assert media.resolve_activity_cover_token(token, owner).id == cover.id


def test_only_owner_or_staff_can_upload_replace_delete_cover():
    owner = _user("cover-manager")
    other = _user("cover-other")
    staff = _user("cover-staff", staff=True)
    activity = _activity(owner)

    with pytest.raises(media.NotAuthorized):
        media.upload_activity_cover(other, activity, _png())

    cover = media.upload_activity_cover(staff, activity, _png(color=(1, 2, 3)))
    assert cover.uploader == staff
    with pytest.raises(media.NotAuthorized):
        media.delete_activity_cover(other, cover)
    media.delete_activity_cover(owner, cover)
    assert not ActivityCover.objects.filter(activity=activity).exists()


def test_cover_upload_rejected_for_closed_hidden_or_started_activity():
    owner = _user("cover-state")

    cancelled = _activity(owner, title="Cancelled")
    cancelled.status = Activity.Status.CANCELLED
    cancelled.save(update_fields=["status"])
    with pytest.raises(media.MediaRejected):
        media.upload_activity_cover(owner, cancelled, _png())

    completed = _activity(owner, title="Completed")
    completed.status = Activity.Status.COMPLETED
    completed.save(update_fields=["status"])
    with pytest.raises(media.MediaRejected):
        media.upload_activity_cover(owner, completed, _png())

    hidden = _activity(owner, title="Hidden")
    hidden.is_hidden = True
    hidden.save(update_fields=["is_hidden"])
    with pytest.raises(media.MediaRejected):
        media.upload_activity_cover(owner, hidden, _png())

    started = _activity(owner, starts_at=timezone.now() - timedelta(minutes=1), title="Started")
    with pytest.raises(media.MediaRejected):
        media.upload_activity_cover(owner, started, _png())


def test_cover_scan_fail_closed_and_match_store_nothing():
    owner = _user("cover-scan")
    activity = _activity(owner)
    with override_settings(MEDIA_REQUIRE_SCANNER=True, MEDIA_CSAM_HASH_BLOCKLIST=[]):
        with pytest.raises(media.MediaRejected):
            media.upload_activity_cover(owner, activity, _png())
    assert not ActivityCover.objects.filter(activity=activity).exists()

    data = _png(color=(9, 9, 9))
    digest = hashlib.sha256(data).hexdigest()
    with override_settings(MEDIA_REQUIRE_SCANNER=True, MEDIA_CSAM_HASH_BLOCKLIST=[digest]):
        with pytest.raises(media.MediaRejected):
            media.upload_activity_cover(owner, activity, data)
    assert not ActivityCover.objects.filter(activity=activity).exists()


@pytest.mark.django_db(transaction=True)
def test_replacing_cover_reclaims_previous_blob_after_commit(tmp_path, settings):
    settings.MEDIA_ROOT = tmp_path / "media"
    owner = _user("cover-replace")
    activity = _activity(owner)
    first = media.upload_activity_cover(owner, activity, _png(color=(1, 1, 1)))
    first_key = first.storage_key
    assert get_storage().exists(first_key) is True

    second = media.upload_activity_cover(owner, activity, _png(color=(2, 2, 2)))

    assert second.id == first.id
    assert second.storage_key != first_key
    assert ActivityCover.objects.filter(activity=activity).count() == 1
    assert get_storage().exists(second.storage_key) is True
    run_pending_tasks()
    assert get_storage().exists(first_key) is False


def test_cover_visibility_follows_activity_visibility_and_public_gate():
    owner = _user("cover-visible-owner")
    viewer = _user("cover-visible-viewer")
    blocked = _user("cover-visible-blocked")
    activity = _activity(owner)
    cover = media.upload_activity_cover(owner, activity, _png())

    assert media.can_view_activity_cover(viewer, cover) is True
    block_user(owner, blocked)
    assert media.can_view_activity_cover(blocked, cover) is False
    assert media.can_view_activity_cover(None, cover) is False

    social.set_public_listing(owner, activity, True)
    assert media.can_view_activity_cover(None, cover) is True
    public_url = media.activity_cover_signed_url(cover, None)
    public_token = public_url.rsplit("/", 2)[1]
    assert media.resolve_activity_cover_token(public_token, None).id == cover.id
