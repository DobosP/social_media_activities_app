"""Seed realistic DEMO data for local testing (idempotent).

Builds a populated Cluj-Napoca world ACROSS ALL THREE COHORTS so every surface has
content no matter who you log in as:

* a superuser + verified adults, teens, and consented under-16s (each with a guardian);
* declared interests for everyone (so the generated interest-constellation avatar shows
  for every account, and recommendations have signal in each cohort);
* Cluj-Napoca places (published) + iCal-style events + civic partners + booking links;
* upcoming activities per cohort with members, pending join-votes, RSVPs, an arrival ping,
  logistics + what-to-expect cards, announcements and thread posts;
* a recurring series, and PAST completed activities with "did we meet?" confirmations;
* donations + an active campaign + spend entries (transparency), saved-search alerts,
  accepted connections, and messaging conversation shells;
* materialized per-cohort communities (adult running / teen football / child board-games).

    python manage.py seed_demo_data

NOT for production — uses the dev self-declaration identity path and weak demo passwords.
"""

from datetime import timedelta

from django.contrib.gis.geos import Point
from django.core.management import call_command
from django.core.management.base import BaseCommand
from django.db import transaction
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance, grant_parental_consent, link_guardian
from apps.events.models import Event
from apps.places.models import Place
from apps.recommendations.services import set_interests
from apps.social.models import Activity, ActivitySeries, Membership
from apps.social.services import (
    complete_activity,
    create_activity,
    create_series,
    mark_arrived,
    post_announcement,
    post_to_thread,
    request_to_join,
    set_attendance_intent,
    set_met_confirmed,
    spawn_due_series,
)
from apps.taxonomy.models import ActivityType

DEMO_PW = "demo12345"

# (username, display_name)
ADULTS = [
    ("alex", "Alex Pop"),
    ("maria", "Maria Ionescu"),
    ("dan", "Dan Munteanu"),
    ("elena", "Elena Voicu"),
    ("george", "George Marin"),
    ("ana", "Ana Dumitru"),
    ("radu", "Radu Ilieș"),
    ("ioana", "Ioana Crețu"),
]
# (username, display_name) — 16-17, participate without parental consent
TEENS = [
    ("tina", "Tina (16)"),
    ("mihai", "Mihai (17)"),
    ("sofia", "Sofia (16)"),
    ("vlad", "Vlad (17)"),
    ("bianca", "Bianca (16)"),
    ("andrei", "Andrei (17)"),
]
# (username, display_name, guardian_username) — under-16, need an active guardian + consent
CHILDREN = [
    ("kevin", "Kevin (12)", "alex"),
    ("luca", "Luca (11)", "maria"),
    ("sara", "Sara (13)", "dan"),
    ("david", "David (12)", "elena"),
    ("ema", "Ema (10)", "george"),
    ("tudor", "Tudor (13)", "ana"),
]

INTERESTS = {
    "alex": ["basketball", "cycling", "running"],
    "maria": ["running", "yoga", "book_club"],
    "dan": ["book_club", "football", "basketball"],
    "elena": ["yoga", "bouldering", "cycling"],
    "george": ["football", "hiking", "running"],
    "ana": ["book_club", "yoga", "reading"],
    "radu": ["running", "cycling", "climbing"],
    "ioana": ["yoga", "running", "reading"],
    "tina": ["football", "climbing", "video_games"],
    "mihai": ["football", "basketball", "cycling"],
    "sofia": ["football", "dance_social", "reading"],
    "vlad": ["climbing", "football", "video_games"],
    "bianca": ["football", "yoga", "reading"],
    "andrei": ["cycling", "football", "video_games"],
    "kevin": ["board_games", "chess", "basketball"],
    "luca": ["board_games", "video_games", "cycling"],
    "sara": ["board_games", "reading", "swimming"],
    "david": ["reading", "board_games", "chess"],
    "ema": ["board_games", "swimming", "reading"],
    "tudor": ["chess", "board_games", "basketball"],
}

