"""Communities domain logic: the single cohort-walled READ primitive and the nightly
GENERATION job. Both the web and DRF views call these so the gates hold identically.

Safety design (see the adversarial review):
- A read ALWAYS asserts viewer.cohort == community.cohort, then routes through the existing
  social.visible_activities(viewer) (cohort + is_hidden + blocked) and only NARROWS it. There is
  no second read path; cross-cohort reads are impossible even by URL guessing.
- The generator materializes PER COHORT, only above a threshold + a k-anonymity floor counted as
  DISTINCT non-guardian peers of that cohort (a supervisory adult guardian on a child activity
  never counts toward the child slice). No member/count is ever stored or rendered.
"""

from datetime import timedelta

from django.conf import settings
from django.db import transaction
from django.db.models import Count, F, Q
from django.db.models.functions import TruncDate
from django.utils import timezone
from django.utils.text import slugify

from apps.accounts.models import Cohort

from .models import Area, Community

# Cohorts that get materialized communities (UNASSIGNED never does).
_COHORTS = [Cohort.CHILD, Cohort.TEEN, Cohort.ADULT]


# --- read primitives (cohort-walled) -------------------------------------------------------


def _area_place_q(area) -> Q:
    """The Place predicate for an Area. v1 CITY-tier matches on address_city (always current,
    no overlay dependency); finer PostGIS areas (later) match via a PlaceArea overlay."""
    if area.derive_method == Area.DeriveMethod.CITY:
        return Q(place__address_city__iexact=area.city)
    return Q(place__placearea__area=area)  # reserved for GRID/CLUSTER tiers (not in v1)


def community_activities(community, viewer, *, upcoming=True):
    """The ONLY way to list a community's activities. Cohort-walled twice: a viewer can read
    only a community of their OWN cohort, and the source query is the already cohort-pinned
    visible_activities(viewer) — so a child sees only child activities/peers, never a
    contactable adult. No identity is surfaced here; identities appear only on activity_detail
    behind the existing membership gate."""
    from apps.social.models import Activity
    from apps.social.services import visible_activities

    if not getattr(viewer, "is_authenticated", False) or viewer.cohort != community.cohort:
        return Activity.objects.none()
    qs = visible_activities(viewer).filter(_area_place_q(community.area))
    if community.tier == Community.Tier.TYPE:
        qs = qs.filter(activity_type=community.activity_type)
    else:
        qs = qs.filter(activity_type__category=community.category)
    if upcoming:
        qs = qs.filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
    return qs.select_related("activity_type", "place", "owner").order_by("starts_at")


def visible_communities(viewer):
    """Published communities of the viewer's OWN cohort, alphabetical (never popularity/hot).
    Anonymous/UNASSIGNED viewers get none — never a named empty community."""
    if not getattr(viewer, "is_authenticated", False) or viewer.cohort == Cohort.UNASSIGNED:
        return Community.objects.none()
    return (
        Community.objects.filter(is_published=True, cohort=viewer.cohort)
        .select_related("area", "category", "activity_type")
        .order_by("name", "tier")
    )


def community_by_slug(slug, viewer):
    """A published community at this slug VISIBLE to the viewer (own cohort), else None — so a
    cross-cohort or unpublished slug is a clean 404, never a content leak."""
    return visible_communities(viewer).filter(slug=slug).first()


def community_graph(viewer) -> dict:
    """Cohort-walled {nodes, links} for the 3D navigator. Sourced ONLY from
    visible_communities(viewer) — so the cohort wall AND the generator's k-anonymity existence
    floor both apply (a community node exists only if it was published) — then the taxonomy
    scaffolding (categories/types/relations) is CLAMPED to exactly the categories/types those
    communities use. A child's graph can never contain another cohort's branch, and NO node or
    edge carries any member/participant count (only category/type/community names + structure)."""
    from apps.taxonomy.models import ActivityCategory, ActivityRelation, ActivityType

    comms = list(visible_communities(viewer))
    if not comms:
        return {"nodes": [], "links": []}
    cat_ids = {c.category_id for c in comms}
    type_ids = {c.activity_type_id for c in comms if c.activity_type_id}

    nodes, links, seen = [], [], set()

    def _node(nid, kind, label, drill=None):
        if nid in seen:
            return
        seen.add(nid)
        n = {"id": nid, "kind": kind, "label": label}
        if drill:
            n["drill"] = drill
        nodes.append(n)

    cats = {c.id: c for c in ActivityCategory.objects.filter(id__in=cat_ids)}
    types = {
        t.id: t for t in ActivityType.objects.filter(id__in=type_ids).select_related("category")
    }
    for c in cats.values():
        _node(f"cat:{c.slug}", "category", c.name)
    for t in types.values():
        _node(f"type:{t.slug}", "type", t.name)
        if t.category_id in cats:  # contains: category -> type
            links.append(
                {
                    "source": f"cat:{cats[t.category_id].slug}",
                    "target": f"type:{t.slug}",
                    "kind": "contains",
                }
            )
    for c in cats.values():  # parent: category -> category (clamped to in-payload categories)
        if c.parent_id and c.parent_id in cats:
            links.append(
                {
                    "source": f"cat:{cats[c.parent_id].slug}",
                    "target": f"cat:{c.slug}",
                    "kind": "parent",
                }
            )
    # lateral knowledge-graph edges (type -> type), only when BOTH endpoints are in the payload.
    for r in ActivityRelation.objects.filter(
        source_id__in=type_ids, target_id__in=type_ids
    ).select_related("source", "target"):
        links.append(
            {"source": f"type:{r.source.slug}", "target": f"type:{r.target.slug}", "kind": r.kind}
        )
    # community leaves + their instance edge (from the type, or the category for a CATEGORY-tier).
    # activity_count = upcoming activities in this community (a DISCOVERY count, never a member
    # count — communities have no membership; it also drives node "relevance" sizing client-side).
    for comm in comms:
        cid = f"comm:{comm.slug}"
        node = {
            "id": cid,
            "kind": "community",
            "label": comm.name,
            "drill": f"/communities/{comm.slug}/",
            "activity_count": community_activities(comm, viewer).count(),
        }
        if cid not in seen:
            seen.add(cid)
            nodes.append(node)
        if comm.activity_type_id and comm.activity_type_id in types:
            links.append(
                {
                    "source": f"type:{types[comm.activity_type_id].slug}",
                    "target": cid,
                    "kind": "instance",
                }
            )
        elif comm.category_id in cats:
            links.append(
                {"source": f"cat:{cats[comm.category_id].slug}", "target": cid, "kind": "instance"}
            )
    return {"nodes": nodes, "links": links}


