from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("events", "0008_roedu_event_lifecycle"),
    ]

    operations = [
        migrations.AddField(
            model_name="event",
            name="source_availability",
            field=models.CharField(blank=True, max_length=16),
        ),
        migrations.AddField(
            model_name="event",
            name="source_currency",
            field=models.CharField(blank=True, max_length=3),
        ),
        migrations.AddField(
            model_name="event",
            name="source_is_free",
            field=models.BooleanField(blank=True, null=True),
        ),
        migrations.AddField(
            model_name="event",
            name="source_price_max",
            field=models.DecimalField(blank=True, decimal_places=2, max_digits=12, null=True),
        ),
        migrations.AddField(
            model_name="event",
            name="source_price_min",
            field=models.DecimalField(blank=True, decimal_places=2, max_digits=12, null=True),
        ),
        migrations.AddField(
            model_name="event",
            name="source_recurrence",
            field=models.CharField(blank=True, max_length=1000),
        ),
        migrations.AddField(
            model_name="event",
            name="source_timezone",
            field=models.CharField(blank=True, max_length=64),
        ),
    ]
