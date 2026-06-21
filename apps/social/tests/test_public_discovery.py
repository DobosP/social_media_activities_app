"""Phase 3: anonymous (logged-out) discovery of ADULT activities & groups, organiser opt-IN
(privacy by default, invariant #4), with three independent walls making minor exposure
structurally impossible.
"""

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Cohort, User
from apps.accounts.services import apply_assurance
from apps.communities.models import Area
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Activity, Group
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _type(slug="pub_sport"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    at, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": slug.title(), "category": cat}
    )
    return at


def _place():
    return Place.objects.create(
        name="Court",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        address_city="Cluj-Napoca",
    )


def _adult_activity(owner, at, place):
    return social.create_activity(
        owner,
        place=place,
        activity_type=at,
        title="Adult meetup",
        starts_at=timezone.now() + timezone.timedelta(days=1),
    )


# --- privacy by default: nothing is listed until the organiser opts in ---


def test_adult_activity_not_listed_by_default():
    owner = _user("ad")
    act = _adult_activity(owner, _type(), _place())
    assert act.is_publicly_listed is False  # opt-in: not discoverable at creation
    assert act not in list(social.public_activities())


def test_adult_activity_listed_after_opt_in():
    owner = _user("adin")
    act = _adult_activity(owner, _type(), _place())
    social.set_public_listing(owner, act, True)
    assert act in list(social.public_activities())


def test_minor_activity_never_public_even_with_flag_force_set():
    teen = _user("teen", AgeBand.AGE_16_17)
    # Build a TEEN activity directly with the public flag force-set True — the cohort=ADULT
    # query wall must still exclude it.
    act = Activity.objects.create(
        owner=teen,
        place=_place(),
        activity_type=_type(),
        title="Teen meetup",
        starts_at=timezone.now() + timezone.timedelta(days=1),
        cohort=Cohort.TEEN,
        is_publicly_listed=True,
        status=Activity.Status.OPEN,
    )
    assert act not in list(social.public_activities())


def test_opted_out_and_hidden_and_suspended_owner_excluded():
    owner = _user("ad2")
    at, place = _type(), _place()
    opted_out = _adult_activity(owner, at, place)
    social.set_public_listing(owner, opted_out, True)  # opt in...
    social.set_public_listing(owner, opted_out, False)  # ...then back out
    assert opted_out not in list(social.public_activities())

    # An opted-in but hidden activity is still excluded.
    hidden = _adult_activity(_user("ad3"), at, _place())
    social.set_public_listing(hidden.owner, hidden, True)
    hidden.is_hidden = True
    hidden.save(update_fields=["is_hidden"])
    assert hidden not in list(social.public_activities())

    # An opted-in activity whose owner is suspended is excluded.
    susp_owner = _user("ad4")
    by_suspended = _adult_activity(susp_owner, at, _place())
    social.set_public_listing(susp_owner, by_suspended, True)
    susp_owner.is_active = False
    susp_owner.save(update_fields=["is_active"])
    assert by_suspended not in list(social.public_activities())


# --- the toggle wall ---


def test_set_public_listing_rejects_non_adult_owner():
    teen = _user("teen2", AgeBand.AGE_16_17)
    act = Activity.objects.create(
        owner=teen,
        place=_place(),
        activity_type=_type(),
        title="Teen meetup",
        starts_at=timezone.now() + timezone.timedelta(days=1),
        cohort=Cohort.TEEN,
    )
    with pytest.raises(social.InvalidState):
        social.set_public_listing(teen, act, True)


def test_set_public_listing_requires_owner():
    owner, stranger = _user("ad5"), _user("ad6")
    act = _adult_activity(owner, _type(), _place())
    with pytest.raises(social.NotAMember):
        social.set_public_listing(stranger, act, False)


# --- the create wall (defense-in-depth) ---


def test_create_activity_for_a_minor_is_not_publicly_listed():
    # A TEEN needs no parental consent, so they can create an activity — but it must never be
    # stored as publicly listed.
    teen = _user("teen3", AgeBand.AGE_16_17)
    act = _adult_activity(teen, _type(), _place())  # _adult_activity is just the create helper
    assert act.cohort == Cohort.TEEN
    assert act.is_publicly_listed is False  # opt-in default off, and a minor can never opt in


# --- groups mirror the activity walls ---


def test_public_groups_excludes_minor_and_opted_out(settings):
    settings.GROUPS_ALLOW_USER_CREATED = True
    owner = _user("gad")
    at = _type()
    area = Area.objects.create(city="Cluj-Napoca", slug="cluj-napoca", name="Cluj-Napoca")
    group = social.create_group(owner, area=area, title="Runners", activity_type=at)
    assert group.is_publicly_listed is False  # opt-in: not listed at creation
    assert group not in list(social.public_groups())

    social.set_public_listing(owner, group, True)
    assert group in list(social.public_groups())

    social.set_public_listing(owner, group, False)
    assert group not in list(social.public_groups())

    # A minor group force-set listed is still excluded by the cohort=ADULT wall.
    minor_group = Group.objects.create(
        owner=_user("gteen", AgeBand.AGE_16_17),
        area=area,
        category=at.category,
        activity_type=at,
        tier=Group.Tier.TYPE,
        cohort=Cohort.TEEN,
        title="Teen runners",
        is_publicly_listed=True,
    )
    assert minor_group not in list(social.public_groups())


# --- anonymous API surface, no owner PII ---


def test_public_api_is_anonymous_and_leaks_no_owner():
    owner = _user("ad7")
    act = _adult_activity(owner, _type(), _place())
    social.set_public_listing(owner, act, True)  # opt in so it appears
    client = APIClient()  # no authentication
    resp = client.get("/api/discovery/public/activities/")
    assert resp.status_code == 200
    assert resp.data and "owner" not in resp.data[0]
    assert "Adult meetup" in [a["title"] for a in resp.data]
