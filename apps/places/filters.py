import django_filters

from .models import Place


class PlaceFilter(django_filters.FilterSet):
    activity = django_filters.CharFilter(
        field_name="place_activities__activity__slug", lookup_expr="iexact"
    )
    city = django_filters.CharFilter(field_name="address_city", lookup_expr="iexact")
    source = django_filters.CharFilter(field_name="source", lookup_expr="iexact")
    min_confidence = django_filters.NumberFilter(
        field_name="place_activities__confidence", lookup_expr="gte"
    )

    class Meta:
        model = Place
        fields = ["activity", "city", "source", "min_confidence"]
