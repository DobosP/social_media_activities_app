"""W3-F9 gauge lane: a saved search also reaches matching interest GAUGES. Cohort gate, one-notice-
per-(user, gauge)-ever, anti-flood (shared with the activity lane), isolation, bounded signal only,
and the constraint-honesty opt-out (a beginners/cost_band search never alerts on a gauge)."""

from datetime import timedelta

import pytest
from django.utils import timezone

from apps.accounts.models import AgeBand
from apps.notifications.models import MUTABLE_KINDS, NON_MUTABLE_KINDS, Notification
from apps.notifications.services import set_muted_kinds, why_reason
from apps.safety.models import Block
from apps.saved_searches import services as ss
from apps.saved_searches.models import SavedSearchGaugeMatch
from apps.social import services as social
from apps.social.models import ActivityInterest
from apps.social.services import create_activity

from .conftest import make_user

pytestmark = pytest.mark.django_db
CW = ActivityInterest.CoarseWindow


def _gauge(proposer, place, activity_type, *, window=CW.WEEKDAY_DAYTIME):
    """Float a gauge through the real propose_interest path (proposer auto-counts interested)."""
    return social.propose_interest(
        proposer, place=place, activity_type=activity_type, coarse_window=window
    )


def _raw_gauge(proposer, place, activity_type, *, window=CW.WEEKDAY_DAYTIME, days=7):
    """A gauge row created directly — used only where the propose_interest gates (public place /
    child-venue) are beside the point (e.g. a CHILD-cohort gauge for the cohort-wall test)."""
    g = ActivityInterest.objects.create(
        proposer=proposer,
        place=place,
        activity_type=activity_type,
        cohort=proposer.cohort,
        coarse_window=window,
        expires_at=timezone.now() + timedelta(days=days),
    )
    g.interested_users.add(proposer)
    return g


def _gauge_count(user):
    return Notification.objects.filter(recipient=user, kind=Notification.Kind.GAUGE_MATCH).count()


def _activity_count(user):
    return Notification.objects.filter(
        recipient=user, kind=Notification.Kind.ACTIVITY_MATCH
    ).count()


def _match_ids(saved_search, viewer):
    return set(ss.matching_gauges(saved_search, viewer).values_list("id", flat=True))


