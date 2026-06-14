"""F30 — minor-group "ask the organiser" relief valve.

The headline child-safety properties pinned here:
  - a minor-group MEMBER can send ONE fixed-enum question to the STAFF organiser;
  - it writes NO Post (never member-visible, never an enumeration surface);
  - ONLY group.owner is notified — never a member fan-out, never the asking child's peers;
  - free text is rejected (closed enum only — no grooming/PII vector);
  - the owner can't "ask themselves"; non-members and adult-group members can't ask;
  - eligibility (can_participate) is required and re-checked;
  - the GROUP_QUESTION kind is MUTABLE (a question alert is not a DSA-mandated notice);
  - it's rate-limited and audited.
"""

import pytest
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand, Cohort, ParentalConsent
from apps.communities.models import Area
from apps.notifications.models import MUTABLE_KINDS, NON_MUTABLE_KINDS, Notification
from apps.safety.models import AuditLog
from apps.social import services as social
from apps.social.models import GroupQuestionPrompt, Post

from .conftest import make_user

pytestmark = pytest.mark.django_db

VALID_PROMPT = GroupQuestionPrompt.NEXT_MEETUP.value


def _staff(username):
    u = make_user(username, AgeBand.ADULT)
    u.is_staff = True
    u.save(update_fields=["is_staff"])
    return u


@pytest.fixture
def area():
    return Area.objects.create(city="Cluj-Napoca", slug="cluj-ask", name="Cluj-Napoca")


def _child_group(staff, area, activity_type, **kw):
    return social.create_group(
        staff,
        area=area,
        title="Kids Reading",
        activity_type=activity_type,
        cohort=Cohort.CHILD,
        **kw,
    )


def _q_count(owner):
    return Notification.objects.filter(
        recipient=owner, kind=Notification.Kind.GROUP_QUESTION
    ).count()


# --- core: owner-only notify, NO post -------------------------------------------------


