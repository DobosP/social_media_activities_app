"""W8: the perceptual (dHash) layer — re-encoded/resized copies of a blocklisted image
are caught; near-duplicate profile pictures are rejected within the cohort wall only;
the PDF document-scanner seam fails closed when required."""

import io

import pytest
from django.test import override_settings
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media import services as media
from apps.media.models import Photo
from apps.media.perceptual import dhash_hex, hamming_hex
from apps.media.scanning import HashBlocklistScanner

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _gradient_png(size=(64, 64), *, fmt="PNG", quality=90) -> bytes:
    """A deterministic non-uniform image (dHash needs structure, not a flat colour)."""
    im = Image.new("L", size)
    im.putdata([(x * 7 + y * 13) % 256 for y in range(size[1]) for x in range(size[0])])
    buf = io.BytesIO()
    im.convert("RGB").save(buf, fmt, quality=quality)
    return buf.getvalue()


def test_dhash_survives_reencode_and_resize():
    original = _gradient_png()
    reencoded = _gradient_png(fmt="JPEG", quality=70)
    with Image.open(io.BytesIO(original)) as im:  # a true downscale of the SAME image
        buf = io.BytesIO()
        im.resize((48, 48), Image.LANCZOS).save(buf, "PNG")
    resized = buf.getvalue()
    h = dhash_hex(original)
    assert h is not None and len(h) == 16
    assert hamming_hex(h, dhash_hex(reencoded)) <= 8
    assert hamming_hex(h, dhash_hex(resized)) <= 8
    assert dhash_hex(b"%PDF-1.4 not an image") is None
    # flat images carry no structure → no fingerprint (so they never cross-match)
    flat = io.BytesIO()
    Image.new("RGB", (32, 32), (5, 5, 5)).save(flat, "PNG")
    assert dhash_hex(flat.getvalue()) is None


def test_blocklist_scanner_catches_perceptual_match():
    bad = _gradient_png()
    fingerprint = dhash_hex(bad)
    with override_settings(MEDIA_PERCEPTUAL_BLOCKLIST=[fingerprint]):
        scanner = HashBlocklistScanner()
        assert scanner.is_effective()  # perceptual entries alone make it effective
        # the EXACT same bytes match…
        assert not scanner.scan(bad).clean
        # …and so does a re-encoded copy (different SHA-256, same structure)
        recompressed = _gradient_png(fmt="JPEG", quality=60)
        result = scanner.scan(recompressed)
        assert not result.clean and result.matched.startswith("phash:")


def test_profile_near_duplicate_rejected_within_cohort_only():
    owner = _user("ph-owner")
    copycat = _user("ph-copycat")
    other_cohort = _user("ph-child", AgeBand.UNDER_16)
    media.upload_photo(owner, Photo.Kind.PROFILE, _gradient_png())
    # a re-encoded near-duplicate is refused for a same-cohort user…
    with pytest.raises(media.DuplicateProfileImage):
        media.upload_photo(copycat, Photo.Kind.PROFILE, _gradient_png(fmt="JPEG", quality=60))
    # …but the cohort wall holds: the same image is fine in another cohort (no probe).
    photo = media.upload_photo(other_cohort, Photo.Kind.PROFILE, _gradient_png())
    assert photo.phash


@override_settings(MEDIA_REQUIRE_DOCUMENT_SCANNER=True)
def test_pdf_requires_document_scanner_when_flagged(place=None):
    from datetime import timedelta

    from django.contrib.gis.geos import Point
    from django.utils import timezone

    from apps.places.models import Place as PlaceModel
    from apps.social import services as social
    from apps.taxonomy.models import ActivityCategory, ActivityType

    owner = _user("ph-pdf")
    cat, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="ph-type", defaults={"name": "Chess", "category": cat}
    )
    venue = PlaceModel.objects.create(
        name="Doc Hall", location=Point(23.6, 46.7, srid=4326), source=PlaceModel.Source.OSM
    )
    activity = social.create_activity(
        owner,
        place=venue,
        activity_type=atype,
        title="Docs",
        starts_at=timezone.now() + timedelta(days=1),
    )
    post = social.post_to_thread(owner, activity, "rules attached", allow_empty=True)
    # Noop scanner is honest (is_effective False) → fail-closed rejection, no orphan blob.
    with pytest.raises(media.MediaRejected):
        media.attach_to_post(owner, post, filename="rules.pdf", data=b"%PDF-1.4 fake pdf bytes")
