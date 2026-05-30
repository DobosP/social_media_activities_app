"""Chat is now realtime-transport-only over the durable ``social.Post`` stream. The former
service functions (``send_message``, ``can_access_thread``, ``message_history``,
``purge_expired``) were removed in the "One Thread" unification:

- the write path is ``social.services.post_to_thread`` / ``post_to_thread_realtime`` (the one
  hardened gate shared by web, DRF, and the WebSocket consumer),
- the read/membership gate is ``social.services.can_read_thread``,
- there is no separate retention purge (thread Posts are permanent + audited).

The swappable content-moderation seam stays in ``apps/chat/policy.py`` and is invoked from
``post_to_thread``.
"""
