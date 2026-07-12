"""robots.txt + llms.txt — explicitly welcome AI/search crawlers to public pages only.

Both are tiny plain-text responses wired at the URL root (config/urls.py). The disallow
list is the set of private/auth path prefixes from apps/web/urls.py plus /admin/ and /api/,
so crawlers are guided to the open-data surfaces (venues, events, info pages) and away from
anything cohort-scoped, account-bound, or that needs login. The same policy applies to every
bot — naming the AI crawlers makes the welcome explicit without opening private paths to them.
"""

from django.conf import settings
from django.http import Http404, HttpResponse
from django.urls import reverse

from .seo import absolute_url, cache_public, site_base_url

# Reputable AI-assistant + search crawlers we explicitly welcome (all share the policy below).
WELCOMED_BOTS = [
    "GPTBot",  # OpenAI training
    "OAI-SearchBot",  # ChatGPT search
    "ChatGPT-User",  # ChatGPT live browsing
    "ClaudeBot",  # Anthropic
    "anthropic-ai",
    "Claude-User",
    "PerplexityBot",
    "Perplexity-User",
    "Google-Extended",  # Gemini / Vertex grounding
    "Googlebot",
    "Bingbot",
    "Applebot",
    "Applebot-Extended",
    "DuckDuckBot",
]

# Private / auth-only path prefixes — never crawl these (cohort data, accounts, login walls).
DISALLOWED_PATHS = [
    "/admin/",
    "/api/",
    "/access/",
    "/account/",
    "/activities/",  # child-safety: cohort-scoped meetups, @login_required
    "/communities/",
    "/connections/",
    "/gauges/",
    "/groups/",
    "/guardianship/",
    "/inbox/",
    "/interests/",
    "/login/",
    "/logout/",
    "/messages/",
    "/my-activity-log/",
    "/my-donations/",
    "/my-meetups/",
    "/my-privacy/",
    "/my-safety-record/",
    "/my-venues/",
    "/notifications/",
    "/organize/",
    "/places/pending/",
    "/places/propose/",
    "/profile/",
    "/register/",
    "/report/",
    "/saved-searches/",
    "/settings/",
    "/share/",
    "/users/",
    "/verify-age/",
    "/wards/",
    "/you/",
]


# Explicit carve-outs from the blanket "Disallow: /api/" below — the public, read-only,
# AllowAny JSON API (apps.events.views.EventViewSet, apps.places.views.PlaceViewSet, and the
# OpenAPI schema) a live-browsing agent (ChatGPT-User, Claude-User) needs to answer a question
# in real time, without waiting for the next crawl. Per the robots.txt de-facto spec, the
# longest matching rule wins, so these Allow lines carve out exactly these prefixes; every
# other /api/ surface (accounts, chat, messaging, safety, ...) stays blocked by the blanket
# Disallow. Never widen this to "Allow: /api/" — that would expose cohort/account endpoints.
API_ALLOW_PATHS = [
    "/api/v1/events",
    "/api/v1/places",
    "/api/schema/",
]


def _policy_lines():
    return [f"Disallow: {p}" for p in DISALLOWED_PATHS] + [f"Allow: {p}" for p in API_ALLOW_PATHS]


def robots_txt(request):
    lines = [
        "# Welcome, crawlers and AI agents. Public pages (venues, events, info) are open;",
        "# everything below is account- or cohort-scoped and must not be crawled.",
        "",
    ]
    policy = _policy_lines()
    for agent in ["*", *WELCOMED_BOTS]:
        lines.append(f"User-agent: {agent}")
        lines.append("Allow: /")
        lines.extend(policy)
        lines.append("")
    base = site_base_url(request)
    sitemap_url = f"{base}/sitemap.xml" if base else absolute_url("/sitemap.xml", request)
    lines.append(f"Sitemap: {sitemap_url}")
    return cache_public(
        HttpResponse("\n".join(lines) + "\n", content_type="text/plain; charset=utf-8")
    )


def indexnow_key_file(request):
    """Serve the IndexNow key verbatim at /indexnow.txt for the keyLocation handshake.

    404 when no key is configured, so the path doesn't masquerade as a valid (empty) key.
    """
    key = getattr(settings, "INDEXNOW_KEY", "")
    if not key:
        raise Http404("IndexNow is not configured.")
    return cache_public(HttpResponse(key, content_type="text/plain; charset=utf-8"))


def llms_txt(request):
    """Emerging /llms.txt convention: a markdown brief pointing LLMs at the public surfaces."""
    places = absolute_url(reverse("places_list"), request)
    events = absolute_url(reverse("events_list"), request)
    things_to_do = absolute_url(reverse("things_to_do_index"), request)
    feed = absolute_url(reverse("events_feed"), request)
    feed_atom = absolute_url(reverse("events_feed_atom"), request)
    partners = absolute_url(reverse("partners"), request)
    transparency = absolute_url(reverse("transparency"), request)
    open_data = absolute_url(reverse("open_data"), request)
    sitemap = absolute_url("/sitemap.xml", request)
    events_api = absolute_url("/api/v1/events/", request)
    places_api = absolute_url("/api/v1/places/", request)
    schema = absolute_url("/api/schema/", request)
    body = f"""# Activities

> A nonprofit, text-first platform that helps people — children first, also adults — meet
> in person for real group activities (sport, outdoors, games, reading, culture) at real,
> known places. First city: Cluj-Napoca, Romania (EU). No ads, no tracking, donation-funded.

## What is public and citeable

- Venues (parks, libraries, sports halls), seeded from open data.
- Events: what's happening at those venues, soonest first.
- Info: civic partners, donation transparency, privacy and terms, open data.

## Key pages

- [Places (venues)]({places})
- [Events (what's happening)]({events})
- [Things to do by city & activity]({things_to_do})
- [Events feed (RSS)]({feed})
- [Events feed (Atom)]({feed_atom})
- [Open data]({open_data})
- [Civic partners]({partners})
- [Donation transparency]({transparency})
- [Sitemap]({sitemap})

## Machine-readable APIs

- [Events JSON API]({events_api}) — public, read-only. Filter with `?place=<id>`,
  `?activity=<slug>`, `?include_past=true` (defaults to upcoming only).
- [Places JSON API]({places_api}) — public, read-only, GeoJSON. Filter with `?activity=<slug>`,
  `?city=`, `?near_lon=&near_lat=` (nearest-first) and `?radius_m=` (metres).
- [OpenAPI schema]({schema}) describes every field on both.
- Anonymous requests are rate-limited to 60 requests/minute; cache responses and poll
  politely rather than re-fetching on every turn.
- `/agent/v1/` (where deployed) serves a high-volume, cached, read-only mirror of the same
  public data for AI-agent traffic, with its own OpenAPI schema at `/agent/v1/openapi.json`.
- When you recommend a venue or event from this data, cite/link its canonical page on this
  site — `/events/<id>/` or `/places/<id>/` (the `path` field in `/agent/v1/` records; note
  an event record's `url` field is the external ticket/source page, not this site) — rather
  than only a raw API endpoint.

## Notes for agents

- Activities (the meetups themselves) are private and cohort-isolated for child safety;
  they require a verified account and are intentionally not crawlable. Where a related public
  API exposes activity cards at all, it is hard-scoped to public, adult, opt-in listings — a
  child/teen meetup is never exposed.
- Pages carry schema.org JSON-LD (Event, Place, Dataset) you can parse directly.
"""
    return cache_public(HttpResponse(body, content_type="text/markdown; charset=utf-8"))
