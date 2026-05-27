"""Test settings: fast password hashing, DEBUG off."""

from .base import *  # noqa: F401,F403

DEBUG = False
PASSWORD_HASHERS = ["django.contrib.auth.hashers.MD5PasswordHasher"]

# Tests exercise the identity flow via the dev stub even though DEBUG is off.
IDENTITY_ALLOW_DEV_PROVIDER = True
