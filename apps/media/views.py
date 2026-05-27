from django.http import FileResponse, Http404
from django.shortcuts import get_object_or_404
from rest_framework import status
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.social.models import Thread

from . import services
from .models import MediaImage
from .serializers import MediaImageSerializer
from .storage import LocalStorageBackend, get_storage_backend, verify_signature


def _read_upload(request):
    upload = request.FILES.get("image")
    if upload is None:
        return None
    return upload.read()


class ProfilePictureView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request):
        image = services.profile_picture(request.user)
        if image is None:
            return Response({"detail": "No profile picture set."}, status=status.HTTP_404_NOT_FOUND)
        return Response(MediaImageSerializer(image, context={"request": request}).data)

    def post(self, request):
        data = _read_upload(request)
        if data is None:
            return Response(
                {"detail": "Provide an 'image' file."}, status=status.HTTP_400_BAD_REQUEST
            )
        try:
            image = services.set_profile_picture(request.user, data)
        except services.MediaError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(
            MediaImageSerializer(image, context={"request": request}).data,
            status=status.HTTP_201_CREATED,
        )


class ThreadPhotosView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request, thread_id):
        thread = get_object_or_404(Thread.objects.select_related("activity"), pk=thread_id)
        try:
            photos = services.thread_photos(request.user, thread)
        except services.MediaError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_403_FORBIDDEN)
        return Response(MediaImageSerializer(photos, many=True, context={"request": request}).data)

    def post(self, request, thread_id):
        thread = get_object_or_404(Thread.objects.select_related("activity"), pk=thread_id)
        data = _read_upload(request)
        if data is None:
            return Response(
                {"detail": "Provide an 'image' file."}, status=status.HTTP_400_BAD_REQUEST
            )
        try:
            image = services.upload_image(
                request.user, kind=MediaImage.Kind.THREAD_PHOTO, data=data, thread=thread
            )
        except services.MediaError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(
            MediaImageSerializer(image, context={"request": request}).data,
            status=status.HTTP_201_CREATED,
        )


class MediaServeView(APIView):
    """Serves bytes for the local storage backend, gated by a signed, expiring token
    AND a re-check that the requesting user may view the image. S3-style backends
    return presigned URLs directly and never hit this view."""

    permission_classes = [IsAuthenticated]

    def get(self, request, key):
        expires = request.query_params.get("expires")
        token = request.query_params.get("token", "")
        if not expires or not verify_signature(key, int(expires), token):
            raise Http404
        image = get_object_or_404(MediaImage, storage_key=key)
        if not services.can_view(request.user, image):
            raise Http404
        backend = get_storage_backend()
        if not isinstance(backend, LocalStorageBackend):
            raise Http404
        try:
            data = backend.read(key)
        except FileNotFoundError as exc:
            raise Http404 from exc
        return FileResponse(iter([data]), content_type=image.content_type)
