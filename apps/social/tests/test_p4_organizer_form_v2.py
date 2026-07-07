from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.models import AgeBand
from apps.notifications.models import Notification
from apps.places.models import ApprovedChildVenue, Place
from apps.safety.models import AuditLog
from apps.social.models import Activity, Membership, UserPlaceProposal
from apps.social.services import InvalidState, NotAMember, create_activity, move_activity
from apps.social.tests.conftest import make_user

pytestmark = pytest.mark.django_db


def _place(name, *, source=Place.Source.OSM, raw_tags=None):
    return Place.objects.create(
        name=name,
        location=Point(23.6, 46.77, srid=4326),
        source=source,
        raw_tags=raw_tags or {},
    )


def _pending_place(proposer, name="Pending hall"):
    place = _place(name, source=Place.Source.USER)
    UserPlaceProposal.objects.create(place=place, proposer=proposer)
    return place


def _activity(owner, place, activity_type, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title=kw.pop("title", "Pickup"),
        starts_at=kw.pop("starts_at", timezone.now() + timedelta(days=2)),
        **kw,
    )


def test_create_activity_cost_amount_requires_paid_or_low_band(adult, place, activity_type, now):
    with pytest.raises(InvalidState):
        create_activity(
            adult,
            place=place,
            activity_type=activity_type,
            title="Free contradiction",
            starts_at=now + timedelta(days=1),
            cost_band=Activity.CostBand.FREE,
            cost_amount=25,
        )

    activity = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Paid court",
        starts_at=now + timedelta(days=1),
        cost_band=Activity.CostBand.PAID,
        cost_amount=25,
        cost_note="Court rental",
    )

    assert activity.cost_amount == 25
    assert activity.cost_note == "Court rental"


def test_create_activity_pending_place_carveout_is_adult_owner_only(
    adult, adult2, activity_type, now
):
    own_pending = _pending_place(adult, "Own pending")
    activity = create_activity(
        adult,
        place=own_pending,
        activity_type=activity_type,
        title="Own pending meetup",
        starts_at=now + timedelta(days=1),
    )
    assert activity.place == own_pending

    teen = make_user("p4-teen", AgeBand.AGE_16_17)
    teen_pending = _pending_place(teen, "Teen pending")
    with pytest.raises(InvalidState):
        create_activity(
            teen,
            place=teen_pending,
            activity_type=activity_type,
            title="Teen pending meetup",
            starts_at=now + timedelta(days=1),
        )

    others_pending = _pending_place(adult2, "Other pending")
    with pytest.raises(InvalidState):
        create_activity(
            adult,
            place=others_pending,
            activity_type=activity_type,
            title="Other pending meetup",
            starts_at=now + timedelta(days=1),
        )


def test_move_activity_gates_owner_state_time_and_noop(adult, adult2, place, activity_type):
    destination = _place("New hall")
    activity = _activity(adult, place, activity_type)

    with pytest.raises(NotAMember):
        move_activity(adult2, activity, place=destination)

    same = move_activity(adult, activity, place=place)
    assert same.pk == activity.pk
    assert Notification.objects.filter(kind=Notification.Kind.ACTIVITY_UPDATED).count() == 0

    activity.status = Activity.Status.COMPLETED
    activity.save(update_fields=["status"])
    with pytest.raises(InvalidState):
        move_activity(adult, activity, place=destination)

    activity.status = Activity.Status.OPEN
    activity.starts_at = timezone.now() - timedelta(minutes=1)
    activity.save(update_fields=["status", "starts_at"])
    with pytest.raises(InvalidState):
        move_activity(adult, activity, place=destination)


def test_move_activity_refuses_pending_places_except_adult_own_pending(
    adult, adult2, activity_type
):
    public = _place("Public hall")
    activity = _activity(adult, public, activity_type)

    with pytest.raises(InvalidState):
        move_activity(adult, activity, place=_pending_place(adult2, "Someone else's pending"))

    own_pending = _pending_place(adult, "Own pending venue")
    moved = move_activity(adult, activity, place=own_pending)
    assert moved.place == own_pending

    teen = make_user("p4-move-teen", AgeBand.AGE_16_17)
    teen_activity = _activity(teen, public, activity_type)
    with pytest.raises(InvalidState):
        move_activity(teen, teen_activity, place=_pending_place(teen, "Teen pending venue"))


def test_move_activity_refuses_pending_for_guardian_accompanied_child(activity_type):
    child = make_user("p4-child-owner", AgeBand.UNDER_16, consented=True)
    safe = _place("Library")
    ApprovedChildVenue.objects.create(place=safe)
    activity = _activity(child, safe, activity_type, guardian_accompanied=True)

    with pytest.raises(InvalidState):
        move_activity(child, activity, place=_pending_place(child, "Child pending"))


def test_move_activity_reruns_child_safe_venue_gate(activity_type, settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = True  # test settings default it OFF
    child = make_user("p4-safe-child", AgeBand.UNDER_16, consented=True)
    safe = _place("Approved start")
    ApprovedChildVenue.objects.create(place=safe)
    unsafe = _place("Bar", raw_tags={"amenity": "bar"})
    activity = _activity(child, safe, activity_type)

    with pytest.raises(InvalidState):
        move_activity(child, activity, place=unsafe)


def test_move_activity_notifies_members_supersedes_reminders_and_audits(
    adult, adult2, place, activity_type
):
    destination = _place("New hall")
    activity = _activity(adult, place, activity_type)
    activity.memberships.create(
        user=adult2, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    url = f"/api/social/activities/{activity.id}/"
    Notification.objects.create(
        recipient=adult2,
        kind=Notification.Kind.EVENT_REMINDER,
        title="Reminder",
        url=url,
    )

    move_activity(adult, activity, place=destination)
    activity.refresh_from_db()

    assert activity.place == destination
    assert not Notification.objects.filter(kind=Notification.Kind.EVENT_REMINDER, url=url).exists()
    notices = Notification.objects.filter(recipient=adult2, kind=Notification.Kind.ACTIVITY_UPDATED)
    assert notices.count() == 1
    assert place.name in notices.get().body
    assert destination.name in notices.get().body
    assert not Notification.objects.filter(recipient=adult, kind=Notification.Kind.ACTIVITY_UPDATED)
    assert AuditLog.objects.filter(
        event="activity.moved", target_ref=f"social.activity:{activity.pk}"
    ).exists()
