# Native public catalog

This package uses the existing PostgreSQL/PostGIS schema and the source public-place gate for all
public reads. Event endpoints preserve source lifecycle/date/search/proximity filters, exact
price strings and credit metadata. Places preserve GeoJSON pagination, non-disputed activity
edges, crowd-corrected display values and honest open/closed/unknown states.

Native reference seeding is embedded, idempotent and preserves existing operator edits. It seeds
only taxonomy, curated child venue classes and content-type reference metadata; no account data
is imported. Synthetic tests retain the actual baseline foreign keys.

Read services and mutation methods cover venue facts, independent correction quorum, inferred-edge
votes/staff reversal, wrong-hours and closure overlays, private steward claims, and event accuracy
reports. Claim contact/evidence data is excluded from public partner projections. The host owns the
legacy web/admin bridges and must wire batch licensed-media callbacks.

The package currently uses the reviewed source default quorums, report decay and action budgets.
A host must reject unsupported nondefault settings or supply reviewed policy adapters before
activation; silently replacing deployment-specific publication rules with defaults is unsafe.