def test_fires_one_notice_for_a_new_gauge_match(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    g = _gauge(adult2, place, activity_type)
    result = ss.match_saved_searches()
    assert result["notified"] == 1
    n = Notification.objects.get(recipient=adult, kind=Notification.Kind.GAUGE_MATCH)
    assert n.url == f"/gauges/{g.id}/"


def test_gauge_idempotent_across_ticks(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _gauge(adult2, place, activity_type)
    ss.match_saved_searches()
    ss.match_saved_searches()
    assert _gauge_count(adult) == 1
    assert SavedSearchGaugeMatch.objects.filter(user=adult).count() == 1


def test_one_gauge_notice_per_user_across_multiple_searches(
    adult, adult2, place, activity_type, category
):
    ss.create_saved_search(adult, activity_type=activity_type)
    ss.create_saved_search(adult, category=category)  # both match the same gauge
    _gauge(adult2, place, activity_type)
    ss.match_saved_searches()
    assert _gauge_count(adult) == 1  # ledger keyed on (user, interest)


def test_no_renotify_after_unmute(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _gauge(adult2, place, activity_type)
    set_muted_kinds(adult, [Notification.Kind.GAUGE_MATCH])
    ss.match_saved_searches()  # ledger written, notify suppressed
    assert _gauge_count(adult) == 0
    assert SavedSearchGaugeMatch.objects.filter(user=adult).count() == 1
    set_muted_kinds(adult, [])
    ss.match_saved_searches()  # must NOT re-fire
    assert _gauge_count(adult) == 0


def test_cross_cohort_never_notifies(adult, child, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _raw_gauge(child, place, activity_type)  # CHILD-cohort gauge
    ss.match_saved_searches()
    assert _gauge_count(adult) == 0


def test_converted_gauge_excluded(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    g = _gauge(adult2, place, activity_type)
    a = create_activity(
        adult2,
        place=place,
        activity_type=activity_type,
        title="real",
        starts_at=timezone.now() + timedelta(days=3),
    )
    g.converted_activity = a
    g.save(update_fields=["converted_activity"])
    ss.match_saved_searches()
    assert _gauge_count(adult) == 0  # a converted gauge is no longer an open signal


def test_expired_gauge_excluded(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    g = _gauge(adult2, place, activity_type)
    g.expires_at = timezone.now() - timedelta(days=1)
    g.save(update_fields=["expires_at"])
    ss.match_saved_searches()
    assert _gauge_count(adult) == 0


def test_blocked_proposer_excluded(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _gauge(adult2, place, activity_type)
    Block.objects.create(blocker=adult, blocked=adult2)
    ss.match_saved_searches()
    assert _gauge_count(adult) == 0


def test_area_filters_by_city(adult, adult2, place, activity_type):
    from apps.places.models import Place

    ss.create_saved_search(adult, activity_type=activity_type, city="Cluj-Napoca")
    _gauge(adult2, place, activity_type)  # Cluj-Napoca -> matches
    other = Place.objects.create(
        name="Far", location=place.location, source=Place.Source.OSM, address_city="Bucharest"
    )
    _gauge(adult2, other, activity_type)  # different city -> no match
    ss.match_saved_searches()
    assert _gauge_count(adult) == 1


def test_category_tier_matches_subtype(adult, adult2, place, activity_type, category):
    ss.create_saved_search(adult, category=category)
    _gauge(adult2, place, activity_type)  # gauge type's category == saved category
    ss.match_saved_searches()
    assert _gauge_count(adult) == 1


def test_coarse_window_exact_match(adult, adult2, place, activity_type):
    s = ss.create_saved_search(adult, activity_type=activity_type, coarse_window=CW.WEEKDAY_DAYTIME)
    yes = _gauge(adult2, place, activity_type, window=CW.WEEKDAY_DAYTIME)
    no = _gauge(adult2, place, activity_type, window=CW.WEEKEND_EVENING)
    ids = _match_ids(s, adult)
    assert yes.id in ids
    assert no.id not in ids


def test_beginners_search_opts_out_of_gauge_lane(adult, adult2, place, activity_type):
    # A gauge carries no beginners flag (set only at conversion), so a beginners search can never be
    # proven to match one -> it opts out of the gauge lane entirely (still gets activity alerts).
    s = ss.create_saved_search(adult, activity_type=activity_type, beginners=True)
    _gauge(adult2, place, activity_type)
    ss.match_saved_searches()
    assert _gauge_count(adult) == 0
    assert _match_ids(s, adult) == set()


def test_cost_band_search_opts_out_of_gauge_lane(adult, adult2, place, activity_type):
    from apps.social.models import Activity

    s = ss.create_saved_search(adult, activity_type=activity_type, cost_band=Activity.CostBand.FREE)
    _gauge(adult2, place, activity_type)
    ss.match_saved_searches()
    assert _gauge_count(adult) == 0
    assert _match_ids(s, adult) == set()


def test_does_not_alert_saver_to_their_own_gauge(adult, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    _gauge(adult, place, activity_type)  # the saver's OWN gauge (proposer + auto-interested)
    ss.match_saved_searches()
    assert _gauge_count(adult) == 0


def test_does_not_alert_on_gauge_already_signalled(adult, adult2, place, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    g = _gauge(adult2, place, activity_type)
    social.mark_interested(adult, g)  # the saver already signalled -> already knows about it
    ss.match_saved_searches()
    assert _gauge_count(adult) == 0


def test_gauge_notice_body_carries_no_raw_count(adult, adult2, place, activity_type):
    # inv.2: the gauge is a bounded ready/needs-N signal, NEVER a raw "N interested" social-proof
    # count. The frozen notice body must therefore carry no digit at all (the live page shows the
    # bounded signal). Make the count > 1 so a leak would actually show up.
    other = make_user("ss_g_other", AgeBand.ADULT)
    ss.create_saved_search(adult, activity_type=activity_type)
    g = _gauge(adult2, place, activity_type)
    social.mark_interested(other, g)
    assert social.interest_count(g) == 2
    ss.match_saved_searches()
    n = Notification.objects.get(recipient=adult, kind=Notification.Kind.GAUGE_MATCH)
    assert not any(ch.isdigit() for ch in n.body)


def test_shared_rate_cap_across_activity_and_gauge_lanes(
    adult, adult2, place, activity_type, settings
):
    from django.core.cache import cache

    settings.SAVED_SEARCH_NOTIFY_RATE_LIMIT = 2
    settings.SAVED_SEARCH_NOTIFY_WINDOW_SECONDS = 600
    ss.create_saved_search(adult, activity_type=activity_type)
    # 2 activities + 2 gauges all match; the activity lane runs first and consumes BOTH tokens, so
    # the gauge lane is capped to 0 this window (proving the cap key is shared, not per-lane).
    for i in range(2):
        create_activity(
            adult2,
            place=place,
            activity_type=activity_type,
            title="a",
            starts_at=timezone.now() + timedelta(days=3 + i),
        )
    _gauge(adult2, place, activity_type, window=CW.WEEKDAY_DAYTIME)
    _gauge(adult2, place, activity_type, window=CW.WEEKEND_EVENING)
    ss.match_saved_searches()
    assert _activity_count(adult) == 2 and _gauge_count(adult) == 0
    assert SavedSearchGaugeMatch.objects.filter(user=adult).count() == 0  # over-cap NOT ledgered
    cache.clear()  # window elapses
    ss.match_saved_searches()
    assert _gauge_count(adult) == 2  # the deferred gauges carry over (delayed, not lost)


def test_gauge_failure_isolation(adult, adult2, place, activity_type, monkeypatch):
    # adult's gauge-lane read blows up; adult2's search still gets its gauge notice.
    ss.create_saved_search(adult, activity_type=activity_type)
    ss.create_saved_search(adult2, activity_type=activity_type)
    _gauge(adult, place, activity_type)  # adult2 (same cohort) can see adult's gauge
    real = ss.matching_gauges

    def boom(saved_search, viewer):
        if saved_search.user_id == adult.id:
            raise RuntimeError("boom")
        return real(saved_search, viewer)

    monkeypatch.setattr(ss, "matching_gauges", boom)
    result = ss.match_saved_searches()
    assert result["skipped"] == 1 and _gauge_count(adult2) == 1


def test_why_reason_and_mutability():
    assert why_reason(Notification.Kind.GAUGE_MATCH)  # "why you got this" line present
    assert Notification.Kind.GAUGE_MATCH in MUTABLE_KINDS
    assert Notification.Kind.GAUGE_MATCH not in NON_MUTABLE_KINDS
