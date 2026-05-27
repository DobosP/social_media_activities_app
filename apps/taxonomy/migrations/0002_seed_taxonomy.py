from django.db import migrations

# slug, name, parent_slug
CATEGORIES = [
    ("sport", "Sport", None),
    ("team_sport", "Team Sport", "sport"),
    ("racquet_sport", "Racquet Sport", "sport"),
    ("tabletop", "Tabletop Games", None),
    ("reading", "Reading", None),
    ("video_games", "Video Games", None),
    ("social", "Social", None),
]

# slug, name, category_slug, aliases
ACTIVITY_TYPES = [
    ("basketball", "Basketball", "team_sport", ["streetball"]),
    ("football", "Football", "team_sport", ["soccer", "futsal"]),
    ("table_tennis", "Table Tennis", "racquet_sport", ["ping pong", "ping-pong", "whiff-whaff"]),
    ("tennis", "Tennis", "racquet_sport", []),
    ("board_games", "Board Games", "tabletop", ["boardgames", "tabletop"]),
    ("chess", "Chess", "tabletop", []),
    ("reading", "Reading", "reading", ["books", "library"]),
    ("video_games", "Video Games", "video_games", ["gaming", "esports", "arcade"]),
]

# source_slug, target_slug, kind
RELATIONS = [
    ("chess", "board_games", "related"),
]


def seed(apps, schema_editor):
    Category = apps.get_model("taxonomy", "ActivityCategory")
    ActivityType = apps.get_model("taxonomy", "ActivityType")
    Relation = apps.get_model("taxonomy", "ActivityRelation")

    categories = {}
    for slug, name, _parent in CATEGORIES:
        categories[slug], _ = Category.objects.get_or_create(slug=slug, defaults={"name": name})
    for slug, _name, parent_slug in CATEGORIES:
        if parent_slug:
            category = categories[slug]
            category.parent = categories[parent_slug]
            category.save(update_fields=["parent"])

    types = {}
    for slug, name, category_slug, aliases in ACTIVITY_TYPES:
        types[slug], _ = ActivityType.objects.get_or_create(
            slug=slug,
            defaults={"name": name, "category": categories[category_slug], "aliases": aliases},
        )

    for source_slug, target_slug, kind in RELATIONS:
        Relation.objects.get_or_create(
            source=types[source_slug],
            target=types[target_slug],
            kind=kind,
            defaults={"symmetric": True},
        )


def unseed(apps, schema_editor):
    Category = apps.get_model("taxonomy", "ActivityCategory")
    ActivityType = apps.get_model("taxonomy", "ActivityType")
    Relation = apps.get_model("taxonomy", "ActivityRelation")

    type_slugs = [row[0] for row in ACTIVITY_TYPES]
    category_slugs = [row[0] for row in CATEGORIES]

    Relation.objects.filter(source__slug__in=type_slugs).delete()
    ActivityType.objects.filter(slug__in=type_slugs).delete()
    # Clear self-references before deleting (parent uses PROTECT).
    Category.objects.filter(slug__in=category_slugs).update(parent=None)
    Category.objects.filter(slug__in=category_slugs).delete()


class Migration(migrations.Migration):
    dependencies = [
        ("taxonomy", "0001_initial"),
    ]

    operations = [
        migrations.RunPython(seed, unseed),
    ]