def communities_for_activity(activity):
    """The published TYPE + CATEGORY communities an activity belongs to, in its own cohort — a
    discovery affordance on activity_detail. Returns [] when the activity's place has no city."""
    city = (activity.place.address_city if activity.place_id else "") or ""
    if not city.strip():
        return []
    return list(
        Community.objects.filter(
            is_published=True,
            cohort=activity.cohort,
            area__derive_method=Area.DeriveMethod.CITY,
            area__city__iexact=city,
        )
        .filter(
            Q(tier=Community.Tier.TYPE, activity_type_id=activity.activity_type_id)
            | Q(tier=Community.Tier.CATEGORY, category_id=activity.activity_type.category_id)
        )
        .select_related("area", "category", "activity_type")
        .order_by("tier")
    )


# --- generation (nightly job) --------------------------------------------------------------


def _unique_slug(model, base, *, exclude_pk=None):
    """A globally-unique slug for ``model``, appending -2/-3/... on collision — so a slug clash
    can never raise an IntegrityError that rolls back the whole nightly run."""
    base = (base or "x")[:140]
    candidate = base
    i = 2
    qs = model.objects.all()
    if exclude_pk is not None:
        qs = qs.exclude(pk=exclude_pk)
    while qs.filter(slug=candidate).exists():
        suffix = f"-{i}"
        candidate = base[: 140 - len(suffix)] + suffix
        i += 1
    return candidate


def _ensure_city_area(city):
    area = Area.objects.filter(derive_method=Area.DeriveMethod.CITY, city__iexact=city).first()
    if area is None:
        area = Area.objects.create(
            city=city,
            slug=_unique_slug(Area, slugify(city)[:96] or "area"),
            name=city,
            derive_method=Area.DeriveMethod.CITY,
        )
    return area


def _compose(area_name, label):
    return f"{area_name} {label}"


