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


def form_errors(form) -> dict:
    """Bound-form errors as {field: [messages]} (incl. __all__) for a bootstrap payload.

    P3 form screens keep the classic POST flow: success redirects (flash message
    renders in the server chrome), failure re-renders the SPA shell with the same
    bootstrap plus these errors so React can mark the fields.
    """
    if form is None or not getattr(form, "is_bound", False):
        return {}
    return {field: [str(msg) for msg in msgs] for field, msgs in form.errors.items()}


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


# --- account & community screens (P3) -------------------------------------------
# Recon verdict: every POST on these surfaces redirects (flash in server chrome) —
# React renders classic forms with the payload csrf; no error marshalling needed.
# The nav builders below are the single source for account destinations
# (recon P1 problem #6): the /you and /settings screens and account tab strips
# render from them.


def _user_row(u) -> dict:
    return {"publicId": str(u.public_id), "name": u.display_name or u.username}


def account_nav(nav: dict) -> dict:
    """Grouped account destinations; labels are existing msgids (you.html/settings.html)."""
    groups = [
        {
            "title": _("Profile & preferences"),
            "links": [
                {"label": _("Your profile"), "url": reverse("profile")},
                {"label": _("Interests"), "url": reverse("interests")},
                {
                    "label": _("Your topics (tune suggestions)"),
                    "url": reverse("topic_preferences"),
                },
                {"label": _("Access needs"), "url": reverse("access_preferences")},
                {
                    "label": _("Display (theme, text size, motion)"),
                    "url": reverse("display_preferences"),
                },
                {"label": _("Age verification"), "url": reverse("verify_age")},
                {
                    "label": _("Notifications"),
                    "url": reverse("notifications"),
                    "pill": nav.get("unread_notifications") or 0,
                },
                {"label": _("Messages"), "url": reverse("messages")},
                *(
                    [{"label": _("Connections"), "url": reverse("connections")}]
                    if nav.get("connections_enabled")
                    else []
                ),
                {"label": _("Saved searches"), "url": reverse("saved_searches")},
                {"label": _("Notification settings"), "url": reverse("notification_preferences")},
            ],
        },
        {
            "title": _("Privacy & safety"),
            "links": [
                {"label": _("Privacy & your data"), "url": reverse("my_privacy")},
                {"label": _("Your safety record"), "url": reverse("safety_record")},
                {"label": _("Your activity log"), "url": reverse("activity_log")},
                {"label": _("Wards you look after"), "url": reverse("wards")},
                *(
                    [{"label": _("Your guardians"), "url": reverse("guardianship")}]
                    if nav.get("has_guardians")
                    else []
                ),
            ],
        },
        {
            "title": _("Giving"),
            "links": [
                {"label": _("My donations"), "url": reverse("my_donations")},
                {"label": _("Support this platform"), "url": reverse("donate")},
                {"label": _("Campaigns"), "url": reverse("campaigns")},
                {"label": _("Where the money goes"), "url": reverse("transparency")},
            ],
        },
    ]
    return {"groups": groups, "logoutAction": reverse("logout"), "logoutLabel": _("Log out")}


def you_tabs(nav: dict) -> list[dict]:
    return [
        {"label": _("Overview"), "url": reverse("you")},
        {"label": _("Profile"), "url": reverse("profile")},
        {"label": _("Interests"), "url": reverse("interests")},
        {"label": _("Display"), "url": reverse("display_preferences")},
        {"label": _("Privacy & data"), "url": reverse("my_privacy")},
        {"label": _("Donations"), "url": reverse("my_donations")},
        *(
            [{"label": _("Guardians"), "url": reverse("guardianship")}]
            if nav.get("has_guardians")
            else []
        ),
        {"label": _("Settings"), "url": reverse("settings")},
    ]


def you_spa(request, *, nav):
    user = request.user
    data = {
        "name": user.display_name or user.username,
        "username": user.username,
        "isGuardian": bool(getattr(user, "is_guardian", False)),
        "tabs": you_tabs(nav),
        "nav": account_nav(nav),
        "ui": {"title": _("Account & settings")},
    }
    return spa_response(request, "you", data, title=_("Account & settings"))


