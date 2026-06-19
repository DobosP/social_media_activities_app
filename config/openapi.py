"""drf-spectacular preprocessing hook for API versioning.

The API is mounted under BOTH a canonical versioned base (``/api/v1/``) and a backward-compatible
unversioned alias (``/api/`` — see config/urls.py). Without filtering, the generated OpenAPI schema
would list every operation twice (duplicate, invalid operationIds). This hook drops ONLY the bare
unversioned ``/api/`` alias, keeping every versioned ``/api/vN/`` path AND any non-/api path (the
``/healthz`` / ``/readyz`` ops probes). The alias still works at runtime; it just isn't duplicated
in the docs.

When ``/api/v2/`` ships it is documented automatically. NOTE: operationIds do not embed the version,
so v1 and v2 of the SAME operation would still collide in one schema — at that point split the
schema per version (a multi-document / SERVERS concern) rather than serving both in one document.
"""

import re

_VERSIONED = re.compile(r"^/api/v\d+/")


def only_versioned_endpoints(endpoints, **kwargs):
    kept = []
    for ep in endpoints:
        path = ep[0]
        # Drop the bare unversioned /api/ alias (a duplicate of the canonical /api/vN/ operation);
        # keep versioned /api/vN/ paths and everything outside /api/ (e.g. the health probes).
        if path.startswith("/api/") and not _VERSIONED.match(path):
            continue
        kept.append(ep)
    return kept
