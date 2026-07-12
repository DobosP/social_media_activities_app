# ADR-0027: Avatar styles — versioned generations with a uniqueness registry

Date: 2026-07-13 · Status: accepted (owner-approved reshape + style pick)

## Context

The owner asked for a profile-picture experience where each person has **one visible picture at
a time**, the picture is **unique to that person** ("needs to be an NFT"), the generator
**evolves in versioned generations**, and each person **chooses which generation** renders their
picture — with an auto-generated picture for people who don't want to make their own.

A literal on-chain NFT breaks four hard invariants: #2 (financialised/speculative vanity
surface), #3 (crypto near minors), #4 (immutable chain vs GDPR Art.17 erasure), #6 (blockchain
vs cheap Postgres-primary hosting). The owner approved the reshape: keep every property "NFT"
was borrowed for — unique, one-owner, non-transferable, provenance-tracked — implemented
entirely in Postgres.

What already existed (extended, not rebuilt): deterministic generated avatars
(`apps/accounts/avatars.py` identicon + interest constellation), the DB-aware composition layer
(`apps/recommendations/services.py`), and the uploaded profile picture with one-per-user +
cohort-scoped near-duplicate uniqueness (`apps/media/`, unchanged by this ADR).

## Decision

1. **Generation registry** (`apps/accounts/avatars.py::GENERATIONS`): Generation 1 =
   constellation/identicon (bytes unchanged); Generation 2 = **Orbits** (owner-picked from three
   rendered candidates): an identity-hued sun, one tilted orbit ring per interest category, one
   planet per interest on its category's ring. Renderer contract shared by every generation:
   pure, deterministic, `intensity==0` byte-identical (public renders never carry progression),
   ids namespaced, no seed text or interest labels in markup, salt-effective at every interest
   count (always-drawn seeded background).
2. **`SignatureAvatar` row** (`apps/accounts/models.py`, OneToOne): `generation`, `salt`,
   `fingerprint` (sha256 of the canonical render: `CANONICAL_PX=240`, intensity 0, **fixed id
   namespace via `_uid_override=FINGERPRINT_UID`**) with a DB `UNIQUE` constraint. Absent row =
   legacy default; such users render byte-identically to before this ADR.
3. **Services** (`apps/accounts/signature.py`): `set_avatar_style` (atomic; `get_or_create` with
   a per-user placeholder fingerprint converges concurrent first picks; `select_for_update`; a
   salt-retry loop with per-attempt savepoints that only ever bumps salt on a fingerprint
   collision; re-picking the current style is a stable no-op), `refresh_avatar_fingerprint`
   (strict no-op without a row; hooked into the now-`@transaction.atomic`
   `recommendations.set_interests`), `avatar_style_info`. Cross-app imports are lazy in both
   directions to avoid the accounts↔recommendations cycle.
4. **Resolution**: `_avatar_svg` renders the picked generation with seed
   `username|g{generation}|s{salt}` everywhere an avatar shows; `attach_interest_nodes`
   bulk-loads picks (one extra constant query) so list surfaces stay non-N+1.
5. **Surfaces**: profile-page "Your avatar style" picker with per-generation self-only previews
   (same render path as the real thing) + `POST profile/avatar-style/`; SPA parity payload;
   `GET/POST /api/accounts/me/avatar-style/`; `MeSerializer.avatar_style`. All rate-limited via
   `safety.allow_action("avatar_style")`.

### Uniqueness analysis (corrected in adversarial review)

For n≥1 interests, renders already differ per user (the layout PRNG is seeded by the unique
username). The genuine collision surfaces are (a) the zero-interest identicon space (~2^15×360 ≈
11.8M variants — birthday-colliding at thousands of users) and (b) *perceptual* near-identity at
small n (the salient shapes coincide even when background dust differs). The registry turns
"practically unique" into a **provable floor**: byte-identical canonical visuals cannot coexist,
and the salt retry re-rolls the layout when they would. Generation 2 additionally fixes (b) by
construction (the sun hue is identity-derived, so same-interest users differ prominently).

### Hard rules (blocking-review outcomes — do not relax)

- **No collectible framing.** The UI is a plain style picker. Banned everywhere (all users, all
  surfaces): "minted", "certificate", "one of a kind", serial numbers, displayed fingerprints,
  provenance dates. The honest line is: "your picture is unique to you." The registry is an
  internal guarantee, not a vanity surface. (Invariants #2/#3 — collectible-scarcity psychology
  and crypto flavour near minors.)
- **The fingerprint never leaves the database.** Not in any serializer/template/SPA payload/
  export, not in `erasure_preview`, and **never in an audit payload**: the audit log is permanent
  and hash-chained, so a username-derived hash there would survive Art.17 erasure as a
  re-identifier. `avatar.style_changed` logs the generation integer only.
- **Generations are never gated.** Every generation is always available to every user. The pick
  is publicly observable through the render, so unlocking generations via participation/
  progression/meetups would leak activity into a public surface (invariants #2/#3).

### Post-implementation review outcomes (2 Opus lenses over the diff; all integrated)

- **Gen 1 at salt 0 renders from the BARE legacy seed** (`signature_seed` special case): the
  style labelled "current" for the row-less majority is a true visual no-op to pick, previews
  are honest against the live render, and the registry fingerprints the user's actual default
  look. (MED finding.)
- **Unknown generation in a DB row degrades to the default look/info** everywhere (render,
  `/me`, previews) instead of KeyError-ing public surfaces — deprecating a generation can
  never 500 the users who picked it. (LOW.)
- **Salt continuity**: the row's current salt is tried first on any re-fingerprint, so an
  established (collision-bumped) layout family survives interest edits; and refresh swallows
  salt exhaustion (log-only) — an avatar-registry hiccup never aborts an interest edit. (LOW.)
- **GDPR export** (`build_user_export.privacy_settings.avatar_style`) carries the chosen
  generation + name — user preference data the UI shows is portable (Art. 15/20); the
  fingerprint/salt/dates stay internal per the hard rule above. (LOW.)

## Consequences

- One tiny table; pure-SVG rendering; zero external calls; GDPR erasure via CASCADE (invariant
  #4, #6). The uploaded-photo pipeline and its profile-page-only override are unchanged.
- `set_interests` gained `@transaction.atomic` (also closing its pre-existing delete→create
  autocommit window) and a re-fingerprint hook that is a no-op for non-picked users, keeping
  seeding/imports side-effect-free.
- A stranded placeholder fingerprint (crash mid-pick) is harmless: renders read
  generation+salt; the next pick/refresh overwrites it.
- Adding Generation 3+ = one pure renderer honouring the contract + a registry entry + gallery
  preview; no schema change.
