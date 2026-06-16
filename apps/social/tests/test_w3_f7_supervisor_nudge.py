"""W3-F7: nudge a CHILD organiser's ACTIVE guardians when their supervised meetup is stuck for
lack of a seated supervisor (a join CLEARED the vote but can't settle).

Pins the load-bearing safety properties: the trigger fires ONLY on a genuinely vote-cleared join
(never a bare REQUESTED row), links to /wards/ (not the cross-cohort thread), carries no count,
is at-most-once per (guardian, activity), is muteable, and excludes blocked pairs.
"""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command
from django.urls import reverse
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.notifications.models import NON_MUTABLE_KINDS, Notification
from apps.notifications.services import set_muted_kinds
from apps.places.models import Place
from apps.safety.models import Block
from apps.social.models import Membership
from apps.social.services import (
    add_guardian,
    cast_vote,
    create_activity,
    join_stuck_on_supervision,
    request_to_join,
)
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)
KIND = Notification.Kind.SUPERVISOR_NEEDED


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _type(slug):
    cat, _ = ActivityCategory.objects.get_or_create(
        slug=f"f7cat-{slug}", defaults={"name": "Sport"}
    )
    return ActivityType.objects.get_or_create(
        slug=f"f7at-{slug}", defaults={"name": "Football", "category": cat}
    )[0]


def _supervised_meetup(slug, *, link=True):
    """A supervised CHILD meetup starting WITHIN the 48h prep window. Returns (owner, guardian)."""
    owner = _child(f"{slug}-o")
    guardian = _adult(f"{slug}-g")
    if link:
        link_guardian(guardian, owner)
    place = Place.objects.create(name=f"P-{slug}", location=PT, source=Place.Source.OSM)
    activity = create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title="Kids meetup",
        starts_at=timezone.now() + timedelta(hours=24),
        supervised=True,
    )
    return owner, guardian, activity


def _clear_a_join(owner, activity, slug):
    """A peer requests + the owner (the lone voting member) votes it through; it stays REQUESTED
    because no supervisor is seated — the stuck state W3-F7 nudges on."""
    joiner = _child(f"{slug}-j")
    request_to_join(joiner, activity)
    m = Membership.objects.get(activity=activity, user=joiner)
    cast_vote(owner, m, True)
    m.refresh_from_db()
    assert m.state == Membership.State.REQUESTED  # cleared the vote, but stuck on supervision
    return m


def _notices_to(user):
    return Notification.objects.filter(recipient=user, kind=KIND)


def test_stuck_meetup_nudges_guardian_once():
    owner, guardian, activity = _supervised_meetup("f7a")
    _clear_a_join(owner, activity, "f7a")
    assert join_stuck_on_supervision(activity) is True
    call_command("supervisor_needed_nudge")
    assert _notices_to(guardian).count() == 1
    call_command("supervisor_needed_nudge")  # at-most-once per (guardian, activity)
    assert _notices_to(guardian).count() == 1


def test_links_to_wards_not_thread_and_carries_no_count():
    owner, guardian, activity = _supervised_meetup("f7b")
    _clear_a_join(owner, activity, "f7b")
    call_command("supervisor_needed_nudge")
    note = _notices_to(guardian).get()
    assert note.url.startswith(reverse("wards"))  # guardian manifest, never the cross-cohort thread
    assert activity.title in note.title
    assert not any(ch.isdigit() for ch in note.body)  # inv.2: no waiting-joiner count / metric


def test_unvoted_request_does_not_summon_guardian():
    # THE safety property: a REQUESTED row that hasn't cleared the vote must NEVER nudge a guardian.
    owner, guardian, activity = _supervised_meetup("f7c")
    joiner = _child("f7c-j")
    request_to_join(joiner, activity)  # requested, but nobody voted it through
    assert join_stuck_on_supervision(activity) is False
    call_command("supervisor_needed_nudge")
    assert _notices_to(guardian).count() == 0


def test_satisfied_supervision_no_nudge():
    owner, guardian, activity = _supervised_meetup("f7d")
    _clear_a_join(owner, activity, "f7d")
    add_guardian(owner, activity, guardian)  # seat the supervisor -> the stuck join settles
    assert join_stuck_on_supervision(activity) is False
    call_command("supervisor_needed_nudge")
    assert _notices_to(guardian).count() == 0


def test_muted_guardian_gets_nothing():
    owner, guardian, activity = _supervised_meetup("f7e")
    _clear_a_join(owner, activity, "f7e")
    set_muted_kinds(guardian, [KIND])  # SUPERVISOR_NEEDED is a mutable convenience nudge
    assert KIND not in NON_MUTABLE_KINDS
    call_command("supervisor_needed_nudge")
    assert _notices_to(guardian).count() == 0


def test_blocked_guardian_excluded():
    owner, guardian, activity = _supervised_meetup("f7f")
    _clear_a_join(owner, activity, "f7f")
    Block.objects.create(blocker=owner, blocked=guardian)  # defensive block-vs-owner exclusion
    call_command("supervisor_needed_nudge")
    assert _notices_to(guardian).count() == 0
