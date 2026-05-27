from rest_framework import serializers

from apps.places.models import Place
from apps.social.models import Activity

from .models import Booking


class BookingSerializer(serializers.ModelSerializer):
    class Meta:
        model = Booking
        fields = [
            "id",
            "place",
            "activity",
            "provider",
            "external_ref",
            "status",
            "starts_at",
            "ends_at",
            "party_size",
            "deep_link",
            "created_at",
        ]
        read_only_fields = fields


class CreateBookingSerializer(serializers.Serializer):
    place = serializers.PrimaryKeyRelatedField(queryset=Place.objects.all())
    activity = serializers.PrimaryKeyRelatedField(
        queryset=Activity.objects.all(), required=False, allow_null=True
    )
    starts_at = serializers.DateTimeField()
    ends_at = serializers.DateTimeField(required=False, allow_null=True)
    party_size = serializers.IntegerField(min_value=1, default=1)
    provider = serializers.CharField(max_length=32, required=False, allow_blank=True)
