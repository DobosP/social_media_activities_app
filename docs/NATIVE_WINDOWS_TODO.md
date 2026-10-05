# Native migration Windows TODO

Last verified: 2026-10-05. Requested bounded restart handoff; resume only on the human's Windows instruction.
Current truth remains [STATUS](../STATUS.md). This checklist does not authorize main landing or production activation.

## Preserved source and qualification

- Repo: social_media_activities_app; existing branch `feat/go-native-toolchain`, Linux worktree
  `/home/dobo/work/_worktrees/social_media_activities_app/feat__go-native-toolchain`.
- Frozen source inventory base: `ce3d0e3ee9f180ce2e95140300b12582db9895d6`; prior toolchain checkpoint
  `b8453f7f10cf5c6e3060e0979b86db2b3505e063`; source restoration `a69329141cc423f8a45a9889a7e9ebdac0a37de0`.
- Independent credential repair: `05febd5003b3640ad57b5c335f356a57d7bba916`. P1/P2 admission binding is
  independently green for this scope; pinned authcore is unchanged. Exact JSON keys, canonical delegation,
  same normalized pair and actual PG/replica/proxy/session proofs remain checked in.
- This file and [restart-checkpoint.json](reviews/native-go/restart-checkpoint.json) are in the final local
  restart checkpoint. Resolve that checkpoint SHA with `git log -1 feat/go-native-toolchain`; the coordinator
  reports/publishes its exact SHA. Worker never pushes or merges main.
- Remote `origin/main` fetched and verified: `cd006e3abe028acb2ccf5be4552bc94aebc1d234`. The shared landing
  checkout remains main/clean. The coordinator must integrate newer main and preserve manual-only CI/ADR claims.
- Older worker refs are published: `feat/go-admin-api-parity`, `feat/go-config-observability`,
  `feat/go-shared-budgets`, `fix/go-review-budgets`, `fix/go-review-profile`, `fix/go-review-media`;
  original `feat/go-migration-finish`/PR108 remains. The coordinator separately preserves the exact stash as
  `chore/go-config-stash-preserve` at `0ac4faf2d8a3940a313231c0d485bdeac4678a15`; third parent
  `4e96f498e8c941d007ea284611d947d9ff509080` preserves untracked originals. Archive only, **not for merge**:
  do not apply/pop/drop it or assume all its source is superseded.
- Strict gate: **2671 original declarations;993 claimed-verified (manifest `runtime_verification`, not checked against a test run),1678 unresolved,0invalid; exit1**. All original Python
  source/tests remain. Test counts or named Go links do not establish equivalence. No reference retirement is complete.
- Frozen bundle:586 affected top-level race tests/14 lanes pass with zero skips;40 unchanged prior qualified
  tests retain their receipts (626/21 lanes). Source/hashes/three-module vet/race pass; source/package/linked
  vulnerability gates pass, with one unused required-module advisory. Aggregate named-test/log hashes are in the receipt.
- Image: `sha256:c44143fdc56f7cde24460124e1c037b33ead934ed72a75b03b6283e17164e44a`.
  Server: `5ae16c13a2b7d105b9408174398bd88c9bd630fcf4fd580d8f13d1ae552e24a8`.
  Offline Go1.27.1/CGO0/buildvcsfalse/trimpath/s-w server overlays exact qualified runtime
  `sha256:5ea84fca75b13acdbc7d13a71e01a52954f3733e358efe29b1e4ba07b771ce5d`; twelve runtime layers and
  execution config unchanged, one server layer added. Canonical fresh Dockerfile build did **not** pass:
  uncached dependency install required networking. Frontend/templates/locale/static/db/auth license sources
  are unchanged since a693291; embedded worker ships in the new server. Payload/codec/license hashes are retained.
- Current image HTTP: health/ready/worker/schema200, UID10001, read-only/drop-all/no host ports; SIGTERM exit0/noOOM.
  The new image vulnerability scan is **unverified**: read-only temp, task UID and existing scanner-cache
  permissions failed before scanning. Prior image audit cannot qualify the changed executable.

