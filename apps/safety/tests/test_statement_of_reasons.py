"""W1-9: DSA Art.17 statement-of-reasons. take_action must notify the affected user
(target user / activity owner / post author) with a MODERATION notification describing
the action, the reason, and that they may contest it — best-effort, never breaking the
moderation action itself."""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.safety.models import ModerationAction, ReasonCode
from apps.safety.services import take_action
from apps.social.services import create_activity, post_to_thread
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _activity(owner, slug):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"sor-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"sor-{slug}-bball", name="Basketball", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2026-06-01T10:00Z"
    )


def _moderation_notice(recipient):
    return Notification.objects.filter(recipient=recipient, kind=Notification.Kind.MODERATION)


def test_ban_notifies_target_user():
    mod, offender = _user("mod-ban"), _user("bad-ban")
    take_action(mod, offender, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    notices = _moderation_notice(offender)
    assert notices.count() == 1
    notice = notices.get()
    assert "contest" in notice.body.lower()
    assert "Ban account" in notice.body
    assert "Grooming" in notice.body
    assert notice.url == ""


def test_suspend_notifies_target_user():
    mod, offender = _user("mod-susp"), _user("bad-susp")
    take_action(mod, offender, ModerationAction.Action.SUSPEND, ReasonCode.HARASSMENT)
    assert _moderation_notice(offender).count() == 1


def test_remove_notifies_post_author():
    mod = _user("mod-post")
    author = _user("author-post")
    activity = _activity(author, "post")
    post = post_to_thread(author, activity, "hello")
    take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.SPAM)
    notices = _moderation_notice(author)
    assert notices.count() == 1
    assert "Remove content" in notices.get().body


def test_remove_notifies_activity_owner():
    mod = _user("mod-act")
    owner = _user("owner-act")
    activity = _activity(owner, "act")
    take_action(mod, activity, ModerationAction.Action.REMOVE, ReasonCode.OFF_PLATFORM)
    notices = _moderation_notice(owner)
    assert notices.count() == 1
    assert "Remove content" in notices.get().body


def test_warn_on_user_notifies_user():
    mod, offender = _user("mod-warn"), _user("bad-warn")
    take_action(mod, offender, ModerationAction.Action.WARN, ReasonCode.OTHER)
    assert _moderation_notice(offender).count() == 1


def test_notify_failure_does_not_break_action(monkeypatch):
    """A notification backend failure must never roll back the moderation action."""
    import apps.notifications.services as notif_services

    def _boom(*args, **kwargs):
        raise RuntimeError("notification backend down")

    monkeypatch.setattr(notif_services, "notify", _boom)

    mod, offender = _user("mod-fail"), _user("bad-fail")
    action = take_action(mod, offender, ModerationAction.Action.BAN, ReasonCode.CSAM)

    offender.refresh_from_db()
    assert action.action == ModerationAction.Action.BAN
    assert offender.is_active is False
    assert _moderation_notice(offender).count() == 0
