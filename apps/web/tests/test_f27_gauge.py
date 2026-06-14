"""F27 — web surface for the ephemeral gauge-interest poll: list, create, count-only detail,
I'd-come toggle, and proposer-only convert. Same-cohort visibility; count shown, never a roster."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import ActivityInterest
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"
WINDOW = ActivityInterest.CoarseWindow.WEEKEND_DAYTIME.value


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="f27-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="f27-bball", defaults={"name": "Basketball", "category": cat}
    )
    return t


def _place(name="Court"):
    return Place.objects.create(
        name=name, location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _gauge(proposer):
    return social.propose_interest(
        proposer, place=_place(), activity_type=_type(), coarse_window=WINDOW
    )


def test_create_gauge_via_web():
    user = _user("f27w_creator")
    place, atype = _place(), _type()
    resp = _client(user).post(
        "/gauges/new/",
        {"place": place.id, "activity_type": atype.id, "coarse_window": WINDOW},
    )
    assert resp.status_code == 302
    assert ActivityInterest.objects.filter(proposer=user).exists()


def test_detail_shows_bounded_signal_not_roster():
    proposer = _user("f27w_prop")
    signer = _user("f27wRosterA")
    g = _gauge(proposer)
    social.mark_interested(signer, g)  # proposer + signer = 2; threshold 3 → needs 1 more
    # View as the proposer (their own name is in the nav, so assert on the OTHER signer's name).
    body = _client(proposer).get(f"/gauges/{g.pk}/").content.decode()
    assert "needs 1 more" in body.lower()  # bounded functional signal, not a raw count
    assert "3 interested" not in body  # no raw cumulative count anywhere
    assert "f27wRosterA" not in body  # never WHO signalled (no roster)


def test_id_come_toggle():
    proposer = _user("f27w_p2")
    peer = _user("f27w_peer2")
    g = _gauge(proposer)
    _client(peer).post(f"/gauges/{g.pk}/interested/")
    assert g.interested_users.filter(id=peer.id).exists()
    _client(peer).post(f"/gauges/{g.pk}/uninterested/")
    assert not g.interested_users.filter(id=peer.id).exists()


def test_cross_cohort_cannot_see_gauge():
    proposer = _user("f27w_p3")
    teen = _user("f27w_teen", AgeBand.AGE_16_17)
    g = _gauge(proposer)
    assert _client(teen).get(f"/gauges/{g.pk}/").status_code == 404


def test_non_proposer_cannot_convert():
    from apps.social.models import Activity

    proposer = _user("f27w_p4")
    peer = _user("f27w_peer4")
    g = _gauge(proposer)
    assert _client(peer).get(f"/gauges/{g.pk}/convert/").status_code == 404
    # The state-changing POST must be blocked too — not just the GET.
    before = Activity.objects.count()
    starts = (timezone.now() + timedelta(days=2)).strftime("%Y-%m-%dT%H:%M")
    resp = _client(peer).post(f"/gauges/{g.pk}/convert/", {"title": "Sneaky", "starts_at": starts})
    assert resp.status_code == 404
    assert Activity.objects.count() == before
    g.refresh_from_db()
    assert g.converted_activity_id is None


def test_proposer_converts_via_web():
    proposer = _user("f27w_p5")
    g = _gauge(proposer)
    starts = (timezone.now() + timedelta(days=2)).strftime("%Y-%m-%dT%H:%M")
    resp = _client(proposer).post(
        f"/gauges/{g.pk}/convert/", {"title": "Web Converted", "starts_at": starts}
    )
    assert resp.status_code == 302
    g.refresh_from_db()
    assert g.converted_activity is not None
    assert g.converted_activity.title == "Web Converted"
