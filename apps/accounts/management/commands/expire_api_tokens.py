"""W10 hardening (review W1-1): API tokens must not live forever. Deletes tokens older
than API_TOKEN_MAX_AGE_DAYS (default 90) — a mobile client simply re-authenticates,
while a token sitting on a forgotten/stolen device stops being a permanent credential.
Runs from ops' run_due_jobs."""

from datetime import timedelta

from django.conf import settings
from django.core.management.base import BaseCommand
from django.utils import timezone


class Command(BaseCommand):
    help = "Delete API auth tokens older than API_TOKEN_MAX_AGE_DAYS (forced re-login)."

    def handle(self, *args, **opts):
        from rest_framework.authtoken.models import Token

        max_age_days = getattr(settings, "API_TOKEN_MAX_AGE_DAYS", 90)
        cutoff = timezone.now() - timedelta(days=max_age_days)
        deleted, _ = Token.objects.filter(created__lt=cutoff).delete()
        self.stdout.write(f"Expired {deleted} API token(s) older than {max_age_days} days.")
