"""F3 matcher: cohort gate, one-notice-per-(user, activity)-ever, anti-flood, isolation."""

from datetime import timedelta
from zoneinfo import ZoneInfo

import pytest
from django.utils import timezone

from apps.notifications.models import MUTABLE_KINDS, NON_MUTABLE_KINDS, Notification
from apps.notifications.services import set_muted_kinds, why_reason
from apps.safety.models import Block
from apps.saved_searches import services as ss
from apps.saved_searches.models import SavedSearchMatch
from apps.social.models import ActivityInterest
from apps.social.services import create_activity

pytestmark = pytest.mark.django_db
CW = ActivityInterest.CoarseWindow
_TZ = ZoneInfo("Europe/Bucharest")


def _activity(owner, place, activity_type, *, days=3, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Match me",
        starts_at=timezone.now() + timedelta(days=days),
        **kw,
    )


def _local_dt(weekday_iso, hour):
    """A future aware datetime whose LOCAL (Europe/Bucharest) ISO weekday == weekday_iso
    (1=Mon..7=Sun) and local hour == ``hour``. Built ~2 weeks out so it's always upcoming;
    whole-day shifts preserve the wall-clock hour across any DST boundary."""
    base = (timezone.now().astimezone(_TZ) + timedelta(days=14)).replace(
        hour=hour, minute=0, second=0, microsecond=0
    )
    return base + timedelta(days=(weekday_iso - base.isoweekday()) % 7)


def _at(owner, place, activity_type, starts_at):
    return create_activity(
        owner, place=place, activity_type=activity_type, title="When", starts_at=starts_at
    )


def _match_ids(saved_search, viewer):
    return set(ss.matching_activities(saved_search, viewer).values_list("id", flat=True))


def _match_count(user, kind=Notification.Kind.ACTIVITY_MATCH):
    return Notification.objects.filter(recipient=user, kind=kind).count()


def test_fires_one_notice_for_a_new_match(adult, adult2, place, activity_type, now):
    ss.create_saved_search(adult, activity_type=activity_type)
    a = _activity(adult2, place, activity_type)
    result = ss.match_saved_searches(now=now)
    assert result["notified"] == 1
    n = Notification.objects.get(recipient=adult, kind=Notification.Kind.ACTIVITY_MATCH)
    assert n.url == f"/activities/{a.id}/"


