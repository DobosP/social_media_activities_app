# Native deployment templates

The owner approved the Go implementation for main; deployment remains **unapplied and launch-blocked** until
owner procurement and the product GDPR/DPIA/parental
assurance gates. [ADR-0032](../docs/adr/0032-complete-native-go-backend.md) records selection;
[NATIVE_SERVER](../docs/NATIVE_SERVER.md) records artifact, codec, proxy and scaling requirements.

The provider/box examples are candidates, not procurement decisions. Do not run Terraform
apply or activate real ingestion/providers/minors from an implementation task.

Cloud-init takes `app_release_url` and its exact `app_release_sha256`, verifies before
extracting, then installs the native binary and precompiled static/template/locale data.
It does not clone application source, install Python application dependencies, or compile
Node/Go on the VPS. Systemd starts native `--migrate-only`, HTTP, `--due` and bounded
`--job transcode_videos` operations. PostgreSQL/PostGIS/vector stay local; Caddy terminates
TLS and forwards only from the explicitly trusted loopback network.

Private media requires explicit verified EU residency and bucket privacy, plus the native
scanner/identity trust configuration. Source examples have these attestations false and
minor onboarding disabled; setting names/values must come through the approved secrets
workflow. Initial administrator bootstrap is an explicit private-stdin native command.

Export the Docker `release` target into task scratch, archive it, and record the exact
SHA-256 for `terraform.tfvars` (gitignored). Native module/container tests and scans must
pass on that exact source candidate before it becomes a deployment artifact. Artifact
and database rollback drills precede a real migration; the old database/runtime remain
available for reviewed rollback. Backup units and EU storage policy still apply.

The backup unit keeps `pg_dump | gzip` and uses the native binary for a private,
conditional S3 upload with confirmed size/hash/SSE metadata. Scratch is0700 and the
temporary dump 0600; the dump is bounded to 1GiB and pg_dump to 15minutes. Bucket lifecycle
retains nightly backups for at least 30days. Quarterly restore rehearsal remains required.
The [native operator guide](../services/server/cmd/social-server/README.md) covers bounded
upload/download and synthetic probe commands. Fixture tests do not qualify real storage
or recovery, and these unapplied templates activate neither.

Native HSTS/HTTPS/logging and reviewed domain policies are mapped. PostgreSQL owns
shared admission/live state; required-shared mode checks its migrated contract. Optional
privacy-safe Sentry is bounded and disabled by default. Retired Python runtime names
(DB_POOL_TIMEOUT, ASGI_THREADS, DJANGO_SETTINGS_MODULE), unused Redis and unsafe policy
overrides refuse startup. Remove those obsolete assignments when adopting an old env profile,
without printing its values. The templates have never been applied or tested on a real VPS.
