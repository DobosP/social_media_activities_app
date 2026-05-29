"""Wikidata (SPARQL) enrichment — CC0, no API key.

Backfills a place's official **website** (Wikidata property P856) from its Wikidata QID
(the OSM ``wikidata`` tag we already store in ``raw_tags``), querying in batches. Network
goes through :meth:`_run_query`, which tests patch — no network or key required.

Policy (docs/DATA_AND_INTEGRATIONS.md / SAFETY): place data is non-personal; Wikidata is
CC0; only durable, public signals (the official website) are stored. No schema change —
writes the existing ``website`` field and a marker in ``raw_tags``.
"""

import logging

from django.conf import settings

logger = logging.getLogger(__name__)

_SPARQL_URL_DEFAULT = "https://query.wikidata.org/sparql"
_BATCH = 50


class WikidataEnricher:
    def __init__(self, *, endpoint: str | None = None, user_agent: str | None = None):
        self.endpoint = endpoint or getattr(settings, "WIKIDATA_SPARQL_URL", _SPARQL_URL_DEFAULT)
        self.user_agent = user_agent or getattr(
            settings, "INGEST_USER_AGENT", "social-activities-app"
        )

    @staticmethod
    def qid_for(place) -> str | None:
        qid = (place.raw_tags or {}).get("wikidata")
        if isinstance(qid, str) and qid.startswith("Q") and qid[1:].isdigit():
            return qid
        return None

    def _run_query(self, qids: list[str]) -> dict[str, str]:
        """Return {QID: official_website} for the given QIDs in one SPARQL query."""
        import requests

        values = " ".join(f"wd:{q}" for q in qids)
        query = (
            "SELECT ?item ?website WHERE { "
            f"VALUES ?item {{ {values} }} "
            "OPTIONAL { ?item wdt:P856 ?website. } }"
        )
        resp = requests.get(
            self.endpoint,
            params={"query": query, "format": "json"},
            headers={
                "User-Agent": self.user_agent,
                "Accept": "application/sparql-results+json",
            },
            timeout=30,
        )
        resp.raise_for_status()
        websites: dict[str, str] = {}
        for binding in resp.json().get("results", {}).get("bindings", []):
            qid = binding.get("item", {}).get("value", "").rsplit("/", 1)[-1]
            website = binding.get("website", {}).get("value", "")
            if qid and website and qid not in websites:
                websites[qid] = website
        return websites

    def websites_for(self, qids) -> dict[str, str]:
        unique = list(dict.fromkeys(qids))
        result: dict[str, str] = {}
        for start in range(0, len(unique), _BATCH):
            chunk = unique[start : start + _BATCH]
            try:
                result.update(self._run_query(chunk))
            except Exception as exc:  # external endpoint: log and continue
                logger.warning("Wikidata query failed for %d QIDs: %s", len(chunk), exc)
        return result

    def enrich_places(self, places) -> int:
        """Backfill ``website`` for places that have a Wikidata QID and no website yet.
        Returns the number of places updated."""
        by_qid: dict[str, list] = {}
        for place in places:
            if place.website:
                continue
            qid = self.qid_for(place)
            if qid:
                by_qid.setdefault(qid, []).append(place)
        if not by_qid:
            return 0

        websites = self.websites_for(list(by_qid))
        updated = 0
        for qid, place_list in by_qid.items():
            website = websites.get(qid)
            if not website:
                continue
            for place in place_list:
                place.website = website[:500]
                place.raw_tags = {
                    **(place.raw_tags or {}),
                    "wikidata": qid,
                    "wikidata_enriched": True,
                }
                place.save(update_fields=["website", "raw_tags"])
                updated += 1
        return updated
