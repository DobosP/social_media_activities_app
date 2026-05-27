import pytest
from django.core.files.uploadedfile import SimpleUploadedFile
from rest_framework.test import APIClient

from .conftest import make_png

pytestmark = pytest.mark.django_db


def client_for(user):
    client = APIClient()
    client.force_authenticate(user=user)
    return client


def _upload(color=(10, 20, 30)):
    return SimpleUploadedFile("p.png", make_png(color=color), content_type="image/png")


def test_profile_picture_upload_and_fetch(owner):
    posted = client_for(owner).post(
        "/api/media/profile-picture/", {"image": _upload()}, format="multipart"
    )
    assert posted.status_code == 201, posted.content
    assert posted.data["status"] == "approved"
    assert posted.data["url"]

    fetched = client_for(owner).get("/api/media/profile-picture/")
    assert fetched.status_code == 200
    assert fetched.data["public_id"] == posted.data["public_id"]


def test_thread_photo_member_only(thread, owner, member, outsider):
    url = f"/api/media/threads/{thread.id}/photos/"
    posted = client_for(owner).post(url, {"image": _upload()}, format="multipart")
    assert posted.status_code == 201, posted.content

    assert client_for(member).get(url).status_code == 200
    assert client_for(outsider).get(url).status_code == 403
    assert (
        client_for(outsider).post(url, {"image": _upload()}, format="multipart").status_code == 400
    )


def test_serve_view_requires_valid_signature_and_access(thread, owner, member, outsider):
    url = f"/api/media/threads/{thread.id}/photos/"
    posted = client_for(owner).post(url, {"image": _upload()}, format="multipart")
    serve_url = posted.data["url"]

    # Member with a valid signed URL can fetch the bytes.
    ok = client_for(member).get(serve_url)
    assert ok.status_code == 200

    # Outsider cannot, even with the same signed URL.
    assert client_for(outsider).get(serve_url).status_code == 404

    # Tampered token is rejected.
    assert client_for(member).get(serve_url + "tampered").status_code == 404
