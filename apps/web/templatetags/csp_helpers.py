"""Template helpers for CSP-compatible script data islands."""

from django import template
from django.utils.html import conditional_escape, json_script
from django.utils.safestring import mark_safe

register = template.Library()


@register.simple_tag(takes_context=True)
def nonced_json_script(context, value, element_id):
    """Render Django's safe JSON script block with the current request CSP nonce."""
    html = json_script(value, element_id)
    request = context.get("request")
    nonce = getattr(request, "csp_nonce", None)
    if nonce is None:
        return html
    nonce_attr = conditional_escape(str(nonce))
    return mark_safe(html.replace("<script ", f'<script nonce="{nonce_attr}" ', 1))
