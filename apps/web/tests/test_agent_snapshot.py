"""Agent-snapshot exporter tests — the child-safety-critical guarantee is that ONLY
gate-filtered public data reaches the JSON files a Go sidecar serves: no minor activity, no
unpublished place, no PII, ever.
"""

import json
from datetime import datetime, timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.models import Cohort, User
from apps.events.models import Event
from apps.places.models import Place
from apps.social.models import Activity, UserPlaceProposal
from apps.taxonomy.models import ActivityCategory, ActivityType
from apps.web import agent_snapshot

pytestmark = pytest.mark.django_db


# --- helpers -------------------------------------------------------------------------------


def _user(name="owner"):
    # is_active is all public_activities()/public_places() gates need from the owner here.
    return User.objects.create_user(username=name, password="pw", display_name=name)


def _place(name="City Library", source=Place.Source.OSM, **kwargs):
    return Place.objects.create(
        name=name, location=Point(23.6, 46.77, srid=4326), source=source, **kwargs
    )


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="snap-sport", defaults={"name": "Sport"})
    at, _ = ActivityType.objects.get_or_create(
        slug="snap-basketball", defaults={"name": "Basketball", "category": cat}
    )
    return at


def _activity(owner, place, at, *, cohort=Cohort.ADULT, listed=False, **kwargs):
    kwargs.setdefault("title", "Pickup game")
    kwargs.setdefault("starts_at", timezone.now() + timedelta(days=2))
    kwargs.setdefault("status", Activity.Status.OPEN)
    return Activity.objects.create(
        owner=owner,
        place=place,
        activity_type=at,
        cohort=cohort,
        is_publicly_listed=listed,
        **kwargs,
    )


def _event(place=None, **kwargs):
    kwargs.setdefault("title", "Reading circle")
    kwargs.setdefault("starts_at", timezone.now() + timedelta(days=3))
    kwargs.setdefault("source", Event.Source.MANUAL)
    return Event.objects.create(place=place, **kwargs)


def _export(tmp_path):
    counts = agent_snapshot.export_snapshot(str(tmp_path))
    return counts


def _load(tmp_path, name):
    with open(tmp_path / name, encoding="utf-8") as fh:
        return json.load(fh)


# --- activities: the minor-exposure wall ---------------------------------------------------


def test_minor_activities_never_exported_even_if_flag_forced(tmp_path):
    owner, place, at = _user(), _place(), _type()
    # Worst case: force the opt-in flag TRUE via .update() (bypassing the cohort-gated service)
    # on a CHILD and a TEEN activity. public_activities() hard-codes cohort=ADULT, so neither
    # can ever reach the file regardless of the flag.
    child = _activity(owner, place, at, cohort=Cohort.CHILD, title="Kids ball")
    teen = _activity(owner, place, at, cohort=Cohort.TEEN, title="Teen ball")
    Activity.objects.filter(pk__in=[child.pk, teen.pk]).update(is_publicly_listed=True)

    _export(tmp_path)
    data = _load(tmp_path, "activities.json")

    titles = {r["title"] for r in data["records"]}
    cohorts = {r["cohort"] for r in data["records"]}
    assert "Kids ball" not in titles
    assert "Teen ball" not in titles
    assert cohorts <= {Cohort.ADULT}


def test_adult_activity_needs_optin_and_has_exact_keys(tmp_path):
    owner, place, at = _user(), _place(), _type()
    # ADULT but NOT opted in — absent.
    _activity(owner, place, at, title="Private adult game", listed=False)
    # ADULT, opted in, OPEN, future — present.
    _activity(owner, place, at, title="Public adult game", listed=True)

    _export(tmp_path)
    data = _load(tmp_path, "activities.json")

    titles = {r["title"] for r in data["records"]}
    assert "Private adult game" not in titles
    assert "Public adult game" in titles

    rec = next(r for r in data["records"] if r["title"] == "Public adult game")
    assert set(rec.keys()) == {
        "id",
        "title",
        "cohort",
        "starts_at",
        "status",
        "activity_type",
        "place_id",
    }
    assert rec["cohort"] == Cohort.ADULT
    assert rec["activity_type"] == "snap-basketball"


# --- events + places: unpublished-place + non-discoverable gates ---------------------------


def test_event_at_unpublished_place_absent_and_place_absent(tmp_path):
    pending = _place("Pending backyard", source=Place.Source.USER)
    UserPlaceProposal.objects.create(
        place=pending, proposer=_user("proposer"), status=UserPlaceProposal.Status.PENDING
    )
    public = _place("Public hall")
    _event(place=pending, title="At pending place")
    _event(place=public, title="At public place")

    _export(tmp_path)
    events = _load(tmp_path, "events.json")
    places = _load(tmp_path, "places.json")

    ev_titles = {r["title"] for r in events["records"]}
    assert "At public place" in ev_titles
    assert "At pending place" not in ev_titles

    place_names = {r["name"] for r in places["records"]}
    assert "Public hall" in place_names
    assert "Pending backyard" not in place_names


def test_non_discoverable_events_absent(tmp_path):
    place = _place()
    _event(place=place, title="Good event")
    _event(
        place=place,
        title="Cancelled",
        source=Event.Source.SCRAPER,
        external_id="snap:cancelled",
        lifecycle_status=Event.LifecycleStatus.CANCELLED,
    )
    _event(
        place=place,
        title="Tombstoned",
        source=Event.Source.SCRAPER,
        external_id="snap:tomb",
        is_tombstone=True,
    )
    _event(place=place, title="Held", is_import_held=True)

    _export(tmp_path)
    events = _load(tmp_path, "events.json")

    titles = {r["title"] for r in events["records"]}
    assert titles == {"Good event"}


