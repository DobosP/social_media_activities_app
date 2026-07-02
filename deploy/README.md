# Deploy — reproducible single-box EU stack (Terraform + cloud-init)

> **STATUS (2026-07-02):** the org-level provider decision is **RECOMMENDED, NOT CONFIRMED** — no
> provider is procured. This IaC has **never been applied**; **no infrastructure exists**. Launch is
> **HARD-BLOCKED** on the GDPR stack (DPIA + DPO + verifiable parental consent — see the org
> `unified-deployment-architecture/docs/00-master-plan.md`). Do **NOT** run `terraform apply` (it
> provisions paid resources) without Paul's explicit go-ahead. The hosting provider is
> **intentionally not yet decided** (owner note, 2026-07-02): Hetzner and the box sizes named here
> (CPX22) and in the org plan (CX23) are **candidate sizings, not commitments** — the final
> provider + size are chosen at procurement time (see `unified-deployment-architecture/docs/adr/`
> ADR-0002 recommendation + ADR-0004 provider-neutral rule).

Infrastructure-as-code for the recommended cheap, EU-resident launch box in
[`docs/HOSTING_EU.md`](../docs/HOSTING_EU.md) §3. One `terraform apply` provisions a Hetzner Cloud
server + firewall, and cloud-init bootstraps the whole stack:

```
Hetzner CPX22 (Ubuntu 24.04, EU)
├─ PostgreSQL 16 + PostGIS + pgvector   (localhost only)
├─ Redis                                 (localhost only — Channels + cache + rate limits)
├─ daphne (ASGI app)            systemd: socialapp.service        127.0.0.1:8000
├─ Caddy (auto Let's Encrypt TLS + reverse proxy, incl. WebSockets)   :80/:443
├─ run_due_jobs                 systemd timer: socialapp-jobs.timer    daily 03:00 UTC
└─ pg_dump → EU object storage  systemd timer: socialapp-backup.timer  daily 02:00 UTC
```

This replaces the prose runbook with version-controlled, repeatable infra. It is a **single box**
(a deliberate cheap-launch SPOF) — the HA/scale-out path (managed Postgres, ≥2 app instances,
PgBouncer, CDN) is in `docs/HOSTING_EU.md` §6 and `docs/PRODUCTION_READINESS.md`.

## Files

| Path | What |
| --- | --- |
| `terraform/` | Hetzner server + firewall + SSH key; renders `cloud-init.yaml.tftpl` with your secrets. |
| `cloud-init.yaml.tftpl` | First-boot bootstrap (packages, DB, venv, `.env`, systemd units, Caddy, SSH hardening). |
| `systemd/*.service`, `*.timer` | The app, the daily jobs, and the daily backup units. |
| `backup.sh` | `pg_dump | gzip` → `s3://<bucket>/backups/db/`. |

## Prerequisites

1. A Hetzner Cloud project + API token; an SSH key.
2. A **Hetzner Object Storage** bucket in the same EU region, with an S3 access key/secret.
   - Add a **lifecycle rule** to expire `backups/db/*` after e.g. 30 days (backup retention).
   - Keep the bucket **private** (the app serves blobs only via signed/presigned URLs).
3. A DNS zone for your domain (you'll add an A/AAAA record after `apply`).
4. The launch-blocking app config: a real `EUDI_TRUSTED_ISSUERS` trust anchor (prod refuses to boot
   without it) and a strong `DJANGO_SECRET_KEY` / `db_password`.

## Use

```bash
cd deploy/terraform
cp terraform.tfvars.example terraform.tfvars   # fill in (token, ssh key, domain, secrets)
terraform init
terraform apply                                 # prints the server IP
# → create an A (and AAAA) record for your domain pointing at that IP
ssh root@<ip> 'cloud-init status --wait && systemctl status socialapp --no-pager'
```

Caddy provisions TLS automatically once DNS resolves to the box. Visit `https://<domain>/healthz`.

## Security notes (verify these)

- **Secrets live in Terraform state.** `terraform.tfvars` and `*.tfstate` are git-ignored; use an
  **encrypted remote state backend** for a real deploy, and rotate the API/S3 tokens if state leaks.
- SSH is firewalled to `admin_ip` only and password auth is disabled (key-only). Postgres + Redis
  are never opened (localhost only). The app runs as the unprivileged `app` user under a hardened
  systemd unit (`NoNewPrivileges`, `ProtectSystem=strict`). `unattended-upgrades` + `fail2ban` are on.
- The rendered `.env` is mode `600`, owned by `app`; the staging copy is `shred`-deleted.
- `prod.py` boot guards still apply (EU media residency, real identity provider, shared-state).

## Restore drill (do this before you rely on backups)

```bash
# On the box (or any host with the bucket creds + Postgres client):
aws --endpoint-url "$MEDIA_S3_ENDPOINT_URL" s3 cp s3://<bucket>/backups/db/<file>.sql.gz /tmp/
gunzip -c /tmp/<file>.sql.gz | psql "postgresql://app:<pw>@localhost:5432/app_restore_test"
# verify, then point /healthz + a login smoke-test at it. Document your RTO/RPO.
```

## Updating the app

```bash
ssh into the box; sudo -u app git -C /home/app/social_media_activities_app pull
sudo systemctl restart socialapp   # ExecStartPre runs migrate + collectstatic
```

> A green-CI-gated deploy hook (so only passing commits ship) is a `docs/PRODUCTION_READINESS.md`
> P1 follow-up; today this is a manual pull+restart.
