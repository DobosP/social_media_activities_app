"""Template context processors for the web UI.

F12 display preferences: the dark/high-contrast theme, text size, and reduced-motion choice live in
functional cookies (no per-user model, no login required — they apply to everyone, signed in or
not). This processor reads + validates them for EVERY request and exposes the values that base.html
stamps onto <html> (data-theme / data-motion / --scale). An unset or tampered cookie falls back to a
safe default ("auto" → honour the OS preference)."""

# Allowlisted cookie values — anything else is ignored (falls back to the default).
THEME_COOKIE = "display_theme"
TEXT_COOKIE = "display_text"
MOTION_COOKIE = "display_motion"

THEMES = ("auto", "light", "dark", "contrast")
TEXT_SIZES = ("normal", "large", "larger")
MOTIONS = ("auto", "reduce", "full")

# Text size -> rem-base multiplier applied via the --scale custom property.
_SCALE = {"normal": "1", "large": "1.15", "larger": "1.3"}


def _pick(value, allowed, default):
    return value if value in allowed else default


def display_preferences(request):
    theme = _pick(request.COOKIES.get(THEME_COOKIE), THEMES, "auto")
    text = _pick(request.COOKIES.get(TEXT_COOKIE), TEXT_SIZES, "normal")
    motion = _pick(request.COOKIES.get(MOTION_COOKIE), MOTIONS, "auto")
    return {
        "display_theme": theme,
        "display_text": text,
        "display_motion": motion,
        "display_scale": _SCALE[text],
    }


def seo(request):
    """Canonical URL + site name for the <head> (canonical link + OpenGraph defaults).

    The canonical URL drops the query string so faceted/filtered variants of a public page
    all point at one indexable URL. Built via the SITE_BASE_URL-aware helper so a custom
    domain flips every canonical link with one env var.
    """
    from django.conf import settings
    from django.utils.translation import get_language

    from .seo import absolute_url

    # OpenGraph locale from the active language (ro/en today; falls back to en_US). Helps social
    # previews + crawlers tag the page's language without a per-language URL scheme.
    lang = (get_language() or "en").split("-")[0]
    og_locale = {"ro": "ro_RO", "en": "en_US"}.get(lang, "en_US")
    return {
        "canonical_url": absolute_url(request.path, request),
        "site_name": getattr(settings, "SITE_NAME", "Activities"),
        "og_locale": og_locale,
        "google_site_verification": getattr(settings, "GOOGLE_SITE_VERIFICATION", ""),
        "bing_site_verification": getattr(settings, "BING_SITE_VERIFICATION", ""),
    }
