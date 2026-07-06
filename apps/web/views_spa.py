"""SPA plumbing for the React frontend (ADR-0016 Phase 2).

One Django view serves BOTH representations of a migrated screen:

- full page: ``web/spa.html`` (extends base.html — server chrome, SEO head, CSP
  nonce) with the screen's bootstrap embedded via the established
  ``nonced_json_script`` island;
- soft navigation: the React router re-requests the same URL with ``?_data=1``
  and receives the bootstrap as JSON — no page reload.

The kill switch ``SOCIAL_REACT_UI`` (default False) keeps every migrated view
serving its legacy template until Paul flips it per environment — the template
test suite keeps asserting the SSR output either way (same pattern as
ro_teacher's ``RO_TEACHER_REACT_UI``).

Bootstrap builders reuse the exact objects the legacy views computed (services
already applied cohort/safety gates) and ship DISPLAY-READY strings: dates via
the same template date formats, labels via gettext on EXISTING msgids — no new
translation entries, no client-side date/i18n logic.
"""

from django.conf import settings
from django.http import JsonResponse
from django.middleware.csrf import get_token
from django.shortcuts import render
from django.template.defaultfilters import date as date_fmt
from django.template.defaultfilters import floatformat, truncatewords
from django.urls import reverse
from django.utils.translation import gettext as _
from django.utils.translation import ngettext


def spa_enabled() -> bool:
    """ADR-0016 kill switch: React screens serve only when SOCIAL_REACT_UI=True."""
    return bool(getattr(settings, "SOCIAL_REACT_UI", False))


def spa_response(
    request,
    route: str,
    bootstrap: dict,
    *,
    title: str,
    public: bool = False,
    seo: dict | None = None,
    snapshot_template: str | None = None,
    snapshot_context: dict | None = None,
):
    """Render a migrated screen: JSON for ``?_data=1`` soft-nav, SPA shell otherwise.

    ``public=True`` marks login-free SEO pages: the payload carries no CSRF token
    (``get_token`` would defeat ``cache_public`` by varying the response) and the
    shell renders ``snapshot_template`` inside ``#root`` — a server-rendered,
    crawler/noscript-readable extract that React replaces on hydration. ``seo``
    passes through the page's head parity: ``description``, ``robots``,
    ``structured_data`` (JSON-LD string), ``breadcrumb_data``, ``rss`` ({url,title}).
    """
    payload = {
        "route": route,
        "title": title,
        "csrf": "" if public else get_token(request),
        "data": bootstrap,
    }
    if request.GET.get("_data") == "1":
        return JsonResponse(payload)
    seo = seo or {}
    context = {
        "spa_route": route,
        "spa_title": title,
        "spa_bootstrap": payload,
        "spa_meta_description": seo.get("description", ""),
        "spa_meta_robots": seo.get("robots", ""),
        "spa_structured_data": seo.get("structured_data") or "",
        "spa_rss": seo.get("rss"),
        "spa_snapshot_template": snapshot_template,
        **(snapshot_context or {}),
    }
    if seo.get("breadcrumb_data"):
        context["breadcrumb_data"] = seo["breadcrumb_data"]  # emitted by base.html's head
    return render(request, "web/spa.html", context)


# --- serializers -------------------------------------------------------------
# Each dict mirrors exactly what the legacy template read (see _activity_card.html
# and the screen templates); anything the template didn't show stays out.


