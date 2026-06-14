"""F27 — ephemeral gauge-interest polls.

Headline properties pinned here:
  - the interest signal is a plain M2M that NEVER touches Membership, so it can NEVER establish
    a shared activity / enable connections.can_connect (the load-bearing regression);
  - propose gates like create_activity (eligibility + public place); cohort pinned from proposer;
  - mark_interested is same-cohort + can_participate + not-blocked + idempotent;
  - count-only (the serializer exposes a count, never a roster of WHO);
  - convert calls create_activity verbatim, pins the gauge's own place/type, and notifies
    interested peers excluding the proposer + blocked pairs;
  - expiry deletes stale gauges (silent, self-healing).
"""

from datetime import timedelta

import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand
from apps.connections import services as connections
from apps.notifications.models import MUTABLE_KINDS, NON_MUTABLE_KINDS, Notification
from apps.places.models import Place
from apps.safety.services import block_user
from apps.social import services as social
from apps.social.models import ActivityInterest, Membership
from apps.social.serializers import GaugeSerializer

from .conftest import make_user

pytestmark = pytest.mark.django_db

WINDOW = ActivityInterest.CoarseWindow.WEEKEND_DAYTIME.value


def _gauge(proposer, place, activity_type, **kw):
    return social.propose_interest(
        proposer, place=place, activity_type=activity_type, coarse_window=WINDOW, **kw
    )


# --- propose ---------------------------------------------------------------------------


def test_propose_creates_gauge_with_proposer_auto_interested(adult, place, activity_type):
    g = _gauge(adult, place, activity_type)
    assert g.cohort == adult.cohort
    assert social.interest_count(g) == 1  # the proposer auto-counts
    assert g.converted_activity_id is None


def test_propose_requires_eligibility(place, activity_type):
    ineligible = make_user("f27_unassigned", AgeBand.UNKNOWN)  # cohort UNASSIGNED
    with pytest.raises(social.NotEligible):
        _gauge(ineligible, place, activity_type)


def test_propose_rejects_non_public_place(adult, activity_type):
    pending = Place.objects.create(
        name="Backyard", location=place_point(), source=Place.Source.USER
    )  # USER place with no published proposal → not public
    with pytest.raises(social.NotEligible):
        _gauge(adult, pending, activity_type)


def test_propose_rejects_bad_window(adult, place, activity_type):
    with pytest.raises(social.InvalidState):
        social.propose_interest(
            adult, place=place, activity_type=activity_type, coarse_window="whenever"
        )


# --- mark / unmark ---------------------------------------------------------------------


def test_mark_interested_idempotent(adult, adult2, place, activity_type):
    g = _gauge(adult, place, activity_type)
    social.mark_interested(adult2, g)
    social.mark_interested(adult2, g)  # repeat
    assert social.interest_count(g) == 2  # proposer + adult2 once


def test_mark_interested_cross_cohort_rejected(adult, place, activity_type):
    g = _gauge(adult, place, activity_type)
    teen = make_user("f27_teen", AgeBand.AGE_16_17)
    with pytest.raises(social.NotEligible):
        social.mark_interested(teen, g)


def test_mark_interested_blocked_rejected(adult, adult2, place, activity_type):
    g = _gauge(adult, place, activity_type)
    block_user(adult2, adult)  # peer blocked the proposer
    with pytest.raises(social.NotEligible):
        social.mark_interested(adult2, g)
    assert social.interest_count(g) == 1


def test_mark_interested_expired_rejected(adult, adult2, place, activity_type):
    g = _gauge(adult, place, activity_type)
    ActivityInterest.objects.filter(pk=g.pk).update(expires_at=timezone.now() - timedelta(days=1))
    g.refresh_from_db()
    with pytest.raises(social.InvalidState):
        social.mark_interested(adult2, g)


def test_unmark_interested_decrements(adult, adult2, place, activity_type):
    g = _gauge(adult, place, activity_type)
    social.mark_interested(adult2, g)
    assert social.interest_count(g) == 2
    social.unmark_interested(adult2, g)
    assert social.interest_count(g) == 1


# --- convert ---------------------------------------------------------------------------


def test_convert_proposer_only(adult, adult2, place, activity_type, now):
    g = _gauge(adult, place, activity_type)
    with pytest.raises(social.NotAMember):
        social.convert_to_activity(adult2, g, title="Game", starts_at=now + timedelta(days=2))


