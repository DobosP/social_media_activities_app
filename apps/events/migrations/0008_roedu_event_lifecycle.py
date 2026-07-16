from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("events", "0007_event_attribution_fields"),
    ]

    operations = [
        migrations.AddField(
            model_name="event",
            name="is_import_held",
            field=models.BooleanField(default=False),
        ),
        migrations.AddField(
            model_name="event",
            name="is_tombstone",
            field=models.BooleanField(default=False),
        ),
        migrations.AddField(
            model_name="event",
            name="lifecycle_status",
            field=models.CharField(
                choices=[
                    ("scheduled", "Scheduled"),
                    ("rescheduled", "Rescheduled"),
                    ("postponed", "Postponed"),
                    ("cancelled", "Cancelled"),
                    ("sold_out", "Sold out"),
                    ("moved_online", "Moved online"),
                    ("expired", "Expired"),
                    ("removed", "Removed upstream"),
                    ("unknown", "Unknown"),
                ],
                default="scheduled",
                max_length=24,
            ),
        ),
        migrations.AddField(
            model_name="event",
            name="source_category",
            field=models.CharField(blank=True, max_length=64),
        ),
        migrations.AddField(
            model_name="event",
            name="source_city",
            field=models.CharField(blank=True, max_length=128),
        ),
        migrations.AddField(
            model_name="event",
            name="source_confidence",
            field=models.FloatField(blank=True, null=True),
        ),
        migrations.AddField(
            model_name="event",
            name="source_first_seen_at",
            field=models.DateTimeField(blank=True, null=True),
        ),
        migrations.AddField(
            model_name="event",
            name="source_last_seen_at",
            field=models.DateTimeField(blank=True, null=True),
        ),
        migrations.AddField(
            model_name="event",
            name="source_pack_id",
            field=models.CharField(blank=True, max_length=255),
        ),
        migrations.AddField(
            model_name="event",
            name="source_release_id",
            field=models.CharField(blank=True, max_length=255),
        ),
        migrations.AddField(
            model_name="event",
            name="source_snapshot_generated_at",
            field=models.DateTimeField(blank=True, null=True),
        ),
        migrations.AddField(
            model_name="event",
            name="source_snapshot_id",
            field=models.CharField(blank=True, max_length=255),
        ),
        migrations.AddField(
            model_name="event",
            name="source_updated_at",
            field=models.DateTimeField(blank=True, null=True),
        ),
        migrations.AddField(
            model_name="event",
            name="source_venue_id",
            field=models.CharField(blank=True, max_length=200),
        ),
        migrations.AddIndex(
            model_name="event",
            index=models.Index(
                fields=["is_tombstone", "is_import_held", "lifecycle_status", "starts_at"],
                name="event_lifecycle_start_idx",
            ),
        ),
        migrations.AddIndex(
            model_name="event",
            index=models.Index(
                fields=["source", "source_pack_id", "source_city"],
                name="event_roedu_scope_idx",
            ),
        ),
        migrations.CreateModel(
            name="RoeduEventSyncState",
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
                ("pack_id", models.CharField(max_length=255)),
                ("city", models.CharField(max_length=128)),
                ("snapshot_id", models.CharField(max_length=255)),
                ("release_id", models.CharField(blank=True, max_length=255)),
                ("snapshot_generated_at", models.DateTimeField()),
                ("completed_at", models.DateTimeField(auto_now=True)),
            ],
            options={
                "constraints": [
                    models.UniqueConstraint(
                        fields=("pack_id", "city"),
                        name="uq_roedu_event_sync_scope",
                    )
                ],
            },
        ),
    ]