def activity_card(a, viewer) -> dict:
    """The _activity_card.html contract (ADR-0007: one cover photo OR generated accent)."""
    from apps.accounts.avatars import activity_accent_svg
    from apps.media.services import activity_visual

    visual = getattr(a, "visual", None) or activity_visual(a, viewer)
    if visual and visual.get("kind") == "activity_cover_photo":
        vis = {"kind": "photo", "url": visual["url"], "alt": visual.get("alt") or a.title}
    else:
        atype = getattr(a, "activity_type", None)
        seed = f"{getattr(atype, 'slug', '') or ''}:{a.title}"
        vis = {"kind": "accent", "svg": activity_accent_svg(seed)}

    tags = [a.activity_type.name]
    if a.cost_band and a.cost_band != "unspecified":
        tags.append(a.get_cost_band_display())
    if a.difficulty and a.difficulty != "unspecified":
        tags.append(a.get_difficulty_display())
    if a.beginners_welcome:
        tags.append(_("beginners welcome"))
    if a.status and a.status != "open":
        tags.append(a.get_status_display())
    if a.guardian_accompanied:
        tags.append(_("guardian-accompanied"))

    place = getattr(a, "place", None)
    place_name = (getattr(place, "display_name", "") or getattr(place, "name", "")) if place else ""
    meta = f"{date_fmt(a.starts_at, 'D j M, H:i')} · {place_name or _('a place')}"
    city = getattr(place, "address_city", "") if place else ""
    if city:
        meta += f", {city}"
    distance = getattr(a, "distance", None)
    if distance is not None:
        meta += " · " + _("%(km)s km away") % {"km": floatformat(distance.km, 1)}

    if getattr(a, "rec_reason", None):
        score = a.rec_reason
    elif getattr(a, "match_pct", None):
        score = _("%(pct)s%% match") % {"pct": a.match_pct}
    else:
        score = None

    return {
        "pk": a.pk,
        "url": reverse("activity_detail", args=[a.pk]),
        "title": a.title,
        "visual": vis,
        "tags": tags,
        "meta": meta,
        "description": truncatewords(a.description, 22) if a.description else "",
        "score": score,
    }


# --- screens ------------------------------------------------------------------


def home_spa(
    request,
    *,
    recommended,
    starter_types,
    beginners,
    upcoming,
    mine,
    events,
    group_updates,
    near_active,
    beginners_only,
    guardian_invites,
):
    user = request.user
    name = user.display_name or user.username

    def cards(items):
        return [activity_card(a, user) for a in items]

    data = {
        "sections": {
            "recommended": cards(recommended),
            "beginners": cards(beginners),
            "upcoming": cards(upcoming),
            "mine": cards(mine),
        },
        "starterTypes": [{"slug": t.slug, "name": t.name} for t in starter_types],
        "events": [
            {
                "pk": e.pk,
                "url": reverse("event_detail", args=[e.pk]),
                "title": e.title,
                "reason": getattr(e, "feed_reason", "") or "",
                "meta": date_fmt(e.starts_at, "D j M, H:i")
                + (f" · {e.place.name}" if getattr(e, "place", None) else ""),
            }
            for e in events
        ],
        "groupUpdates": [
            {
                "url": reverse("group_detail", args=[p.thread.group.pk]),
                "groupTitle": p.thread.group.title,
                "when": date_fmt(p.created_at, "D j M, H:i"),
                "snippet": truncatewords(p.body, 18),
            }
            for p in group_updates
        ],
        "guardianInvites": [
            {
                "name": gi.guardian.display_name or gi.guardian.username,
                "relationship": gi.relationship,
                "acceptAction": reverse("guardian_invite_accept", args=[gi.token]),
                "declineAction": reverse("guardian_invite_decline", args=[gi.token]),
            }
            for gi in guardian_invites
        ],
        "flags": {"nearActive": near_active, "beginnersOnly": beginners_only},
        "urls": {
            "browse": reverse("activity_list"),
            "organizeNew": reverse("activity_create"),
            "places": reverse("places_map"),
            "series": reverse("series_list"),
            "interestsAction": reverse("interests"),
        },
        "ui": {
            "greeting": _("Hi %(name)s") % {"name": name},
            "guardianRequests": _("Guardian requests"),
            "accept": _("Accept"),
            "decline": _("Decline"),
            "organise": _("Organise an activity"),
            "findPlace": _("Find a place"),
            "series": _("Recurring series"),
            "search": _("Search"),
            "fromGroups": _("From your groups"),
            "starterHead": _("New here? Pick what you'd come to"),
            "starterSave": _("Save & see matches"),
            "recommended": _("Recommended for you"),
            "beginnersHead": _("New here? These welcome beginners"),
            "mine": _("Your activities"),
            "upcoming": _("Upcoming for you"),
            "eventsHead": _("Events you may like"),
        },
    }
    return spa_response(request, "home", data, title=_("Social Activities"))


