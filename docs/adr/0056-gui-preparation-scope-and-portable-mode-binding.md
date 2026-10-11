# ADR-0056: GUI preparation scope, portable live mode binding, default-context owner attribute

Date: 2026-10-11
Status: proposed — owner decision Q2 recorded; mode fix on branch `fix/gui-review-20261011`;
the 794573c live-output change awaits programme privacy/independent review before landing

## Decision

**Owner decision Q2** (provenance: owner answer in Claude Code independent-review session,
2026-10-11 ~20:24 UTC; recorded verbatim, unsigned). Question: "Teacher/Social prep slices run
while the plan says Cat alone until M2 green." Answer: "Authorise prep": Teacher/Social may run
original-oracle and pre-M0 preparation slices on their feature branches in parallel with Cat —
no landing, no kit sync, no formal S2A/S3A milestone claims, never during a timed Cat (LCP/INP) run.

**Relabel.** Every GUI slice on `feat/gui-app-migration-a` (6ec8bd8..22f3746) and this fix is
pre-S3A preparation under Q2, not an S3A M1.x milestone. WORKLOG's "S3A M1.3" label for a8b9dc5
reads as preparation; no S3A milestone is claimed or accepted.

**Portable live mode binding.** Live (non-capture) source comparisons in the default
`TestGUIPublicGetWalk`, `TestGUIPublicGetWalkSourceRefusals` and
`TestGUIPreferencesRegisteredGETTransport` compare bytes, SHA-256, file type and executable bits
(mask `0o170111`). Recorded `full_mode` pins, `source.json`, `cases.json` and historical receipts
are unchanged; the strict capture binder `guiPublicGetBindProducers` still compares full modes.

**794573c changes live output.** `renderer.go` adds `public_id` to the default `user` context, so
default-context pages (`privacy`, `terms`, `display_preferences`, `account_delete`, `open_data`;
`views.go` returns an empty context for them) now emit the signed-in owner's `public_id` in
`data-meetups-owner`. `static/js/site.js` then registers the meetups service worker and runs the
owner-change/logout purge on those pages. `server.go` sets `avatar_uri` on an own-context
`user`, so own-context pages now show the nav avatar. This is believed to restore Django parity
(not verified). It needs the programme's privacy/independent review before landing.

## Context / why

Git stores only modes 100644/100755 and checkout permissions follow the umask, but the pins
record 0o100664, 0o100644 and 0o100600 together. At 22f3746 all three tests failed on fresh
`git archive` exports under umask 022, 002 and 077 ("historical native producer binding
differs", "reviewed current repair profile unavailable", "current preference source138
refused"). Why not re-pin modes: no single umask satisfies mixed pins, and the pins are history.

## Consequences

- The repair `mode` refusal control now proves group/owner/other exec bits, a symlink type and a
  setuid bit refuse on two inputs; a 0o100644 checkout of a 0o100664 pin is admitted by design.
- Open owner question: 20 branch-added focused GUI tests (activity browse/form, my meetups,
  places nonce; preferences layout; campaign meter; community pagination/style) fail in the
  default no-flag `go test ./internal/web` because their normalizer/archive/original15 bindings
  are mandatory and "omission fails rather than skips" (docs/gui-public-golden-fixtures.md).
  `main` 78d3a66 passes that package, so `scripts/check-native.sh` is red on the branch, which
  ADR-0040 §1 forbids for landing. Owner to choose: a validated-source-only no-flag path (the
  `TestGUIPublicGetWalk` pattern, no `t.Skip`) or a landing-gate exception.