@transaction.atomic
def generate_communities(*, now=None) -> dict:
    """Materialize communities PER COHORT from REAL activity, above a threshold + k-anon floor.
    Evaluates only candidate (city, cohort, type/category) coordinates that actually have
    activity (a bounded GROUP BY, never the full cartesian product). Deactivate-not-delete:
    a community that falls below the gate is unpublished (self-heals), never dropped. No
    cold-start venue seeding in v1 — a community only ever exists off real activities, which
    keeps every cohort (including minors) behind the k-anon floor by every path."""
    from apps.social.models import Activity, Membership
    from apps.taxonomy.models import ActivityType

    now = now or timezone.now()
    lookback = now - timedelta(days=getattr(settings, "COMMUNITY_LOOKBACK_DAYS", 180))
    min_acts = getattr(settings, "COMMUNITY_MIN_ACTIVITIES", 3)
    k_anon = getattr(settings, "COMMUNITY_K_ANON_FLOOR", 5)
    min_days = getattr(settings, "COMMUNITY_MIN_DAYS", 2)

    # Count only REAL activities the read path would show — exclude moderator-hidden and
    # CANCELLED, so the existence/k-anon gate matches the content gate (no community materialized
    # off hidden/cancelled activity).
    base = (
        Activity.objects.filter(starts_at__gte=lookback, is_hidden=False)
        .filter(status__in=[Activity.Status.OPEN, Activity.Status.COMPLETED])
        .exclude(place__address_city="")
    )

    # Per-(city, cohort, type) activity + distinct-day counts (candidate set: only coords present).
    type_rows = base.values(
        "place__address_city", "cohort", "activity_type", "activity_type__category"
    ).annotate(
        n_act=Count("id", distinct=True), n_days=Count(TruncDate("starts_at"), distinct=True)
    )
    cat_rows = base.values("place__address_city", "cohort", "activity_type__category").annotate(
        n_act=Count("id", distinct=True), n_days=Count(TruncDate("starts_at"), distinct=True)
    )

    # Distinct non-guardian PEERS of the coordinate's own cohort — the k-anon count. A guardian
    # (role=GUARDIAN, an adult on a child activity) never counts toward the child/teen slice.
    peers = (
        Membership.objects.filter(activity__starts_at__gte=lookback, state=Membership.State.MEMBER)
        .filter(activity__is_hidden=False)
        .filter(activity__status__in=[Activity.Status.OPEN, Activity.Status.COMPLETED])
        .exclude(role=Membership.Role.GUARDIAN)
        .exclude(activity__place__address_city="")
        .filter(user__cohort=F("activity__cohort"))
    )
    type_minor = {
        (
            r["activity__place__address_city"],
            r["activity__cohort"],
            r["activity__activity_type"],
        ): r["n"]
        for r in peers.values(
            "activity__place__address_city", "activity__cohort", "activity__activity_type"
        ).annotate(n=Count("user", distinct=True))
    }
    cat_minor = {
        (
            r["activity__place__address_city"],
            r["activity__cohort"],
            r["activity__activity_type__category"],
        ): r["n"]
        for r in peers.values(
            "activity__place__address_city", "activity__cohort", "activity__activity_type__category"
        ).annotate(n=Count("user", distinct=True))
    }

    types = {t.id: t for t in ActivityType.objects.select_related("category").all()}
    keep_ids: set = set()
    published = 0

    def _clears(n_act, n_days, n_minor):
        return n_act >= min_acts and n_days >= min_days and n_minor >= k_anon

    # TYPE-tier
    for r in type_rows:
        city, cohort, type_id = r["place__address_city"], r["cohort"], r["activity_type"]
        if cohort not in _COHORTS or type_id is None:
            continue
        n_minor = type_minor.get((city, cohort, type_id), 0)
        if not _clears(r["n_act"], r["n_days"], n_minor):
            continue
        t = types.get(type_id)
        area = _ensure_city_area(city)
        c = _upsert(
            cohort=cohort,
            area=area,
            category=t.category,
            activity_type=t,
            tier=Community.Tier.TYPE,
            name=_compose(area.name, t.name),
            now=now,
        )
        keep_ids.add(c.id)
        published += 1

    # CATEGORY-tier (rollup over all types in the category)
    for r in cat_rows:
        city, cohort, cat_id = r["place__address_city"], r["cohort"], r["activity_type__category"]
        if cohort not in _COHORTS or cat_id is None:
            continue
        n_minor = cat_minor.get((city, cohort, cat_id), 0)
        if not _clears(r["n_act"], r["n_days"], n_minor):
            continue
        from apps.taxonomy.models import ActivityCategory

        cat = ActivityCategory.objects.filter(id=cat_id).first()
        if cat is None:
            continue
        area = _ensure_city_area(city)
        c = _upsert(
            cohort=cohort,
            area=area,
            category=cat,
            activity_type=None,
            tier=Community.Tier.CATEGORY,
            name=_compose(area.name, cat.name),
            now=now,
        )
        keep_ids.add(c.id)
        published += 1

    # Deactivate (never delete) any previously-published community that no longer clears.
    deactivated = (
        Community.objects.filter(is_published=True)
        .exclude(id__in=keep_ids)
        .update(is_published=False, last_evaluated_at=now)
    )
    from apps.safety.services import record_audit

    record_audit("community.generated", published=published, deactivated=deactivated)
    return {"published": published, "deactivated": deactivated}


def _upsert(*, cohort, area, category, activity_type, tier, name, now):
    if tier == Community.Tier.TYPE:
        lookup = {"cohort": cohort, "area": area, "activity_type": activity_type}
        # The "t-" / "c-" tier prefix means a TYPE and its CATEGORY rollup never collide even when
        # a type's slug equals its category's slug (e.g. seeded "reading"/"video_games"); the
        # _unique_slug loop backstops any residual clash so one row can never abort the run.
        slug_src = f"{area.slug}-t-{activity_type.slug}-{cohort}"
    else:
        lookup = {"cohort": cohort, "area": area, "category": category, "activity_type": None}
        slug_src = f"{area.slug}-c-{category.slug}-{cohort}"
    obj = Community.objects.filter(**lookup).first()
    if obj is None:
        create_kwargs = dict(lookup)
        create_kwargs.setdefault("category", category)  # TYPE lookup omits category; add it once
        obj = Community(**create_kwargs, tier=tier, slug=_unique_slug(Community, slugify(slug_src)))
    obj.name = name
    obj.tier = tier
    obj.category = category
    obj.is_published = True
    obj.last_evaluated_at = now
    obj.save()
    return obj