# (name, lon, lat) — Cluj-Napoca. ALL get a PUBLISHED proposal so they're public.
PLACES = [
    ("Cluj Arena", 23.5720, 46.7667),  # 0
    ("Central Park Cluj", 23.5760, 46.7700),  # 1
    ("Sala Polivalentă", 23.5707, 46.7654),  # 2
    ("Parcul Iuliu Hațieganu (Sport Park)", 23.5660, 46.7610),  # 3
    ("Bookcorner Coffee & Books", 23.5896, 46.7693),  # 4
    ("Klausen Burger Climbing Gym", 23.6210, 46.7720),  # 5
    ("Wonderland Resort", 23.6450, 46.7900),  # 6
    ("Băile Someșeni", 23.6300, 46.7780),  # 7
    ("Tetarom Boardgames Café", 23.6010, 46.7740),  # 8
    ("Yoga Space Cluj", 23.5880, 46.7710),  # 9
    ("Stadionul Cluj Arena Annex", 23.5730, 46.7660),  # 10
    ("Pădurea Făget (Trailhead)", 23.6100, 46.7300),  # 11
    ("Insula Cetățuia", 23.5870, 46.7790),  # 12
    ("Form Space Fitness", 23.6050, 46.7705),  # 13
    ("Casa de Cultură a Studenților", 23.5905, 46.7688),  # 14
    ("Lacul Gheorgheni", 23.6240, 46.7760),  # 15
]

