"""Inject the Vite-built SPA entry (hashed JS + CSS) with the request's CSP nonce.

`vite build` (frontend/) writes hashed assets plus `.vite/manifest.json` into
static/frontend/. This tag resolves an entry (e.g. ``src/main.tsx``) through the
manifest and emits the matching ``<script type="module" nonce>`` and
``<link rel="stylesheet">`` tags. The manifest is read from the source static
dir (present in dev and baked into the image before collectstatic) and cached
per-process, invalidated by mtime so a rebuild is picked up without a restart.
"""

import json
from pathlib import Path

from django import template
from django.conf import settings
from django.templatetags.static import static
from django.utils.html import format_html
from django.utils.safestring import SafeString, mark_safe

register = template.Library()

_MANIFEST_PATH = Path(settings.BASE_DIR) / "static" / "frontend" / ".vite" / "manifest.json"
_cache: dict[str, object] = {"mtime": None, "manifest": None}


def _manifest() -> dict | None:
    try:
        mtime = _MANIFEST_PATH.stat().st_mtime
    except OSError:
        return None
    if _cache["mtime"] != mtime:
        with open(_MANIFEST_PATH, encoding="utf-8") as fh:
            _cache["manifest"] = json.load(fh)
        _cache["mtime"] = mtime
    return _cache["manifest"]


@register.simple_tag(takes_context=True)
def spa_entry(context, entry: str = "src/main.tsx") -> SafeString:
    """Emit nonce'd <script>/<link> tags for a built SPA entry point."""
    manifest = _manifest()
    if manifest is None or entry not in manifest:
        # Frontend not built (e.g. backend-only dev). Fail visibly in templates
        # that need it rather than breaking every page.
        return mark_safe(
            "<!-- spa_entry: frontend build missing; run `npm run build` in frontend/ -->"
        )
    chunk = manifest[entry]
    nonce = getattr(context.get("request"), "csp_nonce", "")
    parts = [
        format_html('<link rel="stylesheet" href="{}">', static(f"frontend/{css}"))
        for css in chunk.get("css", [])
    ]
    parts.append(
        format_html(
            '<script type="module" src="{}" nonce="{}"></script>',
            static(f"frontend/{chunk['file']}"),
            nonce,
        )
    )
    return mark_safe("".join(parts))
