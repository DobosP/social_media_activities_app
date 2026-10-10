# Original-native public GUI fixtures

[ADR-0050](adr/0050-original-public-gui-fixtures.md) records the producer boundary.
The source is `services/server/internal/web/gui_public_golden_test.go`; existing
native routing, templates, translations and rendering are used without edits.

Run inside an owner-approved managed Go1.27.1 runtime, with its task-owned cache
and TMPDIR, from `services/server`:

```sh
go test -mod=readonly -race -count=1 -json ./internal/web -run '^TestGUIPublic'
```

To retain actual original HTML, append
`-gui-public-golden-output /absolute/private-parent/new-capture-directory`.
The parent must already exist with mode0700. The destination must not exist.
No command here authorizes host Go, a new image, DB, hosted workflow or service.

The matrix contains14 captures: privacy and terms in EN/RO with default and
contrast/larger/reduce settings; open-data in EN/RO with and without a synthetic
regular snapshot manifest; anonymous landing in EN/RO. All requests use the real
registered native mux. No account/session/PG/provider fixture is installed.

Outputs are14 raw HTML files and `manifest.json`, private mode0600. The manifest
records actual response/body hashes, current source/locale bindings before and
after rendering, and scenario assertions. A failed case keeps a failed manifest
and available bytes. Raw nonces are not printed or replaced; future canonical
golden comparisons must use the kit's approved attribute-only masks, not a local
body rewrite. Original public HTML is not a deterministic byte fixture until
that separate comparison contract is installed and qualified.

This is neither a full G0/G1 matrix nor template-conditional coverage. The
historical `_breadcrumbs` hold, unsigned84+7 ownership, protected paths, native
authcore/inline moderation separation and1651 retirement failures remain as in
[capture preparation](reviews/gui-capture-preparation/README.md). No owner
signature, Django parity, Native Live, templ/UI adoption or deployment is claimed.
