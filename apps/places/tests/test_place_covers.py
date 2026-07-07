"""ADR-0019 §2 — place cover images: Commons resolution ladder, signed serving,
place_visual contract, and the resolve_place_covers command."""

from unittest.mock import patch

import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command
from django.test import Client

from apps.media.services import place_cover_signed_url
from apps.media.storage import get_storage
from apps.places.enrichment.commons import CommonsCoverResolver, commons_file_title
from apps.places.models import Place, PlaceCover
from apps.places.services import place_visual

pytestmark = pytest.mark.django_db

_IMAGEINFO = {
    "thumburl": "https://upload.wikimedia.org/thumb/Central_Park.jpg/800px-Central_Park.jpg",
    "thumbmime": "image/jpeg",
    "thumbwidth": 800,
    "thumbheight": 533,
    "descriptionurl": "https://commons.wikimedia.org/wiki/File:Central_Park.jpg",
    "extmetadata": {
        "Artist": {"value": '<a href="https://example.org">Ana Pop</a>'},
        "LicenseShortName": {"value": "CC BY-SA 4.0"},
    },
}


def _place(name="Parcul Central", **tags):
    return Place.objects.create(
        name=name,
        location=Point(23.58, 46.77, srid=4326),
        source=Place.Source.OSM,
        raw_tags=tags,
    )


# --- tag ladder -----------------------------------------------------------------------


def test_commons_file_title_from_wikimedia_commons_tag():
    place = Place(raw_tags={"wikimedia_commons": "File:Central_Park.jpg"})
    assert commons_file_title(place) == "File:Central_Park.jpg"


def test_commons_file_title_rejects_categories_and_foreign_urls():
    assert commons_file_title(Place(raw_tags={"wikimedia_commons": "Category:Parks"})) is None
    assert commons_file_title(Place(raw_tags={"image": "https://example.com/x.jpg"})) is None
    assert commons_file_title(Place(raw_tags={})) is None


def test_commons_file_title_from_commons_image_url():
    place = Place(raw_tags={"image": "https://commons.wikimedia.org/wiki/File:Parcul_Central.jpg"})
    assert commons_file_title(place) == "File:Parcul_Central.jpg"


# --- resolver -------------------------------------------------------------------------


@patch.object(CommonsCoverResolver, "_download", return_value=b"jpegbytes")
@patch.object(CommonsCoverResolver, "imageinfo", return_value=dict(_IMAGEINFO))
def test_resolve_stores_cover_with_attribution(mock_info, mock_dl):
    place = _place(wikimedia_commons="File:Central_Park.jpg")

    cover = CommonsCoverResolver().resolve(place)

    assert cover is not None
    assert cover.source == PlaceCover.Source.WIKIMEDIA
    assert cover.content_type == "image/jpeg"
    assert cover.byte_size == len(b"jpegbytes")
    assert cover.attribution == "Ana Pop, CC BY-SA 4.0, via Wikimedia Commons"
    assert cover.license_name == "CC BY-SA 4.0"
    assert cover.source_page_url.endswith("File:Central_Park.jpg")
    assert get_storage().open(cover.storage_key) == b"jpegbytes"


@patch.object(CommonsCoverResolver, "_api_get")
def test_resolve_falls_back_to_wikidata_p18(mock_api):
    def api(params):
        if params.get("action") == "wbgetclaims":
            return {
                "claims": {"P18": [{"mainsnak": {"datavalue": {"value": "Parcul_Central.jpg"}}}]}
            }
        return {"query": {"pages": {"1": {"imageinfo": [dict(_IMAGEINFO)]}}}}

    mock_api.side_effect = api
    place = _place(wikidata="Q123")
    with patch.object(CommonsCoverResolver, "_download", return_value=b"x"):
        cover = CommonsCoverResolver().resolve(place)
    assert cover is not None


@patch.object(CommonsCoverResolver, "imageinfo", return_value=None)
def test_resolve_returns_none_without_imageinfo(mock_info):
    place = _place(wikimedia_commons="File:Missing.jpg")
    assert CommonsCoverResolver().resolve(place) is None


@patch.object(
    CommonsCoverResolver,
    "imageinfo",
    return_value={**_IMAGEINFO, "thumbmime": "image/svg+xml"},
)
def test_resolve_rejects_unsupported_mime(mock_info):
    place = _place(wikimedia_commons="File:Logo.svg")
    assert CommonsCoverResolver().resolve(place) is None


