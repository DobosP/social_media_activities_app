#!/usr/bin/env bash
# Daily Postgres backup to the EU object-storage bucket: pg_dump | gzip -> s3://<bucket>/backups/db/.
#
# Expects these in the environment (the socialapp-backup.service unit loads them from .env):
#   DATABASE_URL  MEDIA_S3_ENDPOINT_URL  MEDIA_S3_BUCKET  AWS_ACCESS_KEY_ID  AWS_SECRET_ACCESS_KEY
#
# Retention is handled by an object-storage LIFECYCLE rule on the bucket (e.g. expire backups/db/*
# after 30 days) — see deploy/README.md — rather than scripted pruning, so a script bug can never
# delete a good backup. Test a real restore (pg_restore / psql) before relying on these.
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL not set}"
: "${MEDIA_S3_ENDPOINT_URL:?MEDIA_S3_ENDPOINT_URL not set}"
: "${MEDIA_S3_BUCKET:?MEDIA_S3_BUCKET not set}"

# pg_dump wants a postgresql:// URL; the app uses django-environ's postgis:// scheme.
pg_url="${DATABASE_URL/postgis:\/\//postgresql:\/\/}"
ts="$(date -u +%Y%m%dT%H%M%SZ)"
dump="$(mktemp -t socialapp-db.XXXXXX).sql.gz"
trap 'rm -f "$dump"' EXIT

pg_dump --no-owner --no-privileges "$pg_url" | gzip -9 >"$dump"
aws --endpoint-url "$MEDIA_S3_ENDPOINT_URL" s3 cp "$dump" \
  "s3://${MEDIA_S3_BUCKET}/backups/db/socialapp-db-${ts}.sql.gz"
echo "backup uploaded: backups/db/socialapp-db-${ts}.sql.gz ($(du -h "$dump" | cut -f1))"
