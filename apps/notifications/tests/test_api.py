import pytest
from rest_framework.test import APIClient

from apps.notifications.models import NotificationType
from apps.notifications.services import notify


@pytest.fixture
def client(user):
    c = APIClient()
    c.force_authenticate(user)
    return c


@pytest.mark.django_db
def test_inbox_list_and_unread_count(client, user):
    notify(user, NotificationType.SYSTEM, title="hi")
    resp = client.get("/api/notifications/")
    assert resp.status_code == 200
    assert resp.json()["count"] == 1
    assert client.get("/api/notifications/unread_count/").json()["unread"] == 1


@pytest.mark.django_db
def test_mark_read_endpoint(client, user):
    notify(user, NotificationType.SYSTEM, title="hi")
    resp = client.post("/api/notifications/mark_read/", {}, format="json")
    assert resp.status_code == 200
    assert resp.json()["marked"] == 1
    assert client.get("/api/notifications/unread_count/").json()["unread"] == 0


@pytest.mark.django_db
def test_preferences_get_and_update(client):
    assert client.get("/api/notifications/preferences/").json()["event_reminders"] is True
    resp = client.put("/api/notifications/preferences/", {"event_reminders": False}, format="json")
    assert resp.status_code == 200
    assert resp.json()["event_reminders"] is False


@pytest.mark.django_db
def test_only_own_notifications_visible(client, owner, user):
    notify(owner, NotificationType.SYSTEM, title="owner-only")
    notify(user, NotificationType.SYSTEM, title="mine")
    titles = [n["title"] for n in client.get("/api/notifications/").json()["results"]]
    assert titles == ["mine"]


@pytest.mark.django_db
def test_requires_auth():
    assert APIClient().get("/api/notifications/").status_code in (401, 403)
