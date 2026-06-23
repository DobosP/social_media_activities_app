import django.utils.timezone
from django.db import migrations, models


class Migration(migrations.Migration):
    initial = True

    dependencies = []

    operations = [
        migrations.CreateModel(
            name="DeferredTask",
            fields=[
                (
                    "id",
                    models.BigAutoField(
                        auto_created=True,
                        primary_key=True,
                        serialize=False,
                        verbose_name="ID",
                    ),
                ),
                ("kind", models.CharField(db_index=True, max_length=64)),
                ("payload", models.JSONField(blank=True, default=dict)),
                (
                    "status",
                    models.CharField(
                        choices=[
                            ("PENDING", "Pending"),
                            ("DONE", "Done"),
                            ("FAILED", "Failed"),
                        ],
                        db_index=True,
                        default="PENDING",
                        max_length=16,
                    ),
                ),
                ("attempts", models.PositiveIntegerField(default=0)),
                ("max_attempts", models.PositiveSmallIntegerField(default=5)),
                (
                    "available_at",
                    models.DateTimeField(default=django.utils.timezone.now),
                ),
                ("dedup_key", models.CharField(blank=True, default="", max_length=200)),
                ("last_error", models.TextField(blank=True, default="")),
                ("created_at", models.DateTimeField(auto_now_add=True)),
                ("started_at", models.DateTimeField(blank=True, null=True)),
                ("finished_at", models.DateTimeField(blank=True, null=True)),
            ],
            options={
                "ordering": ("available_at", "id"),
            },
        ),
        migrations.AddIndex(
            model_name="deferredtask",
            index=models.Index(
                fields=["status", "available_at"], name="ops_task_claim_idx"
            ),
        ),
        migrations.AddConstraint(
            model_name="deferredtask",
            constraint=models.UniqueConstraint(
                condition=models.Q(("status", "PENDING")) & ~models.Q(("dedup_key", "")),
                fields=("kind", "dedup_key"),
                name="ops_task_unique_pending_dedup",
            ),
        ),
    ]
