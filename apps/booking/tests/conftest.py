import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.taxonomy.models import ActivityType


def make_adult(username: str) -> User:
    user = User.objects.create_user(username=username, password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    user.refresh_from_db()
    return user


@pytest.fixture
def adult(db):
    return make_adult("booker")


@pytest.fixture
def place(db):
    return Place.objects.create(
        name="City Sports Hall",
        location=Point(23.6, 46.77, srid=4326),
        source="osm",
        osm_type="node",
        osm_id=9001,
    )


@pytest.fixture
def activity_type(db):
    return ActivityType.objects.get(slug="basketball")


@pytest.fixture
def now():
    return timezone.now()
