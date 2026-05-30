from django import forms

from apps.accounts.models import AgeBand
from apps.donations.models import Campaign
from apps.places.models import Place
from apps.safety.models import ReasonCode
from apps.social.models import Activity
from apps.social.serializers import LOGISTICS_FIELD_MAX_LENGTH
from apps.taxonomy.models import ActivityType


def _logistics_field(help_text=""):
    """An optional, length-capped logistics text field (F9), shared by the create and edit
    forms so the cap matches the serializer/model on both web paths."""
    return forms.CharField(
        required=False,
        max_length=LOGISTICS_FIELD_MAX_LENGTH,
        widget=forms.Textarea(attrs={"rows": 2}),
        help_text=help_text,
    )


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
    meeting_point = _logistics_field("Where exactly to meet (e.g. north gate by the fountain).")
    what_to_bring = _logistics_field("What members should bring.")
    organizer_note = _logistics_field("A short note for members.")
    cost_band = forms.ChoiceField(
        choices=Activity.CostBand.choices,
        required=False,
        initial=Activity.CostBand.UNSPECIFIED,
        help_text="Roughly what it costs to take part.",
    )
    difficulty = forms.ChoiceField(
        choices=Activity.Difficulty.choices,
        required=False,
        initial=Activity.Difficulty.UNSPECIFIED,
        help_text="How physically demanding it is.",
    )
    accessibility_notes = _logistics_field(
        "Accessibility info (step-free access, quiet space, etc.)."
    )
    beginners_welcome = forms.BooleanField(
        required=False,
        label="Beginners welcome",
        help_text="Tick if first-timers are explicitly welcome.",
    )

    def clean(self):
        cleaned = super().clean()
        starts, ends = cleaned.get("starts_at"), cleaned.get("ends_at")
        if starts and ends and ends < starts:
            self.add_error("ends_at", "End time cannot be before the start time.")
        # A ChoiceField(required=False) can yield "" — coerce to the sentinel so the model's
        # choices validation isn't bypassed (Activity.objects.create skips full_clean()).
        if not cleaned.get("cost_band"):
            cleaned["cost_band"] = Activity.CostBand.UNSPECIFIED
        if not cleaned.get("difficulty"):
            cleaned["difficulty"] = Activity.Difficulty.UNSPECIFIED
        return cleaned


class ActivityEditForm(forms.Form):
    """Edit an existing activity. Place and activity type are deliberately omitted: they
    are locked once the meetup exists (identity + cohort pin) — see
    social.services.ACTIVITY_EDITABLE_FIELDS."""

    title = forms.CharField(max_length=200)
    description = forms.CharField(widget=forms.Textarea(attrs={"rows": 4}), required=False)
    starts_at = _dt_field()
    ends_at = _dt_field(required=False)
    capacity = forms.IntegerField(required=False, min_value=1, help_text="Blank = unlimited.")
    meeting_point = _logistics_field("Where exactly to meet (e.g. north gate by the fountain).")
    what_to_bring = _logistics_field("What members should bring.")
    organizer_note = _logistics_field("A short note for members.")
    cost_band = forms.ChoiceField(
        choices=Activity.CostBand.choices,
        required=False,
        initial=Activity.CostBand.UNSPECIFIED,
        help_text="Roughly what it costs to take part.",
    )
    difficulty = forms.ChoiceField(
        choices=Activity.Difficulty.choices,
        required=False,
        initial=Activity.Difficulty.UNSPECIFIED,
        help_text="How physically demanding it is.",
    )
    accessibility_notes = _logistics_field(
        "Accessibility info (step-free access, quiet space, etc.)."
    )
    beginners_welcome = forms.BooleanField(
        required=False,
        label="Beginners welcome",
        help_text="Tick if first-timers are explicitly welcome.",
    )

    def clean(self):
        cleaned = super().clean()
        starts, ends = cleaned.get("starts_at"), cleaned.get("ends_at")
        if starts and ends and ends < starts:
            self.add_error("ends_at", "End time cannot be before the start time.")
        # A ChoiceField(required=False) can yield "" — coerce to the sentinel so the model's
        # choices validation isn't bypassed (Activity.objects.create skips full_clean()).
        if not cleaned.get("cost_band"):
            cleaned["cost_band"] = Activity.CostBand.UNSPECIFIED
        if not cleaned.get("difficulty"):
            cleaned["difficulty"] = Activity.Difficulty.UNSPECIFIED
        return cleaned


class PlaceProposeForm(forms.Form):
    """Add a venue OSM missed (F25). It stays pending until neighbours (or staff) confirm it."""

    name = forms.CharField(max_length=255, label="Place name")
    lat = forms.FloatField(min_value=-90, max_value=90, label="Latitude")
    lon = forms.FloatField(min_value=-180, max_value=180, label="Longitude")
    activity_type = forms.ModelChoiceField(
        queryset=ActivityType.objects.filter(is_active=True).order_by("name"),
        label="Main activity here",
    )
    allow_nearby = forms.BooleanField(
        required=False, label="Add anyway (a different place may already exist very close)"
    )


class PostForm(forms.Form):
    body = forms.CharField(
        widget=forms.Textarea(attrs={"rows": 3, "placeholder": "Write a message..."}), label=""
    )


class DonateForm(forms.Form):
    amount = forms.DecimalField(
        min_value=1, max_digits=8, decimal_places=2, label="Amount (EUR)", initial=10
    )
    # Earmark to an active campaign, or leave on the general fund. The queryset is set in
    # __init__ (per-request, not import time) so a campaign toggled inactive can't be picked.
    campaign = forms.ModelChoiceField(
        queryset=Campaign.objects.none(),
        required=False,
        empty_label="General fund (where it's needed most)",
        label="Direct your gift to",
    )

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.fields["campaign"].queryset = Campaign.objects.filter(is_active=True).order_by("title")


class ReportForm(forms.Form):
    reason = forms.ChoiceField(choices=ReasonCode.choices, label="Reason")
    detail = forms.CharField(
        widget=forms.Textarea(
            attrs={"rows": 3, "placeholder": "Anything that helps moderators..."}
        ),
        required=False,
        label="Details (optional)",
    )
