import pytest
from django.contrib.gis.geos import Point

from apps.places.enrichment.google import GooglePlacesEnricher, GooglePlacesError
from apps.places.models import Place


@pytest.fixture
def place(db):
    return Place.objects.create(
        name="Central Library",
        location=Point(23.59, 46.77, srid=4326),
        source="osm",
        osm_type="node",
        osm_id=1,
        address_city="Cluj-Napoca",
    )


def test_disabled_by_default(settings):
    settings.GOOGLE_PLACES_ENABLED = False
    settings.GOOGLE_PLACES_API_KEY = ""
    enricher = GooglePlacesEnricher()
    assert enricher.enabled is False
    with pytest.raises(GooglePlacesError):
        enricher.live_status("abc")


def test_enabled_requires_key(settings):
    settings.GOOGLE_PLACES_ENABLED = True
    settings.GOOGLE_PLACES_API_KEY = ""
    assert GooglePlacesEnricher().enabled is False


@pytest.mark.django_db
def test_enrich_place_persists_durable_meta(monkeypatch, place):
    enricher = GooglePlacesEnricher(api_key="k", enabled=True)
    monkeypatch.setattr(enricher, "_post", lambda url, json, field_mask: {"places": [{"id": "g1"}]})
    monkeypatch.setattr(
        enricher,
        "_get",
        lambda url, params, field_mask: {
            "id": "g1",
            "googleMapsUri": "https://maps.google/?cid=1",
            "currentOpeningHours": {"openNow": True},
            "regularOpeningHours": {"weekdayDescriptions": ["Monday: 9-5"]},
        },
    )
    status = enricher.enrich_place(place)
    assert status["open_now"] is True
    place.refresh_from_db()
    assert place.raw_tags["google"]["place_id"] == "g1"
    assert place.raw_tags["google"]["maps_uri"] == "https://maps.google/?cid=1"


@pytest.mark.django_db
def test_enrich_places_command_skips_google_when_disabled(settings, place, capsys):
    settings.GOOGLE_PLACES_ENABLED = False
    from django.core.management import call_command

    call_command("enrich_places", "--google")
    out = capsys.readouterr().out
    assert "disabled" in out.lower()
