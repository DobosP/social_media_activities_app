# Data sources & integrations

Strategy for the two "external data" questions from the brief: **(1)** how to collect as much
place information as possible so users rarely create places, and **(2)** whether/how to make
**bookings through the app** via providers' REST APIs. See [ROADMAP](ROADMAP.md) D1/D7/D8 and
[ARCHITECTURE](ARCHITECTURE.md) (adapter seams).

## Native boundary (verified2026-10-04)

Runtime selection is [ADR-0032](adr/0032-complete-native-go-backend.md). The Go
[command registry](../services/server/internal/commands/README.md) owns adapters and
enrichment; producer data is consumed over the approved HTTP contract. Python code paths
under apps/ are offline compatibility references.

The implemented place sources include OSM/Overpass, bounded Overture Parquet reads and
canonical RO-EDU packs. Native Overture supports bounded local/public HTTPS ranges and
public S3 discovery with bbox/projection/footer/file/row/transfer bounds; it requires no
DuckDB/Python serving process. Native hours parsing and optional Google/Wikidata callbacks
enrich only approved empty fields/namespaced overlays. Real source/provider calls remain
owner-authorized operations, never a startup scheduler.

Native typed RawPlace values flow through validated idempotent upserts and taxonomy
classification. Cross-source deduplication/aggregation is explicit and conservative;
manual/user-confirmed links, source ownership, confidence, license/access metadata and
provenance survive transformations. Canonical pack/schema/completeness/lifecycle checks
fail closed. Events use native bounded ICS/recurrence and RO-EDU fact contracts.

Provider possibilities and access/licensing reviews live in [DATA_PROVIDERS](DATA_PROVIDERS.md);
that registry is not a procurement authorization or a price promise. All real keys belong
to the fleet secret bundle. Avoid uncontrolled polling, per-user cloud inference and
unbounded import batches; use dry runs, declared areas and source/license gates.

## Booking integration (D8)

**Reality check:** there is **no universal booking standard** — it's fragmented per provider.
Some venue/facility platforms expose REST APIs and there are sports-booking aggregators, but
coverage and schemas differ widely. So we phase it:

1. **Deep-links first.** For every place, surface "how to book" (provider link / instructions).
   Zero integration cost, universal coverage. Ships as the baseline.
2. **`BookingProvider` adapter interface.** Same pattern as `SourceAdapter`: a common interface
   (`availability()`, `create_booking()`, `cancel()`), one implementation per provider.
3. **Per-provider REST integrations**, prioritising the **largest Romanian** venue/facility
   providers and any aggregators that cover several venues at once (best coverage per integration).
4. **Map bookings to activities** so a meetup can carry a real reservation.

**Definition of done (D8):** at least one provider supports in-app booking tied to an activity;
everything else falls back to deep-links. Expand provider-by-provider as partnerships allow.

### Open questions for D8

- Which Romanian providers/aggregators have usable APIs, and on what commercial terms (a nonprofit
  may get goodwill access)?
- Auth & liability model for making bookings on a user's behalf.
- Handling cancellations/no-shows and keeping availability fresh without heavy polling.
