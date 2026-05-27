"""Test settings: fast password hashing, DEBUG off."""

import tempfile

from .base import *  # noqa: F401,F403

DEBUG = False
PASSWORD_HASHERS = ["django.contrib.auth.hashers.MD5PasswordHasher"]

# Tests exercise the identity flow via the dev stub even though DEBUG is off.
IDENTITY_ALLOW_DEV_PROVIDER = True

# Keep uploaded test blobs out of the repo tree.
MEDIA_ROOT = tempfile.mkdtemp(prefix="test-media-")
