"""Seed varied ADULT-cohort activities (+ topic preferences for `tester`) so the /activities/
List and Cards browse modes have rich, realistic content to demo. DEV ONLY.

Idempotent: re-running tops up what's missing (keyed on title / username / place name). It uses the
dev self-declaration identity path and real services (create_activity), so every seeded meetup is a
genuine ADULT-cohort activity at a public place — nothing here bypasses the cohort wall. The venues
are real Cluj-Napoca places (open-data style); the activity types come from the open taxonomy.
"""

from datetime import timedelta

from django.conf import settings
from django.contrib.gis.geos import Point
from django.core.management.base import BaseCommand, CommandError
from django.db import transaction
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.recommendations.services import set_topic_preferences
from apps.social.models import Activity
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityType

# (name, lon, lat) — real Cluj-Napoca venues, created as public OSM places.
_VENUES = [
    ("Parcul Central Simion Bărnuțiu", 23.5836, 46.7693),
    ("Sala Sporturilor Horia Demian", 23.5921, 46.7752),
    ("Biblioteca Centrală Universitară Lucian Blaga", 23.5944, 46.7676),
    ("Lacul Chios (Gheorgheni)", 23.6210, 46.7679),
    ("Pădurea Hoia", 23.5331, 46.7665),
    ("Parcul Sportiv Iuliu Hațieganu", 23.5848, 46.7625),
    ("Cluj Arena", 23.5722, 46.7686),
    ("Bazinul Olimpic Universitatea", 23.5836, 46.7641),
    ("Centrul Cultural Transilvania", 23.5969, 46.7708),
    ("Parcul Etnografic Romulus Vuia", 23.5256, 46.7820),
    ("Form Space Climbing Cluj", 23.6160, 46.7790),
    ("Muzeul de Artă Cluj-Napoca", 23.5896, 46.7693),
]

