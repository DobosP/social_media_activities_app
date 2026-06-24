from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("places", "0013_alter_placecorrection_field"),
    ]

    operations = [
        migrations.AddField(
            model_name="place",
            name="attribution",
            field=models.CharField(blank=True, max_length=255),
        ),
        migrations.AddField(
            model_name="place",
            name="license_name",
            field=models.CharField(blank=True, max_length=120),
        ),
        migrations.AddField(
            model_name="place",
            name="provenance_url",
            field=models.URLField(blank=True, max_length=500),
        ),
        migrations.AlterField(
            model_name="place",
            name="source",
            field=models.CharField(
                choices=[
                    ("osm", "OpenStreetMap"),
                    ("overture", "Overture Maps"),
                    ("google", "Google"),
                    ("user", "User-submitted"),
                    ("roedu", "RO-EDU"),
                ],
                max_length=16,
            ),
        ),
    ]
