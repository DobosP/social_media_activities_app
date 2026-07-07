# ADR-0017: Load RO-EDU Seed After Migrate

Date: 2026-07-07
Status: accepted

## Decision
Keep the local RO-EDU snapshot as a data-only SQL artifact and load it from the
web container with `python manage.py load_roedu_seed` immediately after
`python manage.py migrate`; the database image may install extension packages
such as pgvector, but it must not create app schema, indexes, constraints, or
`django_migrations` rows through initdb scripts.

## Context / why
The seeded database image introduced in commit 6d1dc90 baked a pg_dump into
`/docker-entrypoint-initdb.d/`. That dump included app tables and
`django_migrations`, so a fresh volume could make Django believe migrations had
already run while the actual schema came from a frozen snapshot. Loading data
after `migrate` preserves the fast local/demo setup without allowing a dump to
shadow the migration graph.

## Consequences
Fresh compose startup still gets RO-EDU Cluj venues and events without a live
ingest, but migration edits are exercised on every fresh database. The seed file
must remain data-only, and refreshes must be checked for schema statements and
`django_migrations` content before landing.
