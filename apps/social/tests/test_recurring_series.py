"""F4 recurring activity series: spawn engine, safety edges, web + DRF surfaces."""

from datetime import datetime, timedelta

import pytest
from django.core.management import call_command
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.safety.models import AuditLog
from apps.social import services as social
from apps.social.models import Activity, ActivitySeries, Membership

pytestmark = pytest.mark.django_db


def _series(owner, place, activity_type, *, first_starts_at, cadence=ActivitySeries.Cadence.WEEKLY):
    return social.create_series(
        owner,
        place=place,
        activity_type=activity_type,
        title="Tuesday run",
        cadence=cadence,
        first_starts_at=first_starts_at,
        meeting_point="North gate",
        cost_band=Activity.CostBand.FREE,
        beginners_welcome=True,
    )


# --- date math ----------------------------------------------------------------------


def test_advance_slot_weekly_and_biweekly():
    with timezone.override("Europe/Bucharest"):
        start = timezone.make_aware(datetime(2026, 6, 2, 18, 0))  # mid-June: no DST boundary nearby
        assert social._advance_slot(start, ActivitySeries.Cadence.WEEKLY) == start + timedelta(
            weeks=1
        )
        assert social._advance_slot(start, ActivitySeries.Cadence.BIWEEKLY) == start + timedelta(
            weeks=2
        )


def test_advance_preserves_local_time_across_dst():
    """A weekly meetup keeps its LOCAL start hour across both DST transitions in the launch city."""
    with timezone.override("Europe/Bucharest"):
        spring = timezone.make_aware(
            datetime(2026, 3, 22, 18, 0)
        )  # week of spring-forward (Mar 29)
        local = timezone.localtime(social._advance_slot(spring, ActivitySeries.Cadence.WEEKLY))
        assert (local.month, local.day, local.hour) == (3, 29, 18)
        autumn = timezone.make_aware(datetime(2026, 10, 18, 18, 0))  # week of fall-back (Oct 25)
        local2 = timezone.localtime(social._advance_slot(autumn, ActivitySeries.Cadence.WEEKLY))
        assert (local2.month, local2.day, local2.hour) == (10, 25, 18)


def test_add_month_clamps_day():
    jan31 = timezone.make_aware(datetime(2026, 1, 31, 18, 0))
    nxt = social._add_month(jan31)
    assert (nxt.year, nxt.month, nxt.day) == (2026, 2, 28)  # clamped, no Feb 31
    dec = timezone.make_aware(datetime(2026, 12, 15, 9, 0))
    assert (social._add_month(dec).year, social._add_month(dec).month) == (2027, 1)


# --- spawn engine -------------------------------------------------------------------


