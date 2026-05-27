import io

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from PIL import Image

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


def make_png(color=(255, 0, 0), size=(32, 32)) -> bytes:
    out = io.BytesIO()
    Image.new("RGB", size, color).save(out, format="PNG")
    return out.getvalue()


def make_jpeg_with_exif() -> bytes:
    """A JPEG carrying EXIF metadata — used to prove metadata is stripped on upload."""
    img = Image.new("RGB", (32, 32), (0, 128, 255))
    exif = img.getexif()
    exif[0x0132] = "2020:01:01 00:00:00"  # DateTime
    exif[0x010F] = "SecretCameraMake"  # Make
    out = io.BytesIO()
    img.save(out, format="JPEG", exif=exif)
    return out.getvalue()


@pytest.fixture
def owner(db):
    return make_user("m_owner")


@pytest.fixture
def member(db):
    return make_user("m_member")


@pytest.fixture
def outsider(db):
    return make_user("m_outsider")


@pytest.fixture
def thread(owner, member, db):
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    category, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    activity_type, _ = ActivityType.objects.get_or_create(
        slug="basketball", defaults={"name": "Basketball", "category": category}
    )
    activity = create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return activity.thread
