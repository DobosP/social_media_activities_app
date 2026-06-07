import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.communities.services import _ensure_city_area
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
    return make_user("ss_adult1", AgeBand.ADULT)


@pytest.fixture
def adult2():
    return make_user("ss_adult2", AgeBand.ADULT)


@pytest.fixture
def child():
    return make_user("ss_child1", AgeBand.UNDER_16, consented=True)


@pytest.fixture
def category(db):
    cat, _ = ActivityCategory.objects.get_or_create(slug="ss-sport", defaults={"name": "Sport"})
    return cat


@pytest.fixture
def activity_type(category):
    at, _ = ActivityType.objects.get_or_create(
        slug="ss-football", defaults={"name": "Football", "category": category}
    )
    return at


@pytest.fixture
def other_type(category):
    at, _ = ActivityType.objects.get_or_create(
        slug="ss-tennis", defaults={"name": "Tennis", "category": category}
    )
    return at


@pytest.fixture
def place(db):
    return Place.objects.create(
        name="Park",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        address_city="Cluj-Napoca",
    )


@pytest.fixture
def area(db):
    return _ensure_city_area("Cluj-Napoca")


@pytest.fixture
def now():
    return timezone.now()
