"""Template filters for canonical, keyword-rich place/event URLs (SEO slugs).

Linking via these avoids the 301 hop that a bare ``{% url 'place_detail' pk %}`` would take, so
crawlers following internal links land straight on the canonical slugged path.
"""

from django import template

from apps.web.seo import event_path, place_path

register = template.Library()


@register.filter
def place_url(place):
    return place_path(place)


@register.filter
def event_url(event):
    return event_path(event)
