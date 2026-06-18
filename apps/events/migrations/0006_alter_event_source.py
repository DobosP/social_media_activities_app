from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("events", "0005_eventreport"),
    ]

    operations = [
        migrations.AlterField(
            model_name="event",
            name="source",
            field=models.CharField(
                choices=[
                    ("ical", "iCalendar feed"),
                    ("google", "Google"),
                    ("user", "User-submitted"),
                    ("manual", "Manual"),
                    ("roedu", "RO-EDU scraper"),
                ],
                default="manual",
                max_length=16,
            ),
        ),
    ]
