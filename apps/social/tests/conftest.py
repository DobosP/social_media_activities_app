import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.taxonomy.models import ActivityCategory, ActivityType


def make_user(username, age_band=AgeBand.ADULT, *, consented=False):
    user = User.objects.create_user(username=username, password="pw", display_name=username)
    apply_assurance(user, AssuranceResult(age_band=age_band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=user, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return user


@pytest.fixture
def adult():
    return make_user("adult1", AgeBand.ADULT)


@pytest.fixture
def adult2():
    return make_user("adult2", AgeBand.ADULT)


@pytest.fixture
def child():
    return make_user("child1", AgeBand.UNDER_16, consented=True)


@pytest.fixture
def place(db):
    return Place.objects.create(
        name="Community Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


@pytest.fixture
def activity_type(db):
    category, _ = ActivityCategory.objects.get_or_create(
        slug="d3-test-sport", defaults={"name": "Sport"}
    )
    activity_type, _ = ActivityType.objects.get_or_create(
        slug="d3-test-basketball", defaults={"name": "Basketball", "category": category}
    )
    return activity_type


@pytest.fixture
def now():
    return timezone.now()
