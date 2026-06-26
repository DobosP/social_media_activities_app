"""Unit tests for RomaniaScraperAdapter (apps/ingestion/sources/ro_scraper.py).

No network, no DB: we inject a fake RoeduClient so ``fetch()`` never does I/O, and
assert on the in-memory ``RawPlace`` it yields plus the synthesized tag heuristic.
``SimpleTestCase`` — neither the adapter, RawPlace, nor the tag heuristic touch the ORM.
"""

from __future__ import annotations

from django.test import SimpleTestCase

from apps.ingestion.sources.ro_scraper import (
    RomaniaScraperAdapter,
    _tags_for,
    app_pack_item_to_raw_place,
)


class FakeRoeduClient:
    """Stands in for RoeduClient: records iter() calls and replays canned records."""

    def __init__(self, records):
        self._records = records
        self.calls = []

    def iter(self, product, *, limit=200, max_records=None, **filters):
        self.calls.append({"product": product, "max_records": max_records, "filters": filters})
        yield from self._records

    def iter_app_pack(self, pack, *, max_records=None, **filters):
        self.calls.append({"pack": pack, "max_records": max_records, "filters": filters})
        yield from self._records


class TagsForTests(SimpleTestCase):
    def test_opera_keyword_ro_and_en(self):
        expected = {"amenity": "theatre", "theatre:genre": "opera"}
        self.assertEqual(_tags_for("Opera Națională Română"), expected)
        self.assertEqual(_tags_for("Cluj Opera House"), expected)

    def test_opera_diacritic_form(self):
        # The 'operă' diacritic form is matched via the 'operă' needle.
        self.assertEqual(
            _tags_for("Sala de operă"),
            {"amenity": "theatre", "theatre:genre": "opera"},
        )

    def test_filarmonic_and_concert_map_to_concert_theatre(self):
        expected = {"amenity": "theatre", "theatre:type": "concert"}
        self.assertEqual(_tags_for("Filarmonica Transilvania"), expected)
        self.assertEqual(_tags_for("Sala de Concerte"), expected)

    def test_teatru_and_theatre(self):
        self.assertEqual(_tags_for("Teatrul Național"), {"amenity": "theatre"})
        self.assertEqual(_tags_for("Hungarian Theatre"), {"amenity": "theatre"})

    def test_muzeu_and_museum(self):
        self.assertEqual(_tags_for("Muzeul de Artă"), {"tourism": "museum"})
        # 'Muzeul' contains 'muzeu'; also note 'Artă' would match gallery, but museum
        # appears earlier in the heuristic list and wins.
        self.assertEqual(_tags_for("National Museum"), {"tourism": "museum"})

    def test_galerie_and_gallery(self):
        self.assertEqual(_tags_for("Galeria de Artă Contemporană"), {"tourism": "gallery"})
        self.assertEqual(_tags_for("Quadro Gallery"), {"tourism": "gallery"})

    def test_bibliotec_library(self):
        self.assertEqual(_tags_for("Biblioteca Județeană"), {"amenity": "library"})
        self.assertEqual(_tags_for("Central Library"), {"amenity": "library"})

    def test_cinema_and_film(self):
        self.assertEqual(_tags_for("Cinema Florin Piersic"), {"amenity": "cinema"})
        self.assertEqual(_tags_for("Festivalul de Film"), {"amenity": "cinema"})

    def test_default_is_arts_centre(self):
        self.assertEqual(_tags_for("Casa de Cultură a Studenților"), {"amenity": "arts_centre"})
        self.assertEqual(_tags_for(""), {"amenity": "arts_centre"})
        self.assertEqual(_tags_for(None), {"amenity": "arts_centre"})

    def test_returns_fresh_dict_each_call(self):
        # The heuristic must not hand out a shared mutable dict (caller mutates tags).
        a = _tags_for("Teatru")
        b = _tags_for("Teatru")
        self.assertIsNot(a, b)
        a["amenity"] = "MUTATED"
        self.assertEqual(b, {"amenity": "theatre"})


