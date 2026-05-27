from django.db import migrations

# slug, name, parent_slug — new top-level groupings for active/outdoor and culture.
CATEGORIES = [
    ("outdoor", "Outdoor & Endurance", None),
    ("fitness", "Fitness & Wellness", None),
    ("culture", "Culture & Community", None),
]

# slug, name, category_slug, aliases, wellness, family_friendly
ACTIVITY_TYPES = [
    # Outdoor & endurance — "going out", healthy, group.
    ("running", "Running", "outdoor", ["jogging", "run", "alergare"], True, False),
    ("marathon", "Marathon", "outdoor", ["half marathon", "race", "maraton"], True, False),
    ("trail_running", "Trail Running", "outdoor", ["trail"], True, False),
    ("hiking", "Hiking", "outdoor", ["trekking", "drumetie", "walking"], True, True),
    ("cycling", "Cycling", "outdoor", ["biking", "bike", "ciclism"], True, True),
    ("mountain_biking", "Mountain Biking", "outdoor", ["mtb"], True, False),
    ("orienteering", "Orienteering", "outdoor", ["orientare"], True, True),
    ("skating", "Skating", "outdoor", ["rollerblading", "inline", "role"], True, True),
    # Fitness & wellness.
    ("swimming", "Swimming", "fitness", ["inot", "swim"], True, True),
    ("climbing", "Climbing", "fitness", ["escalada", "rock climbing"], True, False),
    ("bouldering", "Bouldering", "fitness", [], True, False),
    ("yoga", "Yoga", "fitness", [], True, False),
    ("group_fitness", "Group Fitness", "fitness", ["aerobics", "zumba", "bootcamp"], True, False),
    ("pilates", "Pilates", "fitness", [], True, False),
    # More team / racquet sports.
    ("volleyball", "Volleyball", "team_sport", ["beach volleyball", "volei"], True, True),
    ("handball", "Handball", "team_sport", ["handbal"], True, False),
    ("badminton", "Badminton", "racquet_sport", [], True, True),
    # Culture & community — participatory, join-with-others.
    ("festival", "Festival", "culture", ["festival"], False, True),
    ("city_day", "City Days", "culture", ["zilele orasului", "city day"], False, True),
    ("street_fair", "Street Fair", "culture", ["targ", "fair", "market"], False, True),
    ("open_air_cinema", "Open-air Cinema", "culture", ["outdoor cinema", "cinema"], False, True),
    ("concert", "Concert", "culture", ["gig", "live music", "concert"], False, True),
    ("museum_visit", "Museum Visit", "culture", ["museum", "muzeu", "exhibition"], False, True),
    ("theatre_show", "Theatre", "culture", ["theatre", "teatru", "play"], False, True),
    ("workshop", "Workshop", "culture", ["atelier", "class"], False, True),
    ("dance_social", "Social Dancing", "culture", ["salsa", "bachata", "dance"], True, False),
    ("book_club", "Book Club", "culture", ["reading club"], False, True),
    ("community_event", "Community Event", "culture", ["meetup", "gathering"], False, True),
]

# Traits to backfill onto the originally-seeded types (active sports are healthy and,
# with a guardian, fine for children; tabletop/reading too).
EXISTING_TRAITS = {
    "basketball": (True, True),
    "football": (True, True),
    "table_tennis": (True, True),
    "tennis": (True, True),
    "board_games": (False, True),
    "chess": (False, True),
    "reading": (False, True),
    "video_games": (False, True),
}

# source_slug, target_slug, kind
RELATIONS = [
    ("marathon", "running", "variant"),
    ("trail_running", "running", "variant"),
    ("mountain_biking", "cycling", "variant"),
    ("bouldering", "climbing", "variant"),
    ("book_club", "reading", "related"),
    ("city_day", "festival", "related"),
]


def seed(apps, schema_editor):
    Category = apps.get_model("taxonomy", "ActivityCategory")
    ActivityType = apps.get_model("taxonomy", "ActivityType")
    Relation = apps.get_model("taxonomy", "ActivityRelation")

    categories = {c.slug: c for c in Category.objects.all()}
    for slug, name, _parent in CATEGORIES:
        if slug not in categories:
            categories[slug] = Category.objects.create(slug=slug, name=name)

    types = {}
    for slug, name, category_slug, aliases, wellness, family in ACTIVITY_TYPES:
        types[slug], _ = ActivityType.objects.get_or_create(
            slug=slug,
            defaults={
                "name": name,
                "category": categories[category_slug],
                "aliases": aliases,
                "wellness": wellness,
                "family_friendly": family,
            },
        )

    for slug, (wellness, family) in EXISTING_TRAITS.items():
        ActivityType.objects.filter(slug=slug).update(wellness=wellness, family_friendly=family)

    all_types = {t.slug: t for t in ActivityType.objects.all()}
    for source_slug, target_slug, kind in RELATIONS:
        if source_slug in all_types and target_slug in all_types:
            Relation.objects.get_or_create(
                source=all_types[source_slug],
                target=all_types[target_slug],
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
    Category.objects.filter(slug__in=category_slugs).update(parent=None)
    Category.objects.filter(slug__in=category_slugs).delete()


class Migration(migrations.Migration):
    dependencies = [
        ("taxonomy", "0003_activitytype_family_friendly_activitytype_wellness"),
    ]

    operations = [
        migrations.RunPython(seed, unseed),
    ]
