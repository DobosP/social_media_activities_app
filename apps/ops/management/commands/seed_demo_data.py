"""Seed realistic DEMO data for local testing (idempotent).

Creates a superuser, verified demo users across cohorts, Cluj-Napoca places, upcoming
activities with members + join-vote requests + thread posts, events, and declared interests
so the discovery feed / map / recommendations / join-by-vote / chat all have content.

    python manage.py seed_demo_data

NOT for production — uses the dev self-declaration identity path and weak demo passwords.
"""

from datetime import timedelta

from django.contrib.gis.geos import Point
from django.core.management.base import BaseCommand
from django.db import transaction
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import (
    apply_assurance,
    grant_parental_consent,
    link_guardian,
)
from apps.events.models import Event
from apps.places.models import Place
from apps.recommendations.services import set_interests
from apps.social.models import Activity, Membership
from apps.social.services import create_activity, post_to_thread, request_to_join
from apps.taxonomy.models import ActivityType

DEMO_PW = "demo12345"

# (username, age_band, display_name)
ADULTS = [
    ("alex", "Alex Pop"),
    ("maria", "Maria Ionescu"),
    ("dan", "Dan Munteanu"),
    ("elena", "Elena Voicu"),
    ("george", "George Marin"),
    ("ana", "Ana Dumitru"),
]

# (name, lon, lat)
PLACES = [
    ("Cluj Arena", 23.5720, 46.7667),
    ("Central Park Cluj", 23.5760, 46.7700),
    ("Sala Polivalentă", 23.5707, 46.7654),
    ("Parcul Iuliu Hațieganu (Sport Park)", 23.5660, 46.7610),
    ("Bookcorner Coffee & Books", 23.5896, 46.7693),
    ("Klausen Burger Climbing Gym", 23.6210, 46.7720),
    ("Wonderland Resort", 23.6450, 46.7900),
    ("Băile Someșeni", 23.6300, 46.7780),
    ("Tetarom Boardgames Café", 23.6010, 46.7740),
    ("Yoga Space Cluj", 23.5880, 46.7710),
    ("Stadionul Cluj Arena Annex", 23.5730, 46.7660),
    ("Pădurea Făget (Trailhead)", 23.6100, 46.7300),
    ("Insula Cetățuia", 23.5870, 46.7790),
    ("Form Space Fitness", 23.6050, 46.7705),
    ("Casa de Cultură a Studenților", 23.5905, 46.7688),
    ("Lacul Gheorgheni", 23.6240, 46.7760),
]

# (title, owner_idx, type_slug, days_ahead, capacity, members_idx, requesters_idx)
ACTIVITIES = [
    ("Saturday 3v3 basketball", 0, "basketball", 3, 12, [1, 2], [3]),
    ("Morning trail run in Făget", 1, "trail_running", 2, None, [0, 4], []),
    ("Board games night", 2, "book_club", 5, 8, [3, 5], [0]),
    ("Beginner bouldering meetup", 3, "bouldering", 6, 10, [4], [1, 2]),
    ("Sunday yoga in the park", 4, "yoga", 4, 20, [0, 1, 5], []),
    ("5-a-side football", 5, "football", 7, 10, [2, 3], [4]),
    ("Book club: contemporary fiction", 1, "book_club", 8, 12, [0, 4], []),
    ("Lakeside cycling tour", 0, "cycling", 9, 15, [2, 5], [1]),
]

INTERESTS = {
    "alex": ["basketball", "cycling", "trail_running"],
    "maria": ["trail_running", "yoga", "book_club"],
    "dan": ["book_club", "football", "basketball"],
    "elena": ["yoga", "bouldering", "cycling"],
    "george": ["football", "bouldering"],
    "ana": ["book_club", "yoga", "cycling"],
}


