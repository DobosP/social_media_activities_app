import pytest
from django.contrib.gis.geos import Point

from apps.places.enrichment.google import GooglePlacesEnricher
from apps.places.models import Place


@pytest.fixture
def place(db):
    return Place.objects.create(
        name="City Sports Hall",
        location=Point(23.59, 46.77, srid=4326),
        source="osm",
        osm_type="node",
        osm_id=42,
        address_city="Cluj-Napoca",
    )


@pytest.mark.django_db
def test_google_backfills_website_phone_and_rating(monkeypatch, place):
    enricher = GooglePlacesEnricher(api_key="k", enabled=True)
    monkeypatch.setattr(enricher, "_post", lambda url, json, field_mask: {"places": [{"id": "g9"}]})
    monkeypatch.setattr(
        enricher,
        "_get",
        lambda url, params, field_mask: {
            "id": "g9",
            "googleMapsUri": "https://maps.google/?cid=9",
            "websiteUri": "https://sportshall.example.ro",
            "internationalPhoneNumber": "+40 264 111111",
            "rating": 4.6,
            "userRatingCount": 120,
            "primaryType": "sports_complex",
        },
    )
    status = enricher.enrich_place(place)
    assert status["website"] == "https://sportshall.example.ro"

    place.refresh_from_db()
    assert place.website == "https://sportshall.example.ro"  # backfilled (was empty)
    assert place.phone == "+40 264 111111"
    assert place.raw_tags["google"]["rating"] == 4.6
    assert place.raw_tags["google"]["primary_type"] == "sports_complex"


@pytest.mark.django_db
def test_google_does_not_overwrite_existing_website(monkeypatch, place):
    place.website = "https://original.example.ro"
    place.save(update_fields=["website"])
    enricher = GooglePlacesEnricher(api_key="k", enabled=True)
    monkeypatch.setattr(enricher, "_post", lambda url, json, field_mask: {"places": [{"id": "g9"}]})
    monkeypatch.setattr(
        enricher,
        "_get",
        lambda url, params, field_mask: {
            "id": "g9",
            "websiteUri": "https://google-suggested.example.ro",
        },
    )
    enricher.enrich_place(place)
    place.refresh_from_db()
    assert place.website == "https://original.example.ro"  # ours wins
