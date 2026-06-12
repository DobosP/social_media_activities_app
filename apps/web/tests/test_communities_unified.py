"""W5: groups + communities are ONE discovery surface; /groups/ redirects there; the
group-create form accepts a validated GET prefill (start-a-group-from-a-chat) — and the
creation gates themselves are untouched (service-enforced)."""

import pytest
from django.test import override_settings

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.communities.models import Area
from apps.social import services as social
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, *, staff=False):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    if staff:
        u.is_staff = True
        u.save(update_fields=["is_staff"])
    return u


@pytest.fixture
def football():
    cat, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="w5-football", defaults={"name": "Football", "category": cat}
    )
    return t


def test_groups_list_redirects_to_communities(client):
    client.force_login(_user("w5-redir"))
    resp = client.get("/groups/")
    assert resp.status_code == 302
    assert resp["Location"] == "/communities/"


def test_unified_page_shows_groups_and_communities(client, football):
    staff = _user("w5-staff", staff=True)
    user = _user("w5-viewer")
    area = Area.objects.create(city="Cluj-Napoca", slug="cluj-w5", name="Cluj-Napoca")
    social.create_group(staff, area=area, title="W5 Football Crew", activity_type=football)
    client.force_login(user)
    page = client.get("/communities/").content.decode()
    assert "W5 Football Crew" in page
    assert "Groups" in page and "Around your city" in page


def test_group_create_get_prefill_validated(client, football):
    staff = _user("w5-prefill", staff=True)
    client.force_login(staff)
    page = client.get(
        "/groups/new/", {"city": "Cluj-Napoca", "type": football.slug}
    ).content.decode()
    assert 'value="Cluj-Napoca"' in page
    assert f'<option value="{football.pk}" selected' in page
    # invalid type slug is silently dropped, never an error
    resp = client.get("/groups/new/", {"type": "no-such-type"})
    assert resp.status_code == 200
    assert "selected" not in resp.content.decode().split("activity_type")[1].split("</select>")[0]


@override_settings(GROUPS_ALLOW_USER_CREATED=False)
def test_create_affordance_hidden_when_flag_off(client, football):
    user = _user("w5-noflag")
    client.force_login(user)
    page = client.get("/communities/").content.decode()
    assert "Start a group" not in page
    # and the create view itself still refuses (gate, not just hidden link)
    resp = client.get("/groups/new/")
    assert resp.status_code == 302
