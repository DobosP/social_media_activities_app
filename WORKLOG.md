# Worklog — social_media_activities_app

Append-only, newest first. Current truth is `STATUS.md`; this file holds the dated detail
`STATUS.md` summarizes.

## 2026-10-04 — Go public serving foundation (review branch)

Valid until: this branch is reviewed/landed or superseded — then treat as history.

- Owner selected Go after the Romanian Go rollout. `feat/go-server-foundation`
  implements ADR-0031's first serving slice; source is not landed/deployed here.
- Checked company-ops and canonical deployment/media policy read-only; no org
  source, governance, storage, provider or visibility changes. Shared private
  workflow sources returned unavailable, so local Go checks were added.
- Complete Go race/vet and native static build passed. Isolated PostGIS tests:
  exporter/native contract plus public discovery/listing **38 passed** (3.61s).
  All data is generated test data; native query contract ran, not skipped.
- Official govulncheck 1.1.4 source SSA panicked on Go 1.27 syntax. Conservative
  package and compiled-binary scans both passed: no vulnerabilities found.
- Static scratch OCI image builds with a verified Go builder digest; UID65534,
  read-only/no capabilities/no-new-privileges, 512MiB/1CPU/64PIDs and no-network
  local qualification passed native health. Tiny fixture idle memory ~1.9MiB is
  not a production sizing or total-bill estimate. Final Trivy0.75.0 scan: zero
  fixable HIGH/CRITICAL findings. Ruff621files, YAML/Compose, docs37files, budgets
  and whitespace pass. Receipt: `docs/reviews/go-foundation/verification.json`.
