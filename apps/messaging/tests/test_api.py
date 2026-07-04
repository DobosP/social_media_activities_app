import pytest
from rest_framework.test import APIClient

from apps.messaging import services

from .conftest import PUBLIC_JWK, keys_for, make_user

pytestmark = pytest.mark.django_db


def client_for(user):
    client = APIClient()
    client.force_authenticate(user=user)
    return client


# --- key registry ---
def test_register_and_get_own_key(adult_a):
    c = client_for(adult_a)
    posted = c.post("/api/messaging/keys/", {"public_jwk": PUBLIC_JWK}, format="json")
    assert posted.status_code == 201, posted.content
    mine = c.get("/api/messaging/keys/")
    assert mine.status_code == 200
    assert mine.data["public_jwk"] == PUBLIC_JWK


def test_register_rejects_private_jwk(adult_a):
    resp = client_for(adult_a).post(
        "/api/messaging/keys/", {"public_jwk": {**PUBLIC_JWK, "d": "X"}}, format="json"
    )
    assert resp.status_code == 400


def test_fetch_other_users_key_same_cohort(adult_a, adult_b):
    services.register_public_key(adult_b, PUBLIC_JWK)
    resp = client_for(adult_a).get(f"/api/messaging/keys/{adult_b.username}/")
    assert resp.status_code == 200
    assert resp.data["user"]["username"] == adult_b.username
    # The backup blob is never exposed for another user.
    assert "wrapped_private_jwk" not in resp.data


def test_fetch_key_across_cohort_is_404(adult_a, child):
    services.register_public_key(child, PUBLIC_JWK)
    resp = client_for(adult_a).get(f"/api/messaging/keys/{child.username}/")
    assert resp.status_code == 404  # cohort isolation — not even existence leaks