def test_convert_creates_activity_notifies_interested_excluding_blocked(
    adult, adult2, place, activity_type, now
):
    g = _gauge(adult, place, activity_type)
    keen = make_user("f27_keen", AgeBand.ADULT)
    blocked = make_user("f27_blocked", AgeBand.ADULT)
    social.mark_interested(keen, g)
    social.mark_interested(blocked, g)
    block_user(blocked, adult)  # blocked AFTER signalling

    activity = social.convert_to_activity(
        adult, g, title="Real Game", starts_at=now + timedelta(days=2)
    )
    g.refresh_from_db()

    assert activity.owner_id == adult.id
    assert activity.place_id == place.id and activity.activity_type_id == activity_type.id
    assert g.converted_activity_id == activity.id
    # The keen peer is invited; the blocked one and the proposer are not.
    assert Notification.objects.filter(
        recipient=keen, kind=Notification.Kind.INTEREST_CONVERTED
    ).exists()
    assert not Notification.objects.filter(
        recipient=blocked, kind=Notification.Kind.INTEREST_CONVERTED
    ).exists()
    assert not Notification.objects.filter(
        recipient=adult, kind=Notification.Kind.INTEREST_CONVERTED
    ).exists()


def test_convert_ignores_tampered_place_type(adult, place, activity_type, now):
    g = _gauge(adult, place, activity_type)
    other = Place.objects.create(name="Other", location=place_point(0.01), source=Place.Source.OSM)
    activity = social.convert_to_activity(
        adult, g, title="Game", starts_at=now + timedelta(days=2), place=other
    )
    assert activity.place_id == place.id  # gauge's place wins; tampered kwarg ignored


def test_convert_expired_rejected(adult, place, activity_type, now):
    g = _gauge(adult, place, activity_type)
    ActivityInterest.objects.filter(pk=g.pk).update(expires_at=timezone.now() - timedelta(days=1))
    g.refresh_from_db()
    with pytest.raises(social.InvalidState):
        social.convert_to_activity(adult, g, title="Game", starts_at=now + timedelta(days=2))


# --- visibility / cohort wall / count-only --------------------------------------------


def test_visible_gauges_cohort_walled_and_excludes_converted(adult, place, activity_type, now):
    g = _gauge(adult, place, activity_type)
    assert g in social.visible_gauges(adult)
    teen = make_user("f27_teen2", AgeBand.AGE_16_17)
    assert g not in social.visible_gauges(teen)  # different cohort
    social.convert_to_activity(adult, g, title="Game", starts_at=now + timedelta(days=2))
    g.refresh_from_db()
    assert g not in social.visible_gauges(adult)  # converted drops out


def test_serializer_bounded_signal_no_raw_count_no_roster(adult, adult2, place, activity_type):
    g = _gauge(adult, place, activity_type)
    social.mark_interested(adult2, g)
    data = GaugeSerializer(g).data
    # Bounded functional signal only — "needs N more" / ready. NO raw cumulative count (vanity),
    # NO roster of who signalled.
    assert data["remaining"] == 1  # threshold 3, 2 interested → 1 more
    assert data["ready"] is False
    forbidden = {
        "interested_count",
        "count",
        "interested_users",
        "members",
        "roster",
        "who",
        "participants",
        "interested",
    }
    assert forbidden.isdisjoint(data.keys()), f"gauge serializer leaks a key: {data.keys()}"


def test_kind_is_mutable():
    assert Notification.Kind.INTEREST_CONVERTED in MUTABLE_KINDS
    assert Notification.Kind.INTEREST_CONVERTED not in NON_MUTABLE_KINDS


# --- expiry command --------------------------------------------------------------------


def test_expire_interest_deletes_stale_spares_active(adult, place, activity_type):
    from django.core.management import call_command

    stale = _gauge(adult, place, activity_type)
    fresh = _gauge(adult, place, activity_type)  # default expires_at = now + 14d (active)
    ActivityInterest.objects.filter(pk=stale.pk).update(
        expires_at=timezone.now() - timedelta(days=1)
    )
    call_command("expire_interest")
    assert not ActivityInterest.objects.filter(pk=stale.pk).exists()  # stale gone
    assert ActivityInterest.objects.filter(pk=fresh.pk).exists()  # active survives


def test_expire_interest_registered_in_due_jobs():
    import apps.ops.management.commands.run_due_jobs as run_due_jobs

    assert "expire_interest" in {name for name, _ in run_due_jobs.DUE_JOBS}


# --- THE load-bearing wall: interest never enables connections ------------------------


