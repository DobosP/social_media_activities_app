from django.conf import settings
from django.db import models


class Connection(models.Model):
    """A deliberate, mutual user-to-user link — the discovery layer in front of the existing
    E2EE messaging, NOT a global friend graph. It can only form between two people who have
    shared a REAL activity (co-membership) within the SAME age cohort. Modelled as a directed
    request that becomes the (symmetric) connection on ACCEPTED; ``are_connected`` reads it in
    either direction. No counts / reliability / attendance history are ever stored here — that
    would be the behavioural tracking the invariants forbid; eligibility is derived live."""

    class Status(models.TextChoices):
        PENDING = "pending", "Pending"
        ACCEPTED = "accepted", "Accepted"
        DECLINED = "declined", "Declined"
        WITHDRAWN = "withdrawn", "Withdrawn"
        REMOVED = "removed", "Removed"

    requester = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="connections_sent"
    )
    addressee = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="connections_received"
    )
    status = models.CharField(max_length=12, choices=Status.choices, default=Status.PENDING)
    created_at = models.DateTimeField(auto_now_add=True)
    decided_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(fields=["requester", "addressee"], name="uq_connection_pair"),
        ]
        indexes = [
            models.Index(fields=["addressee", "status"]),
            models.Index(fields=["requester", "status"]),
        ]

    def __str__(self):
        return f"connection({self.requester_id}->{self.addressee_id}, {self.status})"
