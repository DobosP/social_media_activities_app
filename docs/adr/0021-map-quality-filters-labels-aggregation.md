# ADR-0021: Map-quality filters, derived unnamed-place labels, and sub-venue aggregation

Date: 2026-07-07
Status: accepted

## Decision
Map concept filtering treats a typed or selected activity/category as a high-confidence place-activity edge match, using confidence >= 0.5 on the client-side GeoJSON payload; server API defaults remain unchanged, with explicit min_confidence support composing with activity/category filters. Unnamed imported places render a read-time synthetic label from their strongest non-disputed activity edge without writing the label to the database. Unnamed OSM/Overture sport sub-venues can be merged into nearby named public complexes only through an explicit, dry-run-capable aggregation command or the opt-in ingest flag.

## Context / why
Real Cluj OSM data showed hundreds of blank-name pins, broad generic venue rules making typo searches like "fotball" light up every sporty park/school, and sports complexes rendering every unnamed field as a separate map pin. Changing API defaults would alter existing consumers, so the stricter map semantics are client-side for this slice; the server-side min_confidence hook is opt-in only.

## Consequences
The map now distinguishes real pitches/courts from low-confidence generic guesses for concept search and category chips. Public labels are more useful while preserving source data and correction semantics. Aggregation is idempotent and conservative: deletion is skipped when the unnamed place has scheduled social activities, cover, corrections, proposals, or claims. The ingest job remains contract-stable unless run with --aggregate.
