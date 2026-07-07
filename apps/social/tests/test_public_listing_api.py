import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand, Cohort
from apps.communities.models import Area
from apps.social import services as social
from apps.social.models import Group

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _client(user):
    client = APIClient()
    client.force_authenticate(user)
    return client


def _activity(owner, place, activity_type):
    return social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title=f"{owner.username} meetup",
        starts_at=timezone.now() + timezone.timedelta(days=1),
    )


def _area(slug):
    return Area.objects.create(city="Cluj-Napoca", slug=f"pub-api-{slug}", name="Cluj-Napoca")


def _group(owner, activity_type, *, area=None):
    return social.create_group(
        owner,
        area=area or _area(owner.username),
        title=f"{owner.username} group",
        activity_type=activity_type,
    )


def test_activity_set_public_listing_owner_toggles_and_serializer_exposes_flag(
    adult, place, activity_type
):
    activity = _activity(adult, place, activity_type)
    client = _client(adult)

    detail = client.get(f"/api/social/activities/{activity.id}/")
    assert detail.status_code == 200
    assert detail.json()["is_publicly_listed"] is False

    enabled = client.post(
        f"/api/social/activities/{activity.id}/set_public_listing/",
        {"listed": True},
        format="json",
    )
    assert enabled.status_code == 200, enabled.content
    assert enabled.json()["is_publicly_listed"] is True
    activity.refresh_from_db()
    assert activity.is_publicly_listed is True

    disabled = client.post(
        f"/api/social/activities/{activity.id}/set_public_listing/",
        {"is_publicly_listed": False},
        format="json",
    )
    assert disabled.status_code == 200, disabled.content
    assert disabled.json()["is_publicly_listed"] is False
    activity.refresh_from_db()
    assert activity.is_publicly_listed is False


def test_activity_set_public_listing_rejects_non_owner(adult, adult2, place, activity_type):
    activity = _activity(adult, place, activity_type)

    resp = _client(adult2).post(
        f"/api/social/activities/{activity.id}/set_public_listing/",
        {"listed": True},
        format="json",
    )

    assert resp.status_code == 403
    activity.refresh_from_db()
    assert activity.is_publicly_listed is False


def test_activity_set_public_listing_rejects_minor_owner(place, activity_type):
    teen = make_user("pubapi-teen-activity", AgeBand.AGE_16_17)
    activity = _activity(teen, place, activity_type)

    resp = _client(teen).post(
        f"/api/social/activities/{activity.id}/set_public_listing/",
        {"listed": True},
        format="json",
    )

    assert resp.status_code == 403
    activity.refresh_from_db()
    assert activity.is_publicly_listed is False


def test_activity_set_public_listing_requires_canonical_field(adult, place, activity_type):
    activity = _activity(adult, place, activity_type)
    client = _client(adult)

    missing = client.post(
        f"/api/social/activities/{activity.id}/set_public_listing/",
        {},
        format="json",
    )
    assert missing.status_code == 400
    assert "listed" in missing.json()

    malformed = client.post(
        f"/api/social/activities/{activity.id}/set_public_listing/",
        {"listed": "sometimes"},
        format="json",
    )
    assert malformed.status_code == 400
    assert "listed" in malformed.json()

    ambiguous = client.post(
        f"/api/social/activities/{activity.id}/set_public_listing/",
        {"listed": True, "is_publicly_listed": False},
        format="json",
    )
    assert ambiguous.status_code == 400
    assert "listed" in ambiguous.json()
    activity.refresh_from_db()
    assert activity.is_publicly_listed is False


def test_group_set_public_listing_owner_toggles_and_serializer_exposes_flag(
    adult, activity_type, settings
):
    settings.GROUPS_ALLOW_USER_CREATED = True
    group = _group(adult, activity_type)
    client = _client(adult)

    detail = client.get(f"/api/social/groups/{group.id}/")
    assert detail.status_code == 200
    assert detail.json()["is_publicly_listed"] is False

    enabled = client.post(
        f"/api/social/groups/{group.id}/set_public_listing/",
        {"listed": True},
        format="json",
    )
    assert enabled.status_code == 200, enabled.content
    assert enabled.json()["is_publicly_listed"] is True
    group.refresh_from_db()
    assert group.is_publicly_listed is True

    disabled = client.post(
        f"/api/social/groups/{group.id}/set_public_listing/",
        {"is_publicly_listed": False},
        format="json",
    )
    assert disabled.status_code == 200, disabled.content
    assert disabled.json()["is_publicly_listed"] is False
    group.refresh_from_db()
    assert group.is_publicly_listed is False


def test_group_set_public_listing_rejects_non_owner(adult, adult2, activity_type, settings):
    settings.GROUPS_ALLOW_USER_CREATED = True
    group = _group(adult, activity_type)

    resp = _client(adult2).post(
        f"/api/social/groups/{group.id}/set_public_listing/",
        {"listed": True},
        format="json",
    )

    assert resp.status_code == 403
    group.refresh_from_db()
    assert group.is_publicly_listed is False


def test_group_set_public_listing_rejects_minor_owner(activity_type):
    teen = make_user("pubapi-teen-group", AgeBand.AGE_16_17)
    group = Group.objects.create(
        owner=teen,
        area=_area(teen.username),
        category=activity_type.category,
        activity_type=activity_type,
        tier=Group.Tier.TYPE,
        cohort=Cohort.TEEN,
        title="Teen public listing group",
    )

    resp = _client(teen).post(
        f"/api/social/groups/{group.id}/set_public_listing/",
        {"listed": True},
        format="json",
    )

    assert resp.status_code == 403
    group.refresh_from_db()
    assert group.is_publicly_listed is False
