"""F30 — the web surface for the minor-group "ask the organiser" relief valve.

A minor-group MEMBER (never the staff owner, never a non-member) sees a fixed-prompt control
that, on POST, sends one question to the organiser only — writing no Post. The page copy must
own the asymmetry (the organiser answers the whole group; there are no private replies)."""

import pytest
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Cohort, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.communities.models import Area
from apps.notifications.models import Notification
from apps.social import services as social
from apps.social.models import GroupQuestionPrompt, Post
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"
VALID_PROMPT = GroupQuestionPrompt.NEXT_MEETUP.value


def _staff(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    u.is_staff = True
    u.save(update_fields=["is_staff"])
    return u


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="ga-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="ga-read", defaults={"name": "Reading", "category": cat}
    )
    return t


@pytest.fixture
def child_group():
    area = Area.objects.create(city="Cluj-Napoca", slug="cluj-ga", name="Cluj-Napoca")
    staff = _staff("ga_owner")
    group = social.create_group(
        staff, area=area, title="Kids Reading", activity_type=_type(), cohort=Cohort.CHILD
    )
    return staff, group


def test_minor_member_sees_ask_control(child_group):
    staff, group = child_group
    child = _child("ga_member")
    social.join_group(child, group.id)
    html = _client(child).get(f"/groups/{group.id}/").content.decode()
    assert f"/groups/{group.id}/ask/" in html
    assert str(GroupQuestionPrompt.NEXT_MEETUP.label) in html
    # The asymmetry is stated plainly: answers are public to the whole group, not private.
    assert "no private replies" in html.lower()


def test_owner_does_not_see_ask_control(child_group):
    staff, group = child_group
    html = _client(staff).get(f"/groups/{group.id}/").content.decode()
    assert f"/groups/{group.id}/ask/" not in html


def test_non_member_minor_does_not_see_ask_control(child_group):
    staff, group = child_group
    other = _child("ga_nonmember")  # same cohort, can see the group, but not a member
    html = _client(other).get(f"/groups/{group.id}/").content.decode()
    assert f"/groups/{group.id}/ask/" not in html


def test_post_ask_notifies_owner_and_writes_no_post(child_group):
    staff, group = child_group
    child = _child("ga_poster")
    social.join_group(child, group.id)
    resp = _client(child).post(f"/groups/{group.id}/ask/", {"prompt": VALID_PROMPT})
    assert resp.status_code == 302
    assert (
        Notification.objects.filter(recipient=staff, kind=Notification.Kind.GROUP_QUESTION).count()
        == 1
    )
    assert not Post.objects.filter(thread=group.thread).exists()


def test_post_free_text_is_rejected(child_group):
    staff, group = child_group
    child = _child("ga_ft")
    social.join_group(child, group.id)
    resp = _client(child).post(f"/groups/{group.id}/ask/", {"prompt": "meet me at home"})
    assert resp.status_code == 302  # redirects back with an error message
    assert not Notification.objects.filter(
        recipient=staff, kind=Notification.Kind.GROUP_QUESTION
    ).exists()
