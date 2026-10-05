# Native Go candidate qualification

Valid until: candidate code, dependencies, packaging or deployment profile changes — then requalify.

The [packaging receipt](packaging-qualification.json) contains aggregate synthetic-fixture
measurements/proofs for the native release and an exact-source Django reference. It is
not production capacity, billing or a live-provider acceptance test. All databases and
containers used the task-owned internal network, with no real users/data/provider calls.

Native173 PostgreSQL/codec tests passed with zero skips, plus race/vet and independent
payload/markup/cursor projections. Public HTTPS through the actual trusted Caddy fixture,
nonroot/read-only/codec/Python-absence probes and loopback-only public HTTP proxy checks
pass. A previously rejected ordinary-network attachment was not performed; the safer
credential-free GET-only fixed-target proxy retained the internal fixture network.

Source/imported-package and symbol-retained same-source binary vulnerability audits pass.
The stripped-binary analyser falls back to module metadata; the unused openpgp module
advisory is explicitly preserved. The [final image gate](image-security.json) passes on the PCRE2-fixed images with zero
fixable HIGH/CRITICAL findings; original footprint measurements keep their measured IDs.

Current implementation/activation status remains in STATUS.md, decisions in ADR-0032.

The [combined aggregate receipt](restart-checkpoint.json) has705fresh native passes/all21 lanes, zero skips/failures,
canonical no-cache packaging, source/package/binary/image audits and hardened smoke.
The [Windows checkpoint](../../NATIVE_WINDOWS_TODO.md) is history. Python retirement remains blocked
on1672 unresolved frozen declarations;999 mappings are manifest claims rather than exhaustive equivalence. Unique unfinished client prototypes are preserved
as [inert continuation keepers](continuation-keepers/manifest.json), not shipping source.

Remaining REST-facing assertions/transport gaps: [bounded contract plan](rest-contract-plan.md).