def test_spawns_next_instance_copying_template(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    result = social.spawn_due_series(now=now)
    assert result == {"spawned": 1, "skipped": 0, "paused": 0}
    instances = list(Activity.objects.filter(series=s))
    assert len(instances) == 1
    a = instances[0]
    assert a.starts_at == now + timedelta(days=1)
    assert a.owner_id == adult.id and a.place_id == place.id
    assert a.activity_type_id == activity_type.id and a.cohort == adult.cohort
    assert a.meeting_point == "North gate" and a.cost_band == Activity.CostBand.FREE
    assert a.beginners_welcome is True
    s.refresh_from_db()
    assert s.next_starts_at == now + timedelta(days=1) + timedelta(weeks=1)  # cursor advanced once


def test_spawned_instance_has_only_owner_membership(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.spawn_due_series(now=now)
    a = Activity.objects.get(series=s)
    members = Membership.objects.filter(activity=a, state=Membership.State.MEMBER)
    assert members.count() == 1
    assert members.first().role == Membership.Role.OWNER  # no roster carried across instances


def test_one_upcoming_instance_at_a_time(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.spawn_due_series(now=now)
    # A second tick while the first instance is still upcoming spawns nothing more.
    social.spawn_due_series(now=now + timedelta(hours=6))
    assert Activity.objects.filter(series=s).count() == 1


def test_idempotent_within_a_tick(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.spawn_due_series(now=now)
    social.spawn_due_series(now=now)  # same tick, re-run
    assert Activity.objects.filter(series=s).count() == 1


def test_spawns_again_once_prior_instance_has_passed(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.spawn_due_series(now=now)  # instance @ now+1d, cursor -> now+8d
    later = now + timedelta(days=2)  # the first instance is now in the past
    social.spawn_due_series(now=later)
    assert Activity.objects.filter(series=s).count() == 2


def test_not_yet_within_lead_window_does_not_spawn(adult, place, activity_type, now, settings):
    settings.SERIES_SPAWN_LEAD_DAYS = 14
    _series(adult, place, activity_type, first_starts_at=now + timedelta(days=40))
    assert social.spawn_due_series(now=now)["spawned"] == 0  # 40d out > 14d lead


def test_far_past_cursor_fast_forwards_no_backfill(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now - timedelta(weeks=5))
    social.spawn_due_series(now=now)
    a = Activity.objects.get(series=s)
    assert a.starts_at >= now  # never spawns a past-dated meetup
    assert Activity.objects.filter(series=s).count() == 1  # no backfill of the 5 missed weeks


def test_per_series_failure_isolation(adult, adult2, place, activity_type, now):
    bad = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    good = _series(adult2, place, activity_type, first_starts_at=now + timedelta(days=1))
    # Make the owner of `bad` ineligible (same cohort) -> create_activity raises -> skip, not abort.
    adult.is_identity_verified = False
    adult.save(update_fields=["is_identity_verified"])
    result = social.spawn_due_series(now=now)
    assert result["spawned"] == 1 and result["skipped"] == 1
    assert Activity.objects.filter(series=good).count() == 1
    assert Activity.objects.filter(series=bad).count() == 0
    bad.refresh_from_db()
    assert bad.status == ActivitySeries.Status.ACTIVE  # transient loss self-heals, not paused


def test_cohort_drift_pauses_and_audits(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    apply_assurance(adult, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    adult.refresh_from_db()
    assert adult.cohort != s.cohort
    result = social.spawn_due_series(now=now)
    assert result == {"spawned": 0, "skipped": 0, "paused": 1}
    assert Activity.objects.filter(series=s).count() == 0  # never spawned into the wrong cohort
    s.refresh_from_db()
    assert s.status == ActivitySeries.Status.PAUSED
    assert AuditLog.objects.filter(event="series.paused").exists()


def test_paused_and_ended_series_do_not_spawn(adult, place, activity_type, now):
    paused = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.pause_series(adult, paused)
    ended = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.end_series(adult, ended)
    assert social.spawn_due_series(now=now)["spawned"] == 0
    assert Activity.objects.filter(series__in=[paused, ended]).count() == 0


def test_spawn_writes_no_notifications(adult, place, activity_type, now):
    _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.spawn_due_series(now=now)
    assert Notification.objects.count() == 0  # spawn-silent; discovery via the feed


def test_audit_events_for_lifecycle(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    assert AuditLog.objects.filter(event="series.created").exists()
    social.spawn_due_series(now=now)
    assert AuditLog.objects.filter(event="series.spawned").exists()
    social.pause_series(adult, s)
    assert AuditLog.objects.filter(event="series.paused").exists()


# --- owner-only transitions ---------------------------------------------------------


def test_pause_end_owner_only(adult, adult2, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    with pytest.raises(social.NotAMember):
        social.pause_series(adult2, s)
    with pytest.raises(social.NotAMember):
        social.end_series(adult2, s)
    social.pause_series(adult, s)
    with pytest.raises(social.InvalidState):
        social.pause_series(adult, s)  # not active anymore


def test_create_series_rejects_bad_inputs(adult, place, activity_type, now):
    with pytest.raises(social.InvalidState):
        social.create_series(
            adult,
            place=place,
            activity_type=activity_type,
            title="x",
            cadence="never",  # invalid cadence
            first_starts_at=now + timedelta(days=1),
        )
    with pytest.raises(social.InvalidState):
        social.create_series(
            adult,
            place=place,
            activity_type=activity_type,
            title="x",
            cadence=ActivitySeries.Cadence.WEEKLY,
            first_starts_at=now + timedelta(days=1),
            capacity=2,
            min_to_go=5,  # min_to_go > capacity
        )


# --- DRF API ------------------------------------------------------------------------


def test_api_create_and_owner_walls(adult, adult2, place, activity_type, now):
    c = APIClient()
    c.force_authenticate(adult)
    body = {
        "place": place.id,
        "activity_type": activity_type.id,
        "title": "API series",
        "cadence": ActivitySeries.Cadence.WEEKLY,
        "first_starts_at": (now + timedelta(days=1)).isoformat(),
    }
    resp = c.post("/api/social/series/", body, format="json")
    assert resp.status_code == 201
    sid = resp.data["id"]
    # Non-numeric pk is 404 (lookup_value_regex), never a 500.
    assert c.get("/api/social/series/abc/").status_code == 404
    # A different user can neither see nor control it (owner-walled).
    other = APIClient()
    other.force_authenticate(adult2)
    assert other.get(f"/api/social/series/{sid}/").status_code == 404
    assert other.post(f"/api/social/series/{sid}/pause/").status_code in (403, 404)
    # The owner can pause it.
    assert c.post(f"/api/social/series/{sid}/pause/").status_code == 200


def test_api_serializer_allowlist_no_rollup(adult, place, activity_type, now):
    from apps.social.serializers import SeriesSerializer

    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    data = SeriesSerializer(s).data
    forbidden = {"roster", "members", "member_count", "participants", "attendance", "instances"}
    assert forbidden.isdisjoint(data.keys())
    assert not any(k.endswith("_count") or k.endswith("_n") for k in data)


# --- web surfaces -------------------------------------------------------------------


def test_web_create_and_owner_controls(client, adult, place, activity_type, now):
    client.force_login(adult)
    resp = client.post(
        "/activities/series/new/",
        {
            "place": place.id,
            "activity_type": activity_type.id,
            "title": "Web series",
            "cadence": ActivitySeries.Cadence.WEEKLY,
            "first_starts_at": (now + timedelta(days=1)).strftime("%Y-%m-%dT%H:%M"),
        },
    )
    assert resp.status_code == 302
    s = ActivitySeries.objects.get(title="Web series")
    assert s.owner_id == adult.id and s.cohort == adult.cohort
    # Owner sees the detail; pause works via the web POST.
    assert client.get(f"/activities/series/{s.pk}/").status_code == 200
    client.post(f"/activities/series/{s.pk}/pause/")
    s.refresh_from_db()
    assert s.status == ActivitySeries.Status.PAUSED


def test_web_series_detail_owner_only(client, adult, adult2, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    client.force_login(adult2)
    assert client.get(f"/activities/series/{s.pk}/").status_code == 404  # not the owner


# --- ops wiring ---------------------------------------------------------------------


def test_registered_in_due_jobs_and_command_noop_on_empty_db():
    import apps.ops.management.commands.run_due_jobs as run_due_jobs

    assert "spawn_due_series" in {name for name, _ in run_due_jobs.DUE_JOBS}
    call_command("spawn_due_series")  # clean no-op on an empty DB, raises nothing


# --- review follow-ups: edge coverage -----------------------------------------------


def test_guardian_accompanied_rejected_for_adult(adult, place, activity_type, now):
    with pytest.raises(social.InvalidState):
        social.create_series(
            adult,
            place=place,
            activity_type=activity_type,
            title="x",
            cadence=ActivitySeries.Cadence.WEEKLY,
            first_starts_at=now + timedelta(days=1),
            guardian_accompanied=True,
        )


def test_guardian_accompanied_child_series_spawns_with_flag(child, place, activity_type, now):
    s = social.create_series(
        child,
        place=place,
        activity_type=activity_type,
        title="Kids club",
        cadence=ActivitySeries.Cadence.WEEKLY,
        first_starts_at=now + timedelta(days=1),
        guardian_accompanied=True,
    )
    assert s.guardian_accompanied is True and s.cohort == child.cohort
    social.spawn_due_series(now=now)
    a = Activity.objects.get(series=s)
    assert a.guardian_accompanied is True and a.cohort == child.cohort  # CHILD pin preserved


def test_spawned_instance_derives_fresh_ends_at(adult, place, activity_type, now):
    first = now + timedelta(days=1)
    s = social.create_series(
        adult,
        place=place,
        activity_type=activity_type,
        title="Run",
        cadence=ActivitySeries.Cadence.WEEKLY,
        first_starts_at=first,
        ends_at=first + timedelta(hours=2),
    )
    assert s.duration_minutes == 120
    social.spawn_due_series(now=now)
    a = Activity.objects.get(series=s)
    assert a.ends_at == a.starts_at + timedelta(minutes=120)  # fresh ends_at per instance


def test_spawns_monthly_and_advances_one_calendar_month(adult, place, activity_type):
    with timezone.override("Europe/Bucharest"):
        start = timezone.make_aware(datetime(2026, 1, 31, 18, 0))
        now = start - timedelta(days=2)
        s = social.create_series(
            adult,
            place=place,
            activity_type=activity_type,
            title="Monthly",
            cadence=ActivitySeries.Cadence.MONTHLY,
            first_starts_at=start,
        )
        assert s.anchor_day == 31
        social.spawn_due_series(now=now)
        assert Activity.objects.get(series=s).starts_at == start
        s.refresh_from_db()
        nxt = timezone.localtime(s.next_starts_at)
        assert (nxt.month, nxt.day, nxt.hour) == (2, 28, 18)  # clamped to Feb 28, 18:00 local


def test_monthly_anchor_recovers_full_day_after_short_month():
    # 31st-of-month series: Jan 31 -> Feb 28 -> Mar 31 (anchor recovers, never decays to the 28th).
    with timezone.override("Europe/Bucharest"):
        feb = timezone.make_aware(datetime(2026, 2, 28, 18, 0))
        mar = social._advance_slot(feb, ActivitySeries.Cadence.MONTHLY, anchor_day=31)
        local = timezone.localtime(mar)
        assert (local.month, local.day) == (3, 31)


def test_lead_window_exact_edge_spawns(adult, place, activity_type, now, settings):
    settings.SERIES_SPAWN_LEAD_DAYS = 14
    _series(adult, place, activity_type, first_starts_at=now + timedelta(days=14))
    assert social.spawn_due_series(now=now)["spawned"] == 1  # inclusive at the lead edge


def test_extremely_stale_cursor_never_spawns_past(adult, place, activity_type, now):
    # Cursor stale beyond the 240-step fast-forward cap (weekly): fail closed, no past spawn.
    s = _series(adult, place, activity_type, first_starts_at=now - timedelta(weeks=300))
    result = social.spawn_due_series(now=now)
    assert result["spawned"] == 0 and result["skipped"] == 1
    assert Activity.objects.filter(series=s).count() == 0


def test_resume_never_backfills(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.pause_series(adult, s)
    later = now + timedelta(weeks=6)  # several slots missed while paused
    social.resume_series(adult, s)
    social.spawn_due_series(now=later)
    instances = Activity.objects.filter(series=s)
    assert instances.count() == 1  # no backfill of the missed weeks
    assert instances.first().starts_at >= later


def test_api_resume_and_end_owner_only(adult, adult2, place, activity_type, now):
    c = APIClient()
    c.force_authenticate(adult)
    body = {
        "place": place.id,
        "activity_type": activity_type.id,
        "title": "API series",
        "cadence": ActivitySeries.Cadence.WEEKLY,
        "first_starts_at": (now + timedelta(days=1)).isoformat(),
    }
    sid = c.post("/api/social/series/", body, format="json").data["id"]
    c.post(f"/api/social/series/{sid}/pause/")
    other = APIClient()
    other.force_authenticate(adult2)
    assert other.post(f"/api/social/series/{sid}/resume/").status_code in (403, 404)
    assert c.post(f"/api/social/series/{sid}/resume/").status_code == 200
    assert other.post(f"/api/social/series/{sid}/end/").status_code in (403, 404)
    assert c.post(f"/api/social/series/{sid}/end/").status_code == 200
    assert ActivitySeries.objects.get(pk=sid).status == ActivitySeries.Status.ENDED


def test_web_resume_and_end(client, adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.pause_series(adult, s)
    client.force_login(adult)
    client.post(f"/activities/series/{s.pk}/resume/")
    s.refresh_from_db()
    assert s.status == ActivitySeries.Status.ACTIVE
    client.post(f"/activities/series/{s.pk}/end/")
    s.refresh_from_db()
    assert s.status == ActivitySeries.Status.ENDED


def test_series_delete_detaches_instances(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, first_starts_at=now + timedelta(days=1))
    social.spawn_due_series(now=now)
    a = Activity.objects.get(series=s)
    s.delete()
    a.refresh_from_db()
    assert a.series_id is None  # SET_NULL: the meetup stands as a one-off
    assert Activity.objects.filter(pk=a.pk).exists()


# --- ADR-0019 §4 parity: concrete cost templates onto every spawned instance -----------


def test_series_concrete_cost_copied_to_spawned_instance(adult, place, activity_type, now):
    s = social.create_series(
        adult,
        place=place,
        activity_type=activity_type,
        title="Paid run",
        cadence=ActivitySeries.Cadence.WEEKLY,
        first_starts_at=now + timedelta(days=1),
        cost_band=Activity.CostBand.PAID,
        cost_amount="25.00",
        cost_note="court rental",
    )
    assert str(s.cost_amount) == "25.00" and s.cost_note == "court rental"
    social.spawn_due_series(now=now)
    a = Activity.objects.get(series=s)
    assert str(a.cost_amount) == "25.00"
    assert a.cost_note == "court rental"
    assert a.cost_band == Activity.CostBand.PAID


def test_create_series_rejects_cost_amount_on_free_band(adult, place, activity_type, now):
    # Same one-fact rule as create_activity: an amount contradicts an explicitly FREE series.
    with pytest.raises(social.InvalidState):
        social.create_series(
            adult,
            place=place,
            activity_type=activity_type,
            title="x",
            cadence=ActivitySeries.Cadence.WEEKLY,
            first_starts_at=now + timedelta(days=1),
            cost_band=Activity.CostBand.FREE,
            cost_amount="10.00",
        )