def test_event_record_carries_activity_slug(tmp_path):
    # Slug parity with EventSerializer.activity — the sidecar's ?activity= filter keys on it.
    place, at = _place(), _type()
    _event(place=place, title="Typed event", activity_type=at)
    _event(place=place, title="Untyped event")

    _export(tmp_path)
    records = {r["title"]: r for r in _load(tmp_path, "events.json")["records"]}
    assert records["Typed event"]["activity"] == "snap-basketball"
    assert records["Untyped event"]["activity"] is None


def test_place_record_excludes_raw_tags_and_has_display_fields(tmp_path):
    _place(
        "Tagged venue",
        raw_tags={"secret": "should-not-leak", "amenity": "library"},
        website="https://example.org",
    )
    _export(tmp_path)
    places = _load(tmp_path, "places.json")
    rec = next(r for r in places["records"] if r["name"] == "Tagged venue")
    assert "raw_tags" not in rec
    assert "secret" not in json.dumps(rec)
    assert rec["website"] == "https://example.org"
    assert rec["path"].startswith("/places/")


# --- datetime normalisation ----------------------------------------------------------------


def test_all_datetimes_are_rfc3339_utc_z(tmp_path):
    owner, place, at = _user(), _place(), _type()
    _activity(owner, place, at, listed=True)
    _event(place=place)
    _export(tmp_path)

    values = []
    for r in _load(tmp_path, "activities.json")["records"]:
        values.append(r["starts_at"])
    for r in _load(tmp_path, "events.json")["records"]:
        values.append(r["starts_at"])
        if r["ends_at"] is not None:
            values.append(r["ends_at"])
    for name in ("events.json", "places.json", "activities.json", "taxonomy.json", "manifest.json"):
        values.append(_load(tmp_path, name)["generated_at"])

    assert values  # sanity: we actually checked something
    for v in values:
        assert v.endswith("Z"), v
        # Valid RFC3339: parseable once Z is normalised to an explicit offset.
        parsed = datetime.fromisoformat(v.replace("Z", "+00:00"))
        assert parsed.utcoffset() == timedelta(0)


# --- manifest integrity --------------------------------------------------------------------


def test_manifest_counts_match_and_no_tmp_files_remain(tmp_path):
    owner, place, at = _user(), _place(), _type()
    _activity(owner, place, at, listed=True)
    _event(place=place)
    _export(tmp_path)

    manifest = _load(tmp_path, "manifest.json")
    for key, fname in (
        ("events", "events.json"),
        ("places", "places.json"),
        ("activities", "activities.json"),
    ):
        assert manifest["datasets"][key]["count"] == _load(tmp_path, fname)["count"]

    # taxonomy.json has no "records" wrapper; its manifest count is total entities.
    tax = _load(tmp_path, "taxonomy.json")
    expected = len(tax["categories"]) + len(tax["activity_types"])
    assert manifest["datasets"]["taxonomy"]["count"] == expected

    # Atomic write leaves no *.tmp behind.
    assert not list(tmp_path.glob("*.tmp"))
    assert manifest["truncated"] is False


def test_manifest_licenses_populated_from_licensed_place(tmp_path):
    _place(
        "Licensed venue",
        attribution="© OpenStreetMap contributors",
        license_name="ODbL",
        provenance_url="https://www.openstreetmap.org/",
    )
    # A place with no licence contributes no credit pair.
    _place("Bare venue")
    _export(tmp_path)

    manifest = _load(tmp_path, "manifest.json")
    licenses = manifest["licenses"]
    assert {"license_name": "ODbL", "attribution": "© OpenStreetMap contributors"} in licenses
    # No blank-licence pair sneaks in.
    assert all(entry["license_name"] for entry in licenses)


def test_taxonomy_top_level_shape(tmp_path):
    _type()
    ActivityType.objects.filter(slug="snap-basketball").update(family_friendly=True, wellness=True)
    _export(tmp_path)
    tax = _load(tmp_path, "taxonomy.json")
    assert tax["schema_version"] == agent_snapshot.SCHEMA_VERSION
    assert "records" not in tax
    assert isinstance(tax["categories"], list)
    slugs = {t["slug"] for t in tax["activity_types"]}
    assert "snap-basketball" in slugs
    row = next(t for t in tax["activity_types"] if t["slug"] == "snap-basketball")
    assert set(row.keys()) == {
        "slug",
        "name",
        "category",
        "parent",
        "family_friendly",
        "wellness",
    }
    assert row["family_friendly"] is True and row["wellness"] is True


# --- command opt-in no-op ------------------------------------------------------------------


def test_command_noops_when_dir_unset(tmp_path, settings):
    from io import StringIO

    from django.core.management import call_command

    settings.AGENT_SNAPSHOT_DIR = ""
    out = StringIO()
    call_command("export_agent_snapshot", stdout=out)
    assert "disabled" in out.getvalue().lower()
    # Nothing was written.
    assert not list(tmp_path.iterdir())


def test_command_writes_files_when_dir_set(tmp_path, settings):
    from io import StringIO

    from django.core.management import call_command

    owner, place, at = _user(), _place(), _type()
    _activity(owner, place, at, listed=True)
    settings.AGENT_SNAPSHOT_DIR = str(tmp_path)
    out = StringIO()
    call_command("export_agent_snapshot", stdout=out)
    assert "Agent snapshot written" in out.getvalue()
    assert (tmp_path / "manifest.json").exists()
