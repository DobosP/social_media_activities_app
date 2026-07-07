# Database Seed

`seed-data.sql` is a local/demo RO-EDU snapshot: 7 Cluj-Napoca venues, 9 inferred
venue/activity edges, and 39 venue events. It is intentionally data-only.

ADR-0017 records the schema-authority and startup-order decision. Operationally,
the database image installs pgvector but does not mount anything into
`/docker-entrypoint-initdb.d/`; compose loads this data from the web container
after migrations:

```bash
python manage.py migrate
python manage.py load_roedu_seed
python manage.py runserver 0.0.0.0:8000
```

`load_roedu_seed` is local/demo only (`DEBUG=True`), skips when RO-EDU rows are
already present, and skips rather than overwriting an existing non-RO-EDU local
places/events database. Refresh the snapshot from the RO-EDU corpus by
regenerating this data-only SQL file, then checking it for schema statements and
`django_migrations` content before landing.
