from django.contrib.postgres.operations import AddIndexConcurrently
from django.db import migrations, models


class Migration(migrations.Migration):
    """Zero-downtime index add (P1 migration discipline): CREATE INDEX CONCURRENTLY cannot run
    inside a transaction, so this migration is atomic=False. It does not lock the Notification
    table against writes while the index builds — the pattern to use for any index on a
    high-growth table (Post / AuditLog / Notification)."""

    atomic = False

    dependencies = [
        ("notifications", "0016_alter_notification_kind"),
    ]

    operations = [
        AddIndexConcurrently(
            model_name="notification",
            index=models.Index(
                fields=["recipient", "-created_at"], name="notif_recipient_created_idx"
            ),
        ),
    ]