def test_ask_notifies_only_owner_and_writes_no_post(area, activity_type):
    staff = _staff("ask_owner")
    group = _child_group(staff, area, activity_type)
    child = make_user("ask_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)

    posts_before = group.thread.posts.count()
    social.group_ask_organiser(child, group, VALID_PROMPT)

    # Exactly one notification, to the staff organiser.
    assert _q_count(staff) == 1
    # NO Post written — the question is never member-visible / never an enumeration surface.
    assert group.thread.posts.count() == posts_before
    assert not Post.objects.filter(thread=group.thread).exists()
    # The asking child is never notified about their own question.
    assert _q_count(child) == 0


def test_no_member_fanout_other_members_not_notified(area, activity_type):
    staff = _staff("fan_owner")
    group = _child_group(staff, area, activity_type)
    asker = make_user("fan_asker", AgeBand.UNDER_16, consented=True)
    bystander = make_user("fan_bystander", AgeBand.UNDER_16, consented=True)
    social.join_group(asker, group.id)
    social.join_group(bystander, group.id)

    social.group_ask_organiser(asker, group, VALID_PROMPT)

    assert _q_count(staff) == 1
    # A peer member learns nothing — no GROUP_QUESTION (or any) notification to other members.
    assert _q_count(bystander) == 0
    assert not Notification.objects.filter(recipient=bystander).exists()


def test_notification_body_carries_the_fixed_prompt_label(area, activity_type):
    staff = _staff("body_owner")
    group = _child_group(staff, area, activity_type)
    child = make_user("body_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)

    social.group_ask_organiser(child, group, GroupQuestionPrompt.WHAT_TO_BRING.value)
    n = Notification.objects.get(recipient=staff, kind=Notification.Kind.GROUP_QUESTION)
    assert str(GroupQuestionPrompt.WHAT_TO_BRING.label) in n.body
    assert n.url == f"/groups/{group.id}/"


# --- input is a closed enum: no free text --------------------------------------------


def test_free_text_is_rejected(area, activity_type):
    staff = _staff("ft_owner")
    group = _child_group(staff, area, activity_type)
    child = make_user("ft_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)

    with pytest.raises(social.InvalidState):
        social.group_ask_organiser(child, group, "my name is X, meet me at 5 Foo St")
    assert _q_count(staff) == 0


@pytest.mark.parametrize("bad", ["", None, "nonexistent_choice"])
def test_invalid_prompt_choices_rejected(area, activity_type, bad):
    staff = _staff(f"bad_owner_{bad!r}")
    group = _child_group(staff, area, activity_type)
    child = make_user(f"bad_child_{bad!r}", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)
    with pytest.raises(social.InvalidState):
        social.group_ask_organiser(child, group, bad)


# --- who may ask ----------------------------------------------------------------------


def test_non_member_cannot_ask(area, activity_type):
    staff = _staff("nm_owner")
    group = _child_group(staff, area, activity_type)
    outsider = make_user("nm_child", AgeBand.UNDER_16, consented=True)  # never joined
    with pytest.raises(social.NotAMember):
        social.group_ask_organiser(outsider, group, VALID_PROMPT)


def test_owner_cannot_ask_themselves(area, activity_type):
    staff = _staff("self_owner")
    group = _child_group(staff, area, activity_type)
    # The staff curator holds an OWNER-role membership — they must not be able to ask.
    with pytest.raises(social.NotAMember):
        social.group_ask_organiser(staff, group, VALID_PROMPT)
    assert _q_count(staff) == 0


def test_adult_group_is_rejected(area, activity_type):
    staff = _staff("adult_owner")
    group = social.create_group(
        staff, area=area, title="Adult Basketball", activity_type=activity_type
    )
    member = make_user("adult_member", AgeBand.ADULT)
    social.join_group(member, group.id)
    with pytest.raises(social.NotEligible):
        social.group_ask_organiser(member, group, VALID_PROMPT)


def test_member_without_current_consent_cannot_ask(area, activity_type):
    staff = _staff("cons_owner")
    group = _child_group(staff, area, activity_type)
    child = make_user("cons_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)
    # Consent lapses after joining: the relief valve re-checks can_participate at ask time.
    ParentalConsent.objects.filter(minor=child).update(status=ParentalConsent.Status.REVOKED)
    with pytest.raises(social.NotEligible):
        social.group_ask_organiser(child, group, VALID_PROMPT)
    assert _q_count(staff) == 0


# --- kind is mutable; rate-limited; audited ------------------------------------------


def test_group_question_kind_is_mutable():
    assert Notification.Kind.GROUP_QUESTION in MUTABLE_KINDS
    assert Notification.Kind.GROUP_QUESTION not in NON_MUTABLE_KINDS


def test_rate_limited(area, activity_type, settings):
    settings.GROUP_QUESTION_RATE_LIMIT = 2
    settings.GROUP_QUESTION_RATE_WINDOW_SECONDS = 3600
    staff = _staff("rl_owner")
    group = _child_group(staff, area, activity_type)
    child = make_user("rl_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)

    social.group_ask_organiser(child, group, VALID_PROMPT)
    social.group_ask_organiser(child, group, GroupQuestionPrompt.WHERE.value)
    with pytest.raises(social.InvalidState):
        social.group_ask_organiser(child, group, GroupQuestionPrompt.WHAT_TO_BRING.value)
    assert _q_count(staff) == 2


def test_ask_is_audited(area, activity_type):
    staff = _staff("aud_owner")
    group = _child_group(staff, area, activity_type)
    child = make_user("aud_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)

    social.group_ask_organiser(child, group, VALID_PROMPT)
    row = AuditLog.objects.filter(event="group.question_asked").order_by("-id").first()
    assert row is not None
    assert row.actor_ref == child.id
    # The audit records the choice KEY only — never free text.
    assert row.data.get("prompt") == VALID_PROMPT


# --- DRF action -----------------------------------------------------------------------


def test_drf_ask_action(area, activity_type):
    staff = _staff("drf_owner")
    group = _child_group(staff, area, activity_type)
    child = make_user("drf_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)

    client = APIClient()
    client.force_authenticate(child)
    resp = client.post(f"/api/social/groups/{group.id}/ask/", {"prompt": VALID_PROMPT})
    assert resp.status_code == 200, resp.content
    assert resp.json() == {"sent": True}
    # Response leaks no roster/count surface.
    assert set(resp.json().keys()) == {"sent"}
    assert _q_count(staff) == 1


def test_drf_ask_rejects_free_text(area, activity_type):
    staff = _staff("drf_ft_owner")
    group = _child_group(staff, area, activity_type)
    child = make_user("drf_ft_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)

    client = APIClient()
    client.force_authenticate(child)
    resp = client.post(f"/api/social/groups/{group.id}/ask/", {"prompt": "meet me at my house"})
    assert resp.status_code == 400, resp.content
    assert _q_count(staff) == 0
