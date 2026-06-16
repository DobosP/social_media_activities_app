"""F3 saved-search alerts: domain logic. A SavedSearch is an opt-in discovery filter; the nightly
match_saved_searches tells the saver ONCE (per (user, activity), ever) when a new activity they
could already see. Cohort-walled per saver; AREA-only geo; rate-limited; mutable notice."""

import logging

from django.conf import settings
from django.db import transaction
from django.db.models.functions import ExtractHour, ExtractIsoWeekDay
from django.utils import timezone
from django.utils.translation import gettext_lazy as _

from apps.accounts.models import Cohort
from apps.accounts.services import can_participate
from apps.notifications.models import Notification
from apps.notifications.services import notify
from apps.safety.services import allow_action, record_audit
from apps.social.models import Activity, ActivityInterest

from .models import SavedSearch, SavedSearchGaugeMatch, SavedSearchMatch

logger = logging.getLogger(__name__)

# F12: local-time boundary between a "daytime" and an "evening" coarse window. daytime = local
# hour < 18; evening = local hour >= 18. weekday = Mon-Fri (ISO 1-5); weekend = Sat-Sun (ISO 6-7).
_EVENING_START_HOUR = 18


def _apply_coarse_window(qs, coarse_window):
    """F12: narrow an Activity queryset to a CoarseWindow, judging weekday + hour in LOCAL time
    (the project timezone, Europe/Bucharest) via tz-aware SQL Extract — so the result stays a
    QuerySet (the matcher consumes it with ``.iterator()``) and the index scan / bounded query are
    preserved. ``starts_at`` is stored UTC; a naive ``.weekday()``/``.hour`` would misclassify every
    meetup by the 2-3h offset (incl. DST), silently dropping the actionable matches the feature
    exists to surface."""
    cw = ActivityInterest.CoarseWindow
    tz = timezone.get_current_timezone()
    qs = qs.alias(
        _local_weekday=ExtractIsoWeekDay("starts_at", tzinfo=tz),
        _local_hour=ExtractHour("starts_at", tzinfo=tz),
    )
    if coarse_window in (cw.WEEKDAY_DAYTIME, cw.WEEKDAY_EVENING):
        qs = qs.filter(_local_weekday__lte=5)  # Mon-Fri
    else:
        qs = qs.filter(_local_weekday__gte=6)  # Sat-Sun
    if coarse_window in (cw.WEEKDAY_DAYTIME, cw.WEEKEND_DAYTIME):
        qs = qs.filter(_local_hour__lt=_EVENING_START_HOUR)
    else:
        qs = qs.filter(_local_hour__gte=_EVENING_START_HOUR)
    return qs


class SavedSearchError(Exception):
    pass


class NotEligible(SavedSearchError):
    pass


class InvalidState(SavedSearchError):
    pass


def can_save(user) -> bool:
    """Who may save searches: a verified-and-consented user with a real cohort (same gate as
    organising an activity — so a saved search can never out-reach what the user already sees)."""
    return (
        getattr(user, "is_authenticated", False)
        and can_participate(user)
        and user.cohort != Cohort.UNASSIGNED
    )


def saved_searches_for(user):
    """The user's own saved searches (owner-scoped read chokepoint)."""
    if not getattr(user, "is_authenticated", False):
        return SavedSearch.objects.none()
    return (
        SavedSearch.objects.filter(user=user)
        .select_related("activity_type", "category", "area")
        .order_by("-created_at")
    )


