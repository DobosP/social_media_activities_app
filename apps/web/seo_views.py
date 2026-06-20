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


def _policy_lines():
    return [f"Disallow: {p}" for p in DISALLOWED_PATHS]


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
    partners = absolute_url(reverse("partners"), request)
    transparency = absolute_url(reverse("transparency"), request)
    sitemap = absolute_url("/sitemap.xml", request)
    body = f"""# Activities

> A nonprofit, text-first platform that helps people — children first, also adults — meet
> in person for real group activities (sport, outdoors, games, reading, culture) at real,
> known places. First city: Cluj-Napoca, Romania (EU). No ads, no tracking, donation-funded.

## What is public and citeable

- Venues (parks, libraries, sports halls), seeded from open data.
- Events: what's happening at those venues, soonest first.
- Info: civic partners, donation transparency, privacy and terms.

## Key pages

- [Places (venues)]({places})
- [Events (what's happening)]({events})
- [Things to do by city & activity]({things_to_do})
- [Events feed (RSS)]({feed})
- [Civic partners]({partners})
- [Donation transparency]({transparency})
- [Sitemap]({sitemap})

## Notes for agents

- Activities (the meetups themselves) are private and cohort-isolated for child safety;
  they require a verified account and are intentionally not crawlable.
- Pages carry schema.org JSON-LD (Event, Place) you can parse directly.
"""
    return cache_public(HttpResponse(body, content_type="text/markdown; charset=utf-8"))
