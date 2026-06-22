"""Template filters for canonical, keyword-rich place/event URLs (SEO slugs).

Linking via these gives crawlers the keyword-rich slugged path directly. place_detail serves any
form at 200 with a canonical <link> (it never 301-redirects), so the rich path is simply the
better internal link to publish — crawlers land on it without relying on a canonical hop.
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
