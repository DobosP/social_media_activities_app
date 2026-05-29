from django import forms

from apps.accounts.models import AgeBand
from apps.places.models import Place
from apps.safety.models import ReasonCode
from apps.taxonomy.models import ActivityType

# Demo age assurance for the web sign-up. In production the real EU age-verification /
# EUDI flow (apps/accounts) replaces this; the user does not self-declare.
BAND_CHOICES = [
    (AgeBand.ADULT, "Adult (18+)"),
    (AgeBand.AGE_16_17, "16-17"),
    (AgeBand.UNDER_16, "Under 16 (needs parental consent)"),
]

_DT_FORMATS = ["%Y-%m-%dT%H:%M", "%Y-%m-%dT%H:%M:%S"]


def _dt_field(*, required=True):
    return forms.DateTimeField(
        required=required,
        input_formats=_DT_FORMATS,
        widget=forms.DateTimeInput(attrs={"type": "datetime-local"}, format="%Y-%m-%dT%H:%M"),
    )


class RegisterForm(forms.Form):
    username = forms.CharField(max_length=150)
    display_name = forms.CharField(max_length=120, required=False)
    password = forms.CharField(widget=forms.PasswordInput, min_length=8)
    age_band = forms.ChoiceField(
        choices=BAND_CHOICES,
        label="Age (demo age assurance)",
        help_text="Stands in for the EU age-verification flow in this demo.",
    )


class ActivityForm(forms.Form):
    place = forms.ModelChoiceField(queryset=Place.objects.order_by("name"))
    activity_type = forms.ModelChoiceField(
        queryset=ActivityType.objects.filter(is_active=True).order_by("name")
    )
    title = forms.CharField(max_length=200)
    description = forms.CharField(widget=forms.Textarea(attrs={"rows": 4}), required=False)
    starts_at = _dt_field()
    ends_at = _dt_field(required=False)
    capacity = forms.IntegerField(required=False, min_value=1, help_text="Blank = unlimited.")

    def clean(self):
        cleaned = super().clean()
        starts, ends = cleaned.get("starts_at"), cleaned.get("ends_at")
        if starts and ends and ends < starts:
            self.add_error("ends_at", "End time cannot be before the start time.")
        return cleaned


class PostForm(forms.Form):
    body = forms.CharField(
        widget=forms.Textarea(attrs={"rows": 3, "placeholder": "Write a message..."}), label=""
    )


class DonateForm(forms.Form):
    amount = forms.DecimalField(
        min_value=1, max_digits=8, decimal_places=2, label="Amount (EUR)", initial=10
    )


class ReportForm(forms.Form):
    reason = forms.ChoiceField(choices=ReasonCode.choices, label="Reason")
    detail = forms.CharField(
        widget=forms.Textarea(
            attrs={"rows": 3, "placeholder": "Anything that helps moderators..."}
        ),
        required=False,
        label="Details (optional)",
    )
