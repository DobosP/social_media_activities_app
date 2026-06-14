"""F33 — the pre-send nudge is advisory: the server write path stays a pure pass-through.

These pin the load-bearing safety property — the nudge NEVER mutates the canonical write path:
a contact-leak body still posts verbatim, and nothing is auto-reported. The user-facing
warning is delivered client-side; the human-initiated OFF_PLATFORM report stays the recourse.
"""

import pytest
from django.utils import timezone

from apps.safety.models import Report
from apps.social import services as social
from apps.social.models import Membership

LEAK = "ping me on 0712 345 678 and come to my place after"


@pytest.fixture
def thread_member(adult, adult2, place, activity_type):
    activity = social.create_activity(
        adult, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    Membership.objects.create(
        activity=activity, user=adult2, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return adult2, activity


@pytest.mark.django_db
def test_contact_leak_post_is_not_blocked(thread_member):
    member, activity = thread_member
    post = social.post_to_thread(member, activity, LEAK)
    # The default policy is NudgeMessagePolicy, yet the post is created verbatim (soft signal only).
    assert post.pk is not None
    assert post.body == LEAK


@pytest.mark.django_db
def test_contact_leak_post_never_auto_reports(thread_member):
    member, activity = thread_member
    before = Report.objects.count()
    social.post_to_thread(member, activity, LEAK)
    assert Report.objects.count() == before  # the nudge must NEVER file a report itself
