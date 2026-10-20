# ADR-0057 — Development-only RO-EDU local proof of concept (consumer half)

Date: 2026-10-12
Status: accepted (owner authorization 2026-10-12, "POC consumer half"); landing per ADR-0040; human
privacy review gates any use beyond a loopback developer machine
Builds on: [ADR-0055](0055-roedu-policy-schema5-ruleset8.md) (5/8 pin) and
[ADR-0024](0024-canonical-roedu-social-app-pack.md) (canonical pack). Changes neither.

## Decision

The social server can ingest and show a RO-EDU **local POC** release on one developer machine.
The cross-repo local-POC contract v1 is shared with romania_scraper (its ADR-0610), ro_data_server
and ro_teacher. In a POC release each item keeps its REAL publish_source decision: a deny. It also
carries a closed `local_poc_admission` that records which deny reasons the owner waived for a
loopback-only development build. The admission is never a legal basis. A POC page is never accepted
in production, and nothing POC-sourced is shown without the marker
**"LOCAL POC — not cleared for publication"**.

**Gate.** Every condition must hold. If any fails, the server refuses to start; it never runs a
silent half-POC. When `ROEDU_LOCAL_POC` is unset, the server behaves exactly as before.
- `ROEDU_LOCAL_POC` is exactly `1`.
- The existing development mode is on (`--dev` or `DJANGO_DEBUG`).
- `--dev-container` is off.
- `SOCIAL_REACT_UI` is off, because the marker lives in the server-rendered templates.
- `ROEDU_API_URL` is http(s) with a loopback host (`localhost`, 127/8 or `::1`).

Two checks repeat inside `internal/jobs`:
- `RoeduClient.LocalPOCActive` re-checks the loopback host.
- `ApplyRoedu` refuses a POC pack when the client gate is off (`ErrLocalPOCRefused`).

**Page.** A page is a POC page only when:
- its envelope carries `local_poc: true`, as a boolean;
- the gate is on;
- every page of the snapshot agrees (anything else is drift).

The production envelope check is unchanged, so a production client still refuses any page that has
the extra key.

**Items.** On a POC page:
- An item without an admission goes through the unchanged production `ValidatePackItem`.
- An item with an admission must pass the contract verifier rule:
  1. the attestation's `action` is `publish_source` and its `effect` is `deny`;
  2. the set of its reasons equals `waived_reasons`, which must be sorted, unique, non-empty and inside
     the closed WAIVABLE set;
  3. `underlying_decision_id` equals the attestation's `decision_id` and the item's
     `policy_decision_id`;
  4. the admission has exactly 7 keys, `schema_version` 1, the fixed `kind`, `not_a_legal_basis`
     true and an absolute `poc_root`;
  5. `admission_digest` recomputes over the canonical JSON. This is byte-compatible with Python
     `json.dumps(sort_keys=True, separators=(",", ":"), ensure_ascii=False)`, which KAT-1 and KAT-2
     prove.
- Such an item also needs a boolean `gdpr_relevant` and a lowercase-token `privacy_classification`.
- The admission replaces only four clearance checks: `gdpr_relevant=false`,
  `privacy_classification=no_personal_data`, `effect=allow` and `reasons=[explicitly_cleared]`.
- The production validator then runs on a copy that has those four fields set to the production
  constants. So shape, integrity, SHA, tuple and binding checks are exactly the production ones. The
  stored item is never rewritten.

**Storage.** The real values are carried, never the clean constants.
- Each venue gets these raw tags: `roedu_local_poc: true`, `roedu:gdpr_relevant`,
  `roedu:privacy_classification`, `roedu:local_poc_admission`, and the existing
  `roedu:policy_attestation` (with the deny).
- Each event row that the POC sync wrote gets `source_local_poc = true` and
  `source_local_poc_evidence`: the item's real `gdpr_relevant`, `privacy_classification`,
  `policy_decision_id`, `policy_attestation` (with the deny) and its `local_poc_admission`. The POC
  sync adds both columns at the start of its first transaction, so the production schema is
  unchanged. Readers probe for the flag column once per query and only then read the row through
  `to_jsonb(e)`, so it reads as false where the column does not exist and a production database
  pays no per-row cost.
- `ingest_places --source roedu` refuses a POC pack. The generic place merge cannot carry the
  marker, so POC venues enter only through `sync_roedu` / `sync_roedu_events`.

