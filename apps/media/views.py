import re

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
    resolve_place_cover_token,
    resolve_signed_token,
    signed_url,
    thread_photos,
    upload_activity_cover,
    upload_photo,
)
from .storage import get_storage


def _temporary_redirect(url: str) -> HttpResponseRedirect:
    response = HttpResponseRedirect(url)
    response.status_code = 307
    return response


# Digit runs are bounded so a crafted mile-long number can never trip CPython's int-parse
# limit into a 500 — an over-long (nonsensical) Range simply doesn't match and gets a 200.
_RANGE_RE = re.compile(r"^bytes=(\d{0,18})-(\d{0,18})$")


def _media_response(request, key: str, *, content_type: str, private: bool = True):
    """A media byte response served straight from the storage key. Sets nosniff + a short
    private Cache-Control (tokens are per-viewer, so shared caches must never store them),
    and honours a single HTTP Range (RFC 9110) so <video> seeking works against the streaming
    path — reading ONLY the requested window from storage (a scrubbing player must not make
    the app load a whole clip per seek). Multi-range requests get the full body (a valid
    server choice) — players only ever send single ranges."""
    storage = get_storage()
    range_header = request.headers.get("Range", "")
    match = _RANGE_RE.match(range_header.strip()) if range_header else None
    status_code = 200
    content_range = None
    if match:
        total = storage.size(key)
        start_s, end_s = match.groups()
        if total == 0 or (start_s == "" and end_s == ""):
            data = storage.open(key)
        else:
            if start_s == "":  # suffix form: last N bytes
                length = min(int(end_s), total)
                start, end = total - length, total - 1
            else:
                start = int(start_s)
                end = min(int(end_s), total - 1) if end_s else total - 1
            if start >= total or start > end:
                resp = HttpResponse(status=416)
                resp["Content-Range"] = f"bytes */{total}"
                return resp
            data = storage.open_range(key, start, end)
            status_code = 206
            content_range = f"bytes {start}-{end}/{total}"
    else:
        data = storage.open(key)
    resp = HttpResponse(data, content_type=content_type, status=status_code)
    resp["X-Content-Type-Options"] = "nosniff"
    resp["Accept-Ranges"] = "bytes"
    if content_range:
        resp["Content-Range"] = content_range
    ttl = getattr(settings, "MEDIA_SIGNED_URL_TTL", 300)
    resp["Cache-Control"] = f"private, max-age={ttl}" if private else f"public, max-age={ttl}"
    return resp


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
            photo, variant = resolve_signed_token(token, request.user)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        key = photo.storage_key
        if variant == "thumb" and photo.thumb_storage_key:
            key = photo.thumb_storage_key
        # Scale (opt-in): after the access check, offload the bytes to the object store directly.
        presigned = maybe_presigned_url(key, content_type=photo.content_type)
        if presigned:
            return _temporary_redirect(presigned)
        return _media_response(request, key, content_type=photo.content_type)


class AttachmentFileView(APIView):
    """Serve a thread Attachment behind a signed, unexpired, membership-scoped link. A FILE
    (PDF) is ALWAYS served as a forced download with nosniff, so it can never render/execute
    inline in the page context (PDF-borne JS/XSS); images render inline."""

    permission_classes = [IsAuthenticated]

    def get(self, request, token):
        try:
            att, variant = resolve_attachment_token(token, request.user)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        if variant == "poster":
            key = att.poster_storage_key
            content_type = att.poster_content_type or "image/webp"
        elif variant == "thumb" and att.thumb_storage_key:
            key, content_type = att.thumb_storage_key, att.content_type
        else:
            key, content_type = att.storage_key, att.content_type
        is_pdf = att.kind == att.Kind.FILE
        download_name = (att.original_filename or "document.pdf") if is_pdf else None
        # Scale (opt-in): redirect an authorized viewer to a presigned object-store URL. The PDF
        # forced-download + content-type are preserved via the presign response overrides, so the
        # inline-execution guard still holds on the direct fetch. (For video the object store
        # also serves HTTP Range natively — the recommended prod setup once S3 is configured.)
        presigned = maybe_presigned_url(key, content_type=content_type, download_name=download_name)
        if presigned:
            return _temporary_redirect(presigned)
        resp = _media_response(request, key, content_type=content_type)
        if is_pdf:
            resp["Content-Disposition"] = f'attachment; filename="{download_name}"'
        return resp


class ActivityCoverFileView(APIView):
    """Serve a signed activity-cover URL after re-checking activity visibility."""

    permission_classes = [AllowAny]

    def get(self, request, token):
        viewer = request.user if request.user.is_authenticated else None
        try:
            cover, variant = resolve_activity_cover_token(token, viewer)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        key = cover.storage_key
        if variant == "thumb" and cover.thumb_storage_key:
            key = cover.thumb_storage_key
        presigned = maybe_presigned_url(key, content_type=cover.content_type)
        if presigned:
            return _temporary_redirect(presigned)
        return _media_response(request, key, content_type=cover.content_type)


class PlaceCoverFileView(APIView):
    """Serve a signed place-cover URL after re-checking the place is public."""

    permission_classes = [AllowAny]

    def get(self, request, token):
        try:
            cover = resolve_place_cover_token(token)
        except NotAuthorized as exc:
            raise PermissionDenied(str(exc)) from exc
        presigned = maybe_presigned_url(cover.storage_key, content_type=cover.content_type)
        if presigned:
            return _temporary_redirect(presigned)
        # Venue covers are public content — a shared cache may keep them for the token TTL.
        return _media_response(
            request, cover.storage_key, content_type=cover.content_type, private=False
        )
