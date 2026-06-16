"""W3-F10 — honest 'starter interests from what's actually nearby' onboarding.

suggest_starter_interests offers the activity TYPES that have real upcoming visible meetups in the
user's cohort, minus the ones already declared — deterministic, bounded, and COUNT-FREE (never an
'N nearby' supply number, the inv.2 vanity metric a discovery surface must not show).
"""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.recommendations import services
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _type(slug, name, cat):
    return ActivityType.objects.create(slug=slug, name=name, category=cat)


def _activity(owner, atype, *, days=2):
    place = Place.objects.create(
        name="P", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="A",
        starts_at=timezone.now() + timedelta(days=days),
    )


def test_suggests_types_with_upcoming_visible_meetups():
    owner, me = _user("f10_o"), _user("f10_me")
    cat = ActivityCategory.objects.create(slug="f10-sport", name="Sport")
    _activity(owner, _type("f10-bball", "Basketball", cat))
    _activity(owner, _type("f10-chess", "Chess", cat))
    _type("f10-tennis", "Tennis", cat)  # no activity of this type -> not suggested

    slugs = [t.slug for t in services.suggest_starter_interests(me)]
    assert "f10-bball" in slugs
    assert "f10-chess" in slugs
    assert "f10-tennis" not in slugs


def test_excludes_already_declared_interests():
    owner, me = _user("f10_o2"), _user("f10_me2")
    cat = ActivityCategory.objects.create(slug="f10-sport2", name="Sport")
    _activity(owner, _type("f10-bball2", "Basketball", cat))
    _activity(owner, _type("f10-chess2", "Chess", cat))
    services.set_interests(me, ["f10-bball2"])  # already declared

    slugs = [t.slug for t in services.suggest_starter_interests(me)]
    assert "f10-bball2" not in slugs  # already declared -> never re-suggested
    assert "f10-chess2" in slugs


def test_is_deterministic_alphabetical_and_bounded():
    owner, me = _user("f10_o3"), _user("f10_me3")
    cat = ActivityCategory.objects.create(slug="f10-sport3", name="Sport")
    for slug, name in [("f10-z", "Zumba"), ("f10-a", "Aikido"), ("f10-m", "Mahjong")]:
        _activity(owner, _type(slug, name, cat))
    names = [t.name for t in services.suggest_starter_interests(me)]
    assert names == sorted(names)  # alphabetical by name, deterministic
    assert len(services.suggest_starter_interests(me, limit=2)) == 2  # bounded


def test_respects_cohort_wall():
    adult_owner = _user("f10_ao")
    child = _user("f10_child", band=AgeBand.UNDER_16)
    cat = ActivityCategory.objects.create(slug="f10-sport4", name="Sport")
    _activity(adult_owner, _type("f10-bball4", "Basketball", cat))  # ADULT-cohort meetup
    # The child can't see the adult-cohort activity, so its type is never offered to them.
    assert services.suggest_starter_interests(child) == []


def test_excludes_inactive_types():
    owner, me = _user("f10_io"), _user("f10_ime")
    cat = ActivityCategory.objects.create(slug="f10-sport5", name="Sport")
    bball = _type("f10-bball5", "Basketball", cat)
    _activity(owner, bball)
    bball.is_active = False
    bball.save(update_fields=["is_active"])
    assert "f10-bball5" not in [t.slug for t in services.suggest_starter_interests(me)]


def test_excludes_cancelled_and_past_activity_types():
    from apps.social.models import Activity

    owner, me = _user("f10_x_o"), _user("f10_x_me")
    cat = ActivityCategory.objects.create(slug="f10-sport7", name="Sport")
    cancelled = _activity(owner, _type("f10-canc", "Canceltype", cat))
    cancelled.status = Activity.Status.CANCELLED
    cancelled.save(update_fields=["status"])
    past = _activity(owner, _type("f10-past", "Pasttype", cat))
    Activity.objects.filter(pk=past.pk).update(starts_at=timezone.now() - timedelta(hours=1))

    slugs = [t.slug for t in services.suggest_starter_interests(me)]
    assert "f10-canc" not in slugs  # not OPEN -> excluded
    assert "f10-past" not in slugs  # already started -> excluded


def test_empty_when_no_upcoming():
    assert services.suggest_starter_interests(_user("f10_empty")) == []


def test_home_shows_starter_form_only_for_zero_interest_user():
    from django.test import Client

    owner = _user("f10_h_owner")
    cat = ActivityCategory.objects.create(slug="f10-home", name="Sport")
    _activity(owner, _type("f10-home-bball", "Basketball", cat))
    cta = "New here? Pick what you'd come to"

    # A zero-interest newcomer gets the one-tap starter quick-pick — even though the soonest-first
    # `recommended` strip is non-empty (the gate is zero DECLARED interests, not empty recommended).
    newbie = _user("f10_newbie")
    c = Client()
    c.force_login(newbie)
    assert cta in c.get("/").content.decode()

    # A user who already declared an interest must NOT see it: the form posts only the ticked types
    # to set_interests (REPLACE), so rendering it would risk wiping their set.
    haspref = _user("f10_haspref")
    services.set_interests(haspref, ["f10-home-bball"])
    c2 = Client()
    c2.force_login(haspref)
    assert cta not in c2.get("/").content.decode()


def test_returns_plain_types_with_no_nearby_count():
    owner, me = _user("f10_co"), _user("f10_cme")
    cat = ActivityCategory.objects.create(slug="f10-sport6", name="Sport")
    _activity(owner, _type("f10-bball6", "Basketball", cat))
    t = services.suggest_starter_interests(me)[0]
    # inv.2: the suggestion carries no per-type supply count / annotation
    assert not hasattr(t, "nearby_count")
    assert not hasattr(t, "activity_count")
