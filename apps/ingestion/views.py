"""W9: machine-to-machine ingestion API for the planned external aggregator.

Two endpoints, both ADMIN-ONLY (a service account is a staff user; with W10's token
auth it calls with a Bearer token — no session/cookie dance):

- POST /api/ingestion/batch-events/  — idempotent event upsert (the same
  ``upsert_event`` path the iCal sync uses, keyed by (source, external_id); namespace
  your external ids per upstream source to avoid UID collisions).
- GET  /api/ingestion/match-place/   — "is this venue already known?": the same
  fuzzy geo+name dedup the place ingester uses, so the aggregator can link events to
  canonical places instead of minting duplicates.

User-confirmed overlays (F25/F26/F28) live in separate tables the ingest path never
writes, so nothing here can clobber crowd corrections."""

from django.contrib.gis.geos import Point
from rest_framework import status
from rest_framework.permissions import IsAdminUser
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.events.models import Event
from apps.events.services import upsert_event
from apps.events.sources import RawEvent
from apps.places.enrichment.dedup import find_duplicate
from apps.places.models import Place
from apps.taxonomy.models import ActivityType

MAX_BATCH = 500


class BatchEventsView(APIView):
    permission_classes = [IsAdminUser]

    def post(self, request):
        rows = request.data if isinstance(request.data, list) else request.data.get("events")
        if not isinstance(rows, list) or not rows:
            return Response(
                {"detail": "Provide a JSON list of events (or {'events': [...]})."},
                status=status.HTTP_400_BAD_REQUEST,
            )
        if len(rows) > MAX_BATCH:
            return Response(
                {"detail": f"At most {MAX_BATCH} events per batch."},
                status=status.HTTP_400_BAD_REQUEST,
            )
        valid_sources = set(Event.Source.values)
        processed, errors = 0, []
        for i, row in enumerate(rows):
            try:
                source = row.get("source") or Event.Source.MANUAL
                if source not in valid_sources:
                    raise ValueError(f"unknown source '{source}'")
                place = None
                if row.get("place_id") is not None:
                    place = Place.objects.filter(pk=int(row["place_id"])).first()
                    if place is None:
                        raise ValueError(f"no place {row['place_id']}")
                activity_type = None
                if row.get("activity_type"):
                    activity_type = ActivityType.objects.filter(
                        slug=row["activity_type"], is_active=True
                    ).first()
                raw = RawEvent(
                    title=str(row["title"])[:255],
                    starts_at=row["starts_at"],
                    ends_at=row.get("ends_at"),
                    description=str(row.get("description", ""))[:5000],
                    url=str(row.get("url", ""))[:500],
                    external_id=str(row.get("external_id", ""))[:200],
                    source=source,
                )
                # DRF leaves nested JSON datetimes as strings — parse here.
                from django.utils.dateparse import parse_datetime

                if isinstance(raw.starts_at, str):
                    parsed = parse_datetime(raw.starts_at)
                    if parsed is None:
                        raise ValueError("starts_at is not an ISO datetime")
                    raw.starts_at = parsed
                if isinstance(raw.ends_at, str):
                    raw.ends_at = parse_datetime(raw.ends_at)
                upsert_event(raw, place=place, activity_type=activity_type, source=source)
                processed += 1
            except (KeyError, TypeError, ValueError) as exc:
                errors.append({"index": i, "error": str(exc)})
        return Response({"processed": processed, "errors": errors})


class MatchPlaceView(APIView):
    permission_classes = [IsAdminUser]

    def get(self, request):
        p = request.query_params
        try:
            point = Point(float(p["lon"]), float(p["lat"]), srid=4326)
        except (KeyError, TypeError, ValueError):
            return Response(
                {"detail": "lon and lat are required floats."},
                status=status.HTTP_400_BAD_REQUEST,
            )
        name = (p.get("name") or "").strip()
        match = find_duplicate(point, name) if name else None
        if match is None:
            return Response({"match": None})
        return Response(
            {
                "match": {
                    "id": match.pk,
                    "name": match.name,
                    "source": match.source,
                    "distance_m": round(match.distance.m, 1)
                    if getattr(match, "distance", None)
                    else None,
                }
            }
        )
