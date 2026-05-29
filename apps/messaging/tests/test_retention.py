import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.messaging import services
from apps.messaging.models import Message

from .conftest import keys_for

pytestmark = pytest.mark.django_db


def client_for(user):
    client = APIClient()
    client.force_authenticate(user=user)
    return client


def _active_direct(a, b):
    conv = services.start_direct(a, b)
    services.accept_invite(b, conv)
    return conv


def _post(a, conv):
    return services.post_message(
        a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
    )


# --- set_disappearing ---
def test_set_disappearing_direct(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    services.set_disappearing(adult_b, conv, 3600)
    conv.refresh_from_db()
    assert conv.disappearing_seconds == 3600


def test_set_disappearing_rejects_unsupported_value(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    with pytest.raises(services.MessagingError):
        services.set_disappearing(adult_a, conv, 42)


def test_set_disappearing_group_admin_only(adult_a, adult_b):
    conv = services.start_group(adult_a, [adult_b])
    services.accept_invite(adult_b, conv)
    with pytest.raises(services.MessagingError):
        services.set_disappearing(adult_b, conv, 3600)  # b is a member, not admin
    services.set_disappearing(adult_a, conv, 3600)  # a is admin
    conv.refresh_from_db()
    assert conv.disappearing_seconds == 3600


def test_set_disappearing_requires_membership(adult_a, adult_b, adult_c):
    conv = _active_direct(adult_a, adult_b)
    with pytest.raises(services.MessagingError):
        services.set_disappearing(adult_c, conv, 3600)


# --- purge_expired_messages ---
def test_purge_respects_disappearing_timer(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    msg = _post(adult_a, conv)
    services.set_disappearing(adult_a, conv, 3600)
    # Nothing is old enough yet.
    assert services.purge_expired_messages() == 0
    assert Message.objects.filter(pk=msg.id).exists()
    # Pretend an hour passed.
    later = timezone.now() + timezone.timedelta(hours=2)
    assert services.purge_expired_messages(now=later) == 1
    assert not Message.objects.filter(pk=msg.id).exists()


def test_purge_leaves_untimed_conversations(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    _post(adult_a, conv)
    way_later = timezone.now() + timezone.timedelta(days=3650)
    # disappearing_seconds == 0 and no global retention -> nothing purged.
    assert services.purge_expired_messages(now=way_later) == 0
    assert Message.objects.filter(conversation=conv).count() == 1


def test_global_retention_backstop(adult_a, adult_b, settings):
    settings.MESSAGING_RETENTION_DAYS = 30
    conv = _active_direct(adult_a, adult_b)
    _post(adult_a, conv)
    assert services.purge_expired_messages() == 0
    later = timezone.now() + timezone.timedelta(days=31)
    assert services.purge_expired_messages(now=later) == 1


# --- API ---
def test_disappearing_endpoint(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    resp = client_for(adult_a).post(
        f"/api/messaging/conversations/{conv.id}/disappearing/", {"seconds": 86400}, format="json"
    )
    assert resp.status_code == 200
    assert resp.data["disappearing_seconds"] == 86400


def test_disappearing_endpoint_rejects_outsider(adult_a, adult_b, adult_c):
    conv = _active_direct(adult_a, adult_b)
    resp = client_for(adult_c).post(
        f"/api/messaging/conversations/{conv.id}/disappearing/", {"seconds": 86400}, format="json"
    )
    assert resp.status_code == 400
