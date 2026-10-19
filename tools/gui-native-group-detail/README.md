# Native group-detail reproduction

See ADR-0063 and the owning feature qualification record for scope and decisions.
Run only from a clean committed checkout with all capture/export/desktop methods
already committed before the complete six-lane PG producer run.

The existing six-lane PG method must pass and stop first. Its actual App test
emits a bounded synthetic semantic projection; the extractor admits only that
successful source/run. No actual credentials, cookies, sessions, CSRF values,
render nonce, media token or provider/deployment configuration enter the capture.

Use explicit existing dependencies; no installation, external services or image
build occurs here. Use a short owned scratch directory (for example an existing
`/home/dobo/work/_temp/gd-ui`), because Chromium uses Unix socket paths.
The Node argument must be the actual installed Node24 binary, not a launcher
such as a Vite/tool-manager executable. Go must be the existing Go1.27.1 binary.

```sh
python3 -I tools/gui-native-group-detail/qualify.py \
  --repo /absolute/clean-checkout --producer-source <full-successful-PG-SHA> \
  --pg-run /owned/_temp/actual-successful-six-lane-run \
  --scratch /home/dobo/work/_temp/gd-ui \
  --go /usr/local/go/bin/go --node /absolute/actual-node24 \
  --cache /absolute/existing-go-cache --modcache /absolute/existing-go-modcache \
  --playwright-package /absolute/installed-playwright \
  --axe-file /absolute/installed-axe.min.js --chrome /absolute/existing-chrome \
  --benchmark
```

The wrapper retains exact extractor, overlay, export, desktop and optional
benchmark stdout/stderr plus source/runtime/hash/cleanup receipts in one fresh
result directory. All seven supplied permission shapes run with JavaScript and
again without it. The exporter reuses existing helpers and requires all eight
family plus ten group-creation bodies to match their accepted immutable hashes.

Only the actual transient Renderer nonce is replaced in HTML/CSP, after checking
its original shape/coherence. The manifest retains original/normalized hashes,
counts and the fixed synthetic replacement; no actual nonce value is retained.
The fixture server preserves those normalized actual headers and enforces the
same policy for isolated QA, separately recording the original CSP header mode.

Product JS/config/script order is unchanged. Local fixture-only GET responses
serve current assets/worker, generated image and scoped hovercard text. A
Playwright WebSocket mock opens only for the actual admitted chat configuration;
it never contacts a server or injects peer/permission data. Both staff-owner
metadata fallbacks require zero live connection while retaining static content.

Checks include keyboard/ordinary-form serialization, local assets by status and
exact hash, initial/settled colors, three naturally scrolled whole-document Axe
audits per JS state with no violations/incomplete/rule exclusions, and explicit
owned browser/profile/server cleanup. No-JS inspection uses synchronous CDP DOM
reads; it never waits on page font/animation-frame callbacks.
JavaScript visits also keyboard-submit each source-projected archive/delete form
through unchanged site.js, require the real native confirmation's exact text,
dismiss it, and prove that cancellation sends no mutation or navigation.

Optional EN/RO classic/native measurements use the same admitted actual semantic
projection, warmed bodies, three samples of 100 iterations. Native projection
and copy cost remain included. Keep raw ns/B/alloc and HTML/gzip observations;
they imply no HTTP/DB/media performance or generalized speedup acceptance.
