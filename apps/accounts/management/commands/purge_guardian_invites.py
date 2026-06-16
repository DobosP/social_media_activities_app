"""W3-F16: guardian-link invitations must not linger as indefinitely-retained minor PII.

A GuardianLinkInvite carries the ward's identifier + an accept token, but nothing reads it once
its 7-day window (GUARDIAN_INVITE_TTL_DAYS) has passed — a PENDING one is dead (unaccepted), and an
ACCEPTED one is just the handshake record (the GuardianRelationship it created lives on separately).
Deleting every invite past its expires_at makes the data-retention disclosure ("deleted 7 days after
they're sent") literally TRUE and removes the dead PII. Runs from ops' run_due_jobs.
"""

from django.core.management.base import BaseCommand
from django.utils import timezone

from apps.accounts.models import GuardianLinkInvite


class Command(BaseCommand):
    help = "Delete guardian-link invitations whose 7-day window has passed (data minimisation)."

    def handle(self, *args, **opts):
        deleted, _ = GuardianLinkInvite.objects.filter(expires_at__lt=timezone.now()).delete()
        self.stdout.write(f"Purged {deleted} expired guardian invitation(s).")
