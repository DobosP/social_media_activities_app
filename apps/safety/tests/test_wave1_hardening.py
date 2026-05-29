"""Wave 1 safety hardening:

* W1-14 — URL sanitization blocks script-bearing schemes (stored XSS).
* W1-10 — a moderator REMOVE action actually hides the content from member-facing
  surfaces (feed + thread), retaining it for audit. See
  docs/PRODUCTION_HARDENING_PLAN_2026-05.md."""

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.models import ModerationAction, ReasonCode
from apps.safety.sanitize import safe_external_url, safe_href
from apps.safety.services import take_action
from apps.social.services import create_activity, post_to_thread, visible_activities
from apps.taxonomy.models import ActivityCategory, ActivityType


def test_safe_external_url_blocks_script_schemes():
    assert safe_external_url("javascript:alert(1)") == ""
    assert safe_external_url("  JavaScript:alert(1)") == ""
    assert safe_external_url("data:text/html;base64,PHNjcmlwdD4=") == ""
    assert safe_external_url("vbscript:msgbox(1)") == ""
    assert safe_external_url("//evil.example/x") == ""
    assert safe_external_url("") == ""
    assert safe_external_url("https://ok.example/x") == "https://ok.example/x"
    assert safe_external_url("http://ok.example") == "http://ok.example"


def test_safe_href_allows_internal_relative_only():
    assert safe_href("/notifications") == "/notifications"
    assert safe_href("//evil.example") == ""  # protocol-relative is not internal
    assert safe_href("javascript:x") == ""
    assert safe_href("https://ok") == "https://ok"


# --- moderation REMOVE hides content (DB tests decorated individually) ---------------


def _verified(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat, _ = ActivityCategory.objects.get_or_create(slug="modcat", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="modbball", defaults={"name": "Basketball", "category": cat}
    )
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at=timezone.now()
    )


@pytest.mark.django_db
def test_remove_hides_post_from_thread():
    owner = _verified("mod_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "offending content")
    moderator = User.objects.create_user(username="mod1", password="pw", is_staff=True)

    take_action(moderator, post, ModerationAction.Action.REMOVE, ReasonCode.HARASSMENT)

    post.refresh_from_db()
    assert post.is_hidden is True
    # Excluded from the member-facing thread read, but the row is retained for audit.
    assert activity.thread.posts.filter(is_hidden=False).count() == 0
    assert activity.thread.posts.count() == 1


@pytest.mark.django_db
def test_remove_hides_activity_from_feed():
    owner = _verified("mod_owner2")
    activity = _activity(owner)
    assert visible_activities(owner).filter(id=activity.id).exists()

    moderator = User.objects.create_user(username="mod2", password="pw", is_staff=True)
    take_action(moderator, activity, ModerationAction.Action.REMOVE, ReasonCode.SPAM)

    activity.refresh_from_db()
    assert activity.is_hidden is True
    assert not visible_activities(owner).filter(id=activity.id).exists()


@pytest.mark.django_db
def test_remove_hides_activity_from_web_detail():
    from django.http import Http404

    from apps.web.views import _visible_activity_or_404

    owner = _verified("web_owner")
    activity = _activity(owner)
    assert _visible_activity_or_404(owner, activity.id).id == activity.id  # visible before

    moderator = User.objects.create_user(username="webmod", password="pw", is_staff=True)
    take_action(moderator, activity, ModerationAction.Action.REMOVE, ReasonCode.SPAM)
    activity.refresh_from_db()

    with pytest.raises(Http404):
        _visible_activity_or_404(owner, activity.id)  # member can no longer open it
    # ...but staff/moderators still can, for review/appeal.
    assert _visible_activity_or_404(moderator, activity.id).id == activity.id


@pytest.mark.django_db
def test_remove_blocks_chat_thread_access():
    from apps.chat.services import can_access_thread
    from apps.social.models import Thread

    owner = _verified("chat_owner")
    activity = _activity(owner)
    thread = Thread.objects.select_related("activity").get(activity=activity)
    assert can_access_thread(owner, thread) is True  # owner-member before removal

    moderator = User.objects.create_user(username="chatmod", password="pw", is_staff=True)
    take_action(moderator, activity, ModerationAction.Action.REMOVE, ReasonCode.HARASSMENT)

    thread = Thread.objects.select_related("activity").get(activity=activity)
    assert can_access_thread(owner, thread) is False  # removed → thread closed to members


@pytest.mark.django_db
def test_warn_does_not_hide_content():
    owner = _verified("mod_owner3")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "borderline")
    moderator = User.objects.create_user(username="mod3", password="pw", is_staff=True)

    take_action(moderator, post, ModerationAction.Action.WARN, ReasonCode.OTHER)

    post.refresh_from_db()
    assert post.is_hidden is False  # a warning leaves content visible
