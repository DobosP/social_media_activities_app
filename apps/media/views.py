from django.conf import settings
from django.http import HttpResponse, HttpResponseRedirect
from rest_framework import status
from rest_framework.exceptions import (
    NotAuthenticated,
    PermissionDenied,
    Throttled,
    ValidationError,
)
from rest_framework.parsers import FormParser, MultiPartParser
from rest_framework.permissions import AllowAny, IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.safety.services import allow_action
from apps.social.models import Activity, Thread

from .models import ActivityCover, Photo
from .serializers import ActivityCoverSerializer, PhotoSerializer
from .services import (
    DuplicateProfileImage,
    MediaRejected,
    NotAuthorized,
    activity_cover_signed_url,
    delete_activity_cover,
    delete_photo,
    maybe_presigned_url,
    resolve_activity_cover_token,
    resolve_attachment_token,
    resolve_signed_token,
    signed_url,
    thread_photos,
    upload_activity_cover,
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


class ActivityCoverView(APIView):
    """Create, replace, read, or delete one cover photo for an activity."""

    permission_classes = [AllowAny]
    parser_classes = [MultiPartParser, FormParser]

    def _cover(self, activity_id):
        return (
            ActivityCover.objects.filter(activity_id=activity_id)
            .select_related("activity", "activity__owner", "uploader")
            .first()
        )

    def get(self, request, activity_id):
        cover = self._cover(activity_id)
        if cover is None:
            raise ValidationError("No activity cover.")
        viewer = request.user if request.user.is_authenticated else None
        try:
            url = activity_cover_signed_url(cover, viewer)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        ctx = {"signed_urls": {cover.id: url}}
        return Response(ActivityCoverSerializer(cover, context=ctx).data)

    def put(self, request, activity_id):
        if not request.user.is_authenticated:
            raise NotAuthenticated("Authentication required.")
        upload = request.FILES.get("file")
        if upload is None:
            raise ValidationError({"file": "An image file is required."})
        activity = Activity.objects.select_related("owner").filter(pk=activity_id).first()
        if activity is None:
            raise ValidationError("No such activity.")
        try:
            cover = upload_activity_cover(
                request.user,
                activity,
                upload.read(),
                alt_text=request.data.get("alt_text", ""),
            )
            url = activity_cover_signed_url(cover, request.user)
        except MediaRejected as exc:
            raise PermissionDenied(str(exc)) from exc
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        except ValueError as exc:
            raise ValidationError(str(exc)) from exc
        ctx = {"signed_urls": {cover.id: url}}
        return Response(ActivityCoverSerializer(cover, context=ctx).data)

    def delete(self, request, activity_id):
        if not request.user.is_authenticated:
            raise NotAuthenticated("Authentication required.")
        cover = self._cover(activity_id)
        if cover is None:
            raise ValidationError("No activity cover.")
        try:
            delete_activity_cover(request.user, cover)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(status=status.HTTP_204_NO_CONTENT)


class MediaFileView(APIView):
    """Serve raw bytes for a signed, unexpired, membership-scoped media link."""

    permission_classes = [IsAuthenticated]

    def get(self, request, token):
        try:
            photo = resolve_signed_token(token, request.user)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        # Scale (opt-in): after the access check, offload the bytes to the object store directly.
        presigned = maybe_presigned_url(photo.storage_key, content_type=photo.content_type)
        if presigned:
            return HttpResponseRedirect(presigned)
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
        is_pdf = att.kind == att.Kind.FILE
        download_name = (att.original_filename or "document.pdf") if is_pdf else None
        # Scale (opt-in): redirect an authorized viewer to a presigned object-store URL. The PDF
        # forced-download + content-type are preserved via the presign response overrides, so the
        # inline-execution guard still holds on the direct fetch.
        presigned = maybe_presigned_url(
            att.storage_key, content_type=att.content_type, download_name=download_name
        )
        if presigned:
            return HttpResponseRedirect(presigned)
        data = get_storage().open(att.storage_key)
        resp = HttpResponse(data, content_type=att.content_type)
        resp["X-Content-Type-Options"] = "nosniff"
        if is_pdf:
            resp["Content-Disposition"] = f'attachment; filename="{download_name}"'
        return resp


class ActivityCoverFileView(APIView):
    """Serve a signed activity-cover URL after re-checking activity visibility."""

    permission_classes = [AllowAny]

    def get(self, request, token):
        viewer = request.user if request.user.is_authenticated else None
        try:
            cover = resolve_activity_cover_token(token, viewer)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        presigned = maybe_presigned_url(cover.storage_key, content_type=cover.content_type)
        if presigned:
            return HttpResponseRedirect(presigned)
        data = get_storage().open(cover.storage_key)
        resp = HttpResponse(data, content_type=cover.content_type)
        resp["X-Content-Type-Options"] = "nosniff"
        return resp
