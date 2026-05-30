from django.conf import settings
from django.http import HttpResponse
from rest_framework import status
from rest_framework.exceptions import PermissionDenied, Throttled, ValidationError
from rest_framework.parsers import FormParser, MultiPartParser
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.safety.services import allow_action
from apps.social.models import Thread

from .models import Photo
from .serializers import PhotoSerializer
from .services import (
    DuplicateProfileImage,
    MediaRejected,
    NotAuthorized,
    delete_photo,
    resolve_attachment_token,
    resolve_signed_token,
    signed_url,
    thread_photos,
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
        elif kind == Photo.Kind.PROFILE and not allow_action(
            request.user,
            "avatar_upload",
            limit=getattr(settings, "AVATAR_UPLOAD_RATE_LIMIT", 20),
            window_seconds=getattr(settings, "AVATAR_UPLOAD_RATE_WINDOW_SECONDS", 3600),
        ):
            # Brake the avatar uniqueness-check oracle (mirrors the web avatar path).
            raise Throttled(detail="Too many avatar changes; please try again later.")
        try:
            photo = upload_photo(request.user, kind, upload.read(), thread=thread)
        except MediaRejected as exc:
            raise PermissionDenied(str(exc)) from exc
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        except (DuplicateProfileImage, ValueError) as exc:
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

    def delete(self, request, pk):
        photo = Photo.objects.filter(pk=pk).first()
        if photo is None:
            raise ValidationError("No such photo.")
        try:
            delete_photo(request.user, photo)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(status=status.HTTP_204_NO_CONTENT)


class ThreadPhotosView(APIView):
    """List the clean photos in an activity thread (members only), with signed URLs."""

    permission_classes = [IsAuthenticated]

    def get(self, request, thread_id):
        thread = Thread.objects.filter(id=thread_id).first()
        if thread is None:
            raise ValidationError("No such thread.")
        try:
            photos = list(thread_photos(request.user, thread))
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        ctx = {"signed_urls": {p.id: signed_url(p, request.user) for p in photos}}
        return Response(PhotoSerializer(photos, many=True, context=ctx).data)


class MediaFileView(APIView):
    """Serve raw bytes for a signed, unexpired, membership-scoped media link."""

    permission_classes = [IsAuthenticated]

    def get(self, request, token):
        try:
            photo = resolve_signed_token(token, request.user)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        data = get_storage().open(photo.storage_key)
        resp = HttpResponse(data, content_type=photo.content_type)
        resp["X-Content-Type-Options"] = "nosniff"
        return resp


class AttachmentFileView(APIView):
    """Serve a thread Attachment behind a signed, unexpired, membership-scoped link. A FILE
    (PDF) is ALWAYS served as a forced download with nosniff, so it can never render/execute
    inline in the page context (PDF-borne JS/XSS); images render inline."""

    permission_classes = [IsAuthenticated]

    def get(self, request, token):
        try:
            att = resolve_attachment_token(token, request.user)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        data = get_storage().open(att.storage_key)
        resp = HttpResponse(data, content_type=att.content_type)
        resp["X-Content-Type-Options"] = "nosniff"
        if att.kind == att.Kind.FILE:
            name = att.original_filename or "document.pdf"
            resp["Content-Disposition"] = f'attachment; filename="{name}"'
        return resp
