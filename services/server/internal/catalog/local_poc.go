package catalog

// Local-POC provenance markers (ADR-0057). A development-only RO-EDU local POC
// sync flags what it wrote; every page that shows a flagged row labels it with
// LocalPOCMarker. The flags are read in every mode, so a POC database served
// without the POC gate still shows the label.

// LocalPOCMarker is the visible label on every page showing POC-sourced rows.
const LocalPOCMarker = "LOCAL POC — not cleared for publication"

// EventLocalPOCSQL is true for an events_event row `e` written by a POC sync.
// The source_local_poc column exists only in a database a POC sync has written
// to (the production schema is unchanged), so the row is read through to_jsonb:
// where the column is absent this is false, never an error.
const EventLocalPOCSQL = `coalesce((to_jsonb(e)->>'source_local_poc')='true',false)`

// PlaceLocalPOC reports the POC flag a POC sync stored in a place's raw tags.
func PlaceLocalPOC(tags map[string]any) bool { return tags["roedu_local_poc"] == true }
