"""ADR-0019 §4 cleanup regressions: getting_home_note + the Plan-B fallback pair are GONE.

The columns are dropped (migration 0035), the DRF serializers no longer accept or echo the
keys, and the one-shot ``invoke_fallback`` affordance (POST .../fallback/) is removed — the
audited ``move_activity`` path is the replacement (covered in test_p4_organizer_form_v2).
These tests pin the retirement so a rebase/revert can't quietly resurrect the surface.
"""

import pytest
from rest_framework.test import APIClient

from apps.social.models import Activity, ActivitySeries
from apps.social.services import create_activity

pytestmark = pytest.mark.django_db

RETIRED_ACTIVITY_KEYS = ("getting_home_note", "fallback_starts_at", "fallback_meeting_point")


def _client(user):
    client = APIClient()
    client.force_authenticate(user)
    return client


def test_models_carry_no_retired_fields():
    activity_fields = {f.name for f in Activity._meta.get_fields()}
    series_fields = {f.name for f in ActivitySeries._meta.get_fields()}
    assert not activity_fields & set(RETIRED_ACTIVITY_KEYS)
    assert "getting_home_note" not in series_fields


def test_api_create_ignores_retired_keys_and_never_echoes_them(adult, place, activity_type, now):
    resp = _client(adult).post(
        "/api/social/activities/",
        {
            "place": place.id,
            "activity_type": activity_type.id,
            "title": "Evening run",
            "starts_at": now.isoformat(),
            # Old clients may still send these — they must be ignored, not 500/400.
            "getting_home_note": "Bus 25 home",
            "fallback_starts_at": now.isoformat(),
            "fallback_meeting_point": "Covered pavilion",
        },
        format="json",
    )
    assert resp.status_code == 201, resp.content
    data = resp.json()
    for key in RETIRED_ACTIVITY_KEYS:
        assert key not in data


def test_api_patch_ignores_retired_keys(adult, place, activity_type, now):
    from datetime import timedelta

    activity = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Run",
        starts_at=now + timedelta(days=1),
    )
    resp = _client(adult).patch(
        f"/api/social/activities/{activity.id}/",
        {"getting_home_note": "resurrected?", "fallback_starts_at": now.isoformat()},
        format="json",
    )
    assert resp.status_code == 200, resp.content
    for key in RETIRED_ACTIVITY_KEYS:
        assert key not in resp.json()


def test_fallback_action_is_gone(adult, place, activity_type, now):
    from datetime import timedelta

    activity = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Run",
        starts_at=now + timedelta(days=1),
    )
    resp = _client(adult).post(f"/api/social/activities/{activity.id}/fallback/", {})
    assert resp.status_code == 404