def settings_spa(request, *, nav, api_token_created, languages, current_language):
    data = {
        "tabs": you_tabs(nav),
        "nav": account_nav(nav),
        "language": {
            "action": reverse("set_language"),
            "next": request.path,
            "current": current_language,
            "options": [{"code": code, "name": name} for code, name in languages],
        },
        "apiToken": {
            "created": date_fmt(api_token_created, "D d M Y, H:i") if api_token_created else "",
            "revokeAction": reverse("api_token_revoke") if api_token_created else "",
        },
        "account": {
            "export": reverse("account_export"),
            "delete": reverse("account_delete"),
        },
        "ui": {
            "title": _("Settings"),
            "language": _("Language"),
            "languageHelp": _("Choose the language this site is shown in."),
            "save": _("Save"),
            "apiAccess": _("API access"),
            "revoke": _("Revoke API access"),
            "noToken": _("No device currently holds API access to your account."),
            "yourAccount": _("Your account"),
            "download": _("Download my data"),
            "delete": _("Delete my account"),
        },
    }
    return spa_response(request, "settings", data, title=_("Settings"))


def profile_spa(
    request,
    *,
    nav,
    avatar_url,
    can_participate,
    provenance,
    interests,
    blocked,
    connections,
    pending_in,
    progression,
    journey_avatar,
):
    user = request.user
    prov = None
    if provenance and provenance.get("has_row"):
        prov = {
            "isCurrent": bool(provenance.get("is_current")),
            "bandDisplay": provenance.get("band_display") or "",
            "provider": provenance.get("provider") or "",
            "method": provenance.get("method") or "",
            "verifiedAt": (
                date_fmt(provenance["verified_at"], "d M Y")
                if provenance.get("verified_at")
                else ""
            ),
            "expiresAt": (
                date_fmt(provenance["expires_at"], "d M Y") if provenance.get("expires_at") else ""
            ),
            "status": provenance.get("status") or "",
            "expiresSoon": bool(provenance.get("expires_soon")),
            "daysLeft": provenance.get("days_left"),
        }
    shown = list(connections[:8])
    data = {
        "name": user.display_name or user.username,
        "username": user.username,
        "ageBand": user.get_age_band_display() if getattr(user, "age_band", None) else "",
        "identityVerified": bool(getattr(user, "is_identity_verified", False)),
        "canParticipate": bool(can_participate),
        "avatarUrl": avatar_url or "",
        "journeyAvatar": journey_avatar or "",
        "progression": {
            "count": progression.get("count", 0),
            "level": progression.get("level", 0),
            "maxLevel": progression.get("max_level", 0),
        }
        if progression
        else None,
        "provenance": prov,
        "interests": list(interests),
        "connections": [_user_row(u) for u in shown],
        "connectionsTotal": len(connections),
        "pendingIncomingCount": len(pending_in),
        "blocked": [{"pk": b.pk, "name": b.display_name or b.username} for b in blocked],
        "tabs": you_tabs(nav),
        "actions": {
            "avatarUpload": reverse("avatar_upload"),
            "connectionMessage": reverse("connection_message"),
            "unblock": "/users/{pk}/unblock/",
            "verifyAge": reverse("verify_age"),
            "interestsEdit": reverse("interests"),
            "connections": reverse("connections"),
        },
        "ui": {
            "title": _("Profile"),
            "updatePhoto": _("Update photo"),
            "journey": _("Your journey"),
            "interests": _("Interests"),
            "edit": _("edit"),
            "connections": _("Connections"),
            "message": _("Message"),
            "pendingRequests": ngettext(
                "%(counter)s pending connection request",
                "%(counter)s pending connection requests",
                len(pending_in),
            )
            % {"counter": len(pending_in)},
            "review": _("review"),
            "seeAllConnections": _("See all %(total)s connections →") % {"total": len(connections)},
            "ageVerification": _("Age verification"),
            "verifiedAs": _("Verified as:"),
            "current": _("Current"),
            "reVerify": _("Re-verify"),
            "verifyEudi": _("Verify with EU Digital Identity"),
            "blocked": _("Blocked users"),
            "unblock": _("unblock"),
        },
    }
    return spa_response(request, "profile", data, title=_("Profile"))


def interests_spa(request, *, groups, chosen, chosen_count, starter):
    data = {
        "groups": [
            {
                "category": cat.name,
                "types": [
                    {"slug": t.slug, "name": t.name, "checked": t.slug in chosen} for t in types
                ],
            }
            for cat, types in groups
        ],
        "starter": [{"slug": t.slug, "name": t.name, "checked": t.slug in chosen} for t in starter],
        "chosenCount": chosen_count,
        "action": reverse("interests"),
        "ui": {
            "title": _("Your interests"),
            "starterHead": _("Popular near you right now"),
            "save": _("Save interests"),
        },
    }
    return spa_response(request, "interests", data, title=_("Your interests"))


