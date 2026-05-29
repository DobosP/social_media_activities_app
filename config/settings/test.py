"""Test settings: fast password hashing, DEBUG off."""

import tempfile

from .base import *  # noqa: F401,F403
from .base import REST_FRAMEWORK

DEBUG = False
PASSWORD_HASHERS = ["django.contrib.auth.hashers.MD5PasswordHasher"]

# Tests exercise the identity flow via the dev stub even though DEBUG is off.
IDENTITY_ALLOW_DEV_PROVIDER = True
# Trust the local EUDI sandbox issuer in tests.
EUDI_SANDBOX = True

# Disable API throttling in tests so request counts don't accumulate across cases
# (the LocMemCache persists within a test process). Throttling is verified explicitly
# in tests/test_api_security.py via a settings override.
REST_FRAMEWORK = {**REST_FRAMEWORK, "DEFAULT_THROTTLE_CLASSES": [], "DEFAULT_THROTTLE_RATES": {}}

# Keep uploaded test blobs out of the repo tree.
MEDIA_ROOT = tempfile.mkdtemp(prefix="test-media-")

# Media uploads don't require a real scanner under test (no blocklist is configured); the
# fail-closed gate is verified explicitly in the media tests via a settings override.
MEDIA_REQUIRE_SCANNER = False
