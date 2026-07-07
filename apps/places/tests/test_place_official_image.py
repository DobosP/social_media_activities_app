"""P6b (ADR-0019 §2 lane 2) — the official business venue image.

An APPROVED business claimant (or staff) uploads the ONE official image through the same
fail-closed D6 pipeline as every other upload (validate → EXIF strip → scan → store). It
replaces the cached Commons image and, because the idempotent resolver never overwrites an
existing cover, stays stable across enrichment re-runs. NOT a child-safety signal.
"""

from io import BytesIO

import pytest
from django.contrib.gis.geos import Point
from django.test import Client, override_settings
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand
from apps.accounts.services import apply_assurance
from apps.media import services as media
from apps.media.storage import get_storage
from apps.ops.tasks import run_pending_tasks
from apps.places.models import Place, PlaceCover
from apps.places.services import (
    approve_place_claim,
    approved_business_claim_for,
    file_place_claim,
    place_visual,
)

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
    out = BytesIO()
    img.save(out, format="JPEG", exif=exif)
    return out.getvalue()


@pytest.fixture
def place():
    return Place.objects.create(
        name="Sala Polivalentă",
        location=Point(23.6, 46.76, srid=4326),
        source=Place.Source.OSM,
    )


@pytest.fixture
def claimant(django_user_model):
    user = django_user_model.objects.create_user(username="biz-ana", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return user


@pytest.fixture
def stranger(django_user_model):
    user = django_user_model.objects.create_user(username="biz-stranger", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return user


@pytest.fixture
def staff(django_user_model):
    return django_user_model.objects.create_user(username="biz-mod", password="pw", is_staff=True)


def _approved_claim(claimant, staff, place, org_name="SC Sala SRL"):
    claim = file_place_claim(
        claimant, place, org_name=org_name, official_website="https://sala.example/"
    )
    approve_place_claim(staff, claim)
    claim.refresh_from_db()
    return claim


# --- permission anchor ------------------------------------------------------------------


def test_approved_business_claim_helper_requires_live_partner(claimant, staff, place, stranger):
    assert approved_business_claim_for(claimant, place) is None  # no claim yet
    claim = _approved_claim(claimant, staff, place)
    assert approved_business_claim_for(claimant, place).id == claim.id
    assert approved_business_claim_for(stranger, place) is None
    # A partner staff later deactivate silently closes the surface too.
    claim.partner.is_active = False
    claim.partner.save(update_fields=["is_active"])
    assert approved_business_claim_for(claimant, place) is None


# --- upload service ---------------------------------------------------------------------


def test_approved_claimant_uploads_business_cover(claimant, staff, place):
    _approved_claim(claimant, staff, place)
    cover = media.upload_place_cover(claimant, place, _png(), alt_text="The main hall")

    assert cover.source == PlaceCover.Source.BUSINESS
    assert cover.uploaded_by_id == claimant.id
    assert "SC Sala SRL" in cover.attribution
    assert cover.license_name == "Used with permission"
    assert cover.exif_stripped is True and cover.sha256
    visual = place_visual(place)
    assert visual["kind"] == "place_cover_photo"
    assert visual["alt"] == "The main hall"
    assert "SC Sala SRL" in visual["attribution"]


def test_upload_replaces_wikimedia_cover(claimant, staff, place):
    storage = get_storage()
    storage.save("place-covers/old-commons.jpg", b"old-bytes", content_type="image/jpeg")
    PlaceCover.objects.create(
        place=place,
        source=PlaceCover.Source.WIKIMEDIA,
        storage_key="place-covers/old-commons.jpg",
        content_type="image/jpeg",
        attribution="Ana Pop",
        license_name="CC BY-SA 4.0",
    )
    _approved_claim(claimant, staff, place)

    cover = media.upload_place_cover(claimant, place, _png())

    assert PlaceCover.objects.filter(place=place).count() == 1
    assert cover.source == PlaceCover.Source.BUSINESS
    assert cover.storage_key != "place-covers/old-commons.jpg"


@pytest.mark.django_db(transaction=True)
def test_replacing_cover_reclaims_previous_blob_after_commit(tmp_path, settings):
    settings.MEDIA_ROOT = tmp_path / "media"
    from django.contrib.auth import get_user_model

    staff = get_user_model().objects.create_user(username="biz-txn-mod", password="pw")
    staff.is_staff = True
    staff.save(update_fields=["is_staff"])
    place = Place.objects.create(
        name="Txn Hall", location=Point(23.61, 46.75, srid=4326), source=Place.Source.OSM
    )
    first = media.upload_place_cover(staff, place, _png(color=(1, 1, 1)))
    first_key = first.storage_key
    assert get_storage().exists(first_key) is True

    second = media.upload_place_cover(staff, place, _png(color=(2, 2, 2)))

    assert second.id == first.id and second.storage_key != first_key
    run_pending_tasks()
    assert get_storage().exists(first_key) is False
    assert get_storage().exists(second.storage_key) is True


def test_pending_claimant_and_stranger_cannot_upload(claimant, stranger, place):
    file_place_claim(claimant, place, org_name="SC Pending SRL")  # never approved
    with pytest.raises(media.NotAuthorized):
        media.upload_place_cover(claimant, place, _png())
    with pytest.raises(media.NotAuthorized):
        media.upload_place_cover(stranger, place, _png())
    assert not PlaceCover.objects.filter(place=place).exists()


def test_staff_can_upload_without_a_claim(staff, place):
    cover = media.upload_place_cover(staff, place, _png())
    assert cover.source == PlaceCover.Source.BUSINESS
    assert cover.attribution == "Official image"  # no partner to credit


def test_scan_fail_closed_stores_nothing(claimant, staff, place):
    _approved_claim(claimant, staff, place)
    with override_settings(MEDIA_REQUIRE_SCANNER=True, MEDIA_CSAM_HASH_BLOCKLIST=[]):
        with pytest.raises(media.MediaRejected):
            media.upload_place_cover(claimant, place, _png())
    assert not PlaceCover.objects.filter(place=place).exists()


def test_upload_strips_exif(claimant, staff, place):
    _approved_claim(claimant, staff, place)
    cover = media.upload_place_cover(claimant, place, _jpeg_with_exif())
    stored = get_storage().open(cover.storage_key)
    reloaded = Image.open(BytesIO(stored))
    assert "leak-me" not in str(dict(reloaded.getexif()))


@pytest.mark.django_db(transaction=True)
def test_claimant_can_delete_cover_and_blob_is_reclaimed(tmp_path, settings, django_user_model):
    settings.MEDIA_ROOT = tmp_path / "media"
    claimant = django_user_model.objects.create_user(username="biz-del", password="pw")
    apply_assurance(claimant, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    staff = django_user_model.objects.create_user(
        username="biz-del-mod", password="pw", is_staff=True
    )
    place = Place.objects.create(
        name="Del Hall", location=Point(23.62, 46.74, srid=4326), source=Place.Source.OSM
    )
    _approved_claim(claimant, staff, place)
    cover = media.upload_place_cover(claimant, place, _png())
    key = cover.storage_key

    media.delete_place_cover(claimant, cover)

    assert not PlaceCover.objects.filter(place=place).exists()
    run_pending_tasks()
    assert get_storage().exists(key) is False


# --- web surface --------------------------------------------------------------------------


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def test_panel_shown_only_to_approved_claimant(claimant, stranger, staff, place):
    _approved_claim(claimant, staff, place)
    assert "official-image" in _client(claimant).get(f"/places/{place.pk}/").content.decode()
    assert "official-image" not in _client(stranger).get(f"/places/{place.pk}/").content.decode()


def test_claimant_uploads_via_web(claimant, staff, place):
    _approved_claim(claimant, staff, place)
    from django.core.files.uploadedfile import SimpleUploadedFile

    resp = _client(claimant).post(
        f"/places/{place.pk}/official-image/",
        {
            "image": SimpleUploadedFile("hall.png", _png(), content_type="image/png"),
            "alt_text": "The hall",
            "rights_confirmed": "on",
        },
    )
    assert resp.status_code == 302
    cover = PlaceCover.objects.get(place=place)
    assert cover.source == PlaceCover.Source.BUSINESS and cover.alt_text == "The hall"


def test_claimant_removes_via_web(claimant, staff, place):
    _approved_claim(claimant, staff, place)
    media.upload_place_cover(claimant, place, _png())
    resp = _client(claimant).post(f"/places/{place.pk}/official-image/", {"remove": "1"})
    assert resp.status_code == 302
    assert not PlaceCover.objects.filter(place=place).exists()


def test_stranger_post_is_404(stranger, place):
    from django.core.files.uploadedfile import SimpleUploadedFile

    resp = _client(stranger).post(
        f"/places/{place.pk}/official-image/",
        {
            "image": SimpleUploadedFile("x.png", _png(), content_type="image/png"),
            "rights_confirmed": "on",
        },
    )
    assert resp.status_code == 404
    assert not PlaceCover.objects.filter(place=place).exists()