def topics_spa(request, *, categories, chosen):
    data = {
        "topics": [
            {
                "slug": c.slug,
                "name": c.name,
                "description": c.description or "",
                "checked": c.slug in chosen,
            }
            for c in categories
        ],
        "action": reverse("topic_preferences"),
        "ui": {
            "title": _("Your topics"),
            "lean": _("Topics to lean toward"),
            "empty": _("No topics are available yet."),
            "save": _("Save topics"),
        },
    }
    return spa_response(request, "topics", data, title=_("Your topics"))


def access_spa(request, *, pref):
    fields = [
        ("needs_step_free", _("I need step-free access")),
        ("needs_accessible_toilet", _("I need an accessible toilet")),
        ("needs_hearing_loop", _("I need a hearing loop")),
        ("prefers_quiet", _("I prefer quiet / sensory-friendly places")),
    ]
    data = {
        "fields": [
            {"name": name, "label": label, "checked": bool(getattr(pref, name, False))}
            for name, label in fields
        ],
        "action": reverse("access_preferences"),
        "ui": {"title": _("Access preferences"), "save": _("Save preferences")},
    }
    return spa_response(request, "access", data, title=_("Access preferences"))


def notifications_spa(request, *, items):
    from apps.web.templatetags.safe_urls import safe_href

    data = {
        "items": [
            {
                "title": n.title,
                "body": n.body or "",
                "why": getattr(n, "why", "") or "",
                "when": date_fmt(n.created_at, "D d M, H:i"),
                "url": safe_href(n.url) or "",
                "unread": n.read_at is None,
            }
            for n in items
        ],
        "actions": {"readAll": reverse("notifications_read_all")},
        "urls": {"preferences": reverse("notification_preferences")},
        "ui": {
            "title": _("Notifications"),
            "markAllRead": _("Mark all read"),
            "settings": _("Notification settings"),
            "new": _("new"),
            "view": _("view"),
            "empty": _("No notifications yet."),
        },
    }
    return spa_response(request, "notifications", data, title=_("Notifications"))


def notification_preferences_spa(request, *, rows):
    data = {
        "rows": [
            {
                "value": r["value"],
                "label": r["label"],
                "reason": r["reason"],
                "muted": bool(r["muted"]),
            }
            for r in rows
        ],
        "action": reverse("notification_preferences"),
        "ui": {"title": _("Notification settings"), "save": _("Save settings")},
    }
    return spa_response(request, "notification-preferences", data, title=_("Notification settings"))


def connections_spa(
    request,
    *,
    connections,
    conn_page,
    conn_query,
    conn_total,
    incoming,
    outgoing,
    query,
    results,
):
    data = {
        "searchQuery": query,
        "results": [_user_row(u) for u in results],
        "incoming": [{"pk": c.pk, "user": _user_row(c.requester)} for c in incoming],
        "outgoing": [{"pk": c.pk, "user": _user_row(c.addressee)} for c in outgoing],
        "connections": [_user_row(u) for u in connections],
        "filterQuery": conn_query,
        "total": conn_total,
        "page": {
            "number": conn_page.number,
            "numPages": conn_page.paginator.num_pages,
            "previous": conn_page.previous_page_number() if conn_page.has_previous() else None,
            "next": conn_page.next_page_number() if conn_page.has_next() else None,
        },
        "actions": {
            "search": reverse("connections"),
            "request": reverse("connection_request"),
            # pk rides in the URL path for these two (see apps/web/urls.py) — the
            # screen fills the {pk} template, same pattern as unblock/saved-search delete.
            "respond": "/connections/{pk}/respond/",
            "withdraw": "/connections/{pk}/withdraw/",
            "message": reverse("connection_message"),
            "remove": reverse("connection_remove"),
        },
        "ui": {
            "title": _("Connections"),
            "searchLabel": _("Search people you've met"),
            "search": _("Search"),
            "resultsHead": _("Search results"),
            "connect": _("Connect"),
            "incoming": _("Requests to you"),
            "accept": _("Accept"),
            "decline": _("Decline"),
            "outgoing": _("Sent requests"),
            "pending": _("pending"),
            "withdraw": _("Withdraw"),
            "yours": _("Your connections"),
            "filterLabel": _("Filter your connections"),
            "filter": _("Filter"),
            "clear": _("clear"),
            "message": _("Message"),
            "remove": _("Remove"),
            "prev": _("Previous"),
            "next": _("Next"),
        },
    }
    return spa_response(request, "connections", data, title=_("Connections"))


