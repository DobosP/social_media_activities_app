# Secure messaging (D10)

Username-addressable **direct and group** messaging that is **end-to-end encrypted (E2EE)** and
stays inside the platform's child-safety model. This document is the honest reference for what the
system does, how the cryptography works, and — importantly — what it does **not** guarantee.

See also [SAFETY](SAFETY.md) (the invariants this must preserve), [SECURITY](SECURITY.md),
[THREAT_MODEL](THREAT_MODEL.md), and [COMPLIANCE](COMPLIANCE.md).

## What it is

- **Direct (1:1) and group chats addressable by username.**
- **End-to-end encrypted.** Only the participants can read messages. The server stores ciphertext
  and per-recipient wrapped keys; it holds no key that can decrypt any message (a *zero-knowledge
  relay*).
- **Cohort-isolated.** You can only message users in your **own age cohort**. An adult can never
  reach a child — the same anti-grooming invariant as the rest of the product.
- **Invite-accept.** The first contact creates a pending invitation; the recipient must **accept**
  before they can read anything. No unsolicited messaging.
- **Block-aware and rate-limited**, with a tamper-evident audit trail of *metadata* (never content).

## The central tension: E2EE vs. child-safety scanning

A children-first platform normally wants to **scan** message content for grooming and CSAM. True
end-to-end encryption makes that **impossible** — by design the server cannot read messages. These
two goals are in genuine conflict, and this product resolves it deliberately:

> **Safety is enforced by *access control*, not content scanning, plus *report-with-decryption*.**

Concretely:

1. **Who can talk to whom is tightly controlled** (server-side, not encrypted away):
   - **Cohort isolation:** `can_message(a, b)` requires `a.cohort == b.cohort` and rejects the
     `unassigned` cohort, so unverified users and cross-cohort pairs can never connect.
   - **Invite-accept:** recipients opt in before any content reaches them.
   - **Blocking:** honoured in both directions.
   - **Rate limits:** anti-spam / anti-abuse on starting conversations and sending.
   - The **key registry itself is cohort-isolated** — fetching another user's public key 404s
     unless you're allowed to contact them, so you can't even *address* someone in another cohort.
2. **Reporting works without breaking encryption** — *report-with-decryption*: the reporter's
   client attaches the plaintext **it can already read** to the report. A moderator then acts on
   that evidence through the standard D4 loop (warn/suspend/ban, audit log). The server never needs
   the key; the human who received the message provides the content.

This is the same trade-off the EU CSA Regulation ("Chat Control") debate is about. If regulation
ultimately **requires** client-side scanning or weakened E2EE, that belongs at the **client**
(e.g. on-device hashing before encryption) so the server stays zero-knowledge; the swappable
posture is noted in [COMPLIANCE](COMPLIANCE.md). We do **not** add a server backdoor.

## Cryptography

Implemented with the browser-native **Web Crypto API** (`static/js/e2ee-messaging.js`).

| Purpose             | Algorithm                                                        |
| ------------------- | ---------------------------------------------------------------- |
| Identity keypair    | ECDH **P-256**, generated in-browser; private key in IndexedDB   |
| Content encryption  | **AES-256-GCM** with a random per-message content key (CEK)      |
| Key wrapping        | ephemeral **ECDH P-256** → derived AES-256-GCM key wraps the CEK |
| Backup (optional)   | private key wrapped under **PBKDF2-SHA256** (250k) + AES-256-GCM |

**Sending** (ECIES-style hybrid encryption):

1. Generate a random AES-256-GCM **content key (CEK)** and encrypt the message body once.
2. For **each** active recipient (including yourself, for multi-device / history):
   - generate an **ephemeral** ECDH keypair,
   - derive a shared AES key via ECDH against the recipient's public key,
   - wrap (encrypt) the CEK with it.
3. POST `{ ciphertext, iv, recipient_keys[] }` (or send over the WebSocket).

**Receiving:** take the wrapped key addressed to you, redo the ECDH with your private key to derive
the same AES key, unwrap the CEK, decrypt the body.

