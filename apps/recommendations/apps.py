from django.apps import AppConfig


class RecommendationsConfig(AppConfig):
    default_auto_field = "django.db.models.BigAutoField"
    name = "apps.recommendations"
    label = "recommendations"

    def ready(self):
        from . import signals  # noqa: F401  (connect the Activity embedding signal)
