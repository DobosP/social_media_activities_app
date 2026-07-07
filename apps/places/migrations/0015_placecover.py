import django.db.models.deletion
from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("places", "0014_place_attribution_fields"),
    ]

    operations = [
        migrations.CreateModel(
            name="PlaceCover",
            fields=[
                (
                    "id",
                    models.BigAutoField(
                        auto_created=True, primary_key=True, serialize=False, verbose_name="ID"
                    ),
                ),
                (
                    "source",
                    models.CharField(
                        choices=[
                            ("wikimedia", "Wikimedia Commons"),
                            ("business", "Business partner upload"),
                        ],
                        max_length=16,
                    ),
                ),
                ("storage_key", models.CharField(max_length=128)),
                ("content_type", models.CharField(max_length=64)),
                ("byte_size", models.PositiveIntegerField(default=0)),
                ("width", models.PositiveIntegerField(default=0)),
                ("height", models.PositiveIntegerField(default=0)),
                ("attribution", models.CharField(blank=True, max_length=255)),
                ("license_name", models.CharField(blank=True, max_length=120)),
                ("source_page_url", models.URLField(blank=True, max_length=500)),
                ("alt_text", models.CharField(blank=True, max_length=140)),
                ("created_at", models.DateTimeField(auto_now_add=True)),
                ("updated_at", models.DateTimeField(auto_now=True)),
                (
                    "place",
                    models.OneToOneField(
                        on_delete=django.db.models.deletion.CASCADE,
                        related_name="cover",
                        to="places.place",
                    ),
                ),
            ],
        ),
    ]
