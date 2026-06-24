from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("events", "0006_alter_event_source"),
    ]

    operations = [
        migrations.AddField(
            model_name="event",
            name="attribution",
            field=models.CharField(blank=True, max_length=255),
        ),
        migrations.AddField(
            model_name="event",
            name="license_name",
            field=models.CharField(blank=True, max_length=120),
        ),
        migrations.AddField(
            model_name="event",
            name="provenance_url",
            field=models.URLField(blank=True, max_length=500),
        ),
    ]
