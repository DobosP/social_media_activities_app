from rest_framework import serializers

from .models import Donation


class DonationSerializer(serializers.ModelSerializer):
    class Meta:
        model = Donation
        fields = ["id", "amount_cents", "currency", "provider", "status", "created_at"]
        read_only_fields = fields


class StartDonationSerializer(serializers.Serializer):
    amount_cents = serializers.IntegerField(min_value=100)
    currency = serializers.CharField(max_length=3, required=False, default="EUR")
