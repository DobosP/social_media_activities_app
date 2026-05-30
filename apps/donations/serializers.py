from rest_framework import serializers

from .models import Campaign, Donation


class DonationSerializer(serializers.ModelSerializer):
    class Meta:
        model = Donation
        fields = [
            "id",
            "amount_cents",
            "currency",
            "recurring",
            "campaign",
            "provider",
            "status",
            "created_at",
        ]
        read_only_fields = fields


class StartDonationSerializer(serializers.Serializer):
    amount_cents = serializers.IntegerField(min_value=100)
    currency = serializers.CharField(max_length=3, required=False, default="EUR")
    recurring = serializers.BooleanField(required=False, default=False)
    # Only an ACTIVE campaign may be earmarked — an inactive/invalid id is a 400 here, the first
    # of the three inactive-campaign guard layers (serializer / start_donation / form).
    campaign = serializers.PrimaryKeyRelatedField(
        queryset=Campaign.objects.filter(is_active=True), required=False, allow_null=True
    )


class DonationWebhookSerializer(serializers.Serializer):
    external_ref = serializers.CharField(max_length=128)
