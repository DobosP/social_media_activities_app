"""Local development settings."""

from .base import *  # noqa: F401,F403
from .base import env

DEBUG = True
ALLOWED_HOSTS = ["*"]
REQUEST_LOGGING_ENABLED = env.bool("REQUEST_LOGGING_ENABLED", default=False)

# Local dev convenience: don't block uploads on a configured content scanner.
MEDIA_REQUIRE_SCANNER = False