def test_idempotent_across_ticks(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _activity(adult2, place, activity_type)
    ss.match_saved_searches()
    ss.match_saved_searches()
    assert _match_count(adult) == 1
    assert SavedSearchMatch.objects.filter(user=adult).count() == 1


def test_one_notice_per_user_across_multiple_searches(
    adult, adult2, place, activity_type, category
):
    ss.create_saved_search(adult, activity_type=activity_type)
    ss.create_saved_search(adult, category=category)  # both match the same activity
    _activity(adult2, place, activity_type)
    ss.match_saved_searches()
    assert _match_count(adult) == 1  # ledger keyed on (user, activity)


def test_no_renotify_after_unmute(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _activity(adult2, place, activity_type)
    set_muted_kinds(adult, [Notification.Kind.ACTIVITY_MATCH])
    ss.match_saved_searches()  # ledger written, notify suppressed
    assert _match_count(adult) == 0
    assert SavedSearchMatch.objects.filter(user=adult).count() == 1
    set_muted_kinds(adult, [])
    ss.match_saved_searches()  # must NOT re-fire
    assert _match_count(adult) == 0


def test_cross_cohort_never_notifies(adult, child, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _activity(child, place, activity_type)  # CHILD-cohort activity
    ss.match_saved_searches()
    assert _match_count(adult) == 0


def test_hidden_activity_excluded(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    a = _activity(adult2, place, activity_type)
    a.is_hidden = True
    a.save(update_fields=["is_hidden"])
    ss.match_saved_searches()
    assert _match_count(adult) == 0


def test_cancelled_and_completed_excluded(adult, adult2, place, activity_type):
    from apps.social.models import Activity

    ss.create_saved_search(adult, activity_type=activity_type)
    cancelled = _activity(adult2, place, activity_type)
    cancelled.status = Activity.Status.CANCELLED
    cancelled.save(update_fields=["status"])
    completed = _activity(adult2, place, activity_type, days=4)
    completed.status = Activity.Status.COMPLETED
    completed.save(update_fields=["status"])
    ss.match_saved_searches()
    assert _match_count(adult) == 0  # only status=OPEN alerts


def test_blocked_owner_excluded(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _activity(adult2, place, activity_type)
    Block.objects.create(blocker=adult, blocked=adult2)
    ss.match_saved_searches()
    assert _match_count(adult) == 0


def test_area_filters_by_city(adult, adult2, place, activity_type):
    from apps.places.models import Place

    ss.create_saved_search(adult, activity_type=activity_type, city="Cluj-Napoca")
    _activity(adult2, place, activity_type)  # Cluj-Napoca -> matches
    other = Place.objects.create(
        name="Far", location=place.location, source=Place.Source.OSM, address_city="Bucharest"
    )
    _activity(adult2, other, activity_type)  # different city -> no match
    ss.match_saved_searches()
    assert _match_count(adult) == 1


def test_category_tier_matches_subtype(adult, adult2, place, activity_type, category):
    ss.create_saved_search(adult, category=category)
    _activity(adult2, place, activity_type)  # type's category == saved category
    ss.match_saved_searches()
    assert _match_count(adult) == 1


def test_beginners_optional_filter(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type, beginners=True)
    _activity(adult2, place, activity_type, beginners_welcome=False)
    ss.match_saved_searches()
    assert _match_count(adult) == 0  # not beginners-welcome


def test_cost_band_exact_match(adult, adult2, place, activity_type):
    from apps.social.models import Activity

    ss.create_saved_search(adult, activity_type=activity_type, cost_band=Activity.CostBand.FREE)
    _activity(adult2, place, activity_type, cost_band=Activity.CostBand.PAID)
    ss.match_saved_searches()
    assert _match_count(adult) == 0  # exact match: paid != free (no 'free matches unspecified')


def test_per_search_failure_isolation(adult, adult2, place, activity_type, monkeypatch):
    # Two savers; force the first's matching_activities to blow up — the second still gets notified.
    ss.create_saved_search(adult, activity_type=activity_type)
    ss.create_saved_search(adult2, activity_type=activity_type)
    _activity(adult, place, activity_type)  # owned by adult so both can see it (same cohort)
    real = ss.matching_activities

    def boom(saved_search, viewer):
        if saved_search.user_id == adult.id:
            raise RuntimeError("boom")
        return real(saved_search, viewer)

    monkeypatch.setattr(ss, "matching_activities", boom)
    result = ss.match_saved_searches()
    assert result["skipped"] == 1 and _match_count(adult2) == 1


def test_why_reason_and_mutability():
    assert why_reason(Notification.Kind.ACTIVITY_MATCH)  # F31 "why you got this" line present
    assert Notification.Kind.ACTIVITY_MATCH in MUTABLE_KINDS
    assert Notification.Kind.ACTIVITY_MATCH not in NON_MUTABLE_KINDS


def test_notify_cap_caps_then_carries_over(adult, adult2, place, activity_type, settings):
    from django.core.cache import cache

    settings.SAVED_SEARCH_NOTIFY_RATE_LIMIT = 2
    settings.SAVED_SEARCH_NOTIFY_WINDOW_SECONDS = 600
    ss.create_saved_search(adult, activity_type=activity_type)
    for i in range(4):
        _activity(adult2, place, activity_type, days=3 + i)
    ss.match_saved_searches()
    assert _match_count(adult) == 2  # capped at the per-saver limit this window
    assert SavedSearchMatch.objects.filter(user=adult).count() == 2  # over-cap NOT ledgered
    cache.clear()  # window elapses
    ss.match_saved_searches()
    assert _match_count(adult) == 4  # the deferred matches carry over (delayed, not lost)


def test_no_replay_after_delete_and_recreate(adult, adult2, place, activity_type):
    s = ss.create_saved_search(adult, activity_type=activity_type)
    _activity(adult2, place, activity_type)
    ss.match_saved_searches()
    assert _match_count(adult) == 1
    ss.delete_saved_search(adult, s)
    ss.create_saved_search(adult, activity_type=activity_type)  # recreate the identical search
    ss.match_saved_searches()
    assert _match_count(adult) == 1  # ledger survived the delete -> no replay


def test_ineligible_saver_blocked_at_create_and_skipped_by_matcher(
    adult, adult2, place, activity_type
):
    ss.create_saved_search(adult, activity_type=activity_type)
    _activity(adult2, place, activity_type)
    adult.is_identity_verified = False  # assigned cohort, but can_participate() now False
    adult.save(update_fields=["is_identity_verified"])
    ss.match_saved_searches()
    assert _match_count(adult) == 0  # matcher re-checks can_participate
    with pytest.raises(ss.NotEligible):
        ss.create_saved_search(adult, category=activity_type.category)  # create gate also blocks


def test_does_not_alert_saver_to_their_own_activity(adult, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _activity(adult, place, activity_type)  # the saver's OWN activity
    ss.match_saved_searches()
    assert _match_count(adult) == 0


# --- F12: day/time coarse-window predicate -----------------------------------------


def test_coarse_window_weekday_daytime_filters(adult, adult2, place, activity_type):
    s = ss.create_saved_search(adult, activity_type=activity_type, coarse_window=CW.WEEKDAY_DAYTIME)
    wd_day = _at(adult2, place, activity_type, _local_dt(2, 10))  # Tue 10:00 local -> match
    wd_eve = _at(adult2, place, activity_type, _local_dt(2, 20))  # Tue 20:00 local -> no
    we_day = _at(adult2, place, activity_type, _local_dt(6, 10))  # Sat 10:00 local -> no
    ids = _match_ids(s, adult)
    assert wd_day.id in ids
    assert wd_eve.id not in ids
    assert we_day.id not in ids


def test_coarse_window_weekend_evening_filters(adult, adult2, place, activity_type):
    s = ss.create_saved_search(adult, activity_type=activity_type, coarse_window=CW.WEEKEND_EVENING)
    we_eve = _at(adult2, place, activity_type, _local_dt(6, 20))  # Sat 20:00 local -> match
    we_day = _at(adult2, place, activity_type, _local_dt(7, 10))  # Sun 10:00 local -> no
    wd_eve = _at(adult2, place, activity_type, _local_dt(5, 20))  # Fri 20:00 local -> no
    ids = _match_ids(s, adult)
    assert we_eve.id in ids
    assert we_day.id not in ids
    assert wd_eve.id not in ids


def test_no_coarse_window_matches_every_time(adult, adult2, place, activity_type):
    s = ss.create_saved_search(adult, activity_type=activity_type)  # coarse_window unset
    a = _at(adult2, place, activity_type, _local_dt(2, 10))
    b = _at(adult2, place, activity_type, _local_dt(6, 23))
    ids = _match_ids(s, adult)
    assert {a.id, b.id} <= ids


def test_coarse_window_judged_in_local_time_not_utc(adult, adult2, place, activity_type):
    # Bucharest is UTC+2/+3, so a 19:00 LOCAL meetup is 16:00-17:00 UTC — "daytime" if judged in
    # UTC. A WEEKDAY_EVENING search must still match it: the window is judged on the wall clock.
    s = ss.create_saved_search(adult, activity_type=activity_type, coarse_window=CW.WEEKDAY_EVENING)
    evening_local = _at(adult2, place, activity_type, _local_dt(2, 19))  # Tue 19:00 local
    assert evening_local.id in _match_ids(s, adult)
