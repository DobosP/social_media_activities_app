"""F9: seed the staff-curated allowlist of public venue CLASSES safe-enough for children's
meetups. Conservative civic set (library, park, sports centre, school, community centre, plus a
few obvious public/civic types). Staff can edit/extend in Django admin without a deploy; a legit
venue that no class matches can be approved per-place via ApprovedChildVenue."""

from django.db import migrations

# key -> (label, osm_match, overture_categories)
# NOTE: osm_match values are exact OSM tags (high confidence — these are the canonical tags). The
# overture_categories are BEST-EFFORT guesses at Overture's `categories.primary` naming and are
# NOT yet verified against a real Overture extract — at launch we ingest OSM only. They exist so
# the resolver is source-complete; staff refine them in admin (and approve individual places via
# ApprovedChildVenue) once Overture data lands. Fail-closed: an unmatched category is "unknown",
# never wrongly "allowed".
SEED = [
    ("library", "Library", {"amenity": "library"}, ["library"]),
    ("park", "Park", {"leisure": "park"}, ["park", "national_park"]),
    (
        "sports_centre",
        "Sports centre",
        {"leisure": "sports_centre"},
        ["sports_centre", "stadium_arena", "recreation_center"],
    ),
    (
        "school",
        "School",
        {"amenity": "school"},
        ["school", "primary_school", "secondary_school"],
    ),
    (
        "community_centre",
        "Community centre",
        {"amenity": "community_centre"},
        ["community_center", "community_centre"],
    ),
    ("playground", "Playground", {"leisure": "playground"}, ["playground"]),
    ("nature_reserve", "Nature reserve", {"leisure": "nature_reserve"}, ["nature_reserve"]),
    ("college", "College / university", {"amenity": "college"}, ["college_university", "college"]),
]


def seed(apps, schema_editor):
    ChildVenueClass = apps.get_model("places", "ChildVenueClass")
    for key, label, osm_match, overture_categories in SEED:
        ChildVenueClass.objects.update_or_create(
            key=key,
            defaults={
                "label": label,
                "osm_match": osm_match,
                "overture_categories": overture_categories,
                "is_active": True,
            },
        )


def unseed(apps, schema_editor):
    ChildVenueClass = apps.get_model("places", "ChildVenueClass")
    ChildVenueClass.objects.filter(key__in=[row[0] for row in SEED]).delete()


class Migration(migrations.Migration):
    dependencies = [
        ("places", "0006_childvenueclass_approvedchildvenue"),
    ]

    operations = [
        migrations.RunPython(seed, unseed),
    ]
