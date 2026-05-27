import pytest
from rest_framework.test import APIClient

pytestmark = pytest.mark.django_db


def client_for(user):
    client = APIClient()
    client.force_authenticate(user=user)
    return client


def test_post_and_get_history(thread, owner, member):
    url = f"/api/chat/threads/{thread.id}/messages/"
    posted = client_for(owner).post(url, {"body": "hello"}, format="json")
    assert posted.status_code == 201, posted.content

    history = client_for(member).get(url)
    assert history.status_code == 200
    assert [m["body"] for m in history.data] == ["hello"]


def test_outsider_forbidden(thread, outsider):
    url = f"/api/chat/threads/{thread.id}/messages/"
    assert client_for(outsider).get(url).status_code == 403
    assert client_for(outsider).post(url, {"body": "hi"}, format="json").status_code == 400


def test_empty_body_rejected(thread, owner):
    url = f"/api/chat/threads/{thread.id}/messages/"
    assert client_for(owner).post(url, {"body": ""}, format="json").status_code == 400
