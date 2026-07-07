"""F3 web CRUD: the save-only page, create, and owner-scoped delete."""

import pytest
from django.test import override_settings

from apps.saved_searches.models import SavedSearch

pytestmark = pytest.mark.django_db


def test_page_create_and_delete(client, adult, activity_type):
    client.force_login(adult)
    assert client.get("/saved-searches/").status_code == 200
    r = client.post(
        "/saved-searches/create/",
        {"activity_type": str(activity_type.id), "next": "/saved-searches/"},
    )
    assert r.status_code == 302
    s = SavedSearch.objects.get(user=adult)
    assert s.activity_type_id == activity_type.id and s.cohort == adult.cohort
    client.post(f"/saved-searches/{s.pk}/delete/", {"next": "/saved-searches/"})
    assert not SavedSearch.objects.filter(pk=s.pk).exists()


def test_web_create_threads_coarse_window(client, adult, activity_type):
    from apps.social.models import ActivityInterest

    client.force_login(adult)
    r = client.post(
        "/saved-searches/create/",
        {
            "activity_type": str(activity_type.id),
            "coarse_window": ActivityInterest.CoarseWindow.WEEKEND_DAYTIME.value,
            "next": "/saved-searches/",
        },
    )
    assert r.status_code == 302
    s = SavedSearch.objects.get(user=adult)
    assert s.coarse_window == ActivityInterest.CoarseWindow.WEEKEND_DAYTIME


@override_settings(SOCIAL_REACT_UI=True)
def test_spa_create_accepts_readable_activity_type_slug(client, adult, activity_type):
    client.force_login(adult)
    payload = client.get("/saved-searches/", {"_data": "1"}).json()
    assert activity_type.slug in {
        option["slug"] for option in payload["data"]["options"]["activityTypes"]
    }

    response = client.post(
        "/saved-searches/create/",
        {"activity_type": activity_type.slug, "next": "/saved-searches/"},
    )

    assert response.status_code == 302
    saved = SavedSearch.objects.get(user=adult)
    assert saved.activity_type == activity_type


@override_settings(SOCIAL_REACT_UI=True)
def test_spa_create_accepts_readable_category_slug(client, adult, category):
    client.force_login(adult)
    payload = client.get("/saved-searches/", {"_data": "1"}).json()
    assert category.slug in {option["slug"] for option in payload["data"]["options"]["categories"]}

    response = client.post(
        "/saved-searches/create/",
        {"category": category.slug, "next": "/saved-searches/"},
    )

    assert response.status_code == 302
    saved = SavedSearch.objects.get(user=adult)
    assert saved.category == category


def test_delete_is_owner_scoped_via_web(client, adult, adult2, activity_type):
    from apps.saved_searches import services as ss

    s = ss.create_saved_search(adult, activity_type=activity_type)
    client.force_login(adult2)
    resp = client.post(f"/saved-searches/{s.pk}/delete/", {"next": "/saved-searches/"})
    assert resp.status_code == 404  # owner-scoped get_object_or_404
    assert SavedSearch.objects.filter(pk=s.pk).exists()