class Command(BaseCommand):
    help = "Seed idempotent demo data for local testing."

    @transaction.atomic
    def handle(self, *args, **opts):
        out = self.stdout
        # --- superuser ---
        admin, created = User.objects.get_or_create(
            username="admin",
            defaults={
                "display_name": "Admin",
                "age_band": AgeBand.ADULT,
                "is_staff": True,
                "is_superuser": True,
                "role": "admin",
            },
        )
        if created:
            admin.set_password("admin12345")
            admin.save()
            apply_assurance(admin, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))

        # --- verified adult users ---
        users = {}
        for username, display in ADULTS:
            u, made = User.objects.get_or_create(
                username=username, defaults={"display_name": display}
            )
            if made:
                u.set_password(DEMO_PW)
                u.save()
                apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
            users[username] = u
        adults = [users[name] for name, _ in ADULTS]

        # --- a teen (16-17 — participates without parental consent) ---
        tina, made = User.objects.get_or_create(
            username="tina", defaults={"display_name": "Tina (16)"}
        )
        if made:
            tina.set_password(DEMO_PW)
            tina.save()
            apply_assurance(tina, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))

        # --- a child + guardian + consent (demonstrates the under-16 flow; dev only) ---
        kevin, made = User.objects.get_or_create(
            username="kevin", defaults={"display_name": "Kevin (12)"}
        )
        if made:
            kevin.set_password(DEMO_PW)
            kevin.save()
            apply_assurance(kevin, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
            link_guardian(adults[0], kevin)  # alex is Kevin's guardian
            grant_parental_consent(adults[0], kevin)

        # --- places ---
        for name, lon, lat in PLACES:
            Place.objects.get_or_create(
                name=name,
                defaults={
                    "location": Point(lon, lat, srid=4326),
                    "source": Place.Source.USER,
                    "address_city": "Cluj-Napoca",
                    "address_country": "RO",
                    "website": "https://visitclujnapoca.ro",
                },
            )
        place_list = list(Place.objects.order_by("id"))

        # --- interests (drives recommendations) ---
        for username, slugs in INTERESTS.items():
            set_interests(users[username], slugs)

        # --- activities with members, pending join-votes, and thread posts ---
        now = timezone.now()
        for i, (title, owner_idx, slug, days, cap, members, requesters) in enumerate(ACTIVITIES):
            if Activity.objects.filter(title=title).exists():
                continue
            atype = ActivityType.objects.filter(slug=slug).first()
            if atype is None:
                continue
            owner = adults[owner_idx]
            place = place_list[i % len(place_list)]
            activity = create_activity(
                owner,
                place=place,
                activity_type=atype,
                title=title,
                starts_at=now + timedelta(days=days, hours=10),
                ends_at=now + timedelta(days=days, hours=12),
                description=f"Join us at {place.name}. All levels welcome!",
                capacity=cap,
            )
            # Admitted members (direct, same cohort).
            for m_idx in members:
                Membership.objects.get_or_create(
                    activity=activity,
                    user=adults[m_idx],
                    defaults={
                        "role": Membership.Role.MEMBER,
                        "state": Membership.State.MEMBER,
                        "decided_at": now,
                    },
                )
            # Pending join requests (so the join-by-vote UI has something to vote on).
            for r_idx in requesters:
                requester = adults[r_idx]
                if not activity.memberships.filter(user=requester).exists():
                    request_to_join(requester, activity)
            # A couple of thread posts from the owner + a member.
            post_to_thread(
                owner, activity, f"Welcome everyone! Meeting at the {place.name} entrance."
            )
            if members:
                post_to_thread(adults[members[0]], activity, "Sounds great, I'll be there 🙌")

        # --- a few events tied to places ---
        events = [
            ("Cluj Days street festival", "festival", 6),
            ("Open-air cinema night", "open_air_cinema", 10),
            ("Untold-warmup concert", "concert", 14),
            ("Museum late-night tour", "museum_visit", 4),
        ]
        for j, (etitle, eslug, days) in enumerate(events):
            etype = ActivityType.objects.filter(slug=eslug).first()
            Event.objects.get_or_create(
                title=etitle,
                starts_at=now + timedelta(days=days, hours=19),
                defaults={
                    "place": place_list[j % len(place_list)],
                    "activity_type": etype,
                    "description": f"{etitle} in Cluj-Napoca.",
                    "url": "https://visitclujnapoca.ro/events",
                    "source": Event.Source.MANUAL,
                },
            )

        out.write(
            self.style.SUCCESS(
                f"Seeded: {User.objects.count()} users, {Place.objects.count()} places, "
                f"{Activity.objects.count()} activities, {Event.objects.count()} events."
            )
        )
        out.write("Login (web at http://localhost:8000/):")
        out.write("  admin / admin12345   (superuser → /admin/)")
        out.write("  alex,maria,dan,elena,george,ana / demo12345   (verified adults)")
        out.write("  tina / demo12345   (teen 16-17)")
        out.write("  kevin / demo12345   (under-16, guardian=alex, consented)")
