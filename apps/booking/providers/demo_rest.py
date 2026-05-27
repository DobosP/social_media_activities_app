"""Example REST booking provider.

A reference implementation of a real per-provider integration over the common
interface: it talks to a provider's REST API for availability / create / cancel.
HTTP calls (``requests``) are made lazily and isolated in ``_get``/``_post`` so
tests patch them — no network or credentials required to exercise the adapter.

Configure via settings ``BOOKING_DEMO_BASE_URL`` / ``BOOKING_DEMO_API_KEY``.
"""

from __future__ import annotations

from datetime import datetime

from django.conf import settings

from .base import BookingError, BookingProvider, BookingResult, Slot


class DemoRestProvider(BookingProvider):
    name = "demo_rest"
    supports_realtime = True

    def __init__(self):
        self.base_url = getattr(settings, "BOOKING_DEMO_BASE_URL", "").rstrip("/")
        self.api_key = getattr(settings, "BOOKING_DEMO_API_KEY", "")

    def _headers(self) -> dict:
        return {"Authorization": f"Bearer {self.api_key}"} if self.api_key else {}

    def _get(self, path: str, *, params: dict) -> dict:
        import requests

        resp = requests.get(
            f"{self.base_url}{path}", params=params, headers=self._headers(), timeout=15
        )
        resp.raise_for_status()
        return resp.json()

    def _post(self, path: str, *, json: dict) -> dict:
        import requests

        resp = requests.post(
            f"{self.base_url}{path}", json=json, headers=self._headers(), timeout=15
        )
        resp.raise_for_status()
        return resp.json()

    def availability(self, *, place_ref: str, start: datetime, end: datetime) -> list[Slot]:
        data = self._get(
            "/availability",
            params={"venue": place_ref, "from": start.isoformat(), "to": end.isoformat()},
        )
        return [
            Slot(
                start=datetime.fromisoformat(s["start"]),
                end=datetime.fromisoformat(s["end"]),
                available=bool(s.get("available", True)),
            )
            for s in data.get("slots", [])
        ]

    def create_booking(self, *, place_ref, start, end, party_size, user_ref) -> BookingResult:
        if not self.base_url:
            raise BookingError("demo_rest provider is not configured (BOOKING_DEMO_BASE_URL)")
        data = self._post(
            "/bookings",
            json={
                "venue": place_ref,
                "start": start.isoformat(),
                "end": end.isoformat() if end else None,
                "party_size": party_size,
                "customer_ref": user_ref,
            },
        )
        ref = data.get("id") or data.get("booking_id")
        if not ref:
            raise BookingError("provider did not return a booking id")
        return BookingResult(
            external_ref=str(ref), confirmed=data.get("status") == "confirmed", raw=data
        )

    def cancel(self, *, external_ref: str) -> None:
        self._post(f"/bookings/{external_ref}/cancel", json={})
