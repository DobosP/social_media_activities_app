from rest_framework import serializers

from .models import ActivityCategory, ActivityRelation, ActivityType


class ActivityRelationSerializer(serializers.ModelSerializer):
    target = serializers.SlugRelatedField(slug_field="slug", read_only=True)

    class Meta:
        model = ActivityRelation
        fields = ["target", "kind", "symmetric", "note"]


class ActivityTypeSerializer(serializers.ModelSerializer):
    category = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    parent = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    related = ActivityRelationSerializer(source="relations_out", many=True, read_only=True)

    class Meta:
        model = ActivityType
        fields = [
            "slug",
            "name",
            "category",
            "parent",
            "aliases",
            "is_active",
            "wellness",
            "family_friendly",
            "related",
        ]


class ActivityCategorySerializer(serializers.ModelSerializer):
    parent = serializers.SlugRelatedField(slug_field="slug", read_only=True)

    class Meta:
        model = ActivityCategory
        fields = ["slug", "name", "parent", "description"]
