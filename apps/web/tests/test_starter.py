"""Web-layer tests for the starter-set features F1, F2, F5, F6, F7, F11."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.social.models import Activity, Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _user(name, band=AgeBand.ADULT, *, consented=False):
    user = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(user, AssuranceResult(age_band=band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=user, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return user


def _type(slug="starter-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(
        slug="starter-sport", defaults={"name": "Sport"}
    )
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _place(name="Court", lon=23.6, lat=46.77):
    return Place.objects.create(
        name=name, location=Point(lon, lat, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _activity(owner, *, starts_in=timedelta(days=1), place=None, atype=None, **kw):
    return create_activity(
        owner,
        place=place or _place(),
        activity_type=atype or _type(),
        title=kw.pop("title", "Pickup game"),
        starts_at=timezone.now() + starts_in,
        **kw,
    )


# --- F1: cancel + lifecycle -------------------------------------------------------------


def test_owner_cancels_activity_via_web():
    owner = _user("f1owner")
    other = _user("f1member")
    activity = _activity(owner)
    _member(activity, other)
    resp = _client(owner).post(f"/activities/{activity.id}/cancel/", {"reason": "rain"})
    assert resp.status_code == 302
    activity.refresh_from_db()
    assert activity.status == Activity.Status.CANCELLED
    assert Notification.objects.filter(
        recipient=other, kind=Notification.Kind.ACTIVITY_CANCELLED
    ).exists()


def test_cancelled_activity_drops_off_home_mine():
    owner = _user("f1home")
    activity = _activity(owner)
    home = _client(owner).get("/")
    assert activity in list(home.context["mine"])
    _client(owner).post(f"/activities/{activity.id}/cancel/", {"reason": ""})
    home2 = _client(owner).get("/")
    assert activity not in list(home2.context["mine"])


def test_non_owner_cannot_cancel():
    owner = _user("f1o2")
    other = _user("f1n2")
    activity = _activity(owner)
    _member(activity, other)
    _client(other).post(f"/activities/{activity.id}/cancel/", {"reason": "x"})
    activity.refresh_from_db()
    assert activity.status == Activity.Status.OPEN


# --- F2: edit ---------------------------------------------------------------------------


def test_owner_edits_activity():
    owner = _user("f2owner")
    activity = _activity(owner, title="Old")
    c = _client(owner)
    assert c.get(f"/activities/{activity.id}/edit/").status_code == 200
    resp = c.post(
        f"/activities/{activity.id}/edit/",
        {
            "place": str(activity.place_id),  # ADR-0019 §4: venue is an edit-form field now
            "title": "New name",
            "description": "bring water",
            "starts_at": (timezone.now() + timedelta(days=2)).strftime("%Y-%m-%dT%H:%M"),
            "capacity": "",
        },
    )
    assert resp.status_code == 302
    activity.refresh_from_db()
    assert activity.title == "New name"
    assert activity.description == "bring water"


def test_non_owner_edit_is_blocked():
    owner = _user("f2o2")
    other = _user("f2n2")
    activity = _activity(owner, title="Keep")
    resp = _client(other).get(f"/activities/{activity.id}/edit/")
    assert resp.status_code == 302  # redirected away with an error
    activity.refresh_from_db()
    assert activity.title == "Keep"


# --- F11: announcements -----------------------------------------------------------------


def test_owner_posts_announcement_and_it_shows():
    owner = _user("f11owner")
    member = _user("f11member")
    activity = _activity(owner)
    _member(activity, member)
    resp = _client(owner).post(
        f"/activities/{activity.id}/announce/", {"body": "Meet at the fountain"}
    )
    assert resp.status_code == 302
    assert activity.thread.posts.filter(is_announcement=True, body="Meet at the fountain").exists()
    assert Notification.objects.filter(
        recipient=member, kind=Notification.Kind.ANNOUNCEMENT
    ).exists()
    page = _client(member).get(f"/activities/{activity.id}/").content.decode()
    # Announcements are pinned at the top of the unified "Messages" stream.
    assert "Messages" in page
    assert "Meet at the fountain" in page


# --- F5: kid-trusted safe-exit context --------------------------------------------------


def test_safe_exit_card_names_guardian_to_member():
    guardian = _user("f5guardian")
    child_owner = _user("f5owner", AgeBand.UNDER_16, consented=True)
    child_member = _user("f5child", AgeBand.UNDER_16, consented=True)
    link_guardian(guardian, child_member)
    activity = _activity(child_owner)  # CHILD-cohort activity
    _member(activity, child_member)
    page = _client(child_member).get(f"/activities/{activity.id}/")
    assert guardian in page.context["my_guardians"]
    body = page.content.decode()
    assert "Feeling unsafe" in body
    assert "f5guardian" in body
    assert "Report with details" in body  # the detailed report slow path (F8 relabel)
    assert "/unsafe/" in body  # the F8 one-tap "I feel unsafe" fast path


def test_owner_does_not_see_safe_exit_card():
    owner = _user("f5o2")
    activity = _activity(owner)
    body = _client(owner).get(f"/activities/{activity.id}/").content.decode()
    assert "Feeling unsafe" not in body


# --- F6: parent meetup manifest ---------------------------------------------------------


def test_ward_manifest_lists_upcoming_meetups_only():
    guardian = _user("f6guardian")
    ward = _user("f6ward", AgeBand.UNDER_16, consented=True)
    owner = _user("f6owner", AgeBand.UNDER_16, consented=True)  # same CHILD cohort
    link_guardian(guardian, ward)
    upcoming = _activity(owner, place=_place(name="Sunny Park"), starts_in=timedelta(days=1))
    _member(upcoming, ward)
    past = _activity(owner, place=_place(name="Old Gym"), starts_in=timedelta(days=-3))
    _member(past, ward)

    body = _client(guardian).get("/wards/").content.decode()
    assert "Sunny Park" in body
    assert "Basketball" in body
    assert "Old Gym" not in body  # past meetup excluded from the manifest


def test_ward_manifest_is_guardian_scoped():
    stranger = _user("f6stranger")
    ward = _user("f6ward2", AgeBand.UNDER_16, consented=True)
    owner = _user("f6owner2", AgeBand.UNDER_16, consented=True)
    activity = _activity(owner, place=_place(name="Secret Court"))
    _member(activity, ward)
    # A non-guardian sees no wards and none of the child's meetups.
    body = _client(stranger).get("/wards/").content.decode()
    assert "Secret Court" not in body


# --- F7: use-my-location ----------------------------------------------------------------


def test_near_me_orders_activities_by_distance():
    user = _user("f7user")
    near = _activity(user, place=_place(name="Near", lon=23.60, lat=46.77), title="Near one")
    far = _activity(user, place=_place(name="Far", lon=24.80, lat=47.60), title="Far one")

    c = _client(user)
    # Without location: soonest-first (both ~same time, so just assert both present, no crash).
    plain = c.get("/activities/")
    assert plain.status_code == 200
    assert plain.context["near_active"] is False

    located = c.get("/activities/", {"near_lon": "23.60", "near_lat": "46.77"})
    assert located.context["near_active"] is True
    ordered = list(located.context["activities"])
    assert ordered[0].id == near.id
    assert ordered.index(near) < ordered.index(far)
