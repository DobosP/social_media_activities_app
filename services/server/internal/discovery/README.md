# Native discovery projections

All seven source discovery routes and their /api compatibility aliases use native PostgreSQL
queries. Canonical feeds read limit-plus-one through signed cursors; only the source near-me and
deck candidate sets retain their deliberate bounds. Deck ordering uses the original SHA-256
seed/id shuffle and Django-compatible signed seed/offset cursor. Its response contains only the
reviewed card fields and navigation actions; no swipe or engagement state is stored.

Public activities/groups have a hard adult opt-in/active-owner gate. Private cards remain within
the current actor's cohort and mutual-block boundary. Public venues share catalog.PublicPlaceSQL;
events retain lifecycle/import-hold/tombstone and crowd-report rules. Home composition preserves
honest reasons and distinct beginner cards. No request coordinates are stored.

The host supplies the common cursor key and batch media visual callback. Real PostGIS tests cover
stray minor public flags, pending user venues, block vetoes, exact deck fields and cursor continuity.
