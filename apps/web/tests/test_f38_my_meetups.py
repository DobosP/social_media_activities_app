"""F38 — offline-resilient "my next meetups" page + its root-scoped service worker.

The page is strictly self-scoped (only the viewer's own admitted, OPEN, upcoming, same-cohort,
non-hidden meetups) so the on-device offline copy can never resurrect or leak a meetup. The SW
is served at root scope with the right content type; its offline/purge behaviour is browser JS
(syntax-checked separately) — these tests pin the server-side self-scoping + routing.
"""

from datetime import timedelta
from pathlib import Path

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, GuardianRelationship, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Activity, Membership
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"


def _user(name, band=AgeBand.ADULT, *, consented=False):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="f38-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="f38-bball", defaults={"name": "Basketball", "category": cat}
    )
    return t


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _activity(owner, *, title="Pickup game", meeting="North gate", days=1):
    now = timezone.now()
    return social.create_activity(
        owner,
        place=Place.objects.create(
            name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
        ),
        activity_type=_type(),
        title=title,
        starts_at=now + timedelta(days=days),
        meeting_point=meeting,
    )


def _join(activity, user):
    return Membership.objects.create(
        activity=activity, user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def test_shows_own_upcoming_meetup_with_meeting_point():
    owner = _user("f38_owner")
    member = _user("f38_member")
    a = _activity(owner, title="Sunset run", meeting="By the fountain")
    _join(a, member)
    html = _client(member).get("/my-meetups/").content.decode()
    assert "Sunset run" in html and "By the fountain" in html


def test_is_self_scoped_not_another_users_meetup():
    owner = _user("f38_owner2")
    me = _user("f38_me")
    other = _user("f38_other")
    mine = _activity(owner, title="My game", meeting="Gate A")
    theirs = _activity(owner, title="Their secret game", meeting="Gate B")
    _join(mine, me)
    _join(theirs, other)
    html = _client(me).get("/my-meetups/").content.decode()
    assert "My game" in html
    assert "Their secret game" not in html  # never another member's meetup


@pytest.mark.parametrize("mutate", ["cancel", "hide", "past"])
def test_excludes_cancelled_hidden_and_past_meetups(mutate):
    owner = _user(f"f38_o_{mutate}")
    member = _user(f"f38_m_{mutate}")
    a = _activity(owner, title="Edge game")
    _join(a, member)
    if mutate == "cancel":
        a.status = Activity.Status.CANCELLED
        a.save(update_fields=["status"])
    elif mutate == "hide":
        a.is_hidden = True
        a.save(update_fields=["is_hidden"])
    elif mutate == "past":
        Activity.objects.filter(pk=a.pk).update(starts_at=timezone.now() - timedelta(hours=1))
    html = _client(member).get("/my-meetups/").content.decode()
    assert "Edge game" not in html  # the offline copy must never resurrect one of these


def test_stale_cross_cohort_membership_is_walled_off():
    # A membership whose activity cohort no longer matches the viewer (e.g. after a cohort change)
    # must not surface — mirrors the wards manifest wall.
    child_owner = _user("f38_child_owner", AgeBand.UNDER_16, consented=True)
    adult = _user("f38_adult")
    # Build a CHILD-cohort activity and force the adult in as a stale member (bypassing join gates).
    child_act = Activity.objects.create(
        owner=child_owner,
        place=Place.objects.create(
            name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
        ),
        activity_type=_type(),
        title="Kids-only club",
        starts_at=timezone.now() + timedelta(days=1),
        cohort=child_owner.cohort,
    )
    _join(child_act, adult)
    html = _client(adult).get("/my-meetups/").content.decode()
    assert "Kids-only club" not in html  # cohort wall holds


def test_guardian_names_shown_to_a_ward():
    child = _user("f38_ward", AgeBand.UNDER_16, consented=True)
    guardian = _user("f38_guardian")
    GuardianRelationship.objects.create(
        guardian=guardian, ward=child, status=GuardianRelationship.Status.ACTIVE
    )
    html = _client(child).get("/my-meetups/").content.decode()
    assert "f38_guardian" in html  # the safe-exit grown-up, readable offline


def test_login_required():
    resp = Client().get("/my-meetups/")
    assert resp.status_code in (301, 302)  # redirect to login


# --- the service worker route ----------------------------------------------------------------


def test_service_worker_served_at_root_with_correct_headers():
    resp = Client().get("/sw.js")
    assert resp.status_code == 200
    assert resp["Content-Type"].startswith("text/javascript")
    assert resp["Service-Worker-Allowed"] == "/"
    body = resp.content.decode()
    # It is genuinely network-first for the page (live cancel preferred) + purgeable.
    assert "/my-meetups/" in body
    assert "fetch(req)" in body and "caches.match(PAGE)" in body
    assert "'purge'" in body


def test_sw_registration_and_purge_wiring_present_for_member_absent_for_anon():
    member = _user("f38_reg")
    on = _client(member).get("/my-meetups/").content.decode()
    assert "js/site.js" in on and "js/my-meetups.js" in on
    # The shared-phone safety wiring is the core of F38 — pin it so it can't silently regress.
    assert "data-meetups-owner" in on  # user-switch purge config
    assert str(member.public_id) in on  # the owner identity baked in
    off = Client().get("/").content.decode()
    assert "/sw.js" not in off and "mz-meetups-owner" not in off  # anon gets no SW + no wiring

    site_js = Path("static/js/site.js").read_text()
    assert "serviceWorker" in site_js
    assert "mz-meetups-owner" in site_js
    assert "caches.delete" in site_js  # reliable page-level purge (not only postMessage)
    assert '"purge"' in site_js
    assert 'form[action$="/logout/"]' in site_js  # logout-submit purge hook


def test_removed_or_requested_membership_does_not_appear():
    owner = _user("f38_rm_owner")
    member = _user("f38_rm_member")
    a = _activity(owner, title="Left game")
    # A REMOVED (or never-admitted REQUESTED) membership must NOT surface — only state=MEMBER does.
    Membership.objects.create(
        activity=a, user=member, role=Membership.Role.MEMBER, state=Membership.State.REMOVED
    )
    html = _client(member).get("/my-meetups/").content.decode()
    assert "Left game" not in html


def test_empty_state_does_not_crash():
    # A brand-new member with no meetups and no guardians renders cleanly (no 500).
    lonely = _user("f38_lonely")
    resp = _client(lonely).get("/my-meetups/")
    assert resp.status_code == 200
    assert "No upcoming meetups" in resp.content.decode()
