from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [
        ("social", "0031_alter_activity_is_publicly_listed_and_more"),
    ]

    operations = [
        migrations.AddField(
            model_name="activity",
            name="cost_amount",
            field=models.DecimalField(blank=True, decimal_places=2, max_digits=7, null=True),
        ),
        migrations.AddField(
            model_name="activity",
            name="cost_note",
            field=models.CharField(blank=True, default="", max_length=120),
        ),
    ]
