# ADR-0061: Native family management and ward explanation

Date: 2026-10-19
Status: implemented and independently feature-qualified; main/milestone activation remains separate

## Context and decision

Programme ADR-0032 authorizes selected functional Go HTML rebuilds. Rebuild only
the existing `/wards/` and `/guardianship/` views around the present account,
guardrail, recommendation and messaging services. Preserve logical template names,
registered methods, current session/Actor reload, CSRF and Origin admission. This
is neither S3A acceptance nor authorization for real-child onboarding or deployment.

The selected names use cached embedded `html/template` output after the existing
Renderer defaults and data merge; its common cache, Vary, CSP and reporting headers
remain shared. Other names keep cached Pongo. A strict typed projector accepts only
the existing authorized row shapes, builds local named action URLs from positive
owned IDs, and passes ordinary escaped strings. Existing catalog msgids remain the
translation source. Two finite EN/RO unavailable-invitation explanations describe
an environment switch or a missing verified-adult condition without claiming legal
proof. Original templates, tests, route manifests and catalog bytes remain.

Guardian pages retain the four current ordinary forms. Repeated names are exactly
`allowed_weekdays`, `allowed_categories` and `topics`. Optional nil limits differ
from numeric zero. Stored selected categories outside the top-level menu remain
explicit checked plain-text options so a valid restriction cannot disappear on
save. Existing bounded custom relationship labels remain escaped text. The service
still validates every mutation; topic nudges retain known-topic filtering rather
than acquiring hard-permission semantics. Limits shown are each guardian's chosen
values, with the existing combined-block warning; no browser permission union or
unprojected effective bound is invented.

The ward page is read-only and has no family mutation URL or identifier. Child-only
meeting logistics, observer and arrival explanations follow existing projection
capabilities. Revocation uses a native disclosure with consequence text and the
existing CSRF POST; it does not depend on JavaScript. Invitations stay pending and
uniformly acknowledged under existing policy; acceptance stays in the existing
home workflow. ADR-0045 block, co-guardian and adulthood rules are unchanged.

## Verification and limits

New pure projection/template/header tests, registered-route PostgreSQL boundary
and mandatory-audit rollback cases, and assembled-App real-session/CSRF tests are
prepared. Focused family race/vet, header and current-custody checks pass; actual
PostgreSQL/desktop qualification is pending. Existing assertions remain. The exact
renderer custody inverse is independently reviewed; historical bytes, captures
and receipts remain unchanged. Qualify source/authcore hashes, actual
synthetic PG, desktop/no-JS/a11y and matched warmed rendering. No production cost,
legal compliance, security certification, formal milestone or performance result
is established by this source proposal. Full landing gates and human first-launch
review remain as ADR-0040; Teacher/Social milestone landing remains owner-only.

## 2026-10-19: scoped qualification update

The initial verification plan above is historical; current scoped evidence follows.
The family shell now uses an owned 55-byte stylesheet after unchanged `base.css`.
Its `.family-page .btn` rule retains press motion and important reduced-motion
rules, while removing the observed background/border tween. Decorative SVG and
status spans inside list items repair the V1 glyph/list accessibility findings.

V1 failed Axe; V2 passed steady-state checks but retained the 120ms contrast defect.
V3 passed eight EN/RO fixtures plus two no-JS cases, 192 Axe predicates, 48 successful
assets and 27 equal initial/settled button-color samples; the same predicate rejects
V2. These are two samples, not all-frame certification. Independent review binds
report `1d2eab616eb452f739e27a18c2db78b1116706c499bc785ad1762726128e2372`.
The final applied-source export preserves all eight accepted HTML bodies byte-for-byte.

Final warmed body measurements have smaller native HTML (17.7/18.2kB) and about
47% fewer allocated bytes than the initial native implementation. Body CPU remains
higher than original Pongo; no full HTTP/production speedup is claimed. Reviewed
request-local copy memoization, an immutable markup replacer and private buffer
retain escaping and introduce no cross-request account cache.

Complete six-package PostgreSQL qualification and publication remain pending.
Run the committed method from the clean feature checkout; repo/pinned Go have
defaults and scratch/cache paths must be explicit existing owned directories:

