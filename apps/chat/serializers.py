# The chat app no longer serializes a ChatMessage model. The live WebSocket payload for a
# thread message is built directly from the committed social.Post in
# social.services.broadcast_post (it carries the live-derived reply snippet); the no-JS web
# surface server-renders Posts. Nothing to serialize here.
