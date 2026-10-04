#!/usr/bin/env bash
# Daily Postgres backup to private verified EU storage: pg_dump | gzip -> native signed upload.
#
# Expects these in the environment (the socialapp-backup.service unit loads them from .env):
#   DATABASE_URL MEDIA_S3_ENDPOINT_URL MEDIA_S3_BUCKET MEDIA_S3_REGION MEDIA_S3_SSE
#   MEDIA_EU_RESIDENCY_VERIFIED MEDIA_PRIVATE_BUCKET_VERIFIED AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
#
# Retention is handled by an object-storage LIFECYCLE rule on the bucket (e.g. expire backups/db/*
# after 30 days) — see deploy/README.md — rather than scripted pruning, so a script bug can never
# delete a good backup. Test a real restore (pg_restore / psql) before relying on these.
set -euo pipefail
[[ $# -le 1 ]] || exit 2
native_binary=${1:-/usr/local/bin/social-server}
[[ $native_binary = /* && -x $native_binary ]] || exit 2

: "${DATABASE_URL:?DATABASE_URL not set}"
: "${MEDIA_S3_ENDPOINT_URL:?MEDIA_S3_ENDPOINT_URL not set}"
: "${MEDIA_S3_BUCKET:?MEDIA_S3_BUCKET not set}"

# Preserve the adopted postgis:// alias; pg_dump expects a PostgreSQL URL.
pg_url="${DATABASE_URL/postgis:\/\//postgresql:\/\/}"
ts="$(date -u +%Y%m%dT%H%M%SZ)"
umask 077
backup_directory="$(pwd)/var/backup-work"
mkdir -p -m 700 "$backup_directory"
[[ ! -L "$backup_directory" && $(stat -c '%a' "$backup_directory") == 700 ]] || exit 1
dump="$(mktemp "$backup_directory/socialapp-db.XXXXXX.sql.gz")"
trap 'rm -f "$dump"' EXIT

timeout --signal=TERM --kill-after=10s 15m pg_dump --no-owner --no-privileges "$pg_url" \
  | gzip -9 | head -c 1073741825 >"$dump"
"$native_binary" --backup-upload "$dump" \
  --backup-key "backups/db/socialapp-db-${ts}.sql.gz"
