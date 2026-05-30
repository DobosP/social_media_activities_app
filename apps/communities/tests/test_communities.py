"""Communities: per-cohort generation thresholds (incl. the k-anonymity floor that EXCLUDES
supervisory guardians), the cohort wall (a child sees only child communities, never the
existence/content of an adult one), the structural absence of any count/roster, and the boundary
that sharing only a COMMUNITY never enables a private connection."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.communities import services as communities
from apps.communities.models import Community
from apps.communities.serializers import CommunitySerializer
from apps.connections import services as connections
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Membership
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "pw-123-secret"
CITY = "Cluj-Napoca"


@pytest.fixture(autouse=True)
def low_thresholds(settings):
    # Most tests use trivial floors so one activity publishes; k-anon tests override.
    settings.COMMUNITY_MIN_ACTIVITIES = 1
    settings.COMMUNITY_MIN_DAYS = 1
    settings.COMMUNITY_K_ANON_FLOOR = 1


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _type(slug="football", cat_slug="sport", cat_name="Sport"):
    cat, _ = ActivityCategory.objects.get_or_create(slug=cat_slug, defaults={"name": cat_name})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": slug.title(), "category": cat}
    )
    return t


def _place(city=CITY):
    return Place.objects.create(
        name="Court",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        address_city=city,
    )


def _activity(owner, *, atype=None, city=CITY, days_ahead=1):
    return social.create_activity(
        owner,
        place=_place(city),
        activity_type=atype or _type(),
        title="Game",
        starts_at=timezone.now() + timedelta(days=days_ahead),
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


# --- generation + tiers --------------------------------------------------------------------


def test_generation_publishes_type_and_category():
    t = _type()
    _activity(_adult("g1"), atype=t)
    communities.generate_communities()
    names = set(Community.objects.filter(is_published=True).values_list("name", flat=True))
    assert f"{CITY} {t.name}" in names  # TYPE tier
    assert f"{CITY} {t.category.name}" in names  # CATEGORY rollup (name from the real taxonomy)
    assert Community.objects.filter(cohort="adult", tier="type", activity_type=t).exists()


def test_generic_non_sport_category():
    # Unique slugs so we don't collide with the migration-seeded taxonomy and our names stick.
    bg = _type(slug="tu-boardgames", cat_slug="tu-tabletop", cat_name="Tabletop")
    _activity(_adult("g2"), atype=bg)
    communities.generate_communities()
    assert Community.objects.filter(name=f"{CITY} {bg.name}", is_published=True).exists()
    assert Community.objects.filter(name=f"{CITY} {bg.category.name}", is_published=True).exists()


def test_type_slug_equal_to_category_slug_does_not_abort_run():
    # Seeded taxonomy ships a type whose slug == its category's slug (e.g. "reading"). Both the
    # TYPE and CATEGORY-rollup communities must publish in ONE run — a slug clash must NOT raise
    # an IntegrityError that rolls back the whole nightly generation.
    same = _type(slug="reading", cat_slug="reading", cat_name="Reading")
    assert same.slug == same.category.slug  # the collision precondition
    _activity(_adult("ss1"), atype=same)
    result = communities.generate_communities()
    assert result["published"] >= 2  # did not abort
    pub = Community.objects.filter(is_published=True, cohort="adult")
    assert pub.filter(tier="type", activity_type=same).exists()
    assert pub.filter(tier="category", category=same.category, activity_type__isnull=True).exists()


def test_deactivate_when_below_threshold(settings):
    _activity(_adult("g3"))
    communities.generate_communities()
    assert Community.objects.filter(is_published=True).exists()
    settings.COMMUNITY_MIN_ACTIVITIES = 99  # now nothing clears
    communities.generate_communities()
    assert not Community.objects.filter(is_published=True).exists()  # deactivated, not deleted
    assert Community.objects.exists()  # rows retained


# --- the cohort wall -----------------------------------------------------------------------


def test_cohort_wall_child_sees_no_adult_community():
    _activity(_adult("ad1"))  # only adult activity exists
    communities.generate_communities()
    child = _child("ch1")
    assert list(communities.visible_communities(child)) == []  # no child community published
    adult_comm = Community.objects.get(name=f"{CITY} Football", cohort="adult")
    # ...and a child cannot read the adult community by slug or list its activities.
    assert communities.community_by_slug(adult_comm.slug, child) is None
    assert list(communities.community_activities(adult_comm, child)) == []


def test_community_activities_are_cohort_filtered():
    owner = _adult("ad2")
    act = _activity(owner)
    communities.generate_communities()
    comm = Community.objects.get(name=f"{CITY} Football", cohort="adult")
    assert act in list(communities.community_activities(comm, owner))
    # a different-cohort viewer gets nothing, even for the adult community object
    assert list(communities.community_activities(comm, _child("ch2"))) == []


# --- k-anonymity excludes guardians (must-fix) ---------------------------------------------


def test_k_anon_excludes_supervisory_guardians(settings):
    settings.COMMUNITY_K_ANON_FLOOR = 2
    settings.COMMUNITY_MIN_ACTIVITIES = 1
    child_owner = _child("ko1")
    activity = _activity(child_owner)  # child cohort activity
    # add adult guardians (role=GUARDIAN) — these must NOT count toward the child k-anon floor
    for i in range(3):
        g = _adult(f"grd{i}")
        Membership.objects.create(
            activity=activity, user=g, role=Membership.Role.GUARDIAN, state=Membership.State.MEMBER
        )
    communities.generate_communities()
    # only 1 distinct CHILD (the owner) < floor of 2 -> NOT published, despite 3 guardians present
    assert not Community.objects.filter(cohort="child", is_published=True).exists()
    # add a genuine 2nd child member -> now clears
    Membership.objects.create(
        activity=activity,
        user=_child("ko2"),
        role=Membership.Role.MEMBER,
        state=Membership.State.MEMBER,
    )
    communities.generate_communities()
    assert Community.objects.filter(cohort="child", is_published=True).exists()


# --- no count / no roster (structural) -----------------------------------------------------


def test_serializer_exposes_no_count_or_roster():
    _activity(_adult("nc1"))
    communities.generate_communities()
    comm = Community.objects.filter(is_published=True).first()
    data = CommunitySerializer(comm).data
    forbidden = {"member_n", "participant_n", "member_count", "members", "roster", "count", "n"}
    assert forbidden.isdisjoint(data.keys())
    # and no field value is a people-count integer masquerading as something else
    assert set(data.keys()) <= {"slug", "name", "tier", "area", "category", "activity_type"}


# --- the private-contact boundary ----------------------------------------------------------


def test_sharing_only_a_community_does_not_enable_connection():
    # Two adults who each organise a football activity in Cluj share the COMMUNITY but never
    # co-attended an ACTIVITY — so they must NOT be connectable (community != shared activity).
    a, b = _adult("cm_a"), _adult("cm_b")
    _activity(a)
    _activity(b)
    communities.generate_communities()
    assert connections.can_connect(a, b) is False
    assert connections.shares_activity(a, b) is False


# --- web surfaces --------------------------------------------------------------------------


def test_web_list_and_detail_render():
    owner = _adult("w1")
    act = _activity(owner)
    communities.generate_communities()
    comm = Community.objects.get(name=f"{CITY} Football", cohort="adult")
    page = _client(owner).get("/communities/").content.decode()
    assert f"{CITY} Football" in page
    detail = _client(owner).get(f"/communities/{comm.slug}/").content.decode()
    assert act.title in detail  # the activity card renders


def test_web_detail_404_for_other_cohort():
    _activity(_adult("w2"))
    communities.generate_communities()
    comm = Community.objects.get(name=f"{CITY} Football", cohort="adult")
    # a child cannot open the adult community's page
    assert _client(_child("w3")).get(f"/communities/{comm.slug}/").status_code == 404


def test_activity_detail_shows_community_links():
    owner = _adult("w4")
    act = _activity(owner)
    communities.generate_communities()
    page = _client(owner).get(f"/activities/{act.id}/").content.decode()
    assert "Part of:" in page
    assert f"{CITY} Football" in page


def test_api_activities_endpoint_exposes_no_member_count():
    # The community activities API must be count-free too (the web card already is), so a client
    # can't sum per-activity counts into the forbidden community-level aggregate.
    from rest_framework.test import APIClient

    owner = _adult("api1")
    _activity(owner)
    communities.generate_communities()
    comm = Community.objects.get(name=f"{CITY} Football", cohort="adult")
    client = APIClient()
    client.force_authenticate(owner)
    resp = client.get(f"/api/communities/communities/{comm.slug}/activities/")
    assert resp.status_code == 200
    assert resp.json()  # at least one activity
    for item in resp.json():
        assert "member_count" not in item
        assert "open_positions" not in item
        assert "participant_n" not in item and "member_n" not in item


def test_generator_excludes_hidden_and_cancelled():
    owner = _adult("hc1")
    act = _activity(owner)
    act.is_hidden = True
    act.save(update_fields=["is_hidden"])
    communities.generate_communities()
    # the only activity is hidden -> no community materializes off it
    assert not Community.objects.filter(is_published=True).exists()
