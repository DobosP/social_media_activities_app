# ADR-0065: Native notification inbox presentation

Date: 2026-10-20
Status: implemented — focused checks pass; first complete PG attempt failed at a new timestamp assertion; independently reviewed test-only repair applied, fresh complete qualification pending.

## Scope

Rebuild existing `/notifications/` with cached escaped GoHTML and a scoped new
stylesheet. Keep current `/notifications/read-all/` POST action and native Go
services; no new endpoints, SQL, audits, schema, dependencies, provider or role
policy. Native preference pages and all 31 accepted previous bodies remain.

Typed projection consumes only authorized ordered notice display fields and the
current request Actor/default shell. It does not assume AccountView-only user
keys, serialize raw records/IDs, infer age/guardianship or add verification gates.
Original 100-row bound/order, read_at presence alias, optional reason/body, source
Bucharest timestamp and STRICT original safe_href policy are retained. Unsafe
URLs lose only their link. Native forms work without JavaScript. Explicit SPA
selection and its existing separate output stay unchanged.

The source single self-only read-all UPDATE, all-row effect beyond 100,
idempotence, CSRF/Origin, failure propagation and 303/fetch behavior stay exact.
Mandatory system/moderation delivery, optional mod_alert/arrival choices, presence
expiry and protected retention policies are unchanged. Opening a page never
marks a notice read. Existing authcore/current-active-account gates stay intact.

## Evidence and preservation

Accepted source contract review 670f9b38 binds runtime e900960. Clean published
base 05e5a522 has only 18 closing docs/keeper differences; rebind 07ef1b14 preserves
133 refs, all 287 old tests/6,878 assertion-call mentions and 31 actual old bodies
from keeper 29a9ff. Three narrow source-reader adapters restore complete former
bytes before the unchanged 32/31/27 controls. Current live readers require exact
new source, and new negative tests refuse former/missing/duplicate/modified/rehashed
bytes. Original HTML/assets/catalogue, backend/query/auth/audit/schema/dependencies
and all original assertions remain byte-exact. No frozen Pongo page rendering is
used to claim new native proof; the strict URL filter comparison is test-only.

New pure display/malformed/escape/header tests and a standalone automatically
registered App root/six finite leaves are applied after independent source review. App requests use
real login cookies through current App middleware; the final capture forwards the
same original API GET exactly once and whitelists display fields. No extra private
query, Actor/session/CSRF/nonce/raw ID is emitted. A registered direct-handler HEAD
is distinguished from net/http wire body suppression. Complete affected PG,
no-JS/desktop/Axe, all 31-body equality, measurements and keeper remain pending.

## Boundaries

Applied source and methods are independently source-reviewed (75a528c4/96601b3c).
This checkpoint is not successful runtime qualification, a
complete 21/current-image/main/Social milestone or deployment. Government/provider
connections, external delivery and real-child use remain separate unapproved
activation work. No privacy/clinical/content/retention policy ruling is made.
Optional warmed body measurements must report regressions and cannot establish
HTTP/SQL/throughput or a generalized efficiency improvement.

## Applied-source checkpoint

Reviewed 20-path product/method batch is applied. Focused web race checks pass
13 roots/72 named cases with zero failures/skips; App/web vet, five absent-overlay
compile checks, authcore snapshot, full native format, Python/Node syntax,
whitespace and all61-document guard checks pass. These are local checks, not
complete affected PostgreSQL, browser, previous-body or measurement acceptance.

## First actual PG attempt and assertion repair

Exact f087480 completed web199roots/863named without failures/skips, then the
complete App package failed only the new inbox date assertion and its parent:
48roots/96named RUN,94PASS. The other five new leaves and all existing App cases
passed; the remaining five packages did not run. All five created container IDs
and names were removed and the private PostgreSQL stopped. This is a failed
partial campaign, not complete affected qualification; original streams remain.

Actual native-render diagnostic confirmed that GoHTML safely emits the timezone
plus as an HTML entity; the decoded value is exactly the expected Bucharest ISO
timestamp. Independent5973e72e approves NEW test-only patch9aac0388: require one
actual marked time attribute and compare its decoded value with the same full
timestamp. Read-state/date/body assertions, product escaping/rendering, all old
tests and original resource/time limits remain unchanged. Actual failure review
adf73e82 and exact diagnostic are retained; fresh complete seven-package
qualification, UI, prior-body checks and measurements remain pending.
