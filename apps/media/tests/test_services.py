import hashlib
import io

import pytest
from PIL import Image

from apps.media import services
from apps.media.imaging import process_image
from apps.media.models import MediaImage

from .conftest import make_jpeg_with_exif, make_png

pytestmark = pytest.mark.django_db


def test_process_image_strips_metadata():
    processed = process_image(make_jpeg_with_exif())
    reopened = Image.open(io.BytesIO(processed.data))
    assert len(reopened.getexif()) == 0
    assert (processed.width, processed.height) == (32, 32)


def test_invalid_image_rejected(owner):
    with pytest.raises(services.MediaError):
        services.set_profile_picture(owner, b"not an image")


def test_set_and_get_profile_picture(owner):
    image = services.set_profile_picture(owner, make_png())
    assert image.status == MediaImage.Status.APPROVED
    assert services.profile_picture(owner) == image


def test_oversize_rejected(owner, settings):
    settings.MEDIA_MAX_BYTES = 10
    with pytest.raises(services.MediaError):
        services.set_profile_picture(owner, make_png())


def test_blocklisted_image_rejected_and_audited(owner, settings):
    data = make_png(color=(1, 2, 3))
    # The scanner hashes the *processed* bytes, so block that digest.
    processed = process_image(data)
    settings.MEDIA_HASH_BLOCKLIST = [hashlib.sha256(processed.data).hexdigest()]
    with pytest.raises(services.MediaError):
        services.set_profile_picture(owner, data)
    assert MediaImage.objects.filter(owner=owner, status=MediaImage.Status.REJECTED).exists()


def test_thread_photo_requires_membership(thread, owner, member, outsider):
    img = services.upload_image(
        owner, kind=MediaImage.Kind.THREAD_PHOTO, data=make_png(), thread=thread
    )
    assert img.status == MediaImage.Status.APPROVED
    assert member in [m.user for m in thread.activity.memberships.all()]
    assert services.thread_photos(member, thread) == [img]
    with pytest.raises(services.MediaError):
        services.upload_image(
            outsider, kind=MediaImage.Kind.THREAD_PHOTO, data=make_png(), thread=thread
        )
    with pytest.raises(services.MediaError):
        services.thread_photos(outsider, thread)


def test_thread_photo_visibility(thread, owner, member, outsider):
    img = services.upload_image(
        owner, kind=MediaImage.Kind.THREAD_PHOTO, data=make_png(), thread=thread
    )
    assert services.can_view(member, img) is True
    assert services.can_view(outsider, img) is False
    assert services.signed_url(member, img)
    with pytest.raises(services.MediaError):
        services.signed_url(outsider, img)