class FetchTests(SimpleTestCase):
    def _venue(self, **over):
        base = {
            "id": 101,
            "name": "Teatrul Național Cluj-Napoca",
            "lat": 46.7712,
            "lon": 23.5949,
            "address": "Piața Ștefan cel Mare 2-4",
            "city": "Cluj-Napoca",
            "source_url": "https://teatrulnationalcluj.ro",
        }
        base.update(over)
        return base

    def test_fetch_maps_venue_to_rawplace(self):
        client = FakeRoeduClient(
            [
                self._venue(
                    attribution="Primaria Cluj-Napoca",
                    license_name="CC BY 4.0",
                    provenance_url="https://data.example/venues/101",
                )
            ]
        )
        adapter = RomaniaScraperAdapter(client=client)
        out = list(adapter.fetch(city="Cluj-Napoca"))

        self.assertEqual(len(out), 1)
        rp = out[0]
        self.assertEqual(rp.source, "roedu")
        self.assertEqual(rp.name, "Teatrul Național Cluj-Napoca")
        self.assertEqual(rp.external_id, "101")
        self.assertEqual(rp.lat, 46.7712)
        self.assertEqual(rp.lon, 23.5949)
        # website comes from the venue's source_url.
        self.assertEqual(rp.website, "https://teatrulnationalcluj.ro")
        self.assertEqual(rp.attribution, "Primaria Cluj-Napoca")
        self.assertEqual(rp.license_name, "CC BY 4.0")
        self.assertEqual(rp.provenance_url, "https://data.example/venues/101")
        # address normalized with country forced to RO.
        self.assertEqual(rp.address["street"], "Piața Ștefan cel Mare 2-4")
        self.assertEqual(rp.address["city"], "Cluj-Napoca")
        self.assertEqual(rp.address["country"], "RO")
        # synthesized tags from the name heuristic.
        self.assertEqual(rp.tags, {"amenity": "theatre"})

    def test_fetch_passes_city_filter_and_limit_to_client(self):
        client = FakeRoeduClient([self._venue()])
        adapter = RomaniaScraperAdapter(client=client)
        list(adapter.fetch(city="Cluj-Napoca", limit=7))
        self.assertEqual(client.calls[0]["product"], "venues")
        self.assertEqual(client.calls[0]["filters"], {"city": "Cluj-Napoca"})
        self.assertEqual(client.calls[0]["max_records"], 7)

    def test_fetch_no_city_sends_no_city_filter(self):
        client = FakeRoeduClient([self._venue()])
        adapter = RomaniaScraperAdapter(client=client)
        list(adapter.fetch())
        self.assertEqual(client.calls[0]["filters"], {})

    def test_fetch_skips_venues_missing_coordinates(self):
        client = FakeRoeduClient(
            [
                self._venue(id=1),
                self._venue(id=2, lat=None),  # missing lat -> skipped
                self._venue(id=3, lon=None),  # missing lon -> skipped
                self._venue(id=4),
            ]
        )
        adapter = RomaniaScraperAdapter(client=client)
        out = list(adapter.fetch())
        self.assertEqual([rp.external_id for rp in out], ["1", "4"])

    def test_fetch_coerces_string_coordinates_to_float(self):
        client = FakeRoeduClient([self._venue(lat="46.5", lon="23.6")])
        adapter = RomaniaScraperAdapter(client=client)
        rp = list(adapter.fetch())[0]
        self.assertEqual((rp.lat, rp.lon), (46.5, 23.6))

    def test_fetch_falls_back_to_request_city_when_venue_city_blank(self):
        client = FakeRoeduClient([self._venue(city=None)])
        adapter = RomaniaScraperAdapter(client=client)
        rp = list(adapter.fetch(city="Sibiu"))[0]
        self.assertEqual(rp.address["city"], "Sibiu")

    def test_fetch_handles_missing_optional_fields(self):
        client = FakeRoeduClient([{"id": 9, "name": "Galeria X", "lat": 46.0, "lon": 23.0}])
        adapter = RomaniaScraperAdapter(client=client)
        rp = list(adapter.fetch())[0]
        self.assertEqual(rp.external_id, "9")
        self.assertEqual(rp.website, "")
        self.assertEqual(rp.attribution, "")
        self.assertEqual(rp.license_name, "")
        self.assertEqual(rp.provenance_url, "")
        self.assertEqual(rp.address["street"], "")
        self.assertEqual(rp.tags, {"tourism": "gallery"})

    def test_default_client_uses_roeduclient_when_none_injected(self):
        # Constructing without a client must not raise (it builds a RoeduClient lazily,
        # which performs no I/O until iter() is called).
        adapter = RomaniaScraperAdapter()
        from apps.ingestion.sources.roedu_client import RoeduClient

        self.assertIsInstance(adapter._client, RoeduClient)


