"""Seed photo-heavy mobile-card demo data for local manual testing.

Usage:
    python manage.py migrate
    python manage.py seed_mobile_card_demo

Creates one adult owner, one adult viewer, several future public activities, and cover photos for
most of them. The command is idempotent: re-running refreshes start times and replaces demo covers.
"""

from datetime import timedelta
from io import BytesIO

from django.contrib.gis.geos import Point
from django.core.management.base import BaseCommand
from django.utils import timezone
from PIL import Image, ImageDraw

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media.services import upload_activity_cover
from apps.places.models import Place
from apps.social.models import Activity
from apps.social.services import create_activity, set_public_listing
from apps.taxonomy.models import ActivityCategory, ActivityType

DEMO_PASSWORD = "demo12345"
OWNER_USERNAME = "mobile_cards_owner"
VIEWER_USERNAME = "mobile_cards_viewer"

DEMO_CARDS = [
    {
        "title": "Sunset basketball pickup",
        "slug": "basketball",
        "type_name": "Basketball",
        "place": "Central Park Courts",
        "lon": 23.5760,
        "lat": 46.7700,
        "days": 2,
        "hour": 18,
        "description": "Fast half-court games, beginner friendly, bring water.",
        "alt": "Outdoor basketball court at sunset",
        "colors": ((255, 129, 80), (85, 63, 180)),
    },
    {
        "title": "Board games cafe night",
        "slug": "board-games",
        "type_name": "Board games",
        "place": "Tetarom Boardgames Cafe",
        "lon": 23.6010,
        "lat": 46.7740,
        "days": 3,
        "hour": 19,
        "description": "Low-pressure table for Catan, Dixit, chess, and first-timers.",
        "alt": "Colorful board game pieces on a cafe table",
        "colors": ((64, 184, 131), (251, 214, 92)),
    },
    {
        "title": "Morning trail run",
        "slug": "running",
        "type_name": "Running",
        "place": "Padurea Faget Trailhead",
        "lon": 23.6100,
        "lat": 46.7300,
        "days": 4,
        "hour": 8,
        "description": "Conversational pace trail run with regroup points.",
        "alt": "Forest trail with warm morning light",
        "colors": ((52, 130, 90), (174, 219, 132)),
    },
    {
        "title": "Beginner bouldering circle",
        "slug": "bouldering",
        "type_name": "Bouldering",
        "place": "Klausen Climbing Gym",
        "lon": 23.6210,
        "lat": 46.7720,
        "days": 5,
        "hour": 17,
        "description": "Safety intro first, then easy routes together.",
        "alt": "Indoor climbing wall with bright holds",
        "colors": ((238, 91, 119), (81, 120, 242)),
    },
    {
        "title": "Fallback visual chess meetup",
        "slug": "chess",
        "type_name": "Chess",
        "place": "Bookcorner Coffee and Books",
        "lon": 23.5896,
        "lat": 46.7693,
        "days": 6,
        "hour": 16,
        "description": (
            "This one intentionally has no uploaded cover, so you can test generated accent "
            "fallback."
        ),
        "alt": "",
        "colors": None,
    },
]


def _demo_user(username: str, display_name: str) -> User:
    user, created = User.objects.get_or_create(
        username=username,
        defaults={"display_name": display_name},
    )
    if created:
        user.set_password(DEMO_PASSWORD)
        user.save(update_fields=["password"])
    elif user.display_name != display_name:
        user.display_name = display_name
        user.save(update_fields=["display_name"])
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return user


def _activity_type(slug: str, name: str) -> ActivityType:
    category, _ = ActivityCategory.objects.get_or_create(
        slug="mobile-card-demo", defaults={"name": "Mobile card demo"}
    )
    atype, _ = ActivityType.objects.get_or_create(
        slug=slug,
        defaults={"name": name, "category": category, "is_active": True},
    )
    changed = False
    if atype.name != name:
        atype.name = name
        changed = True
    if atype.category_id != category.id:
        atype.category = category
        changed = True
    if not atype.is_active:
        atype.is_active = True
        changed = True
    if changed:
        atype.save(update_fields=["name", "category", "is_active", "updated_at"])
    return atype


