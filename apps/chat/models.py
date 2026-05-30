"""The chat app no longer owns a message store. Realtime delivery is a transport over the
durable ``social.Post`` stream (see apps/chat/consumers.py); the old ``ChatMessage`` model was
retired in the "One Thread" unification and its rows were backfilled into ``social.Post``.
The swappable content-moderation seam lives in ``apps/chat/policy.py`` and is invoked from the
single write path ``social.services.post_to_thread``."""
