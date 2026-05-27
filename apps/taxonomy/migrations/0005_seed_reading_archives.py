from django.db import migrations

# Reading/archive venue types — places to read papers and old archives.
# slug, name, category_slug, aliases, wellness, family_friendly
ACTIVITY_TYPES = [
    (
        "archive",
        "Archive",
        "reading",
        ["archives", "records office", "national archives"],
        False,
        True,
    ),
    (
        "used_bookshop",
        "Antiquarian & Used Books",
        "reading",
        ["antiquarian", "second-hand books", "old books", "rare books", "used books"],
        False,
        True,
    ),
]

# source_slug, target_slug, kind — link the new types into the reading cluster.
RELATIONS = [
    ("archive", "reading", "related"),
    ("used_bookshop", "reading", "related"),
]


def seed(apps, schema_editor):
    Category = apps.get_model("taxonomy", "ActivityCategory")
    ActivityType = apps.get_model("taxonomy", "ActivityType")
    Relation = apps.get_model("taxonomy", "ActivityRelation")

    types = {}
    for slug, name, category_slug, aliases, wellness, family in ACTIVITY_TYPES:
        types[slug], _ = ActivityType.objects.get_or_create(
            slug=slug,
            defaults={
                "name": name,
                "category": Category.objects.get(slug=category_slug),
                "aliases": aliases,
                "wellness": wellness,
                "family_friendly": family,
            },
        )

    for source_slug, target_slug, kind in RELATIONS:
        Relation.objects.get_or_create(
            source=types[source_slug],
            target=ActivityType.objects.get(slug=target_slug),
            kind=kind,
            defaults={"symmetric": True},
        )


def unseed(apps, schema_editor):
    ActivityType = apps.get_model("taxonomy", "ActivityType")
    Relation = apps.get_model("taxonomy", "ActivityRelation")

    type_slugs = [row[0] for row in ACTIVITY_TYPES]
    Relation.objects.filter(source__slug__in=type_slugs).delete()
    ActivityType.objects.filter(slug__in=type_slugs).delete()


class Migration(migrations.Migration):
    dependencies = [("taxonomy", "0004_seed_activities_v2")]

    operations = [migrations.RunPython(seed, unseed)]
