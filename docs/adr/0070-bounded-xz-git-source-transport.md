# ADR-0070: Bounded explicit XZ transport for full Git source qualification

Date: 2026-10-20
Status: applied with independently accepted fixed-fixture controls;actual new-producer whole-source preflight and completePG7 qualification pending.

## Decision

Change only the future qualification source-export transport to an explicit
single-thread XZ-6 CRC64 Git filter using the already installed pinned XZ5.4.5.
The entire literal clean Git SHA remains the input. Retain every Git path/blob,
executable mode, tar/PAX identity and the complete compressed command stdout.
No canonical keeper, source path or artifact is omitted to fit the transport.
This is test-source packaging, not production-data handling or a new package,
image, runtime-service dependency or application security claim.

The unchanged compressed regular-file cap is64MiB. The additional expanded-tar
ceiling is128MiB and decoder memory ceiling64MiB; these are extra refusals, not
cap increases. A first incremental `FORMAT_XZ` pass hashes and discards bounded
expanded chunks, requires CRC64 and full EOF, and refuses truncation, corruption,
trailing bytes/padding and multiple streams. Explicit `r:xz` with the original
`filter='data'` extraction then consumes those same validated compressed bytes.
No autodetection, raw-tar artifact or duplicate expanded workspace is introduced.
Original source_rows extraction/manifests and whole Git/blob/mode joins remain.

The Git filter is literal `/usr/bin/xz --threads=1 --check=crc64 -c`, with
`--format=tar.xz -6`. Pin the installed executable bytes and version before use.
Only PATH/LANG/LC_ALL enter the version/archive commands; XZ_OPT, XZ_DEFAULTS,
GZIP and user configuration are not inherited through that environment. The
existing60-second command deadline, complete stdout/stderr receipts, all seven
lanes, test/resource caps and owned lifecycle guards remain. The added version
command means archive provenance must use full recorded argv, not a fixed
command ordinal. Archive and expanded-tar hashes/lengths are distinct facts.

Old account/language canonical keeper gzip readers remain byte-exact. The
source packet preserves the original gzip driver and four controls as history.
Current qualified native S still uses driver3171/gzip and its original proof;
no XZ result is backfilled into that completed campaign.

## Evidence and pending gates

Read-only T2 observation22fb07a1 measured the identical expanded tar106280960B,
SHA4ae2699108be4340d92c95f97b12be66a621d856d085603f872f5db35f324096.
GNUgzip67126632B exceeded64MiB by17768B; single-thread XZ-6 produced62132108B,
4976756B below the cap, in28.7s. Both completed full CRC/EOF. No raw archive
was retained. This does not predict size, time or PASS for a future producer.

V2 source manifest283662eb binds proposed driver8897d477 and four-control source
d550616c. Independent a9ee0981 conditionally admits that source and closes the
memory-control gap; it does not establish execution. The same tiny valid XZ
input must decode at a sufficient budget and fail in the actual decoder at an
insufficient positive budget. Removing only decoder memlimit must be shown to
fail that control when the sensitivity run is authorized. Actual control and
sensitivity results were null at the initial source-preparation checkpoint;
the later observations below are separate and await independent actual review.

The four source-selected controls must establish complete fixed source bytes,
executable mode/PAX/format/hash/argv roundtrip; wrong-codec/CRC/footer/extra-stream
refusal; the existing data-filter escape refusal; and unchanged compressed plus
expanded/decoder-memory boundaries. No large fixture or database is needed.
A source inspection or a guard-only parameter refusal is not a runtime result.

After independently reviewed whole Topics/bridge/transport composition and
actual controls, commit one clean producer with its documentation. Preflight
that exact entire SHA under the unchanged cap through full codec EOF and full
Git closure, then run complete affectedPG7 once. A new exact audit must bind
current codec/driver/commands/source/PAX and all original raw, case, privacy and
cleanup guards; old gzip qualification is not replayed as XZ acceptance.
Consumer operational bindings require their own exact-source review afterward.
See the [execution plan](../execplans/gui-native-topics-and-xz.md).

Actual display closingC1d0fd84/publication9b7b67aa and accepted S/T2 evidence
remain intact; this proposal adds no deletion or archive availability claim.
Source branches, unmerged packets, canonical keepers and all protected current
setups remain. No main/milestone/production/identity/legal/government or real-
child activation, and no rendering speedup, follows from this codec decision.

## Root transport-control observation only

Root receipt1cbcdb3c reports four source-selected XZ controls passed with zero
failures/skips, and one exact memlimit-removal mutant failed only the expected
fourth root. Driver8897d477/controlsd550616c stayed unchanged; the owned temporary
HOME and mutant copy were removed after evidence. These are tiny fixed-fixture,
fake-Git-stdout observations, not Topics/Go/browser/PG execution or exact-source
qualification. Independent raw/custody review remains pending. The author of
these documents ran no candidate/control; all five Topics pure roots remain
source-only, and future application/producer/fullPG/UI results remain unknown.

## Applied-source focused checkpoint

Earlier source-only checkpoints above remain historical. Applied exact whole-reviewed68fe231f Topics/core/test-preservation/XZ source. Focused race runf5f488cb passed32roots/117named,including all8newTopics roots;19pre-existing database-dependent roots were skipped without a DSN,including the registered topics/guardian and SPA roots,so those remain mandatory zero-skip checks in completePG7. Affected vet web+app,mechanical format10Go/AST2Python,docs69 and whitespace pass;no race warning. Source-selected4XZcontrols and exact fourth-root-only memlimit-removal sensitivity1cbc are independently accepted in68fe;these tiny fake-Git fixtures do not qualify a new producer. Exact whole-SHA XZ/Git blob-mode-PAX preflight,complete affectedPG7,53fresh literal prior bodies and finite desktop qualification remain pending. No benchmark,new policy/main/milestone/production claim.