@transaction.atomic
def create_saved_search(
    user,
    *,
    activity_type=None,
    category=None,
    city="",
    beginners=False,
    cost_band="",
    coarse_window="",
) -> SavedSearch:
    """Create an opt-in saved search. cohort is pinned from the user. Exactly one of
    activity_type / category. The optional `city` is resolved to an Area only AFTER the
    anti-abuse gates pass (so an ineligible/over-cap caller can't mint Area rows), inside this
    transaction (so a failed save rolls it back). Rate-limited + hard-capped + de-duplicated."""
    if not can_save(user):
        raise NotEligible(
            _("You must be verified (consented if a minor) and in a cohort to save searches.")
        )
    if bool(activity_type) == bool(category):
        raise InvalidState(_("Choose exactly one of an activity type or a category."))
    if activity_type is not None and not activity_type.is_active:
        raise InvalidState(_("That activity type isn't available."))
    if cost_band and cost_band not in {c for c, _label in Activity.CostBand.choices}:
        raise InvalidState(_("Invalid cost band."))
    if coarse_window and coarse_window not in ActivityInterest.CoarseWindow.values:
        raise InvalidState(_("Invalid time window."))
    if not allow_action(
        user,
        "saved_search_create",
        limit=getattr(settings, "SAVED_SEARCH_RATE_LIMIT", 20),
        window_seconds=getattr(settings, "SAVED_SEARCH_RATE_WINDOW_SECONDS", 3600),
    ):
        raise NotEligible(_("You're saving searches too quickly; please try again later."))
    if SavedSearch.objects.filter(user=user).count() >= getattr(
        settings, "SAVED_SEARCH_MAX_PER_USER", 20
    ):
        raise InvalidState(_("You've reached the maximum number of saved searches."))
    # Resolve the city to an Area only now — after the gates above — so a rejected/over-cap save
    # never mints an Area, and a duplicate save reuses the existing one (no junk rows).
    area = None
    if city:
        from apps.communities.services import _ensure_city_area

        area = _ensure_city_area(city)
    if SavedSearch.objects.filter(
        user=user,
        activity_type=activity_type,
        category=category,
        area=area,
        beginners=beginners,
        cost_band=cost_band,
        coarse_window=coarse_window,
    ).exists():
        raise InvalidState(_("You've already saved this search."))
    ss = SavedSearch.objects.create(
        user=user,
        cohort=user.cohort,
        activity_type=activity_type,
        category=category,
        area=area,
        beginners=beginners,
        cost_band=cost_band,
        coarse_window=coarse_window,
    )
    record_audit("saved_search.created", actor=user, target=ss)
    return ss


@transaction.atomic
def delete_saved_search(user, saved_search) -> None:
    """Delete the user's own saved search. The (user, activity) ledger rows are NOT deleted, so a
    recreated search can never replay already-sent notices."""
    if saved_search.user_id != getattr(user, "id", None):
        raise NotEligible(_("You can only remove your own saved searches."))
    ss_id = saved_search.id
    saved_search.delete()
    record_audit("saved_search.deleted", actor=user, saved_search_id=ss_id)


def matching_activities(saved_search, viewer):
    """The per-saver read primitive (like communities.community_activities): cohort-walled twice —
    the viewer must be the saver in the search's pinned cohort, and the source query is the already
    cohort-pinned visible_activities(viewer) (cohort + is_hidden + blocked-owner). Then narrowed by
    the saved predicate + status=OPEN + upcoming. AREA-only geo, never a coordinate."""
    from apps.communities.services import _area_place_q
    from apps.social.services import visible_activities

    if not getattr(viewer, "is_authenticated", False) or viewer.cohort != saved_search.cohort:
        return Activity.objects.none()
    qs = visible_activities(viewer).exclude(
        owner_id=viewer.id
    )  # don't alert you to your own meetup
    if saved_search.area_id:
        qs = qs.filter(_area_place_q(saved_search.area))
    if saved_search.activity_type_id:
        qs = qs.filter(activity_type_id=saved_search.activity_type_id)
    else:
        qs = qs.filter(activity_type__category_id=saved_search.category_id)
    if saved_search.beginners:
        qs = qs.filter(beginners_welcome=True)
    if saved_search.cost_band:
        qs = qs.filter(cost_band=saved_search.cost_band)
    if saved_search.coarse_window:
        # F12: schedule-fit window, judged in local time at read time (no coordinate, nothing
        # written on the Activity). Stays a QuerySet so the matcher's .iterator() still holds.
        qs = _apply_coarse_window(qs, saved_search.coarse_window)
    # visible_activities does NOT filter status — add it so a cancelled/done meetup never alerts.
    # Soonest-first so that, under the per-saver rate cap, the deferred tail is the FARTHEST-out
    # (still recoverable next tick) — never an imminent match that would lapse before re-scan.
    return (
        qs.filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
        .select_related("activity_type")
        .order_by("starts_at", "id")
    )


