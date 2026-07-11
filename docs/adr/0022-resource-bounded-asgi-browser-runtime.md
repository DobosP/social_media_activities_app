# ADR-0022: Resource-bounded ASGI and browser runtime

Date: 2026-07-11
Status: accepted

## Decision

Keep the Django/DRF/Channels modular monolith and its React-compatible TypeScript source, while
bounding the two runtimes that matter on a small server and a low-end phone:

- build the SPA through Preact's React-compatibility layer, load non-home screens as dynamic route
  chunks, and fail the build when the recursively imported initial JavaScript plus CSS exceeds
  40 KiB gzip;
- compile with the native TypeScript 7.0.2 CLI, using relative `paths` mappings rather than the
  removed `baseUrl` option, while retaining Preact 10.29.7 and React Router 6.30.4;
- disable Django per-thread persistent connections under ASGI (`CONN_MAX_AGE=0`) and use a
  process-local psycopg pool, defaulting to `min_size=0`, `max_size=4`, `timeout=10`, with
  `ASGI_THREADS=4` in the launch deployment;
- build on explicit Node 24/Python 3.12 Bookworm images, ship only the GeoDjango shared libraries
  in the final image, and do not install the unused Gunicorn server.

The pool bounds are environment-tunable. Set `DB_POOL_ENABLED=False` when an external pooler such
as PgBouncer is introduced; `DB_POOL_ENABLED=True` and `DB_POOLED=True` together fail at boot.

## Context / why

The pre-change production image was 1,317,185,980 bytes: its GeoDjango OS layer was about 840 MB
because development headers and CLI packages were installed in the final stage. The optimized
image is 600,460,607 bytes and its unprivileged ASGI process measured 129,316 KiB maximum RSS and
130,404 KiB current RSS in separate startup readings (about 127 MiB). The earlier loaded
Django/Channels process was about 145 MiB RSS. The previous monolithic SPA shipped 254,611 bytes of
JavaScript (77,813 bytes gzip) to every migrated screen, even though Django supplies data for only
one route. The budget-gated Preact build ships 36.71 KiB gzip of initial JavaScript and CSS.

Django 5.2 explicitly recommends disabling persistent connections under ASGI and using backend
pooling instead. Channels may otherwise create `min(32, CPU + 4)` worker threads, each capable of
holding a database connection. Small explicit bounds preserve concurrency without letting an idle
single-box deployment accumulate connections.

Why not rewrite the backend in Go: the mature GeoDjango/admin/safety service layer is the product's
high-value code, and no measured request bottleneck justifies duplicating it. Why not replace the
frontend source API: `@roedu/ui`, React Router, and the existing screens work through
`preact/compat`; retaining that contract makes the runtime substitution reversible. The React npm
packages remain declared to satisfy library peer dependencies, but Vite aliases browser imports to
Preact.

React Router deliberately remains on 6.30.4. Version 7 currently imports React APIs that Preact 10
does not provide, so taking that major would break the compatibility-runtime decision. TypeScript
7.0.2 is safe here because the frontend uses its CLI only; the removed `baseUrl` setting was
unnecessary and the existing path targets are explicitly relative.

The Python dependency lock moves to `pgvector-python` 0.5.0. This is the Django/Python adapter, not
the PostgreSQL extension: it removes the NumPy dependency and materializes `VectorField` values as
plain lists. Recommendation code already consumes sequences, and a regression test pins the list
contract. The database image separately carries pgvector extension 0.8.4.

## Verification

Verified on 2026-07-11 from clean installs and the built production images:

- both pip lockfiles were regenerated with Python 3.12; `pip check` was clean and `pip-audit`
  reported no known vulnerabilities;
- all 2,365 backend tests passed against the final PostgreSQL 16.14 image; the 67 integration-
  focused and 10 pool-boundary tests also passed separately, including non-finite timeout rejection;
- all three frontend contract tests, TypeScript typecheck, Vite build, and the recursive bundle
  gate passed; initial JS+CSS remained 36.71 KiB gzip, and the complete npm audit reported zero
  vulnerabilities;
- production settings applied every migration and executed a real query through the bounded pool;
  a Redis 8 smoke test also passed for both Django cache operations and Channels send/receive;
- the final production image (`sha256:3be7cf0a52ab…`) measured 600,460,607 bytes versus
  1,317,185,980 bytes before this change, runs as `appuser`, and started ASGI at 129,316 KiB
  maximum RSS and 130,404 KiB current
  RSS in separate readings;
- the final database image (`sha256:e9fae138b641…`, 832,104,954 bytes) reported PostgreSQL 16.14,
  PostGIS 3.5.2, and pgvector extension 0.8.4; all migrations and a pooled query passed, and its
  configured Debian/PGDG repositories reported no remaining package upgrades.

## Consequences

- The initial bundle size is a CI/build invariant; a dependency that crosses 40 KiB gzip must be
  split, replaced, or accompanied by a superseding decision. CI runs the lazy-route/Preact source
  contracts before the same typecheck, build, and recursive budget used by the Docker stage.
- Non-home navigation can briefly suspend while its route chunk loads; the shared Romanian status
  fallback remains visible and accessible.
- A fifth simultaneous database borrower waits up to the configured timeout instead of opening an
  unbounded connection. Pool and thread sizes must be revisited from measured concurrency, not
  raised pre-emptively.
- The Docker image no longer includes GeoDjango development headers or CLI tools. Any future native
  package build belongs in a builder stage.
- Code that reads `VectorField` values must treat them as lists, not NumPy arrays. Reintroducing
  NumPy requires a measured use case and an explicit dependency rather than relying on pgvector's
  former transitive import.
- DuckDB remains in the common Python lock for now. Splitting one-shot Overture ingestion into its
  own environment is deferred until that job has a separate deploy path and lockfile gate; doing it
  halfway would make an advertised management command fail on the production host.
- The verified local database is PostgreSQL 16.14 with PostGIS 3.5.2 on the official `16-3.5`
  line and pgvector extension 0.8.4; this is distinct from the `pgvector-python` 0.5.0 adapter.
  The upstream image still contained PostgreSQL 16.9 plus older OS security packages, so
  `Dockerfile.db` refreshes the Debian/PGDG closure, explicitly requests the server and client,
  and removes obsolete build-only packages at build time.
  docker-postgis does not publish a `16-3.6` manifest. Its recommended `18-3.6` image also changes
  the data-volume path, so adopting it is a separate major-version dump/restore rehearsal rather
  than a silent local-image bump; evaluate that before creating the first production cluster.
- The Docker image is currently the only release path that compiles and carries the Vite assets.
  The older Terraform/cloud-init path clones Python source but does not build `static/frontend`;
  keep `SOCIAL_REACT_UI=False` on that path. Before enabling the new UI there, replace manual
  `git pull` deployment with a green-CI, versioned artifact containing the compiled frontend and
  collected static files.
- This refines ADR-0016's delivery mechanics without superseding its SPA, SEO, CSP, or sensitive-
  subsystem boundaries.