## Next required work

1. Verify the coordinator's branch publication completed and the Windows checkout SHA/dirty state. Reuse this
   branch in a task worktree; never switch the shared landing checkout away from main. Read this TODO/STATUS first.
2. Complete the pending image gate with a fresh task-owned writable scanner cache/temp and the retained public
   database, or rebuild the canonical image when permitted. Recheck source/server/runtime closure for any new artifact.
   Do not download another SDK/model or claim the failed Docker/scanner attempts as passes.
3. Continue the1678 unresolved exact source cases in bounded queues, preserving original IDs/file/line/hash and
   every original assertion. Current coverage manifests under `services/server/internal/contracts/testdata` are authoritative.
   Remaining queues include messaging list/history query ceilings (actual7>5 and9>7), generic V1 product client
   paging/refusal behavior, export/operators, calendar/thread/web forms, media/official-cover and account/safety cases.
4. The bounded generic V1 client prototype remains unpublished/unwired. Its unique source/tests are preserved as
   [inert keepers](reviews/native-go/continuation-keepers/manifest.json). Inspect/recreate a Go overlay, map exact
   legacy cases and review additional bounds before publishing as source. Keep canonical app-pack/pins/gates separate.
5. Keep the inherited guardian observer block behavior as an explicit policy-review question. Current transparent
   oversight is preserved; no guardian block veto is implemented or claimed. Do not silently change parental authority.
6. Independently review source-case semantics and scoped policy replacements. Human auth/privacy/safety review
   precedes landing; PR108 and full reference retirement remain gated. Producer target registry/pin reconciliation
   is coordinator-owned; never hand-edit the generated `_roedu_client_core.py` or bump producer policy implicitly.

## Reproducible checks

Use the available Go1.27.1 runtime and Node24. On Windows adapt paths to the task worktree and put all caches/tmp
under its workspace `_temp` slug. The following Linux paths identify the preserved recipe, not a required Windows host.

```bash
export GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2 GOPROXY=off
export GOMODCACHE=/home/dobo/work/_temp/go-native-toolchain/go-mod-cache
export GOCACHE=/home/dobo/work/_temp/go-native-toolchain/go-cache
export TMPDIR=/home/dobo/work/_temp/go-native-toolchain/test-tmp
scripts/check-native.sh /mnt/data/decision-lab-runtime/kev-native/toolchain/go/bin/go
go -C services/server run ./cmd/check-contracts -root "$PWD" -summary
scripts/qualify-native.sh GO IMAGE PRIVATE_NETWORK SYNTHETIC_DSN ABSOLUTE_TASK_SCRATCH
python3 ~/work/agent-ops/scripts/check_docs.py .
git diff --check
```

Only rerun affected fixtures when source changes justify it. Actual PG/codec qualification requires explicit
synthetic fixtures and zero skips; DSN-free hermetic skips never qualify a release. Generic fleet Python governance
is the scoped exception. Do not execute ordinary Python reference tooling without explicit authorization.

## Restart inventory and keepers

- All owned build/qualification/audit commands completed; no campaign should restart after this handoff.
- Owned `go-native-toolchain-bundle-smoke` is retained exited0. `go-native-toolchain-db` and the private internal
  `go-native-toolchain-test` network are retained; the DB is stopped for restart. No host ports or real data.
- Linux-only logs, test binaries, public Go/Trivy caches, source manifests and local images remain in
  `/home/dobo/work/_temp/go-native-toolchain`. Preserve them and all unlanded worktrees/branches/stashes;
  no shared-cache pruning or unmerged cleanup was authorized. Docker images do not travel through Git.
- The coordinator handles first publication through `ops publish social_media_activities_app feat/go-native-toolchain`
  and pauses its completion heartbeat. This worker owns no continuation automation and must stop implementation now.
