from django import forms

from apps.accounts.models import AgeBand, Cohort
from apps.donations.models import Campaign
from apps.places.models import Place
from apps.safety.models import ReasonCode
from apps.social.models import Activity, ActivitySeries
from apps.social.serializers import (
    ACTIVITY_DESCRIPTION_MAX_LENGTH,
    LOGISTICS_FIELD_MAX_LENGTH,
    POST_BODY_MAX_LENGTH,
)
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
    # The queryset is narrowed to public_places() in __init__ (F25): a still-pending/rejected
    # user-proposed venue must never be offered, and a tampered POST carrying a pending place id
    # must fail form validation. Start from .none() so the field is never accidentally open.
    place = forms.ModelChoiceField(queryset=Place.objects.none())
    activity_type = forms.ModelChoiceField(
        queryset=ActivityType.objects.filter(is_active=True).order_by("name")
    )
    title = forms.CharField(max_length=200)
    description = forms.CharField(widget=forms.Textarea(attrs={"rows": 4}), required=False)
    starts_at = _dt_field()
    ends_at = _dt_field(required=False)
    capacity = forms.IntegerField(required=False, min_value=1, help_text="Blank = unlimited.")
    min_to_go = forms.IntegerField(
        required=False,
        min_value=1,
        label="Minimum to happen",
        help_text="Runs only if at least this many say they're going. Blank = no minimum.",
    )
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
    # F29: CHILD owners only (dropped below for everyone else). Requires the owner's own verified
    # guardian to join as a read-only supervisor before any join settles.
    supervised = forms.BooleanField(
        required=False,
        label="Require a supervising guardian",
        help_text=(
            "Your parent/guardian must join as a read-only supervisor before anyone is admitted."
        ),
    )

    def __init__(self, *args, user=None, **kwargs):
        super().__init__(*args, **kwargs)
        from apps.places.services import public_places

        # Only publicly-visible places (F25 chokepoint), ordered for the dropdown + the map picker.
        self.fields["place"].queryset = public_places(Place.objects.order_by("name"))
        # The supervised pin is meaningful only for a CHILD owner — hide it for everyone else so
        # the form never offers an option create_activity would reject.
        if user is None or getattr(user, "cohort", None) != Cohort.CHILD:
            self.fields.pop("supervised", None)

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


class SeriesForm(forms.Form):
    """Create a recurring activity series (F4). Mirrors ActivityForm; adds a cadence and uses
    first_starts_at for the first instance's time. place is narrowed to public_places() like
    ActivityForm, so a pending/tampered place id fails validation identically."""

    place = forms.ModelChoiceField(queryset=Place.objects.none())
    activity_type = forms.ModelChoiceField(
        queryset=ActivityType.objects.filter(is_active=True).order_by("name")
    )
    title = forms.CharField(max_length=200)
    description = forms.CharField(widget=forms.Textarea(attrs={"rows": 4}), required=False)
    cadence = forms.ChoiceField(
        choices=ActivitySeries.Cadence.choices, help_text="How often the meetup repeats."
    )
    first_starts_at = _dt_field()
    ends_at = _dt_field(required=False)
    capacity = forms.IntegerField(required=False, min_value=1, help_text="Blank = unlimited.")
    min_to_go = forms.IntegerField(
        required=False,
        min_value=1,
        label="Minimum to happen",
        help_text="Runs only if at least this many say they're going. Blank = no minimum.",
    )
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
    # F29: CHILD owners only (dropped below otherwise). Each spawned instance requires the owner's
    # guardian to be seated as a read-only supervisor before anyone is admitted.
    supervised = forms.BooleanField(
        required=False,
        label="Require a supervising guardian",
        help_text="Each meetup needs your parent/guardian to join as a read-only supervisor.",
    )

    def __init__(self, *args, user=None, **kwargs):
        super().__init__(*args, **kwargs)
        from apps.places.services import public_places

        self.fields["place"].queryset = public_places(Place.objects.order_by("name"))
        if user is None or getattr(user, "cohort", None) != Cohort.CHILD:
            self.fields.pop("supervised", None)

    def clean(self):
        cleaned = super().clean()
        starts, ends = cleaned.get("first_starts_at"), cleaned.get("ends_at")
        if starts and ends and ends < starts:
            self.add_error("ends_at", "End time cannot be before the start time.")
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
    min_to_go = forms.IntegerField(
        required=False,
        min_value=1,
        label="Minimum to happen",
        help_text="Runs only if at least this many say they're going. Blank = no minimum.",
    )
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
    # body is optional ONLY when an attachment is present (an image/PDF-only message); the view
    # enforces "text or attachment". max_length closes the divergence where the durable thread
    # surface was the only uncapped one (the API + the MessagePolicy already cap at this length).
    body = forms.CharField(
        required=False,
        max_length=POST_BODY_MAX_LENGTH,
        widget=forms.Textarea(attrs={"rows": 3, "placeholder": "Write a message..."}),
        label="",
    )
    # Optional one-level quote-reply target (a Post id in the same thread). Re-validated in the
    # service (same thread, not hidden, re-parented to the top-level ancestor).
    reply_to = forms.IntegerField(required=False, widget=forms.HiddenInput)
    # Optional photo or PDF shared in the thread (members only; scanned fail-closed). PDFs are
    # adults-only and always served as a download. No video.
    attachment = forms.FileField(required=False)
    # @mentions are always a calm highlight (tag-not-ping). Ticking this opt-in escalates them to
    # a notification to the mentioned peers (still mutable by each recipient). Default off.
    ping = forms.BooleanField(required=False)
    # Optional "temporary picture": how long an attached image/PDF stays before it disappears.
    # "" = keep permanently (default). The service clamps the TTL UP to the cohort floor (24h for
    # minors), so a too-short value here can never make media vanish faster than that floor.
    disappear = forms.ChoiceField(
        required=False,
        choices=[
            ("", "Keep in the thread"),
            ("3600", "Disappear after 1 hour"),
            ("86400", "Disappear after 1 day"),
            ("604800", "Disappear after 1 week"),
        ],
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


class GroupCreateForm(forms.Form):
    """Create a standing group from a city + an activity type. The cohort is pinned by the service
    (to the creator's cohort for a self-created adult group). The optional cohort select is honoured
    only for staff creating a MINOR group; the service rejects a non-staff cross-cohort attempt."""

    city = forms.CharField(max_length=128)
    activity_type = forms.ModelChoiceField(
        queryset=ActivityType.objects.filter(is_active=True).order_by("name")
    )
    title = forms.CharField(max_length=200)
    description = forms.CharField(
        required=False,
        max_length=ACTIVITY_DESCRIPTION_MAX_LENGTH,
        widget=forms.Textarea(attrs={"rows": 3}),
    )
    # Staff-only: create a group for a MINOR cohort. Blank = the creator's own cohort.
    cohort = forms.ChoiceField(
        required=False,
        choices=[
            ("", "My own cohort"),
            ("child", "Child (under 16) — staff only"),
            ("teen", "Teen (16-17) — staff only"),
        ],
    )
