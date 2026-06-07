from rest_framework import status, viewsets
from rest_framework.exceptions import NotFound, PermissionDenied, ValidationError
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response

from . import services
from .serializers import SavedSearchCreateSerializer, SavedSearchSerializer


class SavedSearchViewSet(viewsets.ViewSet):
    """Owner-scoped saved searches (F3). Authenticated-only (NEVER AllowAny); every read routes
    through services.saved_searches_for(request.user) — there is deliberately NO class-level
    ``queryset``, so a row can't be retrieved/deleted by id-guessing. Save-only (no suggestions
    feed); the serializer exposes no counters."""

    permission_classes = [IsAuthenticated]
    lookup_value_regex = r"[0-9]+"

    def _get(self, request, pk):
        ss = services.saved_searches_for(request.user).filter(pk=pk).first()
        if ss is None:
            raise NotFound("No such saved search.")
        return ss

    def list(self, request):
        data = SavedSearchSerializer(services.saved_searches_for(request.user), many=True).data
        return Response(data)

    def retrieve(self, request, pk=None):
        return Response(SavedSearchSerializer(self._get(request, pk)).data)

    def create(self, request):
        serializer = SavedSearchCreateSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        try:
            ss = services.create_saved_search(
                request.user,
                activity_type=data.get("activity_type"),
                category=data.get("category"),
                city=(data.get("city") or "").strip(),
                beginners=data.get("beginners", False),
                cost_band=data.get("cost_band", ""),
            )
        except services.NotEligible as exc:
            raise PermissionDenied(str(exc)) from exc
        except services.SavedSearchError as exc:
            raise ValidationError(str(exc)) from exc
        return Response(SavedSearchSerializer(ss).data, status=status.HTTP_201_CREATED)

    def destroy(self, request, pk=None):
        services.delete_saved_search(request.user, self._get(request, pk))
        return Response(status=status.HTTP_204_NO_CONTENT)