def browse_spa(
    request,
    *,
    activities,
    page_obj,
    view_mode,
    query,
    did_you_mean,
    did_you_mean_q,
    near_active,
    beginners_only,
    base_qs,
):
    data = {
        "cards": [activity_card(a, request.user) for a in activities],
        "filters": {
            "query": query,
            "beginnersOnly": beginners_only,
            "nearActive": near_active,
            "didYouMean": did_you_mean or "",
            "didYouMeanQ": did_you_mean_q,
        },
        "viewMode": view_mode,
        "baseQs": base_qs,
        "page": {
            "count": page_obj.paginator.count,
            "numPages": page_obj.paginator.num_pages,
            "number": page_obj.number,
            "previous": page_obj.previous_page_number() if page_obj.has_previous() else None,
            "next": page_obj.next_page_number() if page_obj.has_next() else None,
        },
        "urls": {"action": reverse("activity_list"), "organizeNew": reverse("activity_create")},
        "ui": {
            "title": _("Upcoming activities"),
            "organizeOne": _("Organize one →"),
            "search": _("Search"),
            "clear": _("clear"),
            "beginnersOnly": _("beginners welcome only"),
            "showAll": _("show all"),
            "list": _("List"),
            "cards": _("Cards"),
            "empty": _("No upcoming activities match your search."),
            "prev": _("← Prev"),
            "next": _("Next →"),
            "shuffle": _("Shuffle"),
        },
    }
    return spa_response(request, "browse", data, title=_("Activities"))


def organize_spa(request, *, activities, series, groups):
    def row_dict(row):
        a = row["activity"]
        detail = reverse("activity_detail", args=[a.pk])
        edit = reverse("activity_edit", args=[a.pk])
        badges = []
        if row.get("pending_joins"):
            n = row["pending_joins"]
            badges.append(
                {
                    "label": ngettext("%(n)s waiting to join", "%(n)s waiting to join", n)
                    % {"n": n},
                    "url": detail,
                    "tone": "info",
                }
            )
        if row.get("needs_supervisor"):
            badges.append(
                {"label": _("Needs a supervising guardian"), "url": detail, "tone": "danger"}
            )
        quorum = row.get("quorum") or {}
        if quorum.get("remaining_needed"):
            n = quorum["remaining_needed"]
            badges.append(
                {
                    "label": ngettext("Needs %(n)s more to go", "Needs %(n)s more to go", n)
                    % {"n": n},
                    "url": detail,
                    "tone": "info",
                }
            )
        if row.get("missing_meeting_point"):
            badges.append({"label": _("Add a meeting point"), "url": edit, "tone": "info"})
        readiness = row.get("readiness") or {}
        if readiness.get("missing_what_to_bring"):
            badges.append({"label": _("Add what to bring"), "url": edit, "tone": "info"})
        if readiness.get("missing_getting_home"):
            badges.append({"label": _("Add a getting-home plan"), "url": edit, "tone": "danger"})
        if readiness.get("near_capacity"):
            badges.append({"label": _("Full"), "url": None, "tone": "info"})
        if row.get("venue_flag") and getattr(a, "place", None):
            badges.append(
                {
                    "label": _("Check this venue's hours"),
                    "url": reverse("place_detail", args=[a.place.pk]),
                    "tone": "danger",
                }
            )
        support = row.get("support_companions") or 0
        place = getattr(a, "place", None)
        place_name = (
            (getattr(place, "display_name", "") or getattr(place, "name", "")) if place else ""
        )
        return {
            "pk": a.pk,
            "url": detail,
            "title": a.title,
            "type": a.activity_type.name,
            "when": date_fmt(a.starts_at, "D d M, H:i"),
            "place": place_name,
            "badges": badges,
            "allClear": not badges,
            "supportNote": (
                ngettext(
                    "%(n)s member is bringing a support person.",
                    "%(n)s members are bringing a support person.",
                    support,
                )
                % {"n": support}
                if support
                else ""
            ),
        }

    data = {
        "activities": [row_dict(r) for r in activities],
        "series": [
            {
                "pk": s.pk,
                "url": reverse("series_detail", args=[s.pk]),
                "title": s.title,
                "cadence": s.get_cadence_display(),
                "next": (
                    _("Next: %(when)s") % {"when": date_fmt(s.next_starts_at, "D d M, H:i")}
                    if s.next_starts_at
                    else ""
                ),
            }
            for s in series
        ],
        "groups": [
            {"pk": g.pk, "url": reverse("group_detail", args=[g.pk]), "title": g.title}
            for g in groups
        ],
        "urls": {"organizeNew": reverse("activity_create")},
        "ui": {
            "title": _("Run my meetups"),
            "intro": _(
                "Everything you organise, with what each one needs next. Tap through to act."
            ),
            "activities": _("Activities"),
            "allClear": _("Nothing needed right now."),
            "emptyLead": _("You're not organising any upcoming activities."),
            "emptyCta": _("Organise one"),
            "seriesHead": _("Recurring series"),
            "groupsHead": _("Standing groups"),
        },
    }
    return spa_response(request, "organize", data, title=_("Run my meetups"))


