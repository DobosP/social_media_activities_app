"""W3-F18 — self-only one-time .ics download of the viewer's OWN upcoming meetups.

Session-authenticated file download (NOT a tokenized subscribable feed), behind the same read
wall as /my-meetups/: only the viewer's own admitted, OPEN, upcoming, same-cohort, non-hidden
meetups — so a child's future place+time can never leak outside the cohort/consent wall.
"""

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
from apps.social.models import Activity, Membership
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"
URL = "/account/calendar.ics"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="f18c-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="f18c-bball", defaults={"name": "Basketball", "category": cat}
    )
    return t


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _activity(owner, *, title="Pickup game", days=1, ends=True, **kw):
    now = timezone.now()
    return social.create_activity(
        owner,
        place=Place.objects.create(
            name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
        ),
        activity_type=_type(),
        title=title,
        starts_at=now + timedelta(days=days),
        ends_at=now + timedelta(days=days, hours=2) if ends else None,
        **kw,
    )


def _join(activity, user):
    return Membership.objects.create(
        activity=activity, user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def test_downloads_own_meetups_as_ics_attachment():
    owner = _user("f18c_o")
    a = _activity(owner, title="Tuesday hoops")  # owner is auto-seated MEMBER
    resp = _client(owner).get(URL)
    assert resp.status_code == 200
    assert resp["Content-Type"].startswith("text/calendar")
    cd = resp["Content-Disposition"]
    assert "attachment" in cd and "my-meetups-" in cd and ".ics" in cd
    body = resp.content.decode()
    assert body.startswith("BEGIN:VCALENDAR")
    assert "END:VCALENDAR" in body
    assert "BEGIN:VEVENT" in body and "END:VEVENT" in body
    assert f"UID:meetup-{a.id}@" in body
    assert "DTSTART:" in body and "DTEND:" in body and "DTSTAMP:" in body
    assert "SUMMARY:Tuesday hoops" in body
    assert "LOCATION:Court" in body


def test_login_required():
    resp = Client().get(URL)
    assert resp.status_code in (301, 302)  # anonymous is redirected to login


def test_is_self_scoped_to_own_meetups():
    me = _user("f18c_me")
    other = _user("f18c_other")
    mine = _activity(me, title="My own game")
    theirs = _activity(other, title="Someone elses game")
    body = _client(me).get(URL).content.decode()
    assert "My own game" in body
    assert "Someone elses game" not in body  # never another member's place+time
    assert f"meetup-{mine.id}@" in body
    assert f"meetup-{theirs.id}@" not in body


def test_excludes_cancelled_hidden_and_past():
    me = _user("f18c_filter")
    _activity(me, title="Live one")
    cancelled = _activity(me, title="Cancelled one")
    cancelled.status = Activity.Status.CANCELLED
    cancelled.save(update_fields=["status"])
    hidden = _activity(me, title="Hidden one")
    hidden.is_hidden = True
    hidden.save(update_fields=["is_hidden"])
    past = _activity(me, title="Past one")
    Activity.objects.filter(pk=past.pk).update(starts_at=timezone.now() - timedelta(hours=1))

    body = _client(me).get(URL).content.decode()
    assert "Live one" in body
    assert "Cancelled one" not in body
    assert "Hidden one" not in body
    assert "Past one" not in body


def test_omits_dtend_when_no_end_time():
    me = _user("f18c_noend")
    _activity(me, title="No end set", ends=False)
    body = _client(me).get(URL).content.decode()
    assert "DTSTART:" in body
    assert "DTEND:" not in body  # ends_at is null -> DTEND omitted


def test_rfc5545_escapes_summary_special_chars():
    me = _user("f18c_esc")
    _activity(me, title="Chess, boards; all welcome")
    body = _client(me).get(URL).content.decode()
    assert "SUMMARY:Chess\\, boards\\; all welcome" in body  # comma + semicolon escaped
    assert "Chess, boards" not in body  # the raw (unescaped) form must not appear


def test_uses_crlf_line_endings():
    me = _user("f18c_crlf")
    _activity(me, title="CRLF check")
    body = _client(me).get(URL).content.decode()
    assert "\r\n" in body  # RFC 5545 requires CRLF line breaks
    assert "BEGIN:VCALENDAR\r\n" in body
