# Post-landing GUI hardening — 2026-10-21

Valid until: the next change to `main` — then treat as history.

Scope: `main` `63e3e25` → product head `9a3c306` (5 commits) plus one documentation commit. Closes the
deferred follow-ups of the [main landing review](../gui-main-landing/README.md): TI-2, TI-3, TI-4, TI-6,
OSP-1 and the go1.27.3 gate assertion (TC-1, [ADR-0072](../../adr/0072-go-1-27-3-security-toolchain.md)).
Authority: [ADR-0073](../../adr/0073-owner-authorized-functional-gui-main-landing.md) (Paul 2026-10-21).
Exact commands, image IDs and log hashes: [results.json](results.json).

## Changes

| ID | Change | Proof it fails without the fix |
|---|---|---|
| OSP-1 | `pages/group-detail.gohtml`: `presend-nudge.js` and `thread-chat.js` carry the response nonce | TI-3 test red on exactly those 2 scripts × 4 live states before the edit |
| TI-3 | `native_output_invariants_test.go`: 110 states (78 completion previews, 4 group-detail variants, 12 other native renderer fixtures; EN and RO) through `Renderer.Render` with CSP enforced — one nonempty `csrfmiddlewaretoken` per POST form (350), header script-src nonce on every executable script (248), no `style=`, `<style>`, inline handlers, `javascript:` URLs or form overrides; comments end where browsers end them (`<!-->`, `--!>`) | 21 single-defect mutations of real output plus a missing-header-nonce case, each with its expected message; deleting any of the 16 checker branches fails a subtest |
| TI-4 | `group_detail_roster_guard_test.go`: 11 control-then-defect cases + outside-cohort curator case | disabling the cohort gate, the roster gate, its member or thread clause, or the 1000-row cap turns exactly the matching cases red |
| TI-2 | `check-native.sh`: `go test -race -count=1` | harness case r red against main's script |
| TC-1 | `check-native.sh`, `qualify-native.sh`: refuse an effective server-module toolchain older than go1.27.3; harness cases s–x; `qualify-native-final.py` re-bound to the new `qualify-native.sh` digest | harness 14/24 against main's scripts, 19/24 with only `-count=1` back-ported |
| TI-6 | `test_qualify_native_final.py`: `TMPDIR` or `tempfile.gettempdir()` | main's file raises `KeyError` with `TMPDIR` unset; head 8/8 set, unset or empty |

No custody-pinned input changed: a combined newline probe of every touched file kept all 26 server packages
green, while the positive control (one newline in `services/server/go.mod`) turned 12 custody tests red. The
custody family (TI-1) is untouched.

## ADR-0040 gates on `9a3c306` — all pass

Source chain with sha256-verified go.dev go1.27.3, `GOTOOLCHAIN=local`, fresh GOCACHE: check-native exit 0
(32 package results + worker 4/4); gate harness 24/24; govulncheck v1.8.0 8/8 clean; Node 24 frontend 3/3,
36.72/40 KiB; check-contracts verified 1020 / unresolved 1651 / invalid 0 (exit 1 expected). Image chain from a
`git archive` of the head: `docker build --no-cache --pull` release, sidecar and DB images; image checks; Trivy
v0.75.0 0 HIGH/CRITICAL on release and sidecar; `--migrate-only`; `qualify-native.sh` on the fresh image: 21 packages, 984 top-level /
2,770 named passes (web 313/1,344: the prior 309/1,308 plus the 4 new tests and their 36 subtests), 0
skips/failures/races, 3,341 s; container smoke 5×200, uid 10001, CapEff 0, SIGTERM exit 0. No Dockerfile or
dependency changed.

## Independent review

An independent Opus reviewer worked only from `git archive` copies.

- On `b7604ed` it returned **APPROVE-WITH-NOTES**:
  - Every new test fails without its fix: the template revert, each guard mutation, the checker and template
    mutations, the harness against main's scripts, and the qualifier test with `TMPDIR` unset.
  - No pinned input changed. Custody tests pass at head (253 top-level passes against 249 on main), and the
    skip set is unchanged.
  - The version-compare edge cases, the module choice and the `NATIVE_SHA` splice anchors are correct.
  - The only product change is the two nonce attributes.
- Its notes and how they were handled:
  - **N1:** `<!-->` and `--!>` let a script slip past the tokenizer. Fixed.
  - **N2:** some checker branches had no mutation, and `javascript:` URLs were not checked. Fixed.
  - **N3:** a test comment overstated what it proved. Fixed.
  - **N4:** the form floor was loose. Fixed.
  - **N5:** STATUS and the stale `AGENTS.md` label. STATUS is updated here; the label stays because the file
    is pinned.
- On the final head `9a3c306` it returned **APPROVE**:
  - Its own sweep killed 16 of 16 checker branches.
  - The new comment parser adds no false negative across 12 comment forms.
  - Nothing under `services/` reads the documents in this commit.
- Information only:
  - Of the URL attributes, only `href` has a mutation.
  - `object data`, meta refresh and `vbscript:` are not checked.
  - The roster gate's two cohort clauses are each redundant with the cohort gate.

Not covered: no browser re-run, no human review (GOV-4 still gates first deployment), no production,
provider, government or minor activation. `AGENTS.md` still labels check-native "Go 1.27.1"; it is
custody-pinned (TI-1), so the label waits for the owner decision.