# --- public SEO screens (P2b) --------------------------------------------------
# All public=True: login-free, cache_public-compatible (no CSRF in payload), each
# ships a crawler/noscript snapshot template plus full head parity (description,
# robots, JSON-LD, RSS) so flipping SOCIAL_REACT_UI never regresses indexability.


def _event_row(e) -> dict:
    from apps.web.seo import event_path, place_path

    place = getattr(e, "place", None)
    return {
        "pk": e.pk,
        "url": event_path(e),
        "title": e.title,
        "type": e.activity_type.name if getattr(e, "activity_type", None) else "",
        "when": date_fmt(e.starts_at, "D j M, H:i"),
        "place": {"name": place.name, "url": place_path(place)} if place else None,
        "description": truncatewords(e.description, 22) if e.description else "",
    }


def events_spa(
    request, *, events, query, activity, areas, area, area_name, filtered, structured_data
):
    data = {
        "events": [_event_row(e) for e in events],
        "filters": {
            "query": query,
            "activity": activity or "",
            "area": area,
            "areaName": area_name,
        },
        "areas": [{"slug": a.slug, "name": a.name} for a in areas],
        "urls": {
            "action": reverse("events_list"),
            "rss": reverse("events_feed"),
            "thingsIndex": reverse("things_to_do_index"),
        },
        "ui": {
            "title": _("What's happening"),
            "subscribe": _("Subscribe (RSS)"),
            "browseBy": _("Browse by city & activity"),
            "lead": _(
                "Upcoming events at places near you. Find one you like, then organise "
                "an activity to go together."
            ),
            "searchPlaceholder": _("Search events…"),
            "searchLabel": _("Search events"),
            "anyArea": _("Any area"),
            "areaLabel": _("Area"),
            "search": _("Search"),
            "clear": _("clear"),
            "empty": _("No upcoming events yet."),
        },
    }
    return spa_response(
        request,
        "events",
        data,
        title=_("What's happening"),
        public=True,
        seo={
            "description": _(
                "Upcoming events at real places in Cluj-Napoca — sport, outdoors, games, "
                "reading and culture you can join in person."
            ),
            "robots": "noindex, follow" if filtered else "",
            "structured_data": structured_data,
            "rss": {"url": reverse("events_feed"), "title": _("Upcoming events")},
        },
        snapshot_template="web/snapshots/events.html",
        snapshot_context={"events": events},
    )


def places_spa(request, *, places, filters, near_active, truncated, filtered, structured_data):
    rows = []
    for p in places:
        distance = getattr(p, "distance", None)
        rows.append(
            {
                "pk": p.pk,
                "url": reverse("place_detail", args=[p.pk]),
                "name": p.name or _("Unnamed place"),
                "street": p.address_street or "",
                "city": p.address_city or "",
                "distance": (
                    _("%(meters)s m away") % {"meters": floatformat(distance.m, 0)}
                    if distance is not None
                    else ""
                ),
                "activities": [pa.activity.name for pa in p.place_activities.all()],
                "accessMatch": bool(getattr(p, "access_match", False)),
                "accessTags": [
                    {"label": r["label"], "state": r["state"]}
                    for r in getattr(p, "access_tags", [])
                ],
            }
        )
    data = {
        "places": rows,
        "filters": filters,
        "flags": {"nearActive": near_active, "truncated": truncated},
        "urls": {"action": reverse("places_list"), "map": reverse("places_map")},
        "ui": {
            "title": _("Places"),
            "lead": _("A plain text list of venues — no map or JavaScript needed."),
            "toMap": _("Switch to the map"),
            "city": _("City"),
            "activity": _("Activity"),
            "filter": _("Filter"),
            "truncated": _("Showing the first 200 places — narrow with a city or activity filter."),
            "empty": _("No places match."),
            "accessMatch": _("Matches your access needs"),
            "limited": _("(limited)"),
        },
    }
    return spa_response(
        request,
        "places",
        data,
        title=_("Places (text list)"),
        public=True,
        seo={
            "description": _(
                "Parks, libraries and sports venues in Cluj-Napoca where people meet up "
                "for in-person group activities."
            ),
            "robots": "noindex, follow" if filtered else "",
            "structured_data": structured_data,
        },
        snapshot_template="web/snapshots/places.html",
        snapshot_context={"places": places},
    )