```sh
python3 -I tools/gui-native-family/qualify.py --sha FULL_CLEAN_SHA \
  --scratch /absolute/task-scratch --cache /absolute/go-cache \
  --modcache /absolute/go-mod --execute
```

The method binds its three committed inputs to the clean source archive. Keep
source/authcore checks, all original test/CSRF/audit requirements, and owner-only
Teacher/Social milestone/main/production decisions separate from this scoped proof.


## PostgreSQL fixture correction — 2026-10-19

The corrected method binds five committed inputs, including the native catalogue
bootstrap and pure metadata-admission controls, to its clean source archive.

The first complete six-lane attempt on `cdbd21a` passed web722 and app59 named
cases, then failed four existing account roots because the method had installed
schema/budgets but omitted canonical reference data. All owned containers and the
PG process/socket were removed with recorded shutdown checks. This remains failed
historical evidence. The corrected method invokes the existing `catalog.Migrate`
in a bounded, owned, network-none helper for each fresh public database. It binds
all canonical content types, taxonomy, relations and child-venue metadata against
the current embedded reference source before and after each complete lane; it
excludes nondeterministic timestamps and legitimately advancing identity sequences.
No product policy, schemas, tests, assertions, skips or native caps change. A fresh
complete six-lane run on the new clean source remains required before acceptance.


## Archive build correction — 2026-10-19

The `595d39c` successor stopped during helper compilation before creating any
database or container: a Git archive has no VCS metadata for automatic compiler
stamping. The helper build now sets `-buildvcs=false`; the explicit clean SHA,
tree, archive/source/method bindings and binary digest remain mandatory. Verify
that build from a fresh committed archive before scheduling the six-lane run.
This build-only failure is retained separately from the earlier account failure.


The dc7a272 attempt passed web722/app59/accounts121/recommendations24, then
failed during the social lane with a PostgreSQL WAL no-space PANIC. The fixed
1GiB PG tmpfs and2GiB memory cap were preserved. All11 owned containers were
removed; graceful PG shutdown failed after the PANIC and is not claimed.
The method now retires only each verified generated lane database after complete
test/metadata/source proofs and verified client-container removal. Exact name,
captured OID,app owner,non-template status and zero client connections are checked
on the current owned CID. DROP has no FORCE/IF EXISTS; absence is verified.
A separate successful CHECKPOINT on postgres precedes the next fresh lane.
Raw catalog/WAL/tmpfs-used/free snapshots are retained before drop,after drop and
after checkpoint. Capture-time metadata remains evidence after intentional DB
retirement. No tests,assertions,schema,configuration or caps are changed.
PostgreSQL may recycle WAL without shrinking file bytes; the repair removes
completed DB/catalog bloat and permits WAL reuse but does not guarantee every
single-lane peak. A fresh complete six-lane run is required; if one isolated lane
still exhausts the fixed cap, fresh-PG-per-lane is the next lifecycle option.


## Feature qualification — 2026-10-19

Exact clean source `118fd7f` passed the committed six-lane method in430.872seconds:
web722/app59/accounts121/recommendations24/social123/messaging100 named passes,
395top-level roots and1,149total named passes. All compiled inventories match
actual race/test2json streams, with zero skipped or failed tests. Seventeen new
family roots, original source-custody assertions and canonical reference datasets
are present. Five exact method inputs and all2,086 exported source files are bound.

Every complete lane was followed by verified client removal, exact owned database
retirement and CHECKPOINT within the unchanged1GiBtmpfs/2GiBmemory cap. All13 owned
containers were removed; PostgreSQL stopped with exit0/status3 and no remaining
PID/socket. Raw PG logs contain expected negative-test errors and no PANIC,
no-space or recovery event. Independent runtime and compact archive reviews are
retained with the [qualified feature keeper](../reviews/gui-native-family/README.md).
The earlier missing-catalogue, archive-build and WAL/storage failures remain failures.

This qualifies the bounded functional feature and isolated desktop fixtures, not
all21 packages, a new deployment image, Social GUI milestone, main landing or
production/real-child activation. No native-body CPU speedup is claimed.
