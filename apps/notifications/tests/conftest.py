import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityType


def make_adult(username: str) -> User:
    user = User.objects.create_user(username=username, password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    user.refresh_from_db()
    return user


@pytest.fixture
def user(db):
    return make_adult("recipient")


@pytest.fixture
def owner(db):
    return make_adult("owner")


@pytest.fixture
def activity(db, owner):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source="osm", osm_type="node", osm_id=5
    )
    at = ActivityType.objects.get(slug="basketball")
    return create_activity(
        owner,
        place=place,
        activity_type=at,
        title="Pickup game",
        starts_at=timezone.now() + timezone.timedelta(hours=2),
    )
