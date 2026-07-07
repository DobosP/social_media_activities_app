import django_filters
from django.db.models import BooleanField, Case, Exists, OuterRef, Q, Value, When
from django.utils import timezone

from .models import Place


def _activity_statuses_for_upcoming():
    from apps.social.models import Activity

    statuses = [Activity.Status.OPEN]
    scheduled = getattr(Activity.Status, "SCHEDULED", None)
    if scheduled is not None:
        statuses.append(scheduled)
    return statuses


def annotate_has_upcoming(qs, user=None):
    """Annotate places with public/viewer-visible upcoming activity or event presence."""
    from apps.events.models import Event
    from apps.social import services as social

    now = timezone.now()
    if getattr(user, "is_authenticated", False):
        activity_qs = social.visible_activities(user)
    else:
        # Public map/API data must not expose private or minor-cohort activities.
        activity_qs = social.public_activities()
    activity_qs = activity_qs.filter(
        place_id=OuterRef("pk"),
        status__in=_activity_statuses_for_upcoming(),
        starts_at__gte=now,
    ).order_by()
    event_qs = Event.objects.filter(place_id=OuterRef("pk"), starts_at__gte=now).order_by()
    return qs.annotate(
        has_upcoming_activity=Exists(activity_qs),
        has_upcoming_event=Exists(event_qs),
    ).annotate(
        has_upcoming=Case(
            When(has_upcoming_activity=True, then=Value(True)),
            When(has_upcoming_event=True, then=Value(True)),
            default=Value(False),
            output_field=BooleanField(),
        )
    )


class PlaceFilter(django_filters.FilterSet):
    activity = django_filters.CharFilter(method="filter_activity")
    category = django_filters.CharFilter(method="filter_category")
    has_upcoming = django_filters.BooleanFilter(method="filter_has_upcoming")
    city = django_filters.CharFilter(field_name="address_city", lookup_expr="iexact")
    source = django_filters.CharFilter(field_name="source", lookup_expr="iexact")
    min_confidence = django_filters.NumberFilter(
        field_name="place_activities__confidence", lookup_expr="gte"
    )

    def _min_confidence(self):
        if not self.request:
            return None
        raw = self.request.GET.get("min_confidence")
        if raw in (None, ""):
            return None
        try:
            return float(raw)
        except (TypeError, ValueError):
            return None

    def filter_activity(self, qs, name, value):
        # F26: a place matches ?activity only via a NON-disputed edge (conjoined so the SAME
        # edge must satisfy both conditions, not two different edges).
        filters = {
            "place_activities__activity__slug__iexact": value,
            "place_activities__is_disputed": False,
        }
        min_confidence = self._min_confidence()
        if min_confidence is not None:
            filters["place_activities__confidence__gte"] = min_confidence
        return qs.filter(**filters)

    def filter_category(self, qs, name, value):
        # Match top-level taxonomy category slugs. Seeded subcategories are one level deep
        # (e.g. team_sport -> sport), so the same edge must either be in the category or in
        # one of its direct child categories. ONE .filter() call so is_disputed and the
        # category clause bind to the SAME edge row (chained filters on a to-many relation
        # would let a disputed edge satisfy one clause and a different edge the other);
        # distinct() because several edges of one place can share a top-level category.
        edge_filter = Q(place_activities__is_disputed=False) & (
            Q(place_activities__activity__category__slug__iexact=value)
            | Q(place_activities__activity__category__parent__slug__iexact=value)
        )
        min_confidence = self._min_confidence()
        if min_confidence is not None:
            edge_filter &= Q(place_activities__confidence__gte=min_confidence)
        return qs.filter(edge_filter).distinct()

    def filter_has_upcoming(self, qs, name, value):
        if "has_upcoming" not in qs.query.annotations:
            qs = annotate_has_upcoming(qs, getattr(self.request, "user", None))
        return qs.filter(has_upcoming=value)

    class Meta:
        model = Place
        fields = [
            "activity",
            "category",
            "has_upcoming",
            "city",
            "source",
            "min_confidence",
        ]