# Each activity: owner + place index (into PLACES) + type slug + schedule + extras. `days` is an
# offset from now (negative = past); `soon=True` ⇒ starts in 1h (so its arrival window is open).
ADULT_ACTS = [
    # --- running cluster → materializes the "Cluj-Napoca Running" adult community ---
    dict(
        owner="alex",
        place=1,
        slug="running",
        title="Tuesday tempo run",
        days=2,
        hour=8,
        dur=1,
        members=["maria", "dan", "elena", "george", "ana"],
        rsvp_going=["maria", "dan"],
        announce="Meet at the park gate at 8 sharp — easy warm-up together first.",
        posts=[("maria", "In! See you there 🏃")],
    ),
    dict(
        owner="maria",
        place=15,
        slug="running",
        title="Riverside evening run",
        days=4,
        hour=18,
        dur=1,
        members=["radu", "ioana", "george"],
        requesters=["dan"],
    ),
    dict(
        owner="dan",
        place=3,
        slug="running",
        title="Sunday long run",
        days=5,
        hour=9,
        dur=2,
        members=["elena", "ana", "radu", "ioana", "alex"],
    ),
    # --- variety, enriched with logistics + what-to-expect + beginners ---
    dict(
        owner="elena",
        place=5,
        slug="bouldering",
        title="Beginner bouldering night",
        days=6,
        hour=19,
        dur=2,
        capacity=10,
        beginners=True,
        members=["alex", "ana"],
        requesters=["george"],
        enrich=dict(
            meeting_point="Reception desk",
            what_to_bring="Comfy clothes; shoes rentable",
            organizer_note="First-timers: arrive 15 min early for a safety briefing.",
            cost_band="low",
            difficulty="easy",
            accessibility_notes="Ground-floor gym, step-free entry.",
        ),
        posts=[("alex", "First time bouldering — excited!")],
    ),
    dict(
        owner="ana",
        place=9,
        slug="yoga",
        title="Sunday park yoga",
        days=3,
        hour=9,
        dur=1,
        capacity=20,
        beginners=True,
        members=["elena", "maria", "ioana"],
        enrich=dict(
            meeting_point="Big oak tree, north lawn",
            what_to_bring="Yoga mat & water",
            cost_band="free",
            difficulty="easy",
        ),
    ),
    # --- happening soon → an arrival ping demo (within the arrival window) ---
    dict(
        owner="alex",
        place=15,
        slug="cycling",
        title="Lunchtime lake loop",
        soon=True,
        dur=2,
        members=["maria", "dan"],
        rsvp_going=["maria", "dan"],
        arrivals=["maria"],
        announce="Rolling out in an hour — meet by the boathouse 🚲",
    ),
    # --- past + completed → post-meetup "did we meet?" confirmations ---
    dict(
        owner="george",
        place=11,
        slug="hiking",
        title="Făget forest hike",
        days=-6,
        hour=10,
        dur=3,
        members=["alex", "maria", "dan", "ana"],
        complete=True,
        met=["george", "alex", "maria", "dan"],
        posts=[
            ("george", "Thanks everyone, great turnout!"),
            ("alex", "Lovely trail, let's do it again 🌲"),
        ],
    ),
]
TEEN_ACTS = [
    # --- football cluster → "Cluj-Napoca Football" teen community ---
    dict(
        owner="tina",
        place=3,
        slug="football",
        title="Friday 5-a-side",
        days=3,
        hour=17,
        dur=2,
        capacity=12,
        members=["mihai", "sofia", "vlad", "bianca", "andrei"],
        rsvp_going=["mihai", "sofia"],
        announce="Bibs provided — wear trainers!",
        posts=[("mihai", "Count me in ⚽")],
    ),
    dict(
        owner="mihai",
        place=1,
        slug="football",
        title="Park kickabout",
        days=5,
        hour=16,
        dur=1,
        members=["tina", "sofia", "vlad", "andrei"],
    ),
    dict(
        owner="sofia",
        place=10,
        slug="football",
        title="Sunday football",
        days=6,
        hour=11,
        dur=2,
        members=["tina", "mihai", "vlad", "bianca", "andrei"],
    ),
    dict(
        owner="vlad",
        place=5,
        slug="climbing",
        title="Climbing intro for teens",
        days=4,
        hour=18,
        dur=2,
        capacity=8,
        beginners=True,
        members=["tina", "bianca"],
        requesters=["sofia", "mihai"],
        enrich=dict(
            meeting_point="Climbing gym lobby",
            what_to_bring="Water; gear provided",
            cost_band="low",
            difficulty="easy",
        ),
    ),
    dict(
        owner="andrei",
        place=15,
        slug="cycling",
        title="Weekend bike ride",
        days=-4,
        hour=10,
        dur=2,
        members=["tina", "mihai", "vlad"],
        complete=True,
        met=["andrei", "tina", "mihai"],
        posts=[("andrei", "Good ride, thanks!")],
    ),
]
CHILD_ACTS = [
    # --- board-games cluster → "Cluj-Napoca Board games" child community (guardian-accompanied) ---
    dict(
        owner="kevin",
        place=8,
        slug="board_games",
        title="Board games afternoon",
        days=3,
        hour=15,
        dur=2,
        guardian=True,
        members=["luca", "sara", "david", "ema", "tudor"],
        announce="Snacks provided. Parents welcome to stay.",
        posts=[("luca", "Bringing Catan! 🎲")],
    ),
    dict(
        owner="luca",
        place=8,
        slug="board_games",
        title="Catan club",
        days=5,
        hour=16,
        dur=2,
        guardian=True,
        members=["kevin", "sara", "david", "tudor"],
    ),
    dict(
        owner="sara",
        place=8,
        slug="board_games",
        title="Game day",
        days=6,
        hour=14,
        dur=2,
        guardian=True,
        members=["kevin", "luca", "david", "ema", "tudor"],
    ),
    dict(
        owner="david",
        place=4,
        slug="reading",
        title="Library reading hour",
        days=4,
        hour=16,
        dur=1,
        guardian=True,
        members=["sara", "ema"],
        enrich=dict(
            meeting_point="Children's section",
            what_to_bring="A favourite book",
            cost_band="free",
            difficulty="easy",
        ),
    ),
]

EVENTS = [
    ("Cluj Days street festival", "festival", 6, 1),
    ("Open-air cinema night", "open_air_cinema", 10, 12),
    ("Untold-warmup concert", "concert", 14, 0),
    ("Museum late-night tour", "museum_visit", 4, 14),
]


