# Native reviewed agent snapshots

New(db, publicSite).Snapshot(ctx, directory) produces the existing schema-two files from one
repeatable-read generation. It shares the native public catalog gate and the source adult-only
activity opt-in subset; no account, membership, raw tag or activity description data is emitted.
Crowd-corrected venue display fields, source facts and provenance/credits survive the transformation.

Files publish atomically and manifest.json publishes last. Manifest entries pin exact published
UTF-8 bytes with SHA-256. Source caps remain 10,000 events, 50,000 venues and 2,000 activities;
truncation is explicit. Taxonomy count is categories plus active types. All dates normalize to UTC Z.

This producer is not wired into the server: the `export_agent_snapshot` job runs
`internal/jobs/snapshot.go`, which applies the same publication gates but differs in several
emitted values (name fallback, timestamp fraction, slug length, credit trimming) and reads
without one repeatable-read generation. Choosing one producer is open work (review item
GO-EXPORT-01). The standalone agentapi consumer serves only the checked file contract. Synthetic tests verify privacy allowlists, exact hashes,
generation consistency and taxonomy counts with actual baseline foreign keys.
