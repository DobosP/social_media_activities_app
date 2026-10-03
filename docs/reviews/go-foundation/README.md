# Go foundation verification

Valid until: source/toolchain changes or deployment qualification — then treat as history.

The 2026-10-04 local gates qualify the first public Go serving slice in
[ADR-0031](../../adr/0031-go-public-serving-foundation.md).
[verification.json](verification.json) records the tools, exact image and outcomes.

The isolated PostGIS suite ran the real publisher → Go binary differential test
and existing public activity visibility/listing tests: 38 passed. Complete native
Go race/vet/format, repository Ruff, YAML/Compose and doc checks passed. The static
image passed native health with no network, UID65534, read-only root, no
capabilities and bounded resources. It is 7.43MB; this does not measure the whole
social application, media/DB/cache footprint or production capacity.

Official Go package and compiled-binary vulnerability scans found none. The
current tagged source-call-graph analyser cannot parse Go 1.27 SSA; that result
is unavailable. Trivy 0.75.0 found zero fixable HIGH/CRITICAL findings in the exact
local image. Vulnerability databases and future builds can change these results.

GitHub CI execution is separate from these local receipts. Full Django and
frontend suites were not rerun locally for this slice. No app was deployed, no
real ingestion/media processing ran, and no user or child-facing data changed.
Human privacy/safety review remains required before landing; live rollout also
needs approved freshness/revocation/erasure behavior and remaining API parity.
