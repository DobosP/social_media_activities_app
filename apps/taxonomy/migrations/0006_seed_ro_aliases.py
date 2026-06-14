"""W2-F1: add missing Romanian (RO) search aliases so launch-city vocabulary actually matches.

The seeds left a few common RO terms off — most notably 'fotbal' (football), which returns nothing
today even though search now reads aliases. Idempotent: each term is added only if absent, and the
migration never removes an alias a later edit added. Reverse is a no-op (aliases are additive).
"""

from django.db import migrations

# slug -> RO aliases to ensure are present (additive; existing aliases are kept).
RO_ALIASES = {
    "football": ["fotbal"],
    "basketball": ["baschet"],
    "volleyball": ["volei"],
    "tennis": ["tenis"],
    "swimming": ["inot", "înot"],
    "cycling": ["ciclism", "bicicleta", "bicicletă"],
    "chess": ["sah", "șah"],
}


def seed(apps, schema_editor):
    ActivityType = apps.get_model("taxonomy", "ActivityType")
    for slug, terms in RO_ALIASES.items():
        t = ActivityType.objects.filter(slug=slug).first()
        if t is None:
            continue  # type not seeded in this deployment — skip, never create
        current = list(t.aliases or [])
        lowered = {a.lower() for a in current if isinstance(a, str)}
        added = [term for term in terms if term.lower() not in lowered]
        if added:
            t.aliases = current + added
            t.save(update_fields=["aliases"])


class Migration(migrations.Migration):
    dependencies = [
        ("taxonomy", "0005_seed_reading_archives"),
    ]

    operations = [
        migrations.RunPython(seed, migrations.RunPython.noop),
    ]
