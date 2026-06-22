"""Seed varied ADULT-cohort activities (+ topic preferences for `tester`) so the /activities/
List and Cards browse modes have rich, realistic content to demo. DEV ONLY.

Idempotent: re-running tops up what's missing (keyed on title / username / place name). It uses the
dev self-declaration identity path and real services (create_activity), so every seeded meetup is a
genuine ADULT-cohort activity at a public place — nothing here bypasses the cohort wall.
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

# (name, lon, lat) — real-ish Cluj-Napoca venues, created as public OSM places.
_VENUES = [
    ("Parcul Central Simion Bărnuțiu", 23.5836, 46.7693),
    ("Sala Sporturilor Horia Demian", 23.5887, 46.7720),
    ("Biblioteca Județeană Octavian Goga", 23.5901, 46.7712),
    ("Grădina Botanică Alexandru Borza", 23.5884, 46.7625),
    ("Baza Sportivă La Terenuri (Mănăștur)", 23.5610, 46.7585),
    ("Cetățuia Park viewpoint", 23.5790, 46.7760),
]

# (type_slug, title, description, days_from_now, hour, cost_band, difficulty, beginners, venue_idx)
_ACTIVITIES = [
    (
        "basketball",
        "Sunday pickup basketball",
        "Casual 3v3 and 5v5 — rotate in, all welcome. Bring water.",
        2,
        10,
        "free",
        "easy",
        True,
        1,
    ),
    (
        "football",
        "Evening 7-a-side football",
        "Friendly small-sided game on the all-weather pitch.",
        3,
        19,
        "low",
        "moderate",
        False,
        4,
    ),
    (
        "running",
        "Easy riverside 5k",
        "Relaxed conversational pace along the Someș. No one left behind.",
        1,
        8,
        "free",
        "easy",
        True,
        0,
    ),
    (
        "trail_running",
        "Hoia forest trail run",
        "10k of rolling singletrack. Sturdy shoes recommended.",
        5,
        9,
        "free",
        "challenging",
        False,
        5,
    ),
    (
        "hiking",
        "Cheile Turzii day hike",
        "Scenic gorge hike, ~12km. Carpool from the park.",
        6,
        8,
        "low",
        "moderate",
        True,
        0,
    ),
    (
        "cycling",
        "Saturday road ride to Feleac",
        "60km tempo ride with a coffee stop. Helmets required.",
        4,
        9,
        "free",
        "challenging",
        False,
        5,
    ),
    (
        "yoga",
        "Morning park yoga",
        "Gentle hatha flow on the grass. Mats provided if you ask.",
        1,
        7,
        "free",
        "easy",
        True,
        0,
    ),
    (
        "climbing",
        "Indoor bouldering social",
        "Climb, spot, and swap beta. Day passes available on site.",
        2,
        18,
        "paid",
        "moderate",
        True,
        1,
    ),
    (
        "chess",
        "Library chess club",
        "Casual rapid games and a friendly mini-tournament.",
        3,
        17,
        "free",
        "easy",
        True,
        2,
    ),
    (
        "board_games",
        "Board game café night",
        "Euro games and party games — teaching tables for newcomers.",
        2,
        19,
        "low",
        "easy",
        True,
        2,
    ),
    (
        "book_club",
        "Book club: a novel a month",
        "This month's pick discussion + next-read vote. Tea on us.",
        7,
        18,
        "free",
        "easy",
        True,
        2,
    ),
    (
        "table_tennis",
        "Table tennis round-robin",
        "Bats provided. We rotate so everyone gets games.",
        4,
        18,
        "free",
        "easy",
        True,
        1,
    ),
    (
        "volleyball",
        "Beach-style volleyball",
        "6s on sand. Mixed skill, we balance the teams.",
        5,
        17,
        "free",
        "moderate",
        False,
        4,
    ),
    (
        "swimming",
        "Lane swim meetup",
        "Shared lanes by pace. Bring a cap. Coffee after.",
        3,
        7,
        "paid",
        "moderate",
        False,
        1,
    ),
    (
        "group_fitness",
        "Outdoor circuit training",
        "Bodyweight circuits in the park, scalable for all levels.",
        2,
        18,
        "free",
        "moderate",
        True,
        0,
    ),
    (
        "museum_visit",
        "Art museum guided visit",
        "Walk the new exhibition together, then a chat over coffee.",
        6,
        11,
        "low",
        "easy",
        True,
        2,
    ),
    (
        "workshop",
        "Intro to orienteering workshop",
        "Learn to read a map & compass, then a short course.",
        8,
        10,
        "low",
        "easy",
        True,
        3,
    ),
    (
        "dance_social",
        "Social dance evening",
        "No partner needed — rotation-based, beginners hour first.",
        5,
        20,
        "low",
        "easy",
        True,
        1,
    ),
    (
        "cycling",
        "Beginner gravel spin",
        "Relaxed 25km on easy gravel. We stop and regroup often.",
        9,
        9,
        "free",
        "easy",
        True,
        5,
    ),
    (
        "hiking",
        "Sunset walk to the Cetățuia",
        "Short uphill stroll for the city skyline at golden hour.",
        1,
        19,
        "free",
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

        # Give `tester` a couple of chosen topics so the feed shows honest "matches your chosen
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