**Sync.**
- A POC sync is never complete. This is enforced, not assumed: when the producer admits every
  item, a POC page is `full`, `snapshot_complete: true` and `withheld: 0`, and the consumer still
  reads it as incomplete (`Read`), and `ApplyRoedu` drops any `Complete` claim on a POC pack. So a
  POC snapshot never reconciles absences (it cannot tombstone non-POC rows), never records a sync
  state and never checks for rollback. This is accepted for the POC. Stale rows stay until a clean
  snapshot replaces them.
- The scheduled job imports POC rows at a floor of 0.9 (Events V3 rows are 1.0 or 0.9). Production
  stays at 1. The manual command already takes `min_confidence`.
- A POC sync never resolves Commons covers, so nothing leaves the PC.
- `ROEDU_SYNC_CITIES` is a comma list of exact producer city facets, for example `Cluj`,
  `București`, `Pitesti`. It is accepted only under the gate. The scheduled job then runs once per
  city and skips a city with no items, which would otherwise be a product refusal.

**Display.**
- Every flagged event or place on event and place pages and lists shows the marker. So do events at
  a flagged venue. This happens in every mode, so a POC database served without the gate is still
  labelled.
- `base.html` shows a banner when the gate is on or when the page shows a flagged row.
- The RSS and Atom event feeds prefix each flagged entry's title with the marker. The sitemap
  ignores every query parameter, so it never lists past events; under the gate it lists upcoming
  POC event URLs, whose pages carry the marker.
- `include_past=1` on `/events/` and on place pages also lists past flagged events (scheduled,
  rescheduled, sold out or expired): upcoming events first, then the most recent past events. This
  works only with the gate on. Production rows are never lifted, and the parameter does nothing
  when the gate is off.

## Context / why

The owner wants a local proof of concept with real local data, where nothing leaves the PC. The
real event documents are denied (privacy `unknown`, `gdpr_relevant=1`), and the canonical contract
correctly refuses them. Relabelling them as cleared would misrepresent provenance. Instead, the deny
is kept and verified, and the waiver is shown.

## Consequences

- Production paths are unchanged: `ValidatePackItem` is untouched, and the production envelope,
  sync floor, schema and pages are as before. Tests pin this.
- A POC item whose waived reasons include `missing_legal_basis` or `privacy_revision_missing` still
  needs the production shape: a non-empty `legal_basis` and a 64-hex `privacy_revision`. Such items
  stay invalid. This is fail-closed and is revisited only by a contract change.
- The SPA (`SOCIAL_REACT_UI`), the JSON API, open-data downloads and agent snapshots carry no
  per-row marker. The gate refuses the SPA, and the remaining machine surfaces are loopback-only in
  the POC; they are an accepted gap. The calendar (ICS) export lists a user's own activities, not
  RO-EDU events.
- The fixture is `internal/jobs/testdata/roedu-local-poc-page.json`, built by
  `generate_roedu_local_poc_fixture.py` from the real 5/8 producer deny.

## Alternatives rejected

- A manifest-level waiver with no per-item admission. Rows would have no waiver evidence of their
  own (contract design, PLAN §D).
- A production schema migration for the event flag. It would change the production schema for a
  development-only feature.
- Accepting `ROEDU_SYNC_CITIES` in production. Empty-city semantics for complete snapshots would need
  their own decision.

## 2026-10-14 — Preserve factual TDM access at the local boundary

The owner's existing private local-POC directive includes usable event facts whose real source
access is `tdm_exception`. The production access allowlist prevented those otherwise valid,
waiver-bearing facts from passing the consumer's local validator. It must not relabel that access
as `public_document` or treat the owner admission as a legal basis.

After the existing admission verifier succeeds, a private access context permits the literal
`tdm_exception` label while applying the same factual shape, digest, policy pin and capture tuple
checks. The access label stays unchanged even on the validation copy. The public
`ValidatePackItem` entry point always selects the strict production context; an item without an
admission still uses it. The existing four clearance substitutions on the private validation copy
remain as described above, and do not rewrite the incoming item or stored deny.

Local event JSON evidence additionally retains the original `access_type`; local venue tags carry
the same literal value. The admission verifier, WAIVABLE set, evaluator, reasons, legal basis,
privacy values, production envelope, local mode/page gates and child-facing authority/consent/
moderation rules are unchanged. The existing digest-bound absolute `poc_root` contract remains;
this clarification adds no configured filesystem-root expectation to the consumer.

Synthetic native controls cover both event and venue acceptance, wholesale input preservation,
strict public/no-admission refusal, malformed or tampered admissions, root grammar/digest changes,
capture/content/acquisition integrity, policy pins, closed factual fields including CNP/prose,
production/remote-client refusal, false/missing page markers, and nil-database dry-run/evidence
retention. They are authored for root qualification; no runtime activation is implied.
