# The HTTP chat fallback (ThreadMessagesView: history GET + message POST) was deleted in the
# "One Thread" unification. The DRF POST was a SECOND write surface that bypassed the hardened
# social.post_to_thread gate, so removing it is load-bearing for the single-write-path
# guarantee. Thread reads/writes now go through:
#   - the no-JS web surface (server-rendered social.Post stream), and
#   - the DRF activities `posts` action (apps/social/views.py), and
#   - the WebSocket consumer (apps/chat/consumers.py) for live delivery.
