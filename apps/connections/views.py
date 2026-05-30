from rest_framework import status, viewsets
from rest_framework.decorators import action
from rest_framework.exceptions import NotFound, PermissionDenied, ValidationError
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response

from apps.accounts.models import User

from . import services
from .models import Connection
from .serializers import ConnectionSerializer, UserRefSerializer


class ConnectionViewSet(viewsets.ViewSet):
    """Connections API: list accepted connections, search (query-only, no suggestions), and
    request/accept/decline/withdraw/remove. The web views call the same service functions."""

    permission_classes = [IsAuthenticated]

    def list(self, request):
        return Response(UserRefSerializer(services.connections_for(request.user), many=True).data)

    @action(detail=False, methods=["get"])
    def pending(self, request):
        return Response(
            {
                "incoming": ConnectionSerializer(
                    services.pending_incoming(request.user), many=True
                ).data,
                "outgoing": ConnectionSerializer(
                    services.pending_outgoing(request.user), many=True
                ).data,
            }
        )

    @action(detail=False, methods=["get"])
    def search(self, request):
        # Query-only discovery: no query -> no results (deliberately no suggestions feed).
        matches = services.search_connectable(request.user, request.query_params.get("q", ""))
        return Response(UserRefSerializer(matches, many=True).data)

    @action(detail=False, methods=["post"])
    def request_to(self, request):
        target = self._target(request)
        try:
            conn = services.request_connection(request.user, target)
        except services.NotEligible as exc:
            raise PermissionDenied(str(exc)) from exc
        except services.InvalidState as exc:
            raise ValidationError(str(exc)) from exc
        return Response(ConnectionSerializer(conn).data, status=status.HTTP_201_CREATED)

    @action(detail=True, methods=["post"])
    def accept(self, request, pk=None):
        return self._respond(request, pk, accept=True)

    @action(detail=True, methods=["post"])
    def decline(self, request, pk=None):
        return self._respond(request, pk, accept=False)

    @action(detail=True, methods=["post"])
    def withdraw(self, request, pk=None):
        conn = self._connection(pk)
        try:
            services.withdraw_request(request.user, conn)
        except services.ConnectionError as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(ConnectionSerializer(conn).data)

    @action(detail=False, methods=["post"])
    def remove(self, request):
        target = self._target(request)
        services.remove_connection(request.user, target)
        return Response(status=status.HTTP_204_NO_CONTENT)

    # --- helpers ---
    def _target(self, request):
        public_id = request.data.get("public_id")
        target = User.objects.filter(public_id=public_id).first() if public_id else None
        if target is None:
            raise NotFound("No such user.")
        return target

    def _connection(self, pk):
        conn = Connection.objects.filter(pk=pk).first()
        if conn is None:
            raise NotFound("No such connection.")
        return conn

    def _respond(self, request, pk, *, accept):
        conn = self._connection(pk)
        try:
            services.respond_to_connection(request.user, conn, accept=accept)
        except services.NotEligible as exc:
            raise PermissionDenied(str(exc)) from exc
        except services.InvalidState as exc:
            raise ValidationError(str(exc)) from exc
        return Response(ConnectionSerializer(conn).data)
