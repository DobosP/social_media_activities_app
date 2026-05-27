import os

from django.core.asgi import get_asgi_application

os.environ.setdefault("DJANGO_SETTINGS_MODULE", "config.settings.prod")

# FUTURE: the real-time chat app (apps.chat) plugs in here via a
# ProtocolTypeRouter (HTTP -> this app, WebSocket -> chat consumers).
application = get_asgi_application()
