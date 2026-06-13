"""Regression test for review finding W1-2: the chat-list ?q= metadata search must not
match a participant the serializer deliberately hides (LEFT/REMOVED) — otherwise a user
who left precisely to disappear from that surface is rediscoverable by name search."""

import pytest
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.messaging import services as messaging
from apps.messaging.models import Participant

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_left_participant_not_matched_by_chat_search():
    owner = _user("ms-owner")
    stays = _user("ms-zelda")
    leaver = _user("ms-quincy")
    conv = messaging.start_group(owner, [stays, leaver], title="Trail crew")
    # accept so they're ACTIVE, then the leaver leaves
    messaging.accept_invite(stays, conv)
    messaging.accept_invite(leaver, conv)
    messaging.leave(leaver, conv)
    assert conv.participants.get(user=leaver).state in (
        Participant.State.LEFT,
        Participant.State.REMOVED,
    )

    api = APIClient()
    api.force_authenticate(owner)
    # searching the active participant's name still finds the conversation…
    assert any(
        c["id"] == conv.id for c in api.get("/api/messaging/conversations/", {"q": "zelda"}).json()
    )
    # …but the LEFT participant's name does NOT resurface the conversation
    found = api.get("/api/messaging/conversations/", {"q": "quincy"}).json()
    assert all(c["id"] != conv.id for c in found)
