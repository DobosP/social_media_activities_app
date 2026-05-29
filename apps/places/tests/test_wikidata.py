from unittest.mock import patch

import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command

from apps.places.enrichment.wikidata import WikidataEnricher
from apps.places.models import Place

pytestmark = pytest.mark.django_db


def _place(name, *, wikidata=None, website=""):
    return Place.objects.create(
        name=name,
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        website=website,
        raw_tags={"wikidata": wikidata} if wikidata else {},
    )


def test_qid_for_validates_format():
    assert WikidataEnricher.qid_for(Place(raw_tags={"wikidata": "Q42"})) == "Q42"
    assert WikidataEnricher.qid_for(Place(raw_tags={"wikidata": "notaqid"})) is None
    assert WikidataEnricher.qid_for(Place(raw_tags={})) is None


@patch.object(WikidataEnricher, "_run_query")
def test_enrich_backfills_official_website(mock_run):
    mock_run.return_value = {"Q42": "https://example.org/"}
    place = _place("Central Library", wikidata="Q42")

    updated = WikidataEnricher().enrich_places([place])

    assert updated == 1
    place.refresh_from_db()
    assert place.website == "https://example.org/"
    assert place.raw_tags.get("wikidata_enriched") is True
    assert place.raw_tags.get("wikidata") == "Q42"


@patch.object(WikidataEnricher, "_run_query")
def test_skips_places_with_existing_website(mock_run):
    place = _place("Has site", wikidata="Q42", website="https://already.example/")
    assert WikidataEnricher().enrich_places([place]) == 0
    mock_run.assert_not_called()  # no candidates -> no SPARQL call


@patch.object(WikidataEnricher, "_run_query")
def test_skips_places_without_qid(mock_run):
    place = _place("No QID")
    assert WikidataEnricher().enrich_places([place]) == 0
    mock_run.assert_not_called()


@patch.object(WikidataEnricher, "_run_query")
def test_enrich_places_command_wikidata_flag(mock_run):
    mock_run.return_value = {"Q7": "https://lib.example/"}
    place = _place("Cmd library", wikidata="Q7")

    call_command("enrich_places", "--wikidata")

    place.refresh_from_db()
    assert place.website == "https://lib.example/"