- GitHub Go quality/container/security run37156292496 passed; frontend,
  dependency audit and Django image job passed. Initial full Django CI stopped
  at archived Bullseye PGDG apt sources before tests. The pinned CI-only database
  now uses the signed official PGDG archive, retaining server hold and signature
  verification ([PGDG notice](https://wiki.postgresql.org/wiki/Apt)). Runtime
  database images and live databases were not changed.
- Remaining review: human privacy/safety sign-off before landing, explicit
  withdrawal/erasure and staleness budget before rollout, then live API parity.
  Separate ManagedScanner malformed-verdict gap is recorded in ADR-0031; no
  scanner or media behavior was changed by this branch.

## 2026-08-22 — one nightly job, one RO-EDU credential

- `sync_roedu` runs the venues lane and the events lane back to back, but only the venues lane ever
  saw `ROEDU_API_KEY`. The events lane took its key from an argparse default of `social-app-dev` that
  nobody passed, so a single nightly job authenticated to the producer as two different clients —
  silently, because a dev credential that happens to be accepted looks exactly like success.
- Three sites, one rule: the credential comes from the environment, and its absence is an error rather
  than a fallback. `sync_roedu.py` forwards `os.environ["ROEDU_API_KEY"]` to the events lane (its
  existing fail-open guard already returns early when the variable is unset, so the subscript cannot
  raise). `sync_roedu_events.py` drops the hard-coded default, resolves `--api-key` or `ROEDU_API_KEY`,
  and raises `CommandError` when neither is present. `ro_scraper.py` drops the same `social-app-dev`
  fallback, so a missing key fails at the call instead of reaching production as dev.
- The test that pinned the broken argv asserted the exact list without `--api-key`; it now asserts the
  key is forwarded. Added: both lanes send the same credential, and neither the events command nor the
  ingestion adapter carries a dev fallback — the last two read the source for the fallback PATTERN, not
  the string. Eleven events-sync tests were resolving that default without saying so; they get an
  explicit credential fixture.
- Verified: 142 passed across the touched lanes; full suite 2773 passed with 15 failures, all in the
  chat/messaging websocket consumer tests, which fail identically (12 of 12, same tests) on pristine
  main in isolation. Both CI ruff commands clean, `makemigrations --check` clean.

## 2026-08-19 — ruff green, stamp beats formatter

- Cleared the ruff red that had sat on main since the vendored-client adoption: unused and mid-file
  imports in the app wrapper, an unsorted import block in the stamp test, and — the real one — the
  generated `_roedu_client_core.py`, which `ruff format` wants to rewrite and the `VENDORED_SHA256`
  stamp forbids.
- Regenerating was not the fix: the producer's own `--check` reports the copy already in sync, and its
  canonical file is not format-clean upstream either. The generated file is excluded from the FORMATTER
  ONLY; `ruff check` still lints it.
- Verified: `ruff check .` and `ruff format --check .` (the two commands CI runs) clean; suite 2785
  passed + 38 subtests on a fresh DB.

## 2026-08-18 — a refused RO-EDU product is loud (ADR-0030)

- **A refused RO-EDU product no longer reads as an empty city (ADR-0030, 2026-08-18).** The
  shared core ends its walk on `available: false` silently, so a policy-gate refusal
  and a city with no events produced the identical output: `places: created=0` /
  `applied 0 events`, exit 0 — while the page `note` naming the actual reason was
  dropped. Verified against a live server on 2026-08-18: every products page came back
  `available: false` ("schema not ready: … missing required policy column(s) …") and
  both commands reported a clean zero. `RoeduClient.iter_required` (this app's layer,
  not the stamped core) now raises `RoeduProductUnavailable` carrying that note, on the
  first page and mid-walk alike — the mid-walk case had been truncating a
  plausible-looking result set with no signal at all. `ingest_places --source=roedu`
  and `sync_roedu_events` exit non-zero with the note; the scheduled `sync_roedu` job
  catches it, logs it with a stack, reports it to Sentry when configured, writes it to
  stderr, and then still runs `resolve_place_covers` and completes the tick — the shared
  tick carries the GDPR/DSA duties and pings its heartbeat only on a fully clean run, so an
  opt-in external source must not red-line it, and cover resolution is city-scoped rather
  than RO-EDU-scoped. The
  app-pack lane refuses the same way (`read_app_pack` raises when a pack is empty because
  the producer withheld items or reported errors) — that is the lane a promoted release
  uses, so leaving it silent would have kept the defect where it matters most. Still quiet
  by design: the configuration skips, a genuinely empty product/pack, and items dropped by
  this app's own canonical checks (they make the read incomplete instead, so absence is
  never reconciled). Plain `iter` keeps the core's semantics.

## 2026-08-10 — Art.17 provenance follow-ups

- **Art.17 provenance follow-ups (2026-08-10).** Five surfaces that read the same
  provenance question now agree, via one helper —
  `safety.targets_with_unlifted_remove` — which is THE single implementation of "the
  platform's removal is still in force". Two independent reasons keep content hidden:
  the AUTHOR's own act (`is_author_deleted`, permanent, never cleared) and a standing
  REMOVE (the platform's act, liftable). (1) A granted appeal whose un-hide is
  declined no longer tells the user "any restriction has been removed" — the
  notification says the message stays deleted because they deleted it, and the F19
  record carries the same line BEFORE they decide whether to contest. (2) The
  self-delete path refuses while a contest of the REMOVE is pending, and its flash
  only claims a moderation decision exists when one actually does. (3) The GDPR
  export returns the author's OWN withdrawn words to the author — but NOT to a
  guardian on the ward path (`build_user_export(..., for_self=False)` keeps
  `[removed]`), because the guardian is a read-only observer and a child's
  affirmative withdrawal gets the most protective reading. **Owner-ratified
  2026-08-12**, together with two related calls: the self-delete refusal while a
  contest of the REMOVE is pending stands (accepting that no appeal-withdraw path
  exists, so it holds until a moderator decides), and `PostAdmin`'s editable
  `is_hidden` stays an operator escape hatch — with the consequence recorded at
  `apps/social/admin.py`, that an admin hide carries no provenance and so becomes
  indistinguishable from a self-delete once the author also deletes. (4) The
  export's own-post slice is
  newest-first with an explicit truncation marker. (5) An expired attachment whose
  post is hidden ONLY by the author's own deletion, with no standing REMOVE, is now
  reclaimed rather than exempted forever — it is nobody's evidence, and permanent
  exemption fails GDPR storage limitation (Art. 5(1)(e)). The REMOVE-then-self-delete
  order stays exempt. An admin manual hide is byte-identical in data to a plain
  self-delete once the author also deletes, so an admin hold that must survive the
  author's deletion needs a real REMOVE action.

## 2026-08-09 — DSA Art.17 redress correctness

- **DSA Art.17 redress correctness (2026-08-09).** Two defects on the statutory
  redress path are fixed. (1) An author self-delete and a moderator REMOVE both set
  `Post.is_hidden`, so granting an appeal republished content the author had
  withdrawn; `Post.is_author_deleted` now records provenance, `_reverse_action`
  declines the un-hide (auditing `moderation.reversal_left_hidden`) while still
  lifting the action, and migration `social/0039` backfills historical self-deletes
  from the `post.self_deleted` audit rows so the fix is retroactive. (2)
  `safety_record_for` prefiltered own content with `[:500]`/`[:1000]` id slices;
  because `Post.Meta.ordering` is `["created_at"]` those kept the OLDEST rows and
  dropped the NEWEST, hiding recent decisions from the Art.16/17 record and from the
  GDPR Art.20 export, and making them uncontestable from that surface (the contest
  form posts `action_id`). The activity slice was worse still — `Activity` declares
  no ordering, so its 500 were arbitrary and could differ between page loads. The
  three scopes are now queried separately (each `[:limit]`, merged newest-first)
  rather than OR-ed: PostgreSQL cannot BitmapOr across a SubPlan arm, so the
  single-filter form seq-scans the whole action table and its hashed SubPlan cannot
  spill. Content rows are locked with `select_for_update` on both the reversal and
  the self-delete path, so the two cannot interleave into a republish.

## 2026-07-26 — canonical /v1 client adopted (romania_scraper ADR-0069)

- **Canonical `/v1` client adopted (2026-07-26, romania_scraper ADR-0069), and it
  fixed a real defect.** This app's private `iter()` followed `next_cursor` until it
  was falsy with **no repeated-cursor guard**, and `max_records` defaults to `None`
  — so there was no bound of any kind. A server or bug echoing one cursor made it
  re-yield the same page forever. `iter_app_pack()` always had that guard; the
  product walk did not. Transport and pagination now come from the generated,
  stamped `apps/ingestion/sources/_roedu_client_core.py`, so product iteration fails
  closed with `RoeduContractError` on a repeated cursor. The app also **gains
  `pages()`**, which the private copy lacked entirely, making page-level
  snapshot/release metadata reachable. What stays local is this app's publication
  gate — redistributability, policy-attestation currency, venue/commerce/event shape
  validation, canonical pack naming, `iter_app_pack`/`read_app_pack` — because
  deciding what may be published is this app's decision, not `/v1` transport.
  `RoeduContractError` is imported from the core so the domain layer and shared
  paging raise one class. Hand-edits are caught by the `VENDORED_SHA256` stamp
  (`apps/ingestion/tests/test_roedu_client_vendored.py`, 10 tests). Because that stamp
  forbids local edits, the generated file is excluded from `ruff format` (and only from
  the formatter — `ruff check` still lints it); the canonical file is not format-clean
  under the producer's own ruff either, so a resync cannot settle it.

## 2026-07-16 — verification gates

- Fresh 2026-07-16 gates: Ruff 0.15.21 check/format and migration drift passed;
  the focused RO-EDU/lifecycle/public-projection suite passed 178 tests plus 27
  subtests; the full isolated PostGIS suite passed 2,672 tests with 30 skips and
  27 subtests; the producer→server→both-real-clients loopback passed 84 tests.
- No real network ingestion, deploy, or child-facing data mutation is part of
  these gates; consumer fixtures and the loopback serving projection are used.