def test_resolve_never_replaces_existing_cover():
    place = _place(wikimedia_commons="File:Central_Park.jpg")
    existing = PlaceCover.objects.create(
        place=place,
        source=PlaceCover.Source.BUSINESS,
        storage_key="place-covers/manual.jpg",
        content_type="image/jpeg",
    )
    place = Place.objects.select_related("cover").get(pk=place.pk)
    assert CommonsCoverResolver().resolve(place) == existing


# --- serving + visual contract --------------------------------------------------------


def _cover_for(place, key=b"imgbytes"):
    storage_key = "place-covers/test.jpg"
    get_storage().save(storage_key, key, content_type="image/jpeg")
    return PlaceCover.objects.create(
        place=place,
        source=PlaceCover.Source.WIKIMEDIA,
        storage_key=storage_key,
        content_type="image/jpeg",
        attribution="Ana Pop, CC BY-SA 4.0, via Wikimedia Commons",
        license_name="CC BY-SA 4.0",
        source_page_url="https://commons.wikimedia.org/wiki/File:Central_Park.jpg",
        alt_text="Parcul Central",
    )


def test_signed_url_serves_public_place_cover(client):
    place = _place()
    _cover_for(place)
    place = Place.objects.select_related("cover").get(pk=place.pk)

    url = place_cover_signed_url(place.cover)
    assert url and url.startswith("/api/media/place-cover-file/")

    resp = client.get(url)
    assert resp.status_code == 200
    assert resp["Content-Type"] == "image/jpeg"


def test_tampered_token_is_rejected(client):
    place = _place()
    _cover_for(place)
    url = place_cover_signed_url(place.cover)
    resp = client.get(url[:-8] + "tampered/")
    assert resp.status_code in (401, 403)


def test_place_visual_prefers_cover_and_carries_attribution():
    place = _place()
    _cover_for(place)
    place = (
        Place.objects.select_related("cover").prefetch_related("place_activities").get(pk=place.pk)
    )

    visual = place_visual(place)

    assert visual["kind"] == "place_cover_photo"
    assert visual["attribution"].startswith("Ana Pop")
    assert visual["alt"] == "Parcul Central"


def test_place_visual_generates_deterministic_accent_without_cover():
    place = _place(name="Sala Sporturilor")
    place = Place.objects.prefetch_related("place_activities").get(pk=place.pk)

    first = place_visual(place)
    second = place_visual(place)

    assert first["kind"] == "accent"
    assert first["svg"] == second["svg"]
    assert "<svg" in first["svg"]


def test_place_detail_hero_renders_cover_with_attribution():
    place = _place()
    _cover_for(place)

    body = Client().get(f"/places/{place.pk}/").content.decode()

    assert 'class="place-cover"' in body
    assert '<img src="/api/media/place-cover-file/' in body
    assert 'alt="Parcul Central"' in body
    assert "Ana Pop, CC BY-SA 4.0, via Wikimedia Commons" in body
    assert "https://commons.wikimedia.org/wiki/File:Central_Park.jpg" in body


def test_place_detail_hero_renders_accent_without_cover():
    place = _place(name="Sala Sporturilor")

    body = Client().get(f"/places/{place.pk}/").content.decode()

    assert 'class="place-cover-accent"' in body
    assert "<svg" in body


# --- command --------------------------------------------------------------------------


@patch.object(CommonsCoverResolver, "_download", return_value=b"jpegbytes")
@patch.object(CommonsCoverResolver, "imageinfo", return_value=dict(_IMAGEINFO))
def test_command_resolves_and_marks_checked(mock_info, mock_dl, capsys):
    with_ref = _place(name="Cu poza", wikimedia_commons="File:Central_Park.jpg")
    without_ref = _place(name="Fara referinta")

    call_command("resolve_place_covers")

    with_ref.refresh_from_db()
    without_ref.refresh_from_db()
    assert PlaceCover.objects.filter(place=with_ref).exists()
    assert with_ref.raw_tags.get("cover_checked") is True
    assert not PlaceCover.objects.filter(place=without_ref).exists()
    assert "cover_checked" not in without_ref.raw_tags


@patch.object(CommonsCoverResolver, "resolve")
def test_command_skips_already_checked_places(mock_resolve):
    _place(name="Deja verificat", wikimedia_commons="File:X.jpg", cover_checked=True)

    call_command("resolve_place_covers")

    mock_resolve.assert_not_called()


@patch.object(CommonsCoverResolver, "resolve")
def test_command_dry_run_touches_nothing(mock_resolve):
    place = _place(wikimedia_commons="File:Central_Park.jpg")

    call_command("resolve_place_covers", "--dry-run")

    mock_resolve.assert_not_called()
    place.refresh_from_db()
    assert "cover_checked" not in place.raw_tags
