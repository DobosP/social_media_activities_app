# ADR-0053: Filesystem-backed original renderer boundary

Date: 2026-10-10
Status: focused managed checks passed at8bdbb4e; independent actual review clear within the partial FS boundary

## Decision

Add `NewRendererFS(assetRoot, files)` for original Pongo templates and the
Romanian catalog. This is a reusable delivery boundary, separate from the
unchanged production `NewRenderer(root)` path. It uses the already locked public
Pongo6.1 `NewFSLoader`, the same `transformTemplate` and the extracted, unchanged
catalog parser. No SDK installation, module change or alternate template engine
is required. No production assembly, route or default is switched.

The supplied filesystem has repository-style `templates/`,
`apps/web/templates/` and `locale/` paths. Template search keeps the native
prefix order inside that filesystem only. `Abs` deliberately keeps native
root-relative extends/includes; Pongo's base-relative resolution would change
existing templates. Noncanonical names refuse. Missing templates/catalogs never
use `assetRoot`; that root remains only for the existing asset method, outside
this partial boundary. Private pages and host-based assets are not qualified.

Every opened FS file must be regular, have a valid declared size and yield that
exact number of bytes. Reads inspect one extra byte beyond the512KiB template
or2MiB catalog limit, so an overlong stream cannot pass as a truncated file.
Stat/read/close errors refuse and opened inputs are closed. Catalog scanner
errors refuse the new constructor. The legacy disk caller intentionally keeps
its existing partial-catalog behavior on scanner errors; its open/size/default
behavior and the parser's translation/fuzzy/plural logic are unchanged.

## Trust and proof boundary

An arbitrary `fs.FS` is a caller-owned trusted, stable input, not something this
constructor seals or proves immutable. Current test inputs are six in-memory
snapshots of the exact five public templates and one PO file, joined to the
physical source hashes and checked again after all cases. Their read-only
virtual file modes do not claim physical filesystem immutability.

The old fourteen registered-handler disk cases remain with all assertions. A
separate FS test runs those same fourteen scenarios through the same registered
handlers and released normalizer, with distinct output directories/manifests.
Neither the original corpus nor a normalized baseline is replaced. Tests also
exercise root-relative includes against competing nested files, host fallback
refusal, path aliases, permission errors, regular/size/read/close errors, actual
overflow and preserved legacy parser behavior.

## Verification

Historical source checkpoint (2026-10-10, source9b220d7): expected app discovery was63actions: the previous
23, a new14-case FS capture plus its parent, and25focused FS actions. Managed
compilation, formatting, captures and normalization have NOT RUN on this source.
The existing21helper/23app proof remains bound to764beb1. This is not fullM1,
templ/Django parity, Native Live, signed group coverage, retirement or deployment.

The later9bformatter preflight returned1 for alignment only; it stopped before
tests/captures. Its exact one-file afterimage became formatting successor8b.

At8bdbb4e, actual readonly managed formatting and63app actions passed (12top,
51subcases, each once, zero fail/skip/cache/race). All23previous action identities
remain. The14disk and14FSregistered-handler cases yielded28hard-free/equal
released-normalizer diagnostics; all14disk/FS canonical hashes also joined.
56template loads and six FSsnapshot/source-hash joins were verified, with all
30source/41SDK/2owned-command/15original files, two archives and the reused
764binary/build information byte/fullmode-unchanged. Parent independently cleared
only this partial original-Pongo FS evidence. No default production switch,
host-asset/private-page, fullM1 or group/Live/templ/retirement claim follows.