def _place(card) -> Place:
    place, created = Place.objects.get_or_create(
        name=card["place"],
        defaults={
            "location": Point(card["lon"], card["lat"], srid=4326),
            "source": Place.Source.OSM,
            "address_city": "Cluj-Napoca",
        },
    )
    if created:
        return place
    place.location = Point(card["lon"], card["lat"], srid=4326)
    place.address_city = place.address_city or "Cluj-Napoca"
    place.save(update_fields=["location", "address_city"])
    return place


def _cover_png(title: str, colors) -> bytes:
    width, height = 1200, 760
    start, end = colors
    img = Image.new("RGB", (width, height), start)
    draw = ImageDraw.Draw(img)
    for y in range(height):
        t = y / max(1, height - 1)
        color = tuple(round(start[i] * (1 - t) + end[i] * t) for i in range(3))
        draw.line([(0, y), (width, y)], fill=color)
    # Big soft circles make the demo look like a photo-card surface while staying generated.
    draw.ellipse((-180, 80, 360, 620), fill=tuple(min(255, c + 35) for c in start))
    draw.ellipse((760, -120, 1320, 420), fill=tuple(max(0, c - 35) for c in end))
    draw.rounded_rectangle((80, 560, 1120, 700), radius=28, fill=(0, 0, 0))
    draw.text((120, 603), title[:54], fill=(255, 255, 255))
    out = BytesIO()
    img.save(out, format="PNG")
    return out.getvalue()


class Command(BaseCommand):
    help = "Seed mobile-card demo users, activities, and cover photos for manual testing."

    def handle(self, *args, **options):
        owner = _demo_user(OWNER_USERNAME, "Mobile Cards Owner")
        _demo_user(VIEWER_USERNAME, "Mobile Cards Viewer")
        created_ids = []
        now = timezone.now()

        for card in DEMO_CARDS:
            atype = _activity_type(card["slug"], card["type_name"])
            place = _place(card)
            starts_at = (now + timedelta(days=card["days"])).replace(
                hour=card["hour"], minute=0, second=0, microsecond=0
            )
            activity = Activity.objects.filter(owner=owner, title=card["title"]).first()
            if activity is None:
                activity = create_activity(
                    owner,
                    place=place,
                    activity_type=atype,
                    title=card["title"],
                    description=card["description"],
                    starts_at=starts_at,
                    capacity=12,
                    beginners_welcome=True,
                )
            else:
                activity.place = place
                activity.activity_type = atype
                activity.description = card["description"]
                activity.starts_at = starts_at
                activity.status = Activity.Status.OPEN
                activity.is_hidden = False
                activity.capacity = 12
                activity.beginners_welcome = True
                activity.save(
                    update_fields=[
                        "place",
                        "activity_type",
                        "description",
                        "starts_at",
                        "status",
                        "is_hidden",
                        "capacity",
                        "beginners_welcome",
                        "updated_at",
                    ]
                )
            set_public_listing(owner, activity, True)
            if card["colors"] is not None:
                upload_activity_cover(
                    owner,
                    activity,
                    _cover_png(card["title"], card["colors"]),
                    alt_text=card["alt"],
                )
            created_ids.append(activity.id)

        self.stdout.write(self.style.SUCCESS("Mobile card demo data ready."))
        self.stdout.write(f"Owner login:  {OWNER_USERNAME} / {DEMO_PASSWORD}")
        self.stdout.write(f"Viewer login: {VIEWER_USERNAME} / {DEMO_PASSWORD}")
        self.stdout.write("Open web deck: /activities/?view=cards")
        self.stdout.write("API deck:      /api/v1/discovery/activity-deck/?seed=demo&limit=6")
        self.stdout.write(
            "Cover upload:  PUT /api/v1/media/activity-covers/<activity_id>/ "
            "multipart file+alt_text"
        )
        self.stdout.write("Activity IDs:  " + ", ".join(str(pk) for pk in created_ids))
        self.stdout.write(
            "Note: the chess meetup intentionally has no cover; it tests generated fallback."
        )
