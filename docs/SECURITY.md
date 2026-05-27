# Engineering & supply-chain security

Application and dependency security. (Child-safety design is in [SAFETY](SAFETY.md); legal/
regulatory duties are in [COMPLIANCE](COMPLIANCE.md).) For a children's platform, security is a
first-order concern, not an afterthought.

## Dependency-security policy

This is a deliberate policy, not a default — it was an explicit project requirement.

1. **Pin exactly, don't float.** `requirements.txt` and `requirements-dev.txt` are **fully
   pinned**, compiled from `requirements.in` / `requirements-dev.in` with `pip-compile`. We do
   **not** use floating ranges in the lockfiles, and we do **not** auto-merge dependency bumps.
2. **Track the latest *secure* releases, bump deliberately.** We stay current, but every upgrade
   is reviewed and must pass the full test + audit gate before merge. Automated tools may *open*
   update PRs and *alert* on CVEs; a human/lead agent reviews and merges them — **no silent
   auto-update on security**.
3. **Django on the LTS line.** Pinned to **Django 5.2 LTS** for the longest security-support
   window (LTS releases get extended security fixes). Re-evaluate at each LTS.
4. **Separate runtime from dev.** Runtime deps (`requirements.txt`) ship in the production image;
   dev/test tools (`pytest`, `ruff`, `pip-audit`) live only in `requirements-dev.txt` and are
   **never** installed in production — smaller attack surface.
5. **`pip-audit` is a release gate.** It must report **no known vulnerabilities** before any
   release, and runs in CI (see [ROADMAP](ROADMAP.md) IS-2). Run locally:
   ```bash
   pip-audit
   ```
6. **Review cadence.** Routine dependency review at least monthly; **critical CVEs** are patched
   out-of-band as soon as a fixed release exists.

### How to bump dependencies

```bash
# edit the floor/cap in requirements.in / requirements-dev.in, then:
pip-compile --upgrade --output-file=requirements.txt requirements.in
pip-compile --upgrade --output-file=requirements-dev.txt requirements-dev.in
pip install -r requirements.txt -r requirements-dev.txt
pip-audit && pytest && ruff check . && python manage.py makemigrations --check
```

### Recommended automation (IS-2)

- **Dependabot/Renovate**: open PRs for updates + security alerts (review-then-merge, not
  auto-merge).
- **CI gate** on every PR: `ruff`, `pytest`, `makemigrations --check`, **`pip-audit`**, secret
  scanning, Docker build.

## Application-security baseline

- **Secrets** come from the environment only. `.env` is git-ignored and never committed;
  `SECRET_KEY` and DB credentials are injected per-environment. Rotate on exposure.
- **Transport & cookies (prod):** `config/settings/prod.py` enables SSL redirect, HSTS, secure
  session/CSRF cookies, and the proxy SSL header. `DEBUG=False`; `ALLOWED_HOSTS` from env.
- **Least-privilege database role (prod).** The local dev DB role is a superuser only to allow
  `CREATE EXTENSION postgis` from migrations. In **production, pre-enable PostGIS** as an admin and
  run the app as a **non-superuser** role with rights only on the app schema.
- **Input boundaries.** Trust framework guarantees internally; validate at the edges (API input,
  ingestion source data, future user uploads). DRF serializers validate API input; ingestion
  treats source tags as untrusted.
- **No raw SQL string-building.** Use the ORM / parameterized queries (avoids SQL injection).
- **Future surfaces** (flagged for their deliverables): auth/session hardening + rate limiting
  (D2/D4), upload validation + image safety scanning + EXIF stripping (D6), chat abuse controls
  (D5). See [SAFETY](SAFETY.md).

## Pre-release security checklist

- [ ] `pip-audit` clean on `requirements.txt` + `requirements-dev.txt`
- [ ] No secrets in the repo or image; `.env` not committed
- [ ] Prod settings active (`DEBUG=False`, HSTS/SSL, scoped `ALLOWED_HOSTS`)
- [ ] DB role is least-privilege (non-superuser) in prod
- [ ] Security review / pen test done before public launch (D9)
- [ ] DPIA + data-retention policy finalized with the DPO (see [COMPLIANCE](COMPLIANCE.md))
