"""The messaging API surfaces the SAME generated avatar as the rest of the app — the interest
constellation (identicon fallback when no interests) — so a person doesn't show one picture in
the web chat chips and a different one in the conversation list / API."""

import pytest

from apps.accounts.avatars import identicon_data_uri
from apps.messaging.serializers import UserRefSerializer
from apps.recommendations import services
from apps.recommendations.services import interest_avatar_data_uri
from apps.taxonomy.models import ActivityCategory, ActivityType

from .conftest import make_user

pytestmark = pytest.mark.django_db


def test_user_ref_serializer_renders_the_interest_constellation():
    u = make_user("mz-stars")
    cat, _ = ActivityCategory.objects.get_or_create(
        slug="team_sport", defaults={"name": "Team Sport"}
    )
    ActivityType.objects.create(slug="mz-bball", name="Basketball", category=cat)
    ActivityType.objects.create(slug="mz-foot", name="Football", category=cat)
    services.set_interests(u, ["mz-bball", "mz-foot"])
    data = UserRefSerializer(u).data
    assert data["avatar"] == interest_avatar_data_uri(u)  # the constellation seam
    assert data["avatar"] != identicon_data_uri("mz-stars")  # no longer the bare identicon


def test_user_ref_serializer_falls_back_to_identicon_without_interests():
    u = make_user("mz-empty")
    data = UserRefSerializer(u).data
    assert data["avatar"] == identicon_data_uri("mz-empty")
