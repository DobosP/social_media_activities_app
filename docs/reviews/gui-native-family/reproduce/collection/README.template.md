# Native family management — bounded feature keeper

Qualified source: 118fd7f46c6cfc9504d752ba382f890cdbbc8a44.
Six complete affected packages passed:395 top-level tests/1,149 named passes,zero skips/fails.
Committed method dccfe6bb uses the original1GiB PG data tmpfs/2GiB memory limits;
completed fresh lane databases are retired with owned-name/OID guards and checkpoints.
This is not full21,current/new image,main,milestone,production or real-child activation.

The three earlier attempts remain failures: cdbd catalogue initialization,595 archive
bootstrap build,and dc7 WAL/tmpfs exhaustion. Their raw records/logs remain selected.
Latest V3 UI passed8 isolated states,2 no-JS cases,192Axe predicates and27 sampled
initial/settled button colors. Final source exports preserve8 accepted HTML bodies;
no new browser run after the three-file perf change or all-frame guarantee is claimed.
Earlier UI V1 failed; V2 passed stable-only checks.16 superseded PNG paths were
already pruned per the retained executed manifest; their old raw PNGs are unavailable.
Native body CPU remains higher than Pongo despite smaller payload and lower allocations.

Archive SHA256: @ARCHIVE_SHA@.
archive-manifest.json records exact member bytes,mode,UID/GID and nanosecond mtime;
roundtrip/source fences passed during collection. Independent actual review is separate.
No database,binary,cache or full workspace is retained. Git archive stdout is excluded
with hashes in omitted-artifacts.json; exclusion is not a claim of physical deletion.
The collector never deletes; Root must recheck keeper/ownership before later cleanup.

Reproduce PG from the exact clean Git checkout with existing pinned images/caches:
`python3 -I tools/gui-native-family/qualify.py --sha 118fd7f46c6cfc9504d752ba382f890cdbbc8a44 --execute --repo /absolute/checkout --scratch /owned/_temp/fresh-pg --go /usr/local/go/bin/go --cache /owned/cache --modcache /owned/modcache`
Copies under reproduce/repository preserve committed method bytes; use the full Git
checkout, not that partial source-copy directory, as --repo. No image pull/install implied.

UI source is committed under tools/gui-native-family/ui; the earlier actual run used
identical scratch method bytes and is not retroactively renamed a committed-method run.
Generate a fresh overlay using prepare-ui-overlay.py --repo /absolute/checkout --output /owned/_temp/overlay.json.
Run Go test with that overlay,./internal/web,-run '^TestSocialFamilyQAExport$',-count=1,
and -args -family-ui-qa-output-dir /owned/_temp/new-fixtures. Run desktop-qa.mjs with
explicit --repo,--fixtures,new --output,--playwright-package,--axe-file and --chrome.
Body-only benchmark:go test ./internal/web -run '^$' -bench '^BenchmarkNativeFamilyBodyRender$' -benchmem.
Selected raw benchmark/review receipts explain the limited comparison; no HTTP speedup.
