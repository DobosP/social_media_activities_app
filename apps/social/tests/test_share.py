"""W6 share-into-the-conversation: a Post may carry ONE validated share (activity /
place / event). Write-time gates: author must see the activity (cohort), the place must
be public (F25), an event's venue must be public. Read-time re-gating: a target hidden
or unpublished later renders as an honest 'gone' stub. A venue card is the only
'location share' — never user coordinates."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.models import AgeBand
from apps.events.models import Event
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Activity, Membership, UserPlaceProposal

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, *, place, activity_type, title="Game", **kw):
    return social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title=title,
        starts_at=kw.pop("starts_at", timezone.now() + timedelta(days=1)),
        **kw,
    )


@pytest.fixture
def setup(place, activity_type):
    owner = make_user("sh-owner")
    member = make_user("sh-member")
    activity = _activity(owner, place=place, activity_type=activity_type)
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return owner, member, activity


def test_share_activity_card_and_regating(setup, place, activity_type):
    owner, member, activity = setup
    other = _activity(member, place=place, activity_type=activity_type, title="Other meetup")
    post = social.post_to_thread(member, activity, "join this too!", share_activity=other.pk)
    assert post.shared_activity_id == other.pk
    card = social.share_card(post)
    assert card["kind"] == "activity" and card["obj"].pk == other.pk
    # moderator-hide the target later → the card degrades to 'gone' on next read
    other.is_hidden = True
    other.save(update_fields=["is_hidden"])
    post.refresh_from_db()
    assert social.share_card(post) == {"kind": "gone"}


def test_share_cross_cohort_activity_rejected(setup, place, activity_type):
    owner, member, activity = setup
    child = make_user("sh-child", AgeBand.UNDER_16, consented=True)
    child_activity = _activity(child, place=place, activity_type=activity_type, title="Kids game")
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, activity, "look", share_activity=child_activity.pk)


def test_share_place_requires_public(setup):
    owner, member, activity = setup
    pending = Place.objects.create(
        name="Pending Court", location=Point(23.62, 46.71, srid=4326), source=Place.Source.USER
    )
    UserPlaceProposal.objects.create(
        place=pending, proposer=make_user("sh-prop"), status=UserPlaceProposal.Status.PENDING
    )
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, activity, "here?", share_place=pending.pk)
    # share-only message with an EMPTY body is fine for a valid public place
    post = social.post_to_thread(member, activity, "", share_place=activity.place_id)
    assert post.shared_place_id == activity.place_id
    assert social.share_card(post)["kind"] == "place"


def test_share_event_venue_gate_and_only_one_share(setup, place):
    owner, member, activity = setup
    event = Event.objects.create(
        title="Open day", starts_at=timezone.now() + timedelta(days=2), place=place
    )
    post = social.post_to_thread(member, activity, "", share_event=event.pk)
    assert social.share_card(post)["kind"] == "event"
    with pytest.raises(social.InvalidState):
        social.post_to_thread(
            member, activity, "", share_event=event.pk, share_place=place.pk
        )


def test_web_share_endpoint_and_thread_search(client, setup, place, activity_type):
    owner, member, activity = setup
    client.force_login(member)
    resp = client.post(
        "/share/",
        {"kind": "place", "obj_id": place.pk, "target": activity.pk, "note": "this venue rocks"},
    )
    assert resp.status_code == 302 and f"/activities/{activity.pk}/" in resp["Location"]
    page = client.get(f"/activities/{activity.pk}/").content.decode()
    assert "share-card" in page and place.name in page
    # W6 in-thread search (?tq=) finds the note, members-only path
    results = client.get(f"/activities/{activity.pk}/", {"tq": "venue rocks"}).content.decode()
    assert "venue rocks" in results
    outsider = make_user("sh-outsider")
    client.force_login(outsider)
    resp = client.get(f"/activities/{activity.pk}/", {"tq": "venue rocks"})
    assert resp.status_code == 200  # page renders (cohort-visible) …
    assert "venue rocks" not in resp.content.decode()  # …but no thread content leaks