def _landing_link(area, activity_type) -> dict:
    return {
        "url": reverse("things_to_do", args=[area.slug, activity_type.slug]),
        # Same phrase the legacy templates build: "<activity> in <city>".
        "label": f"{activity_type.name} {_('in')} {area.name}",
    }


def landing_index_spa(request, *, grouped):
    data = {
        "cities": [
            {
                "name": area.name,
                "url": reverse("things_to_do_city", args=[area.slug]),
                "links": [_landing_link(area, t) for t in activities],
            }
            for area, activities in grouped
        ],
        "ui": {
            "title": _("Things to do"),
            "lead": _("Browse public venues and upcoming events by city and activity."),
            "empty": _("Nothing here yet — check back soon."),
        },
    }
    return spa_response(
        request,
        "things-index",
        data,
        title=_("Things to do"),
        public=True,
        seo={
            "description": _(
                "Find things to do in person — venues and upcoming events by city and "
                "activity, from sport to reading. A nonprofit, text-first way to meet up."
            ),
        },
        snapshot_template="web/snapshots/landing_index.html",
        snapshot_context={"grouped": grouped},
    )


def landing_city_spa(request, *, area, activities):
    title = _("Things to do in %(city)s") % {"city": area.name}
    data = {
        "city": area.name,
        "links": [_landing_link(area, t) for t in activities],
        "breadcrumbs": [
            {"name": _("Home"), "url": "/"},
            {"name": _("Things to do"), "url": reverse("things_to_do_index")},
            {"name": area.name, "url": None},
        ],
        "ui": {"title": title},
    }
    return spa_response(
        request,
        "things-city",
        data,
        title=title,
        public=True,
        seo={
            "description": _(
                "Venues and upcoming events in %(city)s — by activity. Find people and go, "
                "in person."
            )
            % {"city": area.name},
        },
        snapshot_template="web/snapshots/landing_city.html",
        snapshot_context={"area": area, "activities": activities},
    )


def landing_detail_spa(
    request, *, area, activity_type, places, events, structured_data, breadcrumb_data
):
    from apps.web.seo import place_path

    title = _("%(activity)s in %(city)s") % {"activity": activity_type.name, "city": area.name}
    data = {
        "city": area.name,
        "activity": activity_type.name,
        "events": [_event_row(e) for e in events],
        "places": [
            {"url": place_path(p), "name": p.display_name, "city": p.address_city or ""}
            for p in places
        ],
        "breadcrumbs": [
            {"name": _("Home"), "url": "/"},
            {"name": _("Things to do"), "url": reverse("things_to_do_index")},
            {"name": area.name, "url": reverse("things_to_do_city", args=[area.slug])},
            {"name": activity_type.name, "url": None},
        ],
        "urls": {
            "exploreCity": reverse("things_to_do_city", args=[area.slug]),
            "rss": f"{reverse('events_feed')}?activity={activity_type.slug}&area={area.slug}",
        },
        "ui": {
            "title": title,
            "upcoming": _("Upcoming events"),
            "places": _("Places"),
            "exploreCity": _("Browse other activities in %(city)s →") % {"city": area.name},
            "subscribe": _("Subscribe to this list (RSS)"),
        },
    }
    return spa_response(
        request,
        "things-detail",
        data,
        title=title,
        public=True,
        seo={
            "description": _(
                "Where to do %(activity)s in %(city)s — public venues and upcoming events "
                "you can join in person. Nonprofit, no ads, no tracking."
            )
            % {"activity": activity_type.name, "city": area.name},
            "structured_data": structured_data,
            "breadcrumb_data": breadcrumb_data,
        },
        snapshot_template="web/snapshots/landing_detail.html",
        snapshot_context={
            "area": area,
            "activity_type": activity_type,
            "places": places,
            "events": events,
        },
    )
