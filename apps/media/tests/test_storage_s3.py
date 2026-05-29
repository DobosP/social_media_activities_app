from unittest.mock import MagicMock, patch

import pytest
from django.core.exceptions import ImproperlyConfigured
from django.test import override_settings


@override_settings(MEDIA_S3_BUCKET="test-bucket", MEDIA_S3_ENDPOINT_URL="", MEDIA_S3_REGION="")
@patch("boto3.client")
def test_s3_backend_roundtrip(mock_client):
    from apps.media.storage import S3StorageBackend

    s3 = MagicMock()
    mock_client.return_value = s3
    backend = S3StorageBackend()

    backend.save("abc.jpg", b"bytes")
    s3.put_object.assert_called_once_with(Bucket="test-bucket", Key="abc.jpg", Body=b"bytes")

    body = MagicMock()
    body.read.return_value = b"bytes"
    s3.get_object.return_value = {"Body": body}
    assert backend.open("abc.jpg") == b"bytes"

    assert backend.exists("abc.jpg") is True
    backend.delete("abc.jpg")
    s3.delete_object.assert_called_once_with(Bucket="test-bucket", Key="abc.jpg")


@override_settings(MEDIA_S3_BUCKET="test-bucket")
@patch("boto3.client")
def test_s3_exists_false_on_client_error(mock_client):
    from botocore.exceptions import ClientError

    from apps.media.storage import S3StorageBackend

    s3 = MagicMock()
    s3.head_object.side_effect = ClientError({"Error": {"Code": "404"}}, "HeadObject")
    mock_client.return_value = s3
    assert S3StorageBackend().exists("missing.jpg") is False


@override_settings(MEDIA_S3_BUCKET="")
@patch("boto3.client")
def test_s3_requires_bucket(_mock_client):
    from apps.media.storage import S3StorageBackend

    with pytest.raises(ImproperlyConfigured):
        S3StorageBackend()