**Server-side integrity check:** `post_message` requires the set of `recipient_keys` to **exactly
equal** the conversation's active members. A malicious client therefore cannot silently drop a
recipient (so they can't read) or wrap a key to a non-member. The server validates the *set* without
ever seeing key material in the clear.

### Key backup & multi-device

Private keys live only in the browser. Optionally, a user can set a **backup passphrase**: the
client wraps its private key under a PBKDF2-derived key and uploads the **opaque** blob
(`wrapped_private_jwk`). The server stores it but cannot read it (it never sees the passphrase). On
a new device the user enters the passphrase to restore. Without a backup, switching devices means a
**new key** and **no access to old history** (messages were wrapped to the old key) — an accepted,
documented limitation.

## Honest limitations (what this is NOT)

- **Not Signal/MLS.** This is hybrid public-key encryption. There is **no double ratchet**, so **no
  forward secrecy** and **no post-compromise security**: if a private key leaks, past messages
  wrapped to it can be decrypted.
- **Server-asserted trust (no key verification yet).** Clients trust the public key the server hands
  them. A malicious/compromised server could perform a **man-in-the-middle** by substituting keys.
  There is no out-of-band safety-number verification yet — see Roadmap.
- **Metadata is visible to the server.** Who talks to whom, when, message sizes, and group
  membership are **not** encrypted (they're needed to enforce cohort/abuse rules). Only *content*
  is E2EE.
- **No server-side content moderation.** By construction. Moderation depends on
  report-with-decryption and access control. This is the deliberate trade-off above.
- **Group security is simple ("sender keys"-free).** Each message is encrypted to the current member
  set. Membership changes are enforced server-side, but there's no cryptographic group ratchet.

## Data model (`apps/messaging`)

- **`PublicKey`** — the registry: a user's public JWK (+ optional opaque backup blob). One active
  key per user; rotation deactivates the old one.
- **`Conversation`** — `direct` or `group`, with a snapshotted **cohort** it is locked to.
- **`Participant`** — membership with an invite-accept lifecycle
  (`invited → active`, plus `declined / left / removed`) and an `admin / member` role.
- **`Message`** — **ciphertext only** (`ciphertext`, `iv`, `algorithm`). No readable body, ever.
- **`MessageKey`** — the per-recipient wrapped CEK (`ephemeral_public_jwk`, `wrapped_key`,
  `wrap_iv`). One row per recipient per message.

## API surface (`/api/messaging/`)

| Method + path                                             | Purpose                                  |
| --------------------------------------------------------- | ---------------------------------------- |
| `GET/POST /keys/`                                          | get your own key / publish-rotate it     |
| `GET /keys/<username>/`                                    | fetch a contactable user's public key    |
| `GET/POST /conversations/`                                 | list / start a direct or group chat      |
| `POST /conversations/<id>/accept/` `/decline/` `/leave/`   | invitation & membership lifecycle        |
| `POST/DELETE /conversations/<id>/participants/`            | group admin add / remove (by username)   |
| `GET/POST /conversations/<id>/messages/`                   | history (with your key) / send ciphertext |
| `POST /conversations/<id>/messages/<mid>/report/`          | report-with-decryption                   |

Real-time delivery is over a WebSocket at `ws/messaging/<conversation_id>/`, which relays the stored
ciphertext (with every recipient's wrapped key) to connected members; each client decrypts only the
key addressed to it.

## Operational notes

- **Anti-abuse** rate limits: `MESSAGING_START_RATE_LIMIT` (default 20), `MESSAGING_SEND_RATE_LIMIT`
  (default 60) per `MESSAGING_RATE_WINDOW_SECONDS` (default 60). For multi-process deploys, configure
  a shared cache (Redis) so counts are global, and a Redis channel layer for WebSocket fan-out.
- **Audit log** records messaging *events* (key registered, conversation started, message sent with
  a recipient count, reports) — never content.
- **Admin** exposes conversation/participant **metadata** for abuse triage; message bodies are
  unreadable there too.

## Roadmap (to strengthen)

- Out-of-band **key verification** (safety numbers / QR) to close the MITM gap.
- **Forward secrecy** via a ratchet (consider MLS / RFC 9420) if the threat model warrants it.
- **Disappearing messages** + server-side retention windows for ciphertext.
- **Guardian visibility** controls for the youngest cohort, consistent with consent rules.
- Client-side safety hashing **iff** mandated by the CSA Regulation, kept on-device.
