import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.models import AgeBand, User
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType


def make_user(username, *, age_band=AgeBand.ADULT, verified=True):
    user = User.objects.create_user(username=username, password="pw", age_band=age_band)
    user.recompute_cohort()
    user.is_identity_verified = verified
    user.identity_verified_at = timezone.now() if verified else None
    user.save()
    return user


@pytest.fixture
def owner(db):
    return make_user("owner")


@pytest.fixture
def member(db):
    return make_user("member")


@pytest.fixture
def outsider(db):
    return make_user("outsider")


@pytest.fixture
def teen(db):
    return make_user("teen", age_band=AgeBand.AGE_16_17)


@pytest.fixture
def place(db):
    return Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


@pytest.fixture
def activity_type(db):
    # get_or_create so it survives the table flush in transaction=True tests
    # (migration-seeded rows are not restored between TransactionTestCase tests).
    category, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    obj, _ = ActivityType.objects.get_or_create(
        slug="basketball", defaults={"name": "Basketball", "category": category}
    )
    return obj


@pytest.fixture
def thread(owner, member, place, activity_type):
    """An open activity whose thread has `owner` and `member` as active members."""
    activity = create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return activity.thread