class AppPackMappingTests(SimpleTestCase):
    def _item(self, **over):
        base = {
            "id": "venue-1",
            "kind": "venue",
            "title": "Biblioteca Județeană Cluj",
            "tags": ["venue:library", "audience:public"],
            "facets": {
                "city": "Cluj-Napoca",
                "county": "Cluj",
                "category": "library",
                "venue_category": "library",
            },
            "source": "synthetic-fixture",
            "license": "CC BY 4.0",
            "access_type": "open_license",
            "legal_basis": "fixture license",
            "gdpr_relevant": False,
            "redistributable": True,
            "confidence": 0.95,
            "location": {"lat": 46.77, "lon": 23.59},
            "address": {"street": "Strada Test 1"},
            "website": "https://library.example.test",
        }
        base.update(over)
        return base

    def test_app_pack_item_maps_tags_facets_and_license_to_raw_place(self):
        raw = app_pack_item_to_raw_place(self._item(), city="Fallback")

        self.assertIsNotNone(raw)
        self.assertEqual(raw.source, "roedu")
        self.assertEqual(raw.external_id, "venue-1")
        self.assertEqual(raw.name, "Biblioteca Județeană Cluj")
        self.assertEqual((raw.lat, raw.lon), (46.77, 23.59))
        self.assertEqual(raw.website, "https://library.example.test")
        self.assertEqual(raw.attribution, "synthetic-fixture")
        self.assertEqual(raw.license_name, "CC BY 4.0")
        self.assertEqual(raw.provenance_url, "")
        self.assertEqual(raw.address["street"], "Strada Test 1")
        self.assertEqual(raw.address["city"], "Cluj-Napoca")
        self.assertEqual(raw.address["county"], "Cluj")
        self.assertEqual(raw.address["country"], "RO")
        self.assertEqual(raw.tags["amenity"], "library")
        self.assertEqual(raw.tags["roedu:tags"], ["venue:library", "audience:public"])
        self.assertEqual(raw.tags["roedu:city"], "Cluj-Napoca")
        self.assertEqual(raw.tags["roedu:county"], "Cluj")
        self.assertEqual(raw.tags["roedu:category"], "library")
        self.assertEqual(raw.tags["roedu:venue_category"], "library")
        self.assertEqual(raw.tags["roedu:source"], "synthetic-fixture")
        self.assertEqual(raw.tags["roedu:confidence"], 0.95)

    def test_app_pack_mapping_handles_missing_tags_gracefully(self):
        raw = app_pack_item_to_raw_place(
            self._item(tags=None, facets={}, title="Loc Cultural", address={}),
            city="Cluj-Napoca",
        )

        self.assertIsNotNone(raw)
        self.assertEqual(
            raw.tags,
            {
                "amenity": "arts_centre",
                "roedu:source": "synthetic-fixture",
                "roedu:confidence": 0.95,
            },
        )
        self.assertEqual(raw.address["city"], "Cluj-Napoca")

    def test_app_pack_mapping_skips_non_places_and_missing_coordinates(self):
        self.assertIsNone(app_pack_item_to_raw_place(self._item(kind="event")))
        self.assertIsNone(app_pack_item_to_raw_place(self._item(location={"lat": 46.77})))

    def test_fetch_can_consume_configured_app_pack(self):
        client = FakeRoeduClient([self._item()])
        adapter = RomaniaScraperAdapter(client=client, app_pack="events_places")
        out = list(adapter.fetch(city="Cluj-Napoca", limit=5))

        self.assertEqual([raw.external_id for raw in out], ["venue-1"])
        self.assertEqual(client.calls[0]["pack"], "events_places")
        self.assertEqual(client.calls[0]["max_records"], 5)
        self.assertEqual(client.calls[0]["filters"], {"kind": "venue", "city": "Cluj-Napoca"})
