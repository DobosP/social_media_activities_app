# Native manual commands

`commands.Install(runner, Config)` adds manual handlers to the native `jobs.Runner`;
there is no Python command import, subprocess or proxy. The CLI supplies a bounded JSON
options object through its job-options file/stdin interface. Unknown fields fail.
The 27 scheduled command names remain in `jobs.DueNames`; these 16 additional handlers
cover the source management-command inventory:

| Command | Options | Native behavior |
| --- | --- | --- |
| `ingest_places` | `source`, `city`, `bbox`, `overpass_url`, `overture_path`, `limit`, `dry_run`, `no_dedup`/`dedup`, `min_confidence`, `with_website`, `aggregate` | OSM query/conversion and source adapters; per-place atomic upserts, protected manual/confirmed edges and standing disputes preserved |
| `ingest_events` | `ics_url`, `ics_file`, `place` | Bounded ICS read, native recurrence parsing, exact declared-taxonomy classification, source identity upsert |
| `enrich_places` | `city`, `source`, `limit`, `dry_run`, `google`, `wikidata` | Native hours parsing plus optional native provider callbacks; empty-only contact backfill and namespaced overlays |
| `dedup_places` | `city`, `apply`, `max_distance_m`, `min_name_ratio` | Report by default; conservative source-priority merge, names normalized like the source, license/provenance retained |
| `aggregate_unnamed_places` | `source`, `city`, `bbox`, `dry_run` | Closest public named sports-complex/park/school selection and dependency-safe edge aggregation |
| `backfill_embeddings` | none | Only missing embeddings, through native recommendation service |
| `backfill_avatar_phash` | none | Stored profile blobs only, bounded decode/fingerprint; missing or undecodable blobs counted as skipped |
| `seed_booking_links` | `dry_run` | Idempotent website-to-booking-link seed |
| `resolve_place_covers` | `city`, `limit`, `dry_run`, `recheck` | Native Commons resolution with free-license checks and native media import; dry-run performs no external fetch |
| `sync_roedu_events` | `city`, `limit`, `app_pack`, `min_confidence`, `dry_run`, `allow_snapshot_rollback`, `updated_since` | Canonical promoted app-pack only; partial reads never reconcile absence; rollback requires an explicit option; legacy delta/noncanonical products fail |
| `load_roedu_seed` | `path` | Development-only, rooted data-only COPY loader; requires empty catalog or skips an already loaded seed |
| `seed_demo_users` | `force` | Guarded source adult/staff accounts and joined demo activity |
| `seed_demo_data` | none | Source fixture world across three cohorts, interests, public venues, threads/RSVPs/presence, series, events, giving, civic partners, saved searches, connections and E2EE conversation shells |
| `seed_browse_demo` | none | Source 40 adult browse scenarios and 12 venues; idempotent titles |
| `generate_demo_events` | `synthesize`, `dry_run`, `force` | Source weekday-preserving date refresh and marked deterministic demo happenings |
| `seed_mobile_card_demo` | none | Source five adult cards, four generated cover inputs, one accent fallback; native media safety pipeline |

All demo/load handlers require `Config.DemoEnabled`, supplied only by the CLI's validated
loopback development configuration. `force` cannot override this gate. The source fixture
passwords stay inside the development hash writer and are never printed. The native
production password writer is unchanged. Existing unmarked accounts and their age/role
state cannot be rewritten; fixture guardian consent cannot revive an existing relationship.
Child fixture venue approval and historical completion require both fixture identity and
seed-owned venue markers, with native audit records. No real seeds are run in qualification.

The embedded fixture/mapping manifests were extracted offline from the existing source
commands and adapters, without credentials. Overture/Google/Wikidata are typed native
provider seams supplied by the host CLI. Missing providers fail explicitly. Profile dHash
uses the host native codec service, including WebP/AVIF; standard PNG/JPEG decoding is an
offline fallback. Source data licenses and namespaced acquisition metadata survive upserts
and merges. Native dedup additionally declines any referenced venue rather than deleting
ownership, consent/publication, moderation or media dependents.

The local COPY loader accepts only the three source catalog tables and exact harmless
source SET statements. It rejects arbitrary SQL, traversal, unsupported tables and
unterminated blocks. Current event columns omitted by the historic data file receive
source migration defaults. The repository seed contains seven venues, nine edges and
39 event rows (its maximum event ID is 40).

`-commands-test-dsn` tests use random real-FK schemas, fixture actors and mocked source
bytes. They cover source conversion, dry-run, idempotence, protected disputes, dependency
refusal, license retention, ICS, hours, booking seeds, all fixture scenarios, production
refusal and local COPY admission. Mobile seed tests verify generated image shape through
the native media adapter seam; the media package separately qualifies real codecs/scanning.
