from django.apps import AppConfig


class OpsConfig(AppConfig):
    default_auto_field = "django.db.models.BigAutoField"
    name = "apps.ops"
    label = "ops"

    def ready(self):
        # Import the deferred-task handler module so its @register calls run at startup, before any
        # enqueue() can fire (enqueue fails fast on an unregistered kind). Import-only side effect.
        from . import handlers  # noqa: F401