# (type_slug, title, description, days_from_now, hour, cost_band, difficulty, beginners, venue_idx)
_ACTIVITIES = [
    (
        "running",
        "Sunrise park run",
        "An easy morning loop around the park to start the day together.",
        2,
        7,
        "free",
        "easy",
        True,
        0,
    ),
    (
        "basketball",
        "Friendly 3v3 pickup",
        "Casual evening basketball games open to all skill levels.",
        3,
        18,
        "free",
        "moderate",
        True,
        1,
    ),
    (
        "chess",
        "Library chess club",
        "A relaxed afternoon of friendly chess matches and openings chat.",
        5,
        16,
        "free",
        "easy",
        True,
        2,
    ),
    (
        "swimming",
        "Sunrise lake swim",
        "An open-water morning swim for confident swimmers at the lake.",
        6,
        8,
        "low",
        "challenging",
        False,
        3,
    ),
    (
        "hiking",
        "Hoia forest ramble",
        "A gentle half-day walk through the forest trails and clearings.",
        7,
        9,
        "free",
        "moderate",
        True,
        4,
    ),
    (
        "tennis",
        "Evening doubles social",
        "Easygoing doubles rallies and friendly sets on the courts.",
        4,
        19,
        "low",
        "moderate",
        False,
        5,
    ),
    (
        "football",
        "5-a-side kickabout",
        "A relaxed pickup football session for fun and fitness.",
        2,
        20,
        "low",
        "moderate",
        True,
        6,
    ),
    (
        "yoga",
        "Park sunrise yoga",
        "A calm outdoor flow to stretch and breathe in the morning air.",
        5,
        7,
        "free",
        "easy",
        True,
        0,
    ),
    (
        "bouldering",
        "Beginner bouldering social",
        "Try low climbing walls with friendly tips and zero pressure.",
        8,
        18,
        "paid",
        "easy",
        True,
        10,
    ),
    (
        "climbing",
        "Top-rope climbing meetup",
        "Roped climbing for those with some belaying experience.",
        11,
        17,
        "paid",
        "challenging",
        False,
        10,
    ),
    (
        "board_games",
        "Board game night",
        "A cozy evening of modern board games and easy company.",
        3,
        19,
        "free",
        "easy",
        True,
        8,
    ),
    (
        "reading",
        "Quiet reading hour",
        "Bring a book and share a calm hour of silent reading together.",
        6,
        17,
        "free",
        "easy",
        True,
        2,
    ),
    (
        "book_club",
        "Monthly book club",
        "We discuss this month's pick over a friendly chat.",
        14,
        18,
        "free",
        "easy",
        False,
        2,
    ),
    (
        "cycling",
        "City loop ride",
        "An easy-paced group ride along the city's greener routes.",
        4,
        10,
        "free",
        "moderate",
        True,
        0,
    ),
    (
        "mountain_biking",
        "Hoia trail ride",
        "A spirited off-road ride on forest singletrack for confident riders.",
        9,
        9,
        "low",
        "challenging",
        False,
        4,
    ),
    (
        "trail_running",
        "Forest trail run",
        "A scenic run on soft forest paths at a sociable pace.",
        7,
        8,
        "free",
        "moderate",
        False,
        4,
    ),
    (
        "marathon",
        "Long-run training group",
        "A challenging long run for marathon hopefuls building distance.",
        13,
        7,
        "free",
        "challenging",
        False,
        6,
    ),
    (
        "volleyball",
        "Evening volleyball mix",
        "Casual rotating teams and plenty of friendly rallies.",
        5,
        19,
        "low",
        "moderate",
        True,
        6,
    ),
    (
        "handball",
        "Indoor handball pickup",
        "A lively indoor handball session for fun and movement.",
        8,
        18,
        "low",
        "moderate",
        False,
        1,
    ),
    (
        "badminton",
        "Badminton social",
        "Drop in for friendly singles and doubles in the hall.",
        6,
        20,
        "low",
        "easy",
        True,
        1,
    ),
    (
        "table_tennis",
        "Table tennis evening",
        "Round-robin ping pong for laughs and light competition.",
        3,
        18,
        "free",
        "easy",
        True,
        8,
    ),
    (
        "group_fitness",
        "Outdoor bootcamp",
        "A full-body group workout in the fresh air, all levels welcome.",
        4,
        8,
        "low",
        "moderate",
        True,
        0,
    ),
    (
        "pilates",
        "Mat pilates session",
        "A core-focused mat class to build strength gently.",
        10,
        9,
        "low",
        "easy",
        True,
        8,
    ),
    (
        "dance_social",
        "Folk dance social",
        "Learn simple steps and dance together, no partner needed.",
        12,
        19,
        "low",
        "easy",
        True,
        9,
    ),
    (
        "video_games",
        "Retro games meetup",
        "A friendly evening of classic couch multiplayer games.",
        5,
        18,
        "free",
        "easy",
        True,
        8,
    ),
    (
        "skating",
        "Lakeside skating roll",
        "An easy roller-skate cruise on the lakeside paths.",
        8,
        17,
        "free",
        "easy",
        True,
        3,
    ),
    (
        "orienteering",
        "Forest orienteering intro",
        "Learn to read a map and find checkpoints in the woods.",
        16,
        10,
        "low",
        "moderate",
        True,
        4,
    ),
    (
        "museum_visit",
        "Guided art museum tour",
        "A friendly group walk-through of the city's art collection.",
        9,
        11,
        "low",
        "easy",
        True,
        11,
    ),
    (
        "theatre_show",
        "Evening theatre outing",
        "Watch a local stage production together and chat after.",
        17,
        19,
        "paid",
        "easy",
        False,
        9,
    ),
    (
        "concert",
        "Live music evening",
        "Enjoy a relaxed live concert with a welcoming group.",
        15,
        20,
        "paid",
        "easy",
        False,
        9,
    ),
    (
        "workshop",
        "Beginner pottery workshop",
        "Get hands-on shaping clay in a friendly guided session.",
        12,
        17,
        "paid",
        "easy",
        True,
        9,
    ),
    (
        "festival",
        "Local culture festival walk",
        "Explore festival stalls and performances together.",
        18,
        12,
        "free",
        "easy",
        True,
        9,
    ),
    (
        "city_day",
        "City discovery walk",
        "A leisurely walking tour of landmarks and hidden corners.",
        11,
        10,
        "free",
        "easy",
        True,
        0,
    ),
    (
        "street_fair",
        "Weekend street fair stroll",
        "Wander the street fair's crafts and food with new friends.",
        13,
        11,
        "free",
        "easy",
        True,
        0,
    ),
    (
        "open_air_cinema",
        "Open-air film night",
        "Bring a blanket for an outdoor screening under the stars.",
        14,
        21,
        "low",
        "easy",
        True,
        0,
    ),
    (
        "community_event",
        "Park clean-up morning",
        "Join neighbours for a friendly hour tidying the green space.",
        6,
        9,
        "free",
        "easy",
        True,
        0,
    ),
    (
        "festival",
        "Heritage village open day",
        "Wander the open-air ethnographic village together.",
        19,
        11,
        "low",
        "easy",
        True,
        9,
    ),
    (
        "swimming",
        "Lane swim training",
        "A structured pool session for steady technique work.",
        10,
        19,
        "paid",
        "moderate",
        False,
        7,
    ),
    (
        "hiking",
        "Sunday meadow hike",
        "An unhurried walk over rolling meadows and gentle hills.",
        20,
        9,
        "free",
        "easy",
        True,
        4,
    ),
    (
        "tennis",
        "Beginner tennis basics",
        "Learn grips and rallies in a no-pressure intro session.",
        8,
        17,
        "low",
        "easy",
        True,
        5,
    ),
]


