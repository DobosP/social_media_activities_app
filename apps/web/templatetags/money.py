"""Money formatting for templates (F29). The single cents->display conversion point: views
pass INTEGER cents everywhere, and the edge formats to a 2-decimal string here."""

from django import template

register = template.Library()


@register.filter(name="cents")
def cents(value) -> str:
    """Format integer cents as a plain 2-decimal amount, e.g. 1234 -> '12.34'. None/'' -> '0.00'.
    No thousands separators, no currency symbol (the template labels the currency)."""
    try:
        return f"{(value or 0) / 100:.2f}"
    except (TypeError, ValueError):
        return "0.00"