def matching_gauges(saved_search, viewer):
    """W3-F9 gauge-lane read primitive — the ActivityInterest sibling of matching_activities.
    Cohort-walled twice (the viewer must be the saver in the search's pinned cohort, and the source
    is the already cohort-pinned visible_gauges(viewer): cohort + not-converted + not-expired +
    blocked-proposer). Then narrowed by ONLY the predicate dimensions a gauge actually carries —
    area, activity_type XOR category, coarse_window. AREA-only geo, never a coordinate; the gauge
    stays a COUNT-only signal (we never read who signalled).

    A gauge has no beginners flag and no cost band — both are decided only at conversion, by the
    proposer's later create_activity — so a search that constrains on either can NOT be shown to be
    satisfied by any gauge. Rather than alert on a gauge that might convert to a non-matching
    activity, such a search simply opts OUT of the gauge lane (it still gets activity alerts as
    before). This keeps the gauge alert honest: it fires only when the gauge meets every stated
    constraint."""
    from apps.communities.services import _area_place_q
    from apps.social.services import visible_gauges

    if not getattr(viewer, "is_authenticated", False) or viewer.cohort != saved_search.cohort:
        return ActivityInterest.objects.none()
    if saved_search.beginners or saved_search.cost_band:
        return ActivityInterest.objects.none()
    qs = (
        visible_gauges(viewer)
        .exclude(proposer_id=viewer.id)  # never alert you to your OWN gauge
        .exclude(interested_users=viewer)  # nor one you've already signalled on (you've seen it)
    )
    if saved_search.area_id:
        qs = qs.filter(_area_place_q(saved_search.area))
    if saved_search.activity_type_id:
        qs = qs.filter(activity_type_id=saved_search.activity_type_id)
    else:
        qs = qs.filter(activity_type__category_id=saved_search.category_id)
    if saved_search.coarse_window:
        # A gauge's coarse_window is a stored choice (no time math), so this is an exact match —
        # not the local-time Extract the activity lane needs (an Activity stores only a UTC point).
        qs = qs.filter(coarse_window=saved_search.coarse_window)
    # Soonest-to-EXPIRE first so that, under the per-saver rate cap, the deferred tail is the gauge
    # with the most life left (still recoverable next tick) — never one about to lapse.
    return qs.order_by("expires_at", "id")


