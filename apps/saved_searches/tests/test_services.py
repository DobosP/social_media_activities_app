"""F3 create/delete service: cohort pin, XOR, validation, cap, dedup, owner-scope, no coordinate."""

import pytest

from apps.accounts.models import User
from apps.saved_searches import services as ss
from apps.saved_searches.models import SavedSearch

pytestmark = pytest.mark.django_db


def test_create_pins_cohort_from_user(adult, activity_type):
    s = ss.create_saved_search(adult, activity_type=activity_type)
    assert s.cohort == adult.cohort and s.user_id == adult.id


def test_requires_exactly_one_of_type_or_category(adult, activity_type, category):
    with pytest.raises(ss.InvalidState):
        ss.create_saved_search(adult)  # neither
    with pytest.raises(ss.InvalidState):
        ss.create_saved_search(adult, activity_type=activity_type, category=category)  # both


def test_rejects_inactive_activity_type(adult, activity_type):
    activity_type.is_active = False
    activity_type.save(update_fields=["is_active"])
    with pytest.raises(ss.InvalidState):
        ss.create_saved_search(adult, activity_type=activity_type)


def test_rejects_exact_duplicate(adult, activity_type):
    ss.create_saved_search(adult, activity_type=activity_type)
    with pytest.raises(ss.InvalidState):
        ss.create_saved_search(adult, activity_type=activity_type)


def test_hard_cap_per_user(adult, activity_type, category, settings):
    settings.SAVED_SEARCH_MAX_PER_USER = 1
    ss.create_saved_search(adult, activity_type=activity_type)
    with pytest.raises(ss.InvalidState):
        ss.create_saved_search(adult, category=category)


def test_unassigned_or_unverified_cannot_save(activity_type):
    u = User.objects.create_user(username="ss_unassigned", password="pw", display_name="x")
    with pytest.raises(ss.NotEligible):
        ss.create_saved_search(u, activity_type=activity_type)


def test_delete_is_owner_scoped(adult, adult2, activity_type):
    s = ss.create_saved_search(adult, activity_type=activity_type)
    with pytest.raises(ss.NotEligible):
        ss.delete_saved_search(adult2, s)
    ss.delete_saved_search(adult, s)
    assert not SavedSearch.objects.filter(pk=s.pk).exists()


def test_model_has_no_coordinate_field():
    names = {f.name for f in SavedSearch._meta.get_fields()}
    assert not (
        names & {"lat", "lon", "latitude", "longitude", "location", "coordinate", "point", "geom"}
    )
