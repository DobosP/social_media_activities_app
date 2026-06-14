"""W2-F1 (web): the activity-list search shows an honest, URL-encoded "Did you mean X?" only
when the search found nothing AND there's an actionable suggestion."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _football_activity(owner):
    fb = ActivityType.objects.get(slug="football")  # seeded; carries the 'fotbal' alias
    return create_activity(
        owner,
        place=Place.objects.create(
            name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
        ),
        activity_type=fb,
        title="Sunday kickabout",
        starts_at=timezone.now() + timedelta(days=1),
    )


def test_did_you_mean_rendered_on_zero_results():
    owner = _user("w2f1w_o1")
    _football_activity(owner)
    # 'fotball' is NOT a substring of 'football' or the 'fotbal' alias, but is trigram-close.
    html = _client(owner).get("/activities/?q=fotball").content.decode()
    assert "Did you mean" in html
    assert "Football" in html
    assert "?q=Football" in html  # encoded suggestion href (single word => unchanged)


def test_no_did_you_mean_when_results_exist():
    owner = _user("w2f1w_o2")
    _football_activity(owner)
    html = _client(owner).get("/activities/?q=fotbal").content.decode()  # alias hit => results
    assert "Sunday kickabout" in html
    assert "Did you mean" not in html


def test_no_did_you_mean_for_unrelated_query():
    owner = _user("w2f1w_o3")
    _football_activity(owner)
    html = _client(owner).get("/activities/?q=zzzqqxyz").content.decode()
    assert "Did you mean" not in html