def match_saved_searches(*, now=None) -> dict:
    """Nightly matcher: for each saved search, fan out per-saver through the cohort read gate and
    fire ONE ACTIVITY_MATCH notice per (user, activity), EVER. Idempotency + 'one notice even across
    mute toggles' come from the SavedSearchMatch (user, activity) ledger. Per-search isolation,
    per-saver rate cap (anti-flood from one viral activity), per-tick cap. No request user — the
    viewer is always the saver, so cohort isolation + blocking + hidden + status all hold.

    W3-F9 adds a PARALLEL gauge lane in the same per-search atomic block: a GAUGE_MATCH notice, at
    most once per (user, gauge), over matching interest gauges (SavedSearchGaugeMatch ledger). It
    shares the per-saver rate cap key and the per-tick counters, so a viral activity AND a viral
    gauge together can't out-flood the cap."""
    now = now or timezone.now()
    batch = getattr(settings, "SAVED_SEARCH_MATCH_BATCH", 1000)
    notify_limit = getattr(settings, "SAVED_SEARCH_NOTIFY_RATE_LIMIT", 50)
    notify_window = getattr(settings, "SAVED_SEARCH_NOTIFY_WINDOW_SECONDS", 86400)
    notified = scanned = skipped = 0
    searches = SavedSearch.objects.select_related(
        "user", "activity_type", "category", "area"
    ).order_by("id")
    for ss in searches.iterator():
        if scanned >= batch:
            break  # per-tick anomaly cap: never process unbounded matches in one run
        try:
            with transaction.atomic():
                saver = ss.user
                # Re-assert eligibility: skip a drifted/unassigned saver, or one who can no longer
                # participate (e.g. a lapsed age proof) — the search lies dormant until restored.
                if (
                    saver.cohort == Cohort.UNASSIGNED
                    or saver.cohort != ss.cohort
                    or not can_participate(saver)
                ):
                    continue
                for activity in matching_activities(ss, saver).iterator():
                    # Already alerted about this activity (any of the saver's searches) -> skip
                    # BEFORE consuming a rate token, so a backlog re-scan is cheap.
                    if SavedSearchMatch.objects.filter(
                        user=saver, activity_id=activity.id
                    ).exists():
                        continue
                    # Per-saver anti-flood cap: the rest carry to a later tick (never lost).
                    if not allow_action(
                        saver,
                        "saved_search_match",
                        limit=notify_limit,
                        window_seconds=notify_window,
                    ):
                        break
                    _, created = SavedSearchMatch.objects.get_or_create(
                        user=saver, activity=activity
                    )
                    if not created:
                        continue  # raced with a concurrent tick
                    scanned += 1
                    # notify() returns None for a muted saver — but the ledger row above already
                    # marks it handled, so a muted saver is never re-fired after they un-mute.
                    delivered = notify(
                        saver,
                        Notification.Kind.ACTIVITY_MATCH,
                        title=f'New {activity.activity_type.name}: "{activity.title}"',
                        body=f"Starts {timezone.localtime(activity.starts_at):%a %d %b, %H:%M}.",
                        url=f"/activities/{activity.id}/",
                    )
                    if delivered:
                        notified += 1
                # W3-F9 gauge lane: the SAME saver, atomic block, per-saver rate cap key and
                # per-tick counters — a second at-most-once-per-(user, gauge) fan-out over the
                # matching interest gauges, recorded in its own SavedSearchGaugeMatch ledger.
                for gauge in matching_gauges(ss, saver).iterator():
                    if SavedSearchGaugeMatch.objects.filter(
                        user=saver, interest_id=gauge.id
                    ).exists():
                        continue
                    if not allow_action(
                        saver,
                        "saved_search_match",
                        limit=notify_limit,
                        window_seconds=notify_window,
                    ):
                        break
                    _, created = SavedSearchGaugeMatch.objects.get_or_create(
                        user=saver, interest=gauge
                    )
                    if not created:
                        continue  # raced with a concurrent tick
                    scanned += 1
                    # No count in the frozen body — the live page shows the bounded ready/needs-N
                    # signal. A frozen count would go stale AND flirt with inv.2 social-proof.
                    delivered = notify(
                        saver,
                        Notification.Kind.GAUGE_MATCH,
                        title=f"New interest gauge: {gauge.activity_type.name}",
                        body=(
                            f"Someone's gauging interest at {gauge.place.name} "
                            f"({gauge.get_coarse_window_display()}). Add yours to help it start."
                        ),
                        url=f"/gauges/{gauge.id}/",
                    )
                    if delivered:
                        notified += 1
        except Exception:  # noqa: BLE001 — one broken search must not abort the whole tick
            logger.exception("match_saved_searches: skipping search %s", ss.pk)
            skipped += 1
    with transaction.atomic():
        record_audit("saved_search.swept", notified=notified, scanned=scanned, skipped=skipped)
    return {"notified": notified, "scanned": scanned, "skipped": skipped}