class Command(BaseCommand):
    help = "Seed idempotent demo data for local testing (all cohorts)."

    @transaction.atomic
    def handle(self, *args, **opts):
        out = self.stdout
        now = timezone.now()
        users = {}

        def mint(username, display, band, *, staff=False):
            u, made = User.objects.get_or_create(
                username=username,
                defaults={
                    "display_name": display,
                    "is_staff": staff,
                    "is_superuser": staff,
                    "role": "admin" if staff else "user",
                },
            )
            if made:
                u.set_password("admin12345" if staff else DEMO_PW)
                u.save()
                apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
            users[username] = u
            return u

        # --- accounts: superuser, adults, teens, consented children ---
        mint("admin", "Admin", AgeBand.ADULT, staff=True)
        for username, display in ADULTS:
            mint(username, display, AgeBand.ADULT)
        for username, display in TEENS:
            mint(username, display, AgeBand.AGE_16_17)
        for username, display, guardian in CHILDREN:
            child, made = User.objects.get_or_create(
                username=username, defaults={"display_name": display}
            )
            if made:
                child.set_password(DEMO_PW)
                child.save()
                apply_assurance(child, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
                link_guardian(users[guardian], child)
                grant_parental_consent(users[guardian], child)
            users[username] = child

        # --- interests for everyone (drives the constellation avatar + recommendations) ---
        for username, slugs in INTERESTS.items():
            set_interests(users[username], slugs)

        # --- places (published so they're public and usable for activities) ---
        from apps.social.models import UserPlaceProposal

        demo_places = []
        for name, lon, lat in PLACES:
            place, _ = Place.objects.get_or_create(
                name=name,
                defaults={
                    "location": Point(lon, lat, srid=4326),
                    "source": Place.Source.USER,
                    "address_city": "Cluj-Napoca",
                    "address_country": "RO",
                    "website": "https://visitclujnapoca.ro",
                },
            )
            UserPlaceProposal.objects.get_or_create(
                place=place,
                defaults={
                    "proposer": users["alex"],
                    "status": UserPlaceProposal.Status.PUBLISHED,
                    "published_at": now,
                },
            )
            demo_places.append(place)

        types = {t.slug: t for t in ActivityType.objects.all()}

        # --- build activities across all cohorts ---
        def build(spec):
            title = spec["title"]
            if Activity.objects.filter(title=title).exists():
                return  # idempotent: leave an already-seeded activity untouched
            atype = types.get(spec["slug"])
            if atype is None:
                return
            owner = users[spec["owner"]]
            place = demo_places[spec["place"]]
            if spec.get("soon"):
                starts = now + timedelta(hours=1)
            else:
                starts = (now + timedelta(days=spec["days"])).replace(
                    hour=spec.get("hour", 10), minute=0, second=0, microsecond=0
                )
            ends = starts + timedelta(hours=spec.get("dur", 2))
            kwargs = dict(
                place=place,
                activity_type=atype,
                title=title,
                starts_at=starts,
                ends_at=ends,
                description=spec.get(
                    "description", f"{atype.name} at {place.name}. All levels welcome!"
                ),
                beginners_welcome=spec.get("beginners", False),
            )
            if spec.get("capacity"):
                kwargs["capacity"] = spec["capacity"]
            if spec.get("guardian"):
                kwargs["guardian_accompanied"] = True
            kwargs.update(spec.get("enrich", {}))
            activity = create_activity(owner, **kwargs)

            for uname in spec.get("members", []):
                Membership.objects.get_or_create(
                    activity=activity,
                    user=users[uname],
                    defaults={
                        "role": Membership.Role.MEMBER,
                        "state": Membership.State.MEMBER,
                        "decided_at": now,
                    },
                )
            for uname in spec.get("requesters", []):
                u = users[uname]
                if not activity.memberships.filter(user=u).exists():
                    _try(request_to_join, u, activity)
            for uname, text in spec.get("posts", []):
                _try(post_to_thread, users[uname], activity, text)
            if spec.get("announce"):
                _try(post_announcement, owner, activity, spec["announce"])
            for uname in spec.get("rsvp_going", []):
                _try(
                    set_attendance_intent, users[uname], activity, Membership.AttendanceIntent.GOING
                )
            for uname in spec.get("arrivals", []):
                _try(mark_arrived, users[uname], activity)
            if spec.get("complete"):
                complete_activity(activity)
                for uname in spec.get("met", []):
                    _try(set_met_confirmed, users[uname], activity, True)

        for spec in ADULT_ACTS + TEEN_ACTS + CHILD_ACTS:
            build(spec)

        # --- a recurring series (F4): spawn its first concrete instance ---
        if not ActivitySeries.objects.filter(title="Weekly sunrise yoga").exists():
            create_series(
                users["ana"],
                place=demo_places[9],
                activity_type=types["yoga"],
                title="Weekly sunrise yoga",
                cadence=ActivitySeries.Cadence.WEEKLY,
                first_starts_at=(now + timedelta(days=2)).replace(
                    hour=7, minute=0, second=0, microsecond=0
                ),
                ends_at=(now + timedelta(days=2)).replace(
                    hour=8, minute=0, second=0, microsecond=0
                ),
                description="Gentle sunrise flow every week. Beginners welcome.",
                capacity=20,
                beginners_welcome=True,
                meeting_point="Yoga Space Cluj, studio A",
                what_to_bring="Mat & water",
                cost_band="free",
                difficulty="easy",
            )
            spawn_due_series()

        # --- events ---
        for etitle, eslug, days, place_idx in EVENTS:
            Event.objects.get_or_create(
                title=etitle,
                starts_at=now + timedelta(days=days, hours=19),
                defaults={
                    "place": demo_places[place_idx],
                    "activity_type": types.get(eslug),
                    "description": f"{etitle} in Cluj-Napoca.",
                    "url": "https://visitclujnapoca.ro/events",
                    "source": Event.Source.MANUAL,
                },
            )

        self._seed_donations(now)
        self._seed_partners(demo_places)
        self._seed_saved_searches(users)
        self._seed_connections(users, now)
        _try_block(lambda: call_command("seed_booking_links", verbosity=0))

        # --- materialize per-cohort communities from the activity we just seeded ---
        from apps.communities.services import generate_communities

        comm = generate_communities()

        self._summary(out, comm)

    # ------------------------------------------------------------------ helpers

    def _seed_donations(self, now):
        from apps.donations.models import Campaign, Donation, SpendEntry

        campaign, _ = Campaign.objects.get_or_create(
            slug="youth-sports-kit",
            defaults={
                "title": "Youth sports kit fund",
                "description": "Boots, balls and bibs so cost is never the reason a kid sits out.",
                "goal_cents": 500_000,
                "currency": "EUR",
                "is_active": True,
            },
        )
        # (amount_cents, donor_username_or_None, on_campaign)
        donations = [
            (5_000, "alex", False),
            (10_000, None, False),
            (25_000, "maria", True),
            (7_500, None, False),
            (15_000, None, True),
            (30_000, None, True),
        ]
        from apps.accounts.models import User as U

        for i, (cents, donor, on_camp) in enumerate(donations):
            ref = f"demo-{i}"
            if Donation.objects.filter(external_ref=ref).exists():
                continue
            Donation.objects.create(
                donor=U.objects.filter(username=donor).first() if donor else None,
                amount_cents=cents,
                currency="EUR",
                campaign=campaign if on_camp else None,
                provider="demo",
                status=Donation.Status.COMPLETED,
                external_ref=ref,
                completed_at=now,
            )
        for category, cents in [
            ("Infrastructure & hosting", 8_000),
            ("Community events", 5_500),
            ("Safety & moderation", 12_000),
            ("Accessibility improvements", 3_500),
        ]:
            SpendEntry.objects.get_or_create(
                category=category,
                period="2026 Q2",
                defaults={
                    "amount_cents": cents,
                    "currency": "EUR",
                    "note": f"Q2 spend on {category.lower()}.",
                },
            )

    def _seed_partners(self, demo_places):
        from apps.places.models import Partner

        partners = [
            (
                "Cluj-Napoca Public Library",
                Partner.Kind.LIBRARY,
                "Free reading rooms and community spaces in the heart of the city.",
                4,
                "https://bjc.ro",
            ),
            (
                "Sport Park Cluj",
                Partner.Kind.CIVIC,
                "Public sports grounds supporting youth and adult activities all year round.",
                3,
                "https://visitclujnapoca.ro",
            ),
            (
                "Cluj Art Museum",
                Partner.Kind.CULTURAL,
                "Romanian cultural heritage through exhibitions, talks and family workshops.",
                14,
                "https://visitclujnapoca.ro",
            ),
        ]
        for name, kind, blurb, place_idx, website in partners:
            Partner.objects.get_or_create(
                name=name,
                defaults={
                    "kind": kind,
                    "blurb": blurb,
                    "place": demo_places[place_idx],
                    "website": website,
                    "is_verified": True,
                    "is_active": True,
                },
            )

    def _seed_saved_searches(self, users):
        from apps.saved_searches.models import SavedSearch
        from apps.saved_searches.services import create_saved_search

        if SavedSearch.objects.filter(user=users["alex"]).exists():
            return
        for kwargs in (
            dict(
                activity_type=ActivityType.objects.filter(slug="yoga").first(), city="Cluj-Napoca"
            ),
            dict(
                activity_type=ActivityType.objects.filter(slug="book_club").first(),
                city="Cluj-Napoca",
                beginners=True,
            ),
        ):
            if kwargs["activity_type"]:
                _try(create_saved_search, users["alex"], **kwargs)

    def _seed_connections(self, users, now):
        import itertools

        from apps.connections import services as conn_svc
        from apps.messaging import services as msg_svc

        def connect_group(usernames, cap):
            made = 0
            people = [users[u] for u in usernames]
            for a, b in itertools.combinations(people, 2):
                if made >= cap:
                    break
                if conn_svc.are_connected(a, b) or not conn_svc.shares_activity(a, b):
                    continue
                try:
                    c = conn_svc.request_connection(a, b)
                    if c.status != "accepted":
                        conn_svc.respond_to_connection(b, c, accept=True)
                    made += 1
                except Exception:  # noqa: BLE001 — demo seeding is best-effort
                    continue
            return made

        connect_group([u for u, _ in ADULTS], cap=8)
        connect_group([u for u, _ in TEENS], cap=6)
        connect_group([u for u, _, _ in CHILDREN], cap=6)

        # E2EE message bodies are client-side ciphertext and can't be pre-seeded; create live
        # conversation shells so the messenger isn't empty.
        def direct(a, b):
            try:
                conv = msg_svc.start_direct(users[a], users[b])
                msg_svc.accept_invite(users[b], conv)
            except Exception:  # noqa: BLE001
                pass

        direct("alex", "maria")
        direct("alex", "dan")
        try:
            g = msg_svc.start_group(
                users["alex"], [users["maria"], users["george"]], title="Saturday crew"
            )
            msg_svc.accept_invite(users["maria"], g)
            msg_svc.accept_invite(users["george"], g)
        except Exception:  # noqa: BLE001
            pass

    def _summary(self, out, comm):
        from apps.connections.models import Connection
        from apps.donations.models import Donation
        from apps.places.models import Partner

        out.write(
            self.style.SUCCESS(
                f"Seeded: {User.objects.count()} users, {Place.objects.count()} places, "
                f"{Activity.objects.count()} activities "
                f"(adult/teen/child cohorts), {Event.objects.count()} events, "
                f"{Connection.objects.filter(status='accepted').count()} connections, "
                f"{Donation.objects.filter(status='completed').count()} donations, "
                f"{Partner.objects.count()} partners. "
                f"Communities published={comm.get('published')}."
            )
        )
        out.write("Login (web at http://localhost:8000/):")
        out.write("  admin / admin12345               superuser → /admin/")
        out.write("  alex,maria,dan,elena,george,ana,radu,ioana / demo12345   verified adults")
        out.write("  tina,mihai,sofia,vlad,bianca,andrei / demo12345          teens (16-17)")
        out.write("  kevin,luca,sara,david,ema,tudor / demo12345              under-16 (consented)")


def _try(fn, *args, **kwargs):
    """Best-effort service call: each is @transaction.atomic, so a caught failure rolls back to
    its own savepoint and leaves the outer seeding transaction usable."""
    try:
        return fn(*args, **kwargs)
    except Exception:  # noqa: BLE001 — demo seeding is best-effort
        return None


def _try_block(fn):
    try:
        with transaction.atomic():
            return fn()
    except Exception:  # noqa: BLE001
        return None