def test_activity_interest_never_enables_connections(adult, adult2, place, activity_type):
    """Both signal interest in a gauge but share NO real activity → can_connect stays False.
    Only a real shared PEER Membership opens it."""
    g = _gauge(adult, place, activity_type)
    social.mark_interested(adult2, g)
    assert connections.shares_activity(adult, adult2) is False
    assert connections.can_connect(adult, adult2) is False

    # A real shared activity (both peer MEMBERs) DOES enable it — proving the test isn't vacuous.
    activity = social.create_activity(
        adult, place=place, activity_type=activity_type, title="Real", starts_at=timezone.now()
    )
    activity.memberships.create(
        user=adult2, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    assert connections.shares_activity(adult, adult2) is True
    assert connections.can_connect(adult, adult2) is True


# --- DRF happy path --------------------------------------------------------------------


def test_drf_create_interest_convert_flow(adult, adult2, place, activity_type, now):
    proposer = APIClient()
    proposer.force_authenticate(adult)
    resp = proposer.post(
        "/api/social/gauges/",
        {"place": place.id, "activity_type": activity_type.id, "coarse_window": WINDOW},
    )
    assert resp.status_code == 201, resp.content
    gid = resp.json()["id"]
    assert resp.json()["remaining"] == 2  # threshold 3, proposer only → 2 more
    assert "interested_count" not in resp.json()
    assert "interested_users" not in resp.json()

    peer = APIClient()
    peer.force_authenticate(adult2)
    r2 = peer.post(f"/api/social/gauges/{gid}/interested/")
    assert r2.status_code == 200, r2.content
    assert r2.json()["remaining"] == 1

    r3 = proposer.post(
        f"/api/social/gauges/{gid}/convert/",
        {"title": "Converted", "starts_at": (now + timedelta(days=2)).isoformat()},
    )
    assert r3.status_code == 201, r3.content
    assert r3.json()["title"] == "Converted"


# --- convert keeps the wall closed + is single-shot (review regressions) --------------


def test_convert_does_not_make_interested_peers_co_members(
    adult, adult2, place, activity_type, now
):
    """The load-bearing wall at the convert mutation point: a peer who only SIGNALLED interest
    must never become a Membership of the spawned activity (else connections would open)."""
    g = _gauge(adult, place, activity_type)
    social.mark_interested(adult2, g)  # signals interest but never JOINS
    activity = social.convert_to_activity(
        adult, g, title="Converted", starts_at=now + timedelta(days=2)
    )
    assert not activity.memberships.filter(user=adult2).exists()
    assert list(activity.memberships.values_list("user_id", flat=True)) == [adult.id]
    assert connections.shares_activity(adult, adult2) is False
    assert connections.can_connect(adult, adult2) is False


def test_convert_twice_blocked_no_duplicate(adult, place, activity_type, now):
    from apps.social.models import Activity

    g = _gauge(adult, place, activity_type)
    a1 = social.convert_to_activity(adult, g, title="First", starts_at=now + timedelta(days=2))
    g.refresh_from_db()
    n = Activity.objects.count()
    with pytest.raises(social.InvalidState):
        social.convert_to_activity(adult, g, title="Second", starts_at=now + timedelta(days=3))
    assert Activity.objects.count() == n  # no duplicate Activity spawned
    g.refresh_from_db()
    assert g.converted_activity_id == a1.id  # original link not overwritten


def test_convert_fanout_skips_cohort_changed_peer(adult, adult2, place, activity_type, now):
    """A peer who re-verified into another cohort after signalling must NOT be pushed a
    cross-cohort INTEREST_CONVERTED invite."""
    from apps.accounts.identity.base import AssuranceResult
    from apps.accounts.services import apply_assurance

    g = _gauge(adult, place, activity_type)
    social.mark_interested(adult2, g)
    # adult2 re-verifies down to TEEN — the interest row survives, but the invite must not fire.
    apply_assurance(adult2, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    social.convert_to_activity(adult, g, title="Adult Meetup", starts_at=now + timedelta(days=2))
    assert not Notification.objects.filter(
        recipient=adult2, kind=Notification.Kind.INTEREST_CONVERTED
    ).exists()


def test_child_proposer_rejected_at_non_child_safe_venue(place, activity_type, settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = True
    from apps.accounts.models import ParentalConsent

    child = make_user("f27_child_prop", AgeBand.UNDER_16, consented=True)
    assert ParentalConsent.objects.filter(minor=child).exists()
    # conftest `place` is a generic OSM venue, not on the child-safe allowlist.
    with pytest.raises(social.SocialError):
        _gauge(child, place, activity_type)


def test_convert_rejects_ends_before_starts(adult, place, activity_type, now):
    g = _gauge(adult, place, activity_type)
    with pytest.raises(social.InvalidState):
        social.convert_to_activity(
            adult,
            g,
            title="Bad times",
            starts_at=now + timedelta(days=2),
            ends_at=now + timedelta(days=1),
        )


def place_point(dx=0.0):
    from django.contrib.gis.geos import Point

    return Point(23.6 + dx, 46.77, srid=4326)