def saved_searches_spa(request, *, items, activity_types, categories, cost_bands, coarse_windows):
    def item_row(s):
        what = s.activity_type.name if s.activity_type else (s.category.name if s.category else "")
        extras = []
        if getattr(s, "area", None):
            extras.append(s.area.name)
        if s.cost_band:
            extras.append(s.get_cost_band_display())
        if s.coarse_window:
            extras.append(s.get_coarse_window_display())
        if s.beginners:
            extras.append(_("beginners welcome"))
        return {"pk": s.pk, "what": what, "extras": extras}

    data = {
        "items": [item_row(s) for s in items],
        "options": {
            "activityTypes": [{"slug": t.slug, "name": t.name} for t in activity_types],
            "categories": [{"slug": c.slug, "name": c.name} for c in categories],
            "costBands": [{"value": v, "label": str(label)} for v, label in cost_bands],
            "coarseWindows": [{"value": v, "label": str(label)} for v, label in coarse_windows],
        },
        "actions": {
            "create": reverse("saved_search_create"),
            "delete": "/saved-searches/{pk}/delete/",
        },
        "next": request.path,
        "ui": {
            "title": _("Saved searches"),
            "createHead": _("Save a new search"),
            "activityType": _("Activity type"),
            "orCategory": _("...or a whole category"),
            "city": _("City (optional)"),
            "cost": _("Cost (optional)"),
            "when": _("When (optional)"),
            "beginners": _("Beginners-welcome activities only"),
            "save": _("Save search"),
            "yours": _("Your saved searches"),
            "remove": _("Remove"),
        },
    }
    return spa_response(request, "saved-searches", data, title=_("Saved searches"))


def communities_spa(request, *, page, groups_page, can_create):
    def page_dict(p):
        return {
            "number": p.number,
            "numPages": p.paginator.num_pages,
            "previous": p.previous_page_number() if p.has_previous() else None,
            "next": p.next_page_number() if p.has_next() else None,
        }

    data = {
        "groups": [
            {
                "pk": g.pk,
                "url": reverse("group_detail", args=[g.pk]),
                "title": g.title,
                "type": g.activity_type.name if g.activity_type else "",
                "category": g.category.name if getattr(g, "category", None) else "",
                "area": g.area.name if getattr(g, "area", None) else "",
                "description": truncatewords(g.description, 22) if g.description else "",
            }
            for g in groups_page.object_list
        ],
        "communities": [
            {
                "slug": c.slug,
                "url": reverse("community_detail", args=[c.slug]),
                "name": c.name,
                "tier": c.tier or "",
                "category": c.category.name if getattr(c, "category", None) else "",
                "area": c.area.name if getattr(c, "area", None) else "",
            }
            for c in page.object_list
        ],
        "pages": {"communities": page_dict(page), "groups": page_dict(groups_page)},
        "canCreate": bool(can_create),
        "urls": {
            "graph": reverse("community_graph"),
            "createGroup": reverse("group_create"),
            "action": reverse("communities"),
        },
        "ui": {
            "title": _("Communities & groups"),
            "graph": _("Explore as a 3D graph"),
            "startGroup": _("+ Start a group"),
            "groupsHead": _("Groups — join and belong"),
            "groupsEmpty": _("No groups here yet."),
            "startFirst": _("Start the first one."),
            "communitiesHead": _("Around your city"),
            "prev": _("Previous"),
            "next": _("Next"),
        },
    }
    return spa_response(request, "communities", data, title=_("Communities & groups"))


def community_detail_spa(request, *, community, activities, linked_group):
    type_name = (
        community.activity_type.name
        if community.activity_type
        else (community.category.name if getattr(community, "category", None) else "")
    )
    data = {
        "name": community.name,
        "lead": _("Upcoming %(type_name)s activities in %(area)s — soonest first.")
        % {"type_name": type_name, "area": community.area.name if community.area else ""},
        "linkedGroup": (
            {
                "url": reverse("group_detail", args=[linked_group.pk]),
                "label": _("Join the standing group: %(title)s") % {"title": linked_group.title},
            }
            if linked_group
            else None
        ),
        "cards": [activity_card(a, request.user) for a in activities],
        "urls": {"communities": reverse("communities"), "organizeNew": reverse("activity_create")},
        "ui": {
            "back": _("Communities"),
            "empty": _("No upcoming activities here right now."),
            "organise": _("Organise one"),
        },
    }
    return spa_response(request, "community-detail", data, title=community.name)
