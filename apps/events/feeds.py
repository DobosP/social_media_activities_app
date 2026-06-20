"""Event syndication feeds (RSS + Atom) — fast-crawled, freshness-signalling, aggregator-ready.

Lists only PUBLIC upcoming events via ``upcoming_events()`` (the F25 gate), optionally narrowed
by ``?area=<slug>`` and ``?activity=<slug>`` — the same filters as the web events list. No
people, no cohort data: events are venue happenings.
"""

from django.contrib.syndication.views import Feed
from django.urls import reverse
from django.utils.feedgenerator import Atom1Feed


class UpcomingEventsFeed(Feed):
    title = "Upcoming activities & events"
    description = "Public events at real places — soonest first."

    def link(self):
        return reverse("events_list")

    def get_object(self, request, *args, **kwargs):
        # Stash the optional filters; resolved against the gated queryset in items().
        return {
            "area": (request.GET.get("area") or "").strip(),
            "activity": (request.GET.get("activity") or "").strip(),
        }

    def items(self, obj):
        from apps.events.services import upcoming_events

        qs = upcoming_events().select_related("place", "activity_type")
        if obj.get("activity"):
            qs = qs.filter(activity_type__slug=obj["activity"])
        if obj.get("area"):
            from apps.communities.models import Area
            from apps.communities.services import _area_place_q

            area = Area.objects.filter(slug=obj["area"]).first()
            if area is not None:
                qs = qs.filter(_area_place_q(area))
        return qs.order_by("starts_at")[:100]

    def item_title(self, item):
        return item.title

    def item_description(self, item):
        return item.description or ""

    def item_link(self, item):
        from apps.web.seo import event_path

        return event_path(item)

    def item_pubdate(self, item):
        return item.created_at

    def item_updateddate(self, item):
        return item.updated_at


class UpcomingEventsAtomFeed(UpcomingEventsFeed):
    feed_type = Atom1Feed
    subtitle = UpcomingEventsFeed.description
