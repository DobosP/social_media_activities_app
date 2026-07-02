# ADR-0005: Dependencies fully pinned via pip-compile; Django on LTS; review-then-merge bumps

Date: 2026-05-27
Status: accepted

## Decision
Pin every dependency exactly: `requirements.txt`/`requirements-dev.txt` are compiled lockfiles
from `requirements*.in` (`pip-compile`), no floating ranges. Django stays on the **5.2 LTS** line.
Automated tools (Dependabot) may *open* update PRs and alert on CVEs, but a human/lead agent
reviews and merges — **never auto-merge**, even for security bumps. `pip-audit` is a CI/release
gate (must be clean); runtime and dev deps are separated (dev tools never ship in the prod
image). Routine review at least monthly; critical CVEs patched out-of-band. Decided 2026-05-27
(`3b7ba20`); policy detail lives in `docs/SECURITY.md`.

## Context / why
This was an explicit project requirement, not a tooling default: a child-safety platform's supply
chain must be reproducible and auditable.
- **Why not floating ranges**: unreproducible builds — the deploy you tested is not the deploy
  you run; a compromised transitive release walks in silently.
- **Why not auto-merge security bumps**: a bump is a code change to a safety-critical system; the
  full gate (ruff, pytest, migrations-check, pip-audit, Docker build) plus review runs first.
  "Silent auto-update on security" trades a known CVE window for unreviewed behaviour change.
- **Why LTS**: longest security-support window per upgrade effort; re-evaluate at each LTS.

## Consequences
- Bump workflow is mechanical (`pip-compile --upgrade` → install → full gate; see SECURITY.md);
  Dependabot PRs queue for review rather than landing themselves.
- Staying current is a deliberate chore — schedule it; skipping the monthly review accumulates
  a risky multi-major jump.
- `pip-audit` failures block release even when inconvenient (that is the point).
- Supersedes: none (policy set at project start). Superseded-by: none.
