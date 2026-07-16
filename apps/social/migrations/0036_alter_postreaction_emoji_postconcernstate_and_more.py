"""ADR-0029 schema: widen ``PostReaction.emoji`` to hold a facet slug and add the derived
sentiment models (``PostConcernState``, ``PostSentimentFooter``, ``PostConcern``, ``PostDissent``).

IRREVERSIBLE ON POPULATED DATA — this migration and its data-migration partner (0037) are a pair:
Django's auto-generated reverse of the ``emoji`` AlterField would narrow the column back to the
old ``varchar(8)``, which hard-fails once slug values (up to 32 chars, e.g. ``got_me_thinking``)
have been written. Do not attempt to reverse past this point on a populated database; roll forward
with a corrective migration instead."""

import django.db.models.deletion
from django.conf import settings
from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("social", "0035_remove_activity_fallback_meeting_point_and_more"),
        migrations.swappable_dependency(settings.AUTH_USER_MODEL),
    ]

    operations = [
        migrations.AlterField(
            model_name="postreaction",
            name="emoji",
            field=models.CharField(max_length=32),
        ),
        migrations.CreateModel(
            name="PostConcernState",
            fields=[
                (
                    "id",
                    models.BigAutoField(
                        auto_created=True, primary_key=True, serialize=False, verbose_name="ID"
                    ),
                ),
                ("note_sent_at", models.DateTimeField(blank=True, null=True)),
                ("note_barred", models.BooleanField(default=False)),
                (
                    "post",
                    models.OneToOneField(
                        on_delete=django.db.models.deletion.CASCADE,
                        related_name="concern_state",
                        to="social.post",
                    ),
                ),
            ],
        ),
        migrations.CreateModel(
            name="PostSentimentFooter",
            fields=[
                (
                    "id",
                    models.BigAutoField(
                        auto_created=True, primary_key=True, serialize=False, verbose_name="ID"
                    ),
                ),
                ("appreciation_slugs", models.JSONField(blank=True, default=list)),
                ("appreciation_permanent", models.JSONField(blank=True, default=list)),
                ("dissent_active", models.BooleanField(default=False)),
                ("dissent_consecutive_hits", models.PositiveSmallIntegerField(default=0)),
                ("dissent_consecutive_misses", models.PositiveSmallIntegerField(default=0)),
                ("dissent_window_key", models.CharField(blank=True, max_length=16)),
                ("computed_at", models.DateTimeField(auto_now=True)),
                (
                    "post",
                    models.OneToOneField(
                        on_delete=django.db.models.deletion.CASCADE,
                        related_name="sentiment_footer",
                        to="social.post",
                    ),
                ),
            ],
        ),
        migrations.CreateModel(
            name="PostConcern",
            fields=[
                (
                    "id",
                    models.BigAutoField(
                        auto_created=True, primary_key=True, serialize=False, verbose_name="ID"
                    ),
                ),
                ("created_at", models.DateTimeField(auto_now_add=True)),
                (
                    "post",
                    models.ForeignKey(
                        on_delete=django.db.models.deletion.CASCADE,
                        related_name="concerns",
                        to="social.post",
                    ),
                ),
                (
                    "user",
                    models.ForeignKey(
                        on_delete=django.db.models.deletion.CASCADE,
                        related_name="post_concerns",
                        to=settings.AUTH_USER_MODEL,
                    ),
                ),
            ],
            options={
                "indexes": [
                    models.Index(fields=["post"], name="social_post_post_id_44d657_idx"),
                    models.Index(fields=["created_at"], name="social_post_created_e10466_idx"),
                ],
                "constraints": [
                    models.UniqueConstraint(fields=("post", "user"), name="uq_post_concern")
                ],
            },
        ),
        migrations.CreateModel(
            name="PostDissent",
            fields=[
                (
                    "id",
                    models.BigAutoField(
                        auto_created=True, primary_key=True, serialize=False, verbose_name="ID"
                    ),
                ),
                ("created_at", models.DateTimeField(auto_now_add=True)),
                (
                    "post",
                    models.ForeignKey(
                        on_delete=django.db.models.deletion.CASCADE,
                        related_name="dissents",
                        to="social.post",
                    ),
                ),
                (
                    "user",
                    models.ForeignKey(
                        on_delete=django.db.models.deletion.CASCADE,
                        related_name="post_dissents",
                        to=settings.AUTH_USER_MODEL,
                    ),
                ),
            ],
            options={
                "indexes": [models.Index(fields=["post"], name="social_post_post_id_60e4d3_idx")],
                "constraints": [
                    models.UniqueConstraint(fields=("post", "user"), name="uq_post_dissent")
                ],
            },
        ),
    ]
