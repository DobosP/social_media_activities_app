"""Pluggable delivery channels.

The in-app (DB) channel is always on — it writes the inbox row. Additional channels
(email, web push) can be layered later via ``settings.NOTIFICATION_EXTRA_CHANNELS``;
they receive an already-persisted :class:`Notification` and deliver out-of-band.
Channels never expand the audience or content — they only deliver what the service
already decided to send (opt-in, no tracking)."""

from __future__ import annotations

import logging

from django.conf import settings
from django.utils.module_loading import import_string

logger = logging.getLogger(__name__)


class Channel:
    name = "base"

    def deliver(self, notification) -> None:  # pragma: no cover - interface
        raise NotImplementedError


class InAppChannel(Channel):
    """No-op delivery: the Notification row itself IS the in-app inbox entry."""

    name = "in_app"

    def deliver(self, notification) -> None:
        return None


class LoggingChannel(Channel):
    """Dev channel: logs that a notification would be delivered (no PII payload)."""

    name = "logging"

    def deliver(self, notification) -> None:
        logger.info("notify recipient=%s type=%s", notification.recipient_id, notification.ntype)


def extra_channels() -> list[Channel]:
    return [import_string(path)() for path in getattr(settings, "NOTIFICATION_EXTRA_CHANNELS", [])]