class Command(BaseCommand):
    help = "Seed varied ADULT activities + tester topic prefs to demo the browse modes (DEV ONLY)."

    @transaction.atomic
    def handle(self, *args, **options):
        if not settings.DEBUG:
            raise CommandError(
                "seed_browse_demo is for local development only (refusing because DEBUG=False)."
            )

        organizer = self._adult("demo_organizer", "Demo Organizer")
        places = [self._place(name, lon, lat) for name, lon, lat in _VENUES]

        created = 0
        now = timezone.now()
        for slug, title, desc, days, hour, cost, diff, beginners, venue_idx in _ACTIVITIES:
            atype = ActivityType.objects.filter(slug=slug, is_active=True).first()
            if atype is None:
                self.stdout.write(self.style.WARNING(f"  skip '{title}': type '{slug}' not seeded"))
                continue
            if Activity.objects.filter(title=title).exists():
                continue
            starts = (now + timedelta(days=days)).replace(
                hour=hour, minute=0, second=0, microsecond=0
            )
            create_activity(
                organizer,
                place=places[venue_idx],
                activity_type=atype,
                title=title,
                starts_at=starts,
                description=desc,
                cost_band=cost,
                difficulty=diff,
                beginners_welcome=beginners,
            )
            created += 1

        # Give `tester` a couple of chosen topics so the home feed shows honest "matches your chosen
        # topics" reasons (no-op if the account doesn't exist on this dev DB).
        tester = User.objects.filter(username="tester").first()
        if tester is not None:
            set_topic_preferences(tester, ["sport", "outdoor"])
            self.stdout.write("  set tester topics: sport, outdoor")

        self.stdout.write(
            self.style.SUCCESS(
                f"Seeded {created} new activit{'y' if created == 1 else 'ies'} "
                f"across {len(places)} venues. Browse at /activities/ (List / Cards)."
            )
        )

    def _adult(self, username: str, display: str) -> User:
        user = User.objects.filter(username=username).first()
        if user is None:
            user = User.objects.create_user(
                username=username, password="Testpass!123", display_name=display
            )
        if not user.is_identity_verified:
            apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
        return user

    def _place(self, name: str, lon: float, lat: float) -> Place:
        place = Place.objects.filter(name=name).first()
        if place is None:
            place = Place.objects.create(
                name=name,
                location=Point(lon, lat, srid=4326),
                source=Place.Source.OSM,  # OSM-source places are public (no proposal needed)
                address_city="Cluj-Napoca",
            )
        return place
