"""Regression tests for the adversarial-review fixes on the modern-UX refactor branch.
Each pins a CONFIRMED finding so it can't silently come back."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.events.models import Event
from apps.media import services as media
from apps.media.models import Photo
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Activity, Membership, UserPlaceProposal

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _img_bytes():
    import io

    from PIL import Image

    im = Image.new("L", (64, 64))
    im.putdata([(x * 7 + y * 13) % 256 for y in range(64) for x in range(64)])
    buf = io.BytesIO()
    im.convert("RGB").save(buf, "PNG")
    return buf.getvalue()


# --- W1-3: the events API must apply the F25 pending-place gate ----------------------


def test_events_api_hides_pending_place_event(place, activity_type):
    viewer = make_user("rf-evapi")
    Event.objects.create(
        title="Public game", starts_at=timezone.now() + timedelta(days=1), place=place
    )
    pending = Place.objects.create(
        name="Pending Arena", location=Point(23.62, 46.78, srid=4326), source=Place.Source.USER
    )
    UserPlaceProposal.objects.create(
        place=pending, proposer=make_user("rf-prop"), status=UserPlaceProposal.Status.PENDING
    )
    Event.objects.create(
        title="Secret game", starts_at=timezone.now() + timedelta(days=1), place=pending
    )
    api = APIClient()
    api.force_authenticate(viewer)
    titles = [e["title"] for e in api.get("/api/events/").json()["results"]]
    assert "Public game" in titles
    assert "Secret game" not in titles


# --- W1-4: a cancelled activity can't be shared and degrades on read -----------------


def test_cannot_share_cancelled_activity(place, activity_type):
    owner = make_user("rf-cx-owner")
    member = make_user("rf-cx-member")
    host = social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Host",
        starts_at=timezone.now() + timedelta(days=1),
    )
    Membership.objects.create(
        activity=host, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    cancelled = social.create_activity(
        member,
        place=place,
        activity_type=activity_type,
        title="Doomed",
        starts_at=timezone.now() + timedelta(days=2),
    )
    cancelled.status = Activity.Status.CANCELLED
    cancelled.save(update_fields=["status"])
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, host, "", share_activity=cancelled.pk)


def test_share_card_degrades_when_target_cancelled_after_share(place, activity_type):
    owner = make_user("rf-deg-owner")
    member = make_user("rf-deg-member")
    host = social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Host2",
        starts_at=timezone.now() + timedelta(days=1),
    )
    Membership.objects.create(
        activity=host, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    other = social.create_activity(
        member,
        place=place,
        activity_type=activity_type,
        title="Live",
        starts_at=timezone.now() + timedelta(days=2),
    )
    post = social.post_to_thread(member, host, "join this", share_activity=other.pk)
    assert social.share_card(post)["kind"] == "activity"
    other.status = Activity.Status.CANCELLED
    other.save(update_fields=["status"])
    post.refresh_from_db()
    assert social.share_card(post) == {"kind": "gone"}


# --- W1-6: web /share/ rejects a missing obj_id (no empty post) ----------------------


def test_web_share_rejects_missing_obj_id(client, place, activity_type):
    owner = make_user("rf-share-owner")
    activity = social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="ShareHost",
        starts_at=timezone.now() + timedelta(days=1),
    )
    client.force_login(owner)
    before = activity.thread.posts.count()
    resp = client.post("/share/", {"kind": "place", "target": activity.pk})  # no obj_id
    assert resp.status_code == 404
    assert activity.thread.posts.count() == before  # no empty post created


# --- W8-0: thread photos never get a stored perceptual fingerprint -------------------


def test_thread_photo_has_no_phash(place, activity_type):
    owner = make_user("rf-photo")
    activity = social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="PhotoHost",
        starts_at=timezone.now() + timedelta(days=1),
    )
    photo = media.upload_photo(owner, Photo.Kind.THREAD, _img_bytes(), thread=activity.thread)
    assert photo.phash == ""  # data-minimisation: no unused fingerprint on private photos
    avatar = media.upload_photo(owner, Photo.Kind.PROFILE, _img_bytes())
    assert avatar.phash  # the avatar-uniqueness consumer DOES get one


# --- W1-12: an explicit empty body on the DRF thread POST behaves like omitted -------


def test_drf_post_share_only_empty_body_ok(place, activity_type):
    owner = make_user("rf-emptybody")
    member = make_user("rf-eb-member")
    activity = social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="EBHost",
        starts_at=timezone.now() + timedelta(days=1),
    )
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    api = APIClient()
    api.force_authenticate(member)
    resp = api.post(
        f"/api/social/activities/{activity.pk}/posts/",
        {"body": "", "share_activity": activity.pk},
        format="json",
    )
    assert resp.status_code == 201
    assert resp.json()["share"]["kind"] == "activity"
