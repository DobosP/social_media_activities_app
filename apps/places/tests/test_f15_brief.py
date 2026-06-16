"""W3-F15 — read-aloud plain-language brief on the place page. Mirrors W2-F27 plain_meetup_brief:
labelled declarative sentences, NO counts. The list surface composes ONLY from the free
accessibility dict-read (no venue_facts N+1); the detail surface adds the crowd venue facts.
"""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.places.models import Place
from apps.places.services import place_plain_brief, venue_facts_detail

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)


def _place(**kw):
    kw.setdefault("name", "Cluj Library")
    kw.setdefault("source", Place.Source.OSM)
    return Place.objects.create(location=PT, **kw)


def _labels(brief):
    return [label for label, _ in brief]


def test_brief_includes_place_name_and_honest_accessibility_states():
    place = _place(name="Cluj Library", raw_tags={"wheelchair": "yes", "toilets:wheelchair": "no"})
    brief = place_plain_brief(place)
    assert ("Place", "Cluj Library") in brief
    d = dict(brief)
    assert d["Step-free access"] == "yes"
    assert d["Accessible toilet"] == "no"
    assert (
        d["Baby changing table"] == "not recorded"
    )  # unknown -> honest, never asserted accessible


def test_address_included_when_present():
    place = _place(address_street="Main St", address_housenumber="1", address_city="Cluj")
    d = dict(place_plain_brief(place))
    assert "Where" in d and "Cluj" in d["Where"]


def test_list_surface_excludes_venue_facts_no_n_plus_1():
    # venue_fact_rows=None (the up-to-200-row list surface) -> accessibility-only; the brief must
    # NEVER call venue_facts() per row (the fact_votes N+1 the reshape forbids).
    labels = _labels(place_plain_brief(_place()))
    assert "Drinking water" not in labels  # a venue fact (FactKey), absent in list mode
    assert "Toilets" not in labels


def test_detail_surface_includes_passed_venue_facts():
    place = _place()
    d = dict(place_plain_brief(place, venue_fact_rows=venue_facts_detail(place)))
    assert "Drinking water" in d  # crowd venue fact added on the detail surface
    assert d["Drinking water"] in ("yes", "no", "limited", "not recorded")


def test_facts_are_plain_state_words_never_counts():
    place = _place(raw_tags={"wheelchair": "yes"})
    brief = place_plain_brief(place, venue_fact_rows=venue_facts_detail(place))
    states = {"yes", "no", "limited", "not recorded"}
    fact_sentences = [s for label, s in brief if label not in ("Place", "Where")]
    assert fact_sentences  # there are fact rows
    assert all(s in states for s in fact_sentences)  # only states, never a vote count


def test_place_detail_renders_aria_landmarked_brief():
    place = _place(name="Read Aloud Hall", raw_tags={"wheelchair": "yes"})
    body = Client().get(f"/places/{place.id}/").content.decode()
    assert 'aria-labelledby="place-brief-heading"' in body
    assert "At a glance" in body
    assert "Step-free access" in body
