"""W3-F16: expired guardian-link invitations are deleted (so the data-retention disclosure
"deleted N days after they're sent" is literally true, and minor-PII doesn't linger)."""

from datetime import timedelta
from io import StringIO

import pytest
from django.core.management import call_command
from django.utils import timezone

from apps.accounts.models import GuardianLinkInvite, User

pytestmark = pytest.mark.django_db


def _u(name):
    return User.objects.create_user(username=name, password="pw", display_name=name)


def _invite(token, *, days_offset, status=GuardianLinkInvite.Status.PENDING):
    return GuardianLinkInvite.objects.create(
        guardian=_u(f"g_{token}"),
        ward=_u(f"w_{token}"),
        token=token,
        status=status,
        expires_at=timezone.now() + timedelta(days=days_offset),
    )


def test_purge_deletes_past_window_keeps_live():
    live = _invite("live", days_offset=3)  # still inside its 7-day window
    expired_pending = _invite("exp", days_offset=-1)  # window passed, never accepted
    accepted = _invite("acc", days_offset=-2, status=GuardianLinkInvite.Status.ACCEPTED)
    call_command("purge_guardian_invites", stdout=StringIO())
    assert GuardianLinkInvite.objects.filter(pk=live.pk).exists()  # live invite kept
    assert not GuardianLinkInvite.objects.filter(pk=expired_pending.pk).exists()
    assert not GuardianLinkInvite.objects.filter(pk=accepted.pk).exists()  # dead PII removed


def test_registered_in_due_jobs():
    from apps.ops.management.commands import run_due_jobs

    assert "purge_guardian_invites" in {name for name, _ in run_due_jobs.DUE_JOBS}
