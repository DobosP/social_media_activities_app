import django_filters

from .models import Place


class PlaceFilter(django_filters.FilterSet):
    activity = django_filters.CharFilter(method="filter_activity")
    city = django_filters.CharFilter(field_name="address_city", lookup_expr="iexact")
    source = django_filters.CharFilter(field_name="source", lookup_expr="iexact")
    min_confidence = django_filters.NumberFilter(
        field_name="place_activities__confidence", lookup_expr="gte"
    )

    def filter_activity(self, qs, name, value):
        # F26: a place matches ?activity only via a NON-disputed edge (conjoined so the SAME
        # edge must satisfy both conditions, not two different edges).
        return qs.filter(
            place_activities__activity__slug__iexact=value,
            place_activities__is_disputed=False,
        )

    class Meta:
        model = Place
        fields = ["activity", "city", "source", "min_confidence"]
