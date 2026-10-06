# Windows handover — unfinished activity lifecycle assertions

Valid until: this task branch is requalified, changed, or landed — then treat this as history.
Prepared on 2026-10-06 for the Linux session cutoff at 09:50 EEST (06:50 UTC).

## Exact state

- Repository: `social_media_activities_app` (registered fleet repository; existing `origin`).
- Shared main stays clean at `aa10645922ad708db66c9ca0ce982e9f70e4a190`; this lifecycle work is **not merged to main**.
- Main's last verified source: `874b437e8a93caf0340da96bd3448c138cce60bb`, run `cba78ec5232b`:714 fresh tests/all21 lanes, zero skips/failures. [Landed receipt](rest-thread-access-checkpoint.json).
- Unfinished branch: `test/rest-contract-activity-lifecycle-20261006`.
- Assertion source commit: `953d19e5478d038fca8721979b201d44ea682316`.
- The documentation commit containing this file follows that source commit; fetch the published branch and inspect its exact HEAD. The Linux wrap report records the final published HEAD.
- Publication lane: authorized `ops publish` for this task branch only. The Linux wrap report confirms the actual pushed HEAD; verify `origin/test/rest-contract-activity-lifecycle-20261006` before resuming.

## Current finite work and evidence

One named registered-handler test covers three scenarios across both `/api/social` and `/api/v1/social`:

1. Frozen `test_owner_can_edit_activity_via_patch`:274 — tomorrow's **Old**, PATCH title **New name**, HTTP200 and independently stored title.
2. `test_non_owner_cannot_patch_activity`:290 — same-cohort visible **nonmember**, future **Keep**, PATCH **hijack**, HTTP403 with the full activity row and audit count unchanged.
3. `test_owner_can_cancel_via_api`:307 — current **Run**, POST reason **weather**, HTTP200 and independently stored status **cancelled**.

Frozen file hash: `fc35dea7bf6fc36444cb7aa4fcb52a274677bc0c78c328882dcd6ebdb759842f`.
Current test source hash: `39de2e0a93cd2e1d3386f85481b88a9f3dbf32abf53d7ae330366524e2a3ae34`.
Actual affected PostgreSQL/race social lane: **53 PASS, zero skips/failures, all six new cases**.
Positive log SHA: `93fe38d6e5693042acf946e7339ff7fec3359322edc1828ecdce4e1b32076b1c`.
Control evidence and scoped reviewer outcome: `four scoped controls each detected exactly one intended top-level failure with zero skips; correct200/403 statuses remained. Source/runtime checkpoint reviews approved; full21/main review remains pending.`.

Production, schema, dependencies, frontend, app-pack, and accepted policies are unchanged. Owner-positive/nonmember-negative names do **not** exclude active co-organizers; the existing organizer authority remains intact. No production defect or new endpoint is presumed.

The three coverage entries remain **unresolved/not_run**, byte-identical to main. Global ledger stays1020 claimed/1651 unresolved/0invalid of2671; six social declarations remain unresolved. Proposed mappings are pending until actual final qualification and review. This is an unfinished checkpoint, not a passing source-completion manifest.

## Windows pickup

Keep the Windows shared checkout on clean `main`; do not switch or commit task work there. In PowerShell, fetch and create the task worktree from the published branch:

```powershell
$taskRepo = Join-Path $HOME 'work/social_media_activities_app'
$taskOpsRepo = Join-Path $HOME 'work/agent-ops'
$taskBranch = 'test/rest-contract-activity-lifecycle-20261006'
git -C $taskRepo fetch origin $taskBranch
python (Join-Path $taskOpsRepo 'scripts/create_task_worktree.py') --repo $taskRepo --branch $taskBranch --base "origin/$taskBranch" --task 'Resume unfinished lifecycle assertions; preserve co-organizers; qualify before landing' --write
```

If that local branch/worktree already exists, inspect and reuse it without resetting files or deleting unmerged history. In the returned worktree, read this handover, `AGENTS.md`, `STATUS.md`, and [the testing guide](../../agent-testing.md); verify branch, commit and dirty state after moving devices.

## Remaining gates and next commands

**NOT RUN for this branch:** complete `scripts/check-native.sh`, full fresh21-lane `scripts/qualify-native.sh`, source-completion manifest, final source/receipt review, main landing, or main push. The prior main's714/all21 receipt cannot qualify the new test inputs. Expected new total715 is a forecast only, not a passing result.

Native Windows cannot run `qualify-native.sh`: it explicitly requires a Linux host. Use a fresh Linux/WSL2 checkout with working Docker, Go1.27.1, Node24, task-owned caches, an isolated PostgreSQL16/PostGIS/vector fixture, and actual codecs. Recheck real MemAvailable floors (12GiB affected tests;16GiB commit/final), separate disk budget, and the shared heavy-test lock on that host. Do not invent a disk floor or bypass a refusal.

After preserving and rechecking the published assertion source:

```bash
scripts/check-native.sh /absolute/path/to/go
bash scripts/test-native-gates.sh
scripts/qualify-native.sh /absolute/path/to/go IMMUTABLE_CODEC_IMAGE PRIVATE_NETWORK SYNTHETIC_DISPOSABLE_DSN /absolute/task/scratch
go -C services/server run ./cmd/check-contracts -root "$PWD" -summary
python3 "$HOME/work/agent-ops/scripts/check_docs.py" "$PWD"
git diff --check
```

Supply `GOWORK=off`, `GOTOOLCHAIN=local`, `GOMAXPROCS=2`, `GOFLAGS='-mod=readonly -p=2'` and task-owned `GOCACHE`, `GOMODCACHE`, `TMPDIR`; keep scratch under the fleet `_temp` task slug. Use freshly reviewed fixture commands with explicit synthetic credentials; do not discover local environments or secrets. The retained Linux recipes below are historical, tied to Linux absolute paths, and must not be run unmodified elsewhere.

The immutable prior codec environment was `sha256:34c1c2281d9fc96cf486dda8e88497ef5061e21a412c712d8ef61ca14b07031b`; it supplies codecs only. Apply ADR-0040 if serving/dependency/image/frontend inputs change; no optional image/frontend/audit campaign is authorized just by this handover. Do not claim a new deployment image from old codec reuse.

Once actual final qualification passes, independently review only these three mapping proposals and apply them with preserved IDs/lines/hash. Then recheck any changed protected inputs under ADR-0040, record the exact qualifying source and receipts, obtain final independent closure approval, and only then land/push green work. Reference retirement and human first-deployment reviews remain separate. Do not start further cases.

## Preserved Linux-only evidence

Linux task worktree: `/home/dobo/work/_worktrees/social_media_activities_app/test__rest-contract-activity-lifecycle-20261006`.
Private buffer: `/home/dobo/work/_temp/test__rest-contract-activity-lifecycle-20261006` — `worker-receipt.json`, `qualify/qualified/social.log`, `controls/`, `format.log`, module receipts, `deadline-plan.json`, scoped review aggregate and publication/fixture status receipts. These local files are **not in Git and not automatically available on Windows**. Native binaries, caches, database contents and raw local evidence do not replace fresh qualification on another host.

Only aggregate synthetic results are recorded here. No credentials, raw agent transcripts, private audio or corpus content is included. The unmerged branch/worktree, private results, unknown buffers, backups, and absolute keepers are retained. No cleanup75, fleet start, D044 release, deployment, provider/minor/ingestion/schedule activation or recurring work is authorized by this handover.
