# Rollout & enablement — accountability wave (2026-06)

Operator guide for the four features merged in #65 (identity uniqueness, tiered sanctions +
authority referral, anonymous adult-only discovery, self-only progression). All four are in
`main`; the production-sensitive ones ship **dark behind a flag** and are listed here with the
exact env var, default, prerequisite, and what flipping it does. Feature behaviour itself is
documented in `FEATURES_BUILT.md` (the built-feature catalog, moved out of `CLAUDE.md`
2026-07-02) and the code; this doc is only how to
turn it on safely.

## Feature flags

| Env var | Default | What it does | Prerequisite |
|---|---|---|---|
| `IDENTITY_UNIQUENESS_ENFORCED` | `False` | Binds one EUDI wallet holder → one account (`accounts.bind_identity`); a duplicate wallet gets **409**, a lifetime-banned wallet **403**. No-op until a presentation proves holder-key possession. | A real EUDI provider presenting key-binding proofs (see below). Harmless to leave off. |
| `IDENTITY_BINDING_SECRET` | `SECRET_KEY` | HMAC key for the stored holder hash (keeps it unlinkable to the raw subject). | Set a **dedicated, stable** secret in prod (rotating it orphans existing bindings). |
| `PROGRESSION_AVATAR_PUBLIC` | `False` | When on, the evolving avatar's progression flourish is visible to **other** users too (default: self-only). | None. Off = zero observable signal to peers (recommended). |
| `EUDI_TRUSTED_ISSUERS` | `{}` | The EU trust list (issuer → public key) the EUDI verifier checks. Empty = every verification fails closed. | Required in prod with the EUDI provider (prod settings refuse to boot without it). |
| `EUDI_SANDBOX` | `DEBUG` | Trusts a local test issuer (dev/sandbox only). | Must be `False` in prod. |
| `IDENTITY_PROVIDER` | dev stub | The age-assurance provider. | Set to the EUDI provider in prod. |

Anonymous discovery (feature 3) has **no global flag** — it's organiser opt-out per object
(`is_publicly_listed`, default ON for adults) and is always safe (three walls make minor exposure
structurally impossible). Sanctions/referral (feature 2) are staff actions, always on.

## Safe enablement order

1. **EUDI provider first.** Set `IDENTITY_PROVIDER` to the EUDI provider, `EUDI_SANDBOX=False`, and
   a real `EUDI_TRUSTED_ISSUERS`. Verify the prod boot-time guards pass (`manage.py check --deploy`).
2. **Then uniqueness.** Once real wallets present key-binding proofs, set
   `IDENTITY_UNIQUENESS_ENFORCED=True` and a dedicated `IDENTITY_BINDING_SECRET`. Before this point
   binding is a no-op, so turning it on early does nothing — but turn it on only after step 1 or it
   can never fire.
3. **Lifetime-ban ledger** (`BannedIdentity`) populates automatically when a moderator issues a
   lifetime `BAN` on a wallet-bound account; nothing to enable. It is keyed by the holder hash, so it
   only has teeth once step 2 is on.
4. **Progression visibility** is optional and cosmetic: leave `PROGRESSION_AVATAR_PUBLIC=False`
   unless you deliberately want peer-visible progression.

## Product defaults worth a conscious decision

- **Timed-ban expiry auto-reactivates** the account (reuses the suspension-lift path). Change to a
  manual-review gate if you want a human in the loop for the heavier tier.
- **Authority referrals are silent to the subject** (so a grooming/CSAM referral can't tip off a
  suspect). The accompanying account sanction still sends its DSA Art.17 statement of reasons.

## Periodic jobs

No new scheduled job — the timed ban reuses the existing `lift_suspensions` entry in
`apps/ops/.../run_due_jobs.py` (widened to cover `TIMED_BAN`). Ensure `run_due_jobs` is on its cron.
