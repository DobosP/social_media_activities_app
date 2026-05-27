from django.http import HttpResponse
from rest_framework import status
from rest_framework.exceptions import PermissionDenied, ValidationError
from rest_framework.parsers import FormParser, MultiPartParser
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.social.models import Thread

from .models import Photo
from .serializers import PhotoSerializer
from .services import (
    MediaRejected,
    NotAuthorized,
    resolve_signed_token,
    signed_url,
    upload_photo,
)
from .storage import get_storage


class PhotoUploadView(APIView):
    """Upload a profile picture or a thread photo (multipart `file`)."""

    permission_classes = [IsAuthenticated]
    parser_classes = [MultiPartParser, FormParser]

    def post(self, request):
        upload = request.FILES.get("file")
        if upload is None:
            raise ValidationError({"file": "An image file is required."})
        kind = request.data.get("kind", Photo.Kind.PROFILE)
        thread = None
        if kind == Photo.Kind.THREAD:
            thread_id = request.data.get("thread")
            thread = Thread.objects.filter(id=thread_id).first()
            if thread is None:
                raise ValidationError({"thread": "No such thread."})
        try:
            photo = upload_photo(request.user, kind, upload.read(), thread=thread)
        except MediaRejected as exc:
            raise PermissionDenied(str(exc)) from exc
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        except ValueError as exc:
            raise ValidationError(str(exc)) from exc
        ctx = {"signed_urls": {photo.id: signed_url(photo, request.user)}}
        return Response(PhotoSerializer(photo, context=ctx).data, status=status.HTTP_201_CREATED)


class PhotoDetailView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request, pk):
        photo = Photo.objects.filter(pk=pk).first()
        if photo is None:
            raise ValidationError("No such photo.")
        try:
            url = signed_url(photo, request.user)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        ctx = {"signed_urls": {photo.id: url}}
        return Response(PhotoSerializer(photo, context=ctx).data)


class MediaFileView(APIView):
    """Serve raw bytes for a signed, unexpired, membership-scoped media link."""

    permission_classes = [IsAuthenticated]

    def get(self, request, token):
        try:
            photo = resolve_signed_token(token, request.user)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        data = get_storage().open(photo.storage_key)
        return HttpResponse(data, content_type=photo.content_type)
