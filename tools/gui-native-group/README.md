# Native group fixture reproduction

Use a clean committed checkout. These tools cover isolated native Go rendering,
desktop1280×720,keyboard/no-JS/Axe/assets and the eight existing family HTML bodies.
They do not replace registered HTTP/CSRF/authority/PG tests or qualify devices.

The PG producer test emits one bounded actual s.form projection after its original
assertions. Commit its marker/extractor before complete PG qualification. After
that run passes and stops, extract at the exact producer checkout:
`python3 -I tools/gui-native-group/extract-projection.py --repo /absolute/checkout --run /owned/_temp/successful-run --source <actual-PG-SHA> --output /owned/_temp/new-form.json`
This contains the same actual classic as_p and typed native snapshot,plus finite
synthetic adult Actor fields. No production HTML or credentials enter this path.

Commit the remaining UI-only methods before UI/benchmark execution. Record that
later method commit separately from the PG producer; require native/PG producer
source rows unchanged. Every phase checks an exact six-path UI-only Git delta and clean HEAD before/after.
No PG pass is claimed on the later UI-only commit; no extra PG fixture is needed.

Create a fresh overlay in an existing owned task scratch parent:
`python3 -I tools/gui-native-group/prepare-ui-overlay.py --repo /absolute/checkout --producer-source <actual-PG-SHA> --output /owned/_temp/overlay.json`
Export actual native fixtures and compare all eight family bodies to accepted hashes:
`go -C services/server test -overlay /owned/_temp/overlay.json ./internal/web -run '^TestSocialGroupQAExport$' -count=1 -args -group-ui-producer-source <actual-PG-SHA> -group-ui-qa-output-dir /owned/_temp/new-fixtures`
Run the desktop script with existing installed tooling and a new output directory:
`node tools/gui-native-group/desktop-qa.mjs --repo /absolute/checkout --producer-source <actual-PG-SHA> --fixtures /owned/_temp/new-fixtures --output /owned/_temp/new-desktop --playwright-package /absolute/playwright --axe-file /absolute/axe.min.js --chrome /absolute/chrome`
Ten native projection shapes cover EN/RO,staff/nonstaff,validation/service errors,
prefill-shaped values,display preferences and supplementary-plane description text.
Two no-JS cases exercise ordinary controls/disclosures. Actual query prefill remains
an HTTP/PG assertion,not a claim derived from these synthetic projections.

After all fixture workloads stop,measure the SAME captured actual form in EN/RO:
`go -C services/server test -overlay /owned/_temp/overlay.json ./internal/web -run '^$' -bench '^BenchmarkSocialGroupBodyRender$' -benchmem -benchtime=100x -count=3 -cpu=1 -args -group-ui-producer-source <actual-PG-SHA> -group-benchmark-capture /owned/_temp/new-form.json`
This is warmed nonstaff body render only,including native VM/copy cost; database,
widget preparation,full HTTP and staff performance are outside the timed scope.
Keep raw results/source hashes,then judge CPU,allocations and HTML/gzip honestly.
No speedup or actual qualification is asserted by these unrun method sources.
