"""Dev-only demo-user seeding: DEBUG guard + idempotency."""

from io import StringIO

import pytest
from django.contrib.auth import get_user_model
from django.contrib.gis.geos import Point
from django.core.management import CommandError, call_command

from apps.accounts.services import can_participate
from apps.places.models import Place, PlaceActivity
from apps.taxonomy.models import ActivityType

pytestmark = pytest.mark.django_db


def _venue():
    place = Place.objects.create(
        name="Sala Demo", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    atype = ActivityType.objects.filter(is_active=True).order_by("slug").first()
    PlaceActivity.objects.create(place=place, activity=atype, confidence=0.9)
    return place


def test_refuses_outside_debug(settings):
    settings.DEBUG = False
    with pytest.raises(CommandError):
        call_command("seed_demo_users")


def test_seeds_participating_users_and_is_idempotent(settings):
    settings.DEBUG = True
    _venue()
    out = StringIO()

    call_command("seed_demo_users", stdout=out)
    call_command("seed_demo_users", stdout=out)  # second run: no dupes, no crash

    User = get_user_model()
    ana = User.objects.get(username="ana.demo")
    staff = User.objects.get(username="staff.demo")
    assert can_participate(ana)
    assert staff.is_staff and staff.is_superuser
    assert User.objects.filter(username__endswith=".demo").count() == 3
    assert ana.owned_activities.filter(title__startswith="[DEMO]").count() == 1