# --- conversations ---
def test_create_direct_conversation_by_username(adult_a, adult_b):
    resp = client_for(adult_a).post(
        "/api/messaging/conversations/",
        {"kind": "direct", "username": adult_b.username},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    assert resp.data["kind"] == "direct"
    assert resp.data["my_state"] == "active"


def test_create_cross_cohort_conversation_rejected(adult_a, child):
    resp = client_for(adult_a).post(
        "/api/messaging/conversations/",
        {"kind": "direct", "username": child.username},
        format="json",
    )
    assert resp.status_code == 400


def test_create_group_conversation(adult_a, adult_b, adult_c):
    resp = client_for(adult_a).post(
        "/api/messaging/conversations/",
        {"kind": "group", "title": "Trail crew", "usernames": [adult_b.username, adult_c.username]},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    assert resp.data["title"] == "Trail crew"
    assert len(resp.data["participants"]) == 3


def test_unknown_recipient_is_400(adult_a):
    resp = client_for(adult_a).post(
        "/api/messaging/conversations/", {"username": "ghost"}, format="json"
    )
    assert resp.status_code == 400


def test_accept_invite_and_list(adult_a, adult_b):
    conv = services.start_direct(adult_a, adult_b)
    # b sees the pending invite in their list.
    listing = client_for(adult_b).get("/api/messaging/conversations/")
    assert listing.status_code == 200
    assert listing.data[0]["my_state"] == "invited"
    accepted = client_for(adult_b).post(f"/api/messaging/conversations/{conv.id}/accept/")
    assert accepted.status_code == 200
    assert accepted.data["my_state"] == "active"


# --- messages (ciphertext relay) ---
def _active_direct(adult_a, adult_b):
    conv = services.start_direct(adult_a, adult_b)
    services.accept_invite(adult_b, conv)
    return conv


def test_send_and_read_history(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    url = f"/api/messaging/conversations/{conv.id}/messages/"
    sent = client_for(adult_a).post(
        url,
        {"ciphertext": "Y2lwaGVy", "iv": "aXY=", "recipient_keys": keys_for(conv)},
        format="json",
    )
    assert sent.status_code == 201, sent.content
    history = client_for(adult_b).get(url)
    assert history.status_code == 200
    assert history.data[0]["ciphertext"] == "Y2lwaGVy"
    # Each reader only ever receives the key wrapped to them.
    assert history.data[0]["key"]["wrapped_key"]


def test_send_with_incomplete_keys_rejected(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    url = f"/api/messaging/conversations/{conv.id}/messages/"
    resp = client_for(adult_a).post(
        url,
        {"ciphertext": "x", "iv": "y", "recipient_keys": keys_for(conv, users=[adult_a])},
        format="json",
    )
    assert resp.status_code == 400


def test_outsider_cannot_read_or_send(adult_a, adult_b, adult_c):
    conv = _active_direct(adult_a, adult_b)
    url = f"/api/messaging/conversations/{conv.id}/messages/"
    assert client_for(adult_c).get(url).status_code == 403
    assert (
        client_for(adult_c)
        .post(url, {"ciphertext": "x", "iv": "y", "recipient_keys": []}, format="json")
        .status_code
        == 400
    )


def test_report_message_endpoint(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    msg = services.post_message(
        adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
    )
    resp = client_for(adult_b).post(
        f"/api/messaging/conversations/{conv.id}/messages/{msg.id}/report/",
        {"reason": "harassment", "decrypted_excerpt": "decoded text"},
        format="json",
    )
    assert resp.status_code == 201
    from apps.safety.models import Report

    assert Report.objects.filter(reason="harassment").exists()


def test_requires_authentication(adult_a, adult_b):
    conv = services.start_direct(adult_a, adult_b)
    assert APIClient().get(f"/api/messaging/conversations/{conv.id}/messages/").status_code in (
        401,
        403,
    )


# --- list bounding ---
def test_conversation_list_is_bounded(settings, adult_a):
    settings.MESSAGING_CONVERSATION_LIST_LIMIT = 3
    # Distinct same-cohort partners ⇒ distinct direct conversations (start_direct
    # reuses an existing 1:1, so each partner must be unique).
    for i in range(6):
        partner = make_user(f"partner_{i}")
        services.start_direct(adult_a, partner)
    resp = client_for(adult_a).get("/api/messaging/conversations/")
    assert resp.status_code == 200, resp.content
    assert len(resp.data) == 3


def test_v1_conversation_list_is_cursor_paginated(settings, adult_a):
    settings.MESSAGING_CONVERSATION_LIST_LIMIT = 4
    for i in range(6):
        partner = make_user(f"v1_partner_{i}")
        services.start_direct(adult_a, partner)
    resp = client_for(adult_a).get("/api/v1/messaging/conversations/", {"limit": 2})
    assert resp.status_code == 200, resp.content
    body = resp.json()
    assert body["limit"] == 2
    assert len(body["results"]) == 2
    assert body["next_cursor"]


def test_v1_conversation_list_query_count_is_constant(
    settings, adult_a, django_assert_max_num_queries
):
    settings.MESSAGING_CONVERSATION_LIST_LIMIT = 20
    for i in range(8):
        partner = make_user(f"v1_q_partner_{i}")
        services.start_direct(adult_a, partner)

    with django_assert_max_num_queries(5):
        resp = client_for(adult_a).get("/api/v1/messaging/conversations/", {"limit": 6})
    assert resp.status_code == 200, resp.content
    assert len(resp.json()["results"]) == 6


def test_message_history_is_bounded(settings, adult_a, adult_b):
    settings.MESSAGING_MESSAGE_PAGE_LIMIT = 5
    conv = _active_direct(adult_a, adult_b)
    keys = keys_for(conv)
    for i in range(9):
        services.post_message(
            adult_a, conv, ciphertext=f"Y2lwaGVy{i}", iv="aXY=", recipient_keys=keys
        )
    url = f"/api/messaging/conversations/{conv.id}/messages/"
    history = client_for(adult_b).get(url)
    assert history.status_code == 200, history.content
    # Capped to the newest-N, returned oldest-first.
    assert len(history.data) == 5
    assert history.data[-1]["ciphertext"] == "Y2lwaGVy8"


def test_v1_message_history_uses_older_cursor(settings, adult_a, adult_b):
    settings.MESSAGING_MESSAGE_PAGE_LIMIT = 5
    conv = _active_direct(adult_a, adult_b)
    keys = keys_for(conv)
    for i in range(9):
        services.post_message(
            adult_a, conv, ciphertext=f"Y2lwaGVy{i}", iv="aXY=", recipient_keys=keys
        )
    url = f"/api/v1/messaging/conversations/{conv.id}/messages/"
    first = client_for(adult_b).get(url, {"limit": 3})
    assert first.status_code == 200, first.content
    body = first.json()
    assert [row["ciphertext"] for row in body["results"]] == [
        "Y2lwaGVy6",
        "Y2lwaGVy7",
        "Y2lwaGVy8",
    ]
    older = client_for(adult_b).get(url, {"limit": 3, "cursor": body["next_cursor"]})
    assert [row["ciphertext"] for row in older.json()["results"]] == [
        "Y2lwaGVy3",
        "Y2lwaGVy4",
        "Y2lwaGVy5",
    ]

    # A caller-supplied ?limit may shrink the window but never exceed the hard cap.
    bounded = client_for(adult_b).get(url, {"limit": 50})
    assert bounded.data["limit"] == 5
    assert len(bounded.data["results"]) == 5
    smaller = client_for(adult_b).get(url, {"limit": 2})
    assert len(smaller.data["results"]) == 2


def test_v1_message_history_query_count_is_constant(
    settings, adult_a, adult_b, django_assert_max_num_queries
):
    settings.MESSAGING_MESSAGE_PAGE_LIMIT = 20
    conv = _active_direct(adult_a, adult_b)
    keys = keys_for(conv)
    for i in range(12):
        services.post_message(
            adult_a, conv, ciphertext=f"Y2lwaGVyq{i}", iv="aXY=", recipient_keys=keys
        )

    with django_assert_max_num_queries(6):
        resp = client_for(adult_b).get(
            f"/api/v1/messaging/conversations/{conv.id}/messages/",
            {"limit": 10},
        )
    assert resp.status_code == 200, resp.content
    assert len(resp.json()["results"]) == 10
