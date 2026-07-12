# Built features & their invariant gates

**Living behavioral-contract catalog** of the shipped feature set (D1–D10 + the 2026-06 feature
waves), moved out of `CLAUDE.md` on 2026-07-02 (the contracts are unchanged). Check this BEFORE
building a "new" feature — much of what gets asked for already exists, with deliberate gates.
Ops-level do-not-rebuild list: [PRODUCTION_READINESS](PRODUCTION_READINESS.md) §0. Dated
wave-by-wave build records: `archive/FEATURE_CATALOG_2026-06*.md` (immutable).

Last verified: 2026-07-11 — contracts match `apps/*/services.py` + the 2,365-test green suite
(see `STATUS.md`).

## The catalog

Built on the social core; see services/tests for exact behaviour. All uphold the six hard
invariants in [`CLAUDE.md`](../CLAUDE.md).

- **AI-agent & search-engine access surface** (ADR-0025) — anonymous read-only events API
  (public gates unchanged); `export_agent_snapshot` job (opt-in `AGENT_SNAPSHOT_DIR`) writes
  gate-filtered public JSON (activities strictly the `public_activities()` ADULT+opt-in card
  subset); stdlib-only Go sidecar `services/agentapi/` serves it at `/agent/v1/*` (cached,
  rate-limited, DB-free); `/open-data/` page + `Dataset` JSON-LD + snapshot downloads;
  agent-grade API filters (events: place/activity/city/from/to/q/near; public activity
  cards: from/to + proximity); `llms.txt` v2; `robots.txt` public-API Allow carve-outs. Gate:
  `social.Activity` never on a crawler surface; snapshot key-sets pinned by exact-key tests.
- **Activity lifecycle** — `cancel_activity` / `complete_activity` (`social/services.py`) +
  `auto_complete_activities` command; cancel notifies members and blocks joins.
- **Edit before start** — `update_activity` (whitelisted `ACTIVITY_EDITABLE_FIELDS`; place/type/
  cohort locked); a time change re-notifies and **supersedes the stale reminder** (`_supersede_reminders`).
- **Organiser announcements** — `post_announcement` (`Post.is_announcement`), pinned + notifies all.
- **Logistics card** — owner-curated `meeting_point` / `what_to_bring` / `organizer_note` on
  `Activity`, edited via the same `update_activity` path, shown to members only (stricter than
  `description`, which is cohort-visible).
- **RSVP intent** — transient `Membership.attendance_intent`; per-activity go/no-go count only,
  reset on leave, **never** aggregated into per-user history (`set_attendance_intent`/`attendance_summary`).
- **Arrival ping** — self-declared `mark_arrived` (`Membership.arrived_at`): no location, no free
  text, idempotent, notifies other members and (for a CHILD) the active guardian(s); cleared by
  `expire_arrivals` so it never becomes a presence record.
- **Parent meetup manifest** — read-only `/wards/` view of each ward's upcoming place/time/type.
- **Safe-exit card** + **use-my-location** (request-only proximity) in `apps/web/`.
- **Unique profile images** — `media.services.profile_image_is_taken` rejects a profile picture
  byte-identical (post-EXIF-strip `sha256`) to another user's **within the same cohort** (the
  single seam to refine "unique" later). Generic rejection message + rate-limited upload so it
  can't be used as an enumeration oracle. Best-effort, not perceptual / not impersonation-proof.
- **Consent & guardianship legibility (F13)** — two-sided read-only panels (`/wards/`, `/guardianship/`)
  stating exactly what a link grants, from `accounts.guardianship_capabilities`; guardian-side revoke
  reuses `accounts.revoke_guardian`. Ward side is legibility-only (no ward-initiated unlink).
- **Notification reasons & per-kind mute (F31)** — `NotificationPreference` + a mute gate in the single
  `notifications.notify()` choke point. **MODERATION (DSA Art.17) and SYSTEM (DSA Art.16) are never
  mutable** — checked first, before any lookup. Each notice carries a "why you got this" line.
- **Post-meetup "did we meet?" (F22)** — `Membership.met_confirmed_at`, settable only when the activity is
  COMPLETED; shows a member-only **count** ("Confirmed: N of M") — never a who-confirmed list and **never a
  per-person rating or cross-activity rollup**. Cleared on leave.
- **Age-proof provenance (F14)** — `accounts.assurance_provenance` renders a read-only profile panel: band +
  method + provider + verify/expiry dates + a re-verify nudge. Exposes **no DOB/identity/raw attestation**.
- **Your safety record (F19)** — `safety.safety_record_for` powers `/my-safety-record/`: a user's own DSA
  Art.16/17 record (moderation decisions about their account/activities/posts + reports they filed).
  Strictly self-scoped, field-allowlisted — never another user's data or the moderator's identity.
- **What-to-expect fields (F8)** — owner-curated `Activity.cost_band` / `difficulty` (choices) +
  `accessibility_notes`, routed through the F2 edit path; shown as cohort-visible chips (not member-gated).
- **Honest "why recommended" + beginners filter (F17)** — the home feed shows a true reason from the viewer's
  own declared interests ("matches your interest in X") or "soonest first" on cold-start, else the genuine
  "% match"; `Activity.beginners_welcome` adds a `?beginners=true` filter (the ranked strip stays unfiltered).
- **Topic preferences (the user's hand on suggestions) + text-first browse modes** — `recommendations.TopicPreference`
  lets a user STATE which taxonomy **categories** their suggestion feed should lean toward (declared, never inferred —
  same contract as `UserInterest`/`AccessPreference`). It is **SOFT only**: `sort_by_topic_match` floats chosen-topic
  meetups to the front of the *already cohort-gated* `recommended_with_reasons` list and adds an honest "· matches your
  chosen topics" reason — it **never hides** a meetup, never widens visibility past the cohort wall, never tracks (upholds
  inv.1/inv.2). `category_ancestry_slugs` means picking a parent topic ("sport") covers its sub-types. A **CHILD ward's
  active guardian** can set the ward's topics from `/wards/` ("the responsible person controls the feed") via the same
  `_active_ward_or_none` gate as the F7 guardrails — this is the SOFT steering layer; the **HARD** child-safety category
  envelope stays `GuardianGuardrail.allowed_categories` (a separate join/create gate). Self-service `/topics/` + DRF
  `TopicsView`; both call the one `set_topic_preferences` service. Two presentation-only browse modes on `/activities/`
  (`?view=list|card` over the same `visible_activities` query — compact rows / one focused meetup with prev-next pager):
  text-first, no images, **no swipe, no infinite scroll** (an explicit reshape of a photo-swipe ask that would have
  broken inv.1/inv.2).
- **"Organize one here" prefill (F40)** — an event's "Organise" link seeds the create form's activity type +
  start time; `activity_create` validates every GET value (type exists/active, time parses) before seeding.
- **Catch-up thread digest (F35)** — `social.thread_digest` is a deterministic (no-ML) extractive recap
  (latest announcements + keyword-matched logistics + recent posts + going/total) shown member-only in a no-JS
  `<details>` "Catch up". Same digest for every member — **no per-user read-tracking**. Bounded queries.
- **First-timer welcome mat (F39)** — `_admit` marks a genuinely-new joiner's first membership (`welcomed_at`)
  and appends a line to their `JOIN_APPROVED` notice; a self-dismissing banner (7-day TTL) shows on the activity.
  **No thread Post is written** (avoids the required `Post.author` FK) — the welcome is unmistakably systemic.
- **Activity draft helper (F36)** — `social.draft_activity_text` composes a deterministic (template-only) draft
  title/description from the organizer's chosen type/place/time (+ a CHILD/TEEN safety reminder); `activity_create`
  seeds them via `setdefault` so it **never overwrites typed input**. Composes with F40's prefill.
- **Accessibility facts + access preference (F15)** — `places.accessibility_facts` derives honest states
  (true/limited/false/**unknown**) from a venue's existing OSM tags at **read time** (never written back — re-ingest
  would clobber). A per-user `AccessPreference` (a *stated* setting, not inferred) drives a **soft** "matches your
  access needs" badge that **never hides** unknown-accessibility places. `/access/` edits it.
- **WCAG chrome + JS-free places list (F16)** — a server-rendered `/places/list/` text fallback for the Leaflet
  map (mirrors the API filter/proximity, `.distinct()`), plus a skip link, ARIA landmarks, visible `:focus-visible`
  styles, and an `aria-live` chat region (muted during history load so screen readers don't replay the backlog).
- **Donation transparency (F29)** — `/transparency/` shows aggregate `completed_total_cents` raised next to
  staff-entered `SpendEntry` rows by category (two separate sections, **never** an "X of Y goal" bar; no donor
  PII); `/my-donations/` gives a donor their own plain receipts (self-only, no card data). `|cents` templatetag.
- **Earmarked campaigns (F34)** — staff `Campaign` + optional `Donation.campaign` FK (`SET_NULL`); `/campaigns/`
  shows a **calm static** progress bar (integer percent, capped 100; no countdown/scarcity/vanity). Inactive
  campaigns are blocked at all 3 layers (form/serializer/`start_donation`); general fund stays the default.
- **Verified civic partners (F37)** — `places.Partner` (text-only; **no image/logo field**), `/partners/` and a
  one-line place_detail credit. `Partner.objects.public()` (verified+active) is the single visibility chokepoint;
  website sanitised via `safe_external_url`; blurb capped at 280; neutral alphabetical order (no pay-for-placement).
- **User-proposed places, co-created (F25)** — `social.propose_place_with_venue` creates a `source=USER` `Place` +
  a `UserPlaceProposal` that needs **N independent confirmers** (`confirm_place`, proposer excluded) before going
  public. `places.public_places()` is the **single visibility chokepoint** — a *positive keep-filter* (`~USER OR
  proposal.PUBLISHED`, so a USER place with **no proposal row** is correctly hidden, not leaked by `NULL IN`). EVERY
  AllowAny Place surface (API `PlaceViewSet`, discovery `NearMe`/`Happening`, web list/detail) routes through it;
  `place_detail` 404s a pending place to everyone but its proposer/staff. Duplicate guard: 60 m hard / 25 m soft
  (`allow_nearby` override). Pending UI shows confirm **counts only**, never the proposer/confirmer identities.
- **Crowd confirm/dispute of activity edges (F26)** — `places/edges.py`: members `vote_on_edge` confirm/dispute a
  `PlaceActivity`. Tally lives in `ActivityEdgeVote` (one row per (edge,user); a mind-change updates it) — **ingest
  never touches that table**, so it survives re-ingest. A quorum (3) of disputes sets the **ingest-safe**
  `PlaceActivity.is_disputed` (absent from `ingest_places` `defaults`, so re-ingest can't clear it) and every read
  surface hides the edge; a quorum of confirms promotes an **INFERRED** edge to **CONFIRMED** (then in
  `PROTECTED_ORIGINS`, so ingest won't demote it). Only INFERRED edges auto-flip — a CONFIRMED edge is **not**
  crowd-hideable (no griefing); `moderator_reverse_edge` (demote/restore/reset) is the only reversal. Disputes are
  weighed **before** confirms (accuracy-first). `edge_vote_summary` exposes counts + the viewer's own vote only.
- **Open-now accuracy reports (F28)** — `places.open_now_status` returns open/closed from parsed hours, **downgraded
  to `"unverified"`** when ≥3 recent member reports (`OpenNowReport`) say the posted hours are wrong; `None` if hours
  are unknown. A **dedicated overlay** model (never on `Place`, which re-ingest clobbers) with **read-time decay**
  (reports outside `OPEN_NOW_REPORT_DECAY_SECONDS`=14 d stop counting — hours self-heal). `file_open_now_report` gates
  on `can_participate`, is **rate-limited** across venues and **idempotent** per reporter/place/window (anti-brigading);
  `clear_open_now_reports` is the staff reset. `PlaceViewSet` annotates `recent_report_n` so the serializer avoids N+1.
- **One Thread — unified activity conversation** — collapses the two old overlapping surfaces (durable `social.Post`
  + the retired realtime `chat.ChatMessage`) into a SINGLE durable `Post` stream; the WebSocket is pure live delivery.
  `social.post_to_thread` is the **single hardened write path** for web/DRF/socket (the DRF chat POST was deleted; a
  test asserts `post_to_thread`/`post_announcement` are the **only** Post creators). Its union gate: current MEMBER +
  `role≠GUARDIAN` + `can_participate` + activity not hidden + **`status≠CANCELLED`** (OPEN *and* COMPLETED post, so the
  post-meetup + F22 flow survive; only a cancelled meetup freezes) + not blocked-vs-owner + rate-limit +
  MessagePolicy/CSAR. `can_read_thread` is the single read gate (web view + keyset history + consumer connect/receive/
  per-delivery 4403). **Depth-1 quote-reply** `Post.reply_to` (`SET_NULL`, re-parented to the top-level ancestor in the
  service — never a tree, no recursive CTE); the quote snippet is **derived live** from the current parent at render/
  serialize time (a hidden/edited parent updates its replies on next read — never stored). Author `edit_post` /
  `delete_own_post` are audited soft-edits (the "edited" marker is derived; no field/edit-count). `thread_page` is
  **bounded keyset** pagination (no infinite scroll); the `?before=` cursor + `#post-N` permalink stay behind the
  membership wall. `broadcast_post` fires on `transaction.on_commit` (rolled-back writes broadcast nothing; graceful
  no-op without a channel layer — **needs `REDIS_URL`** cross-process). `post_announcement` now excludes blocked pairs
  from the fan-out. Thread Posts are **permanent + audited** (the `purge_chat`/`CHAT_RETENTION_DAYS` retention was
  dropped). The `aria-live` region announces only the viewer's own send + announcements — **never** every peer message.
  Explicitly OUT (each needs its own review): reactions, acks, @mentions, markdown, typing, unread dividers.
- **Connections** — find/reconnect with people you've shared a real activity with; the discovery layer in front of the
  existing E2EE `messaging`. `connections.can_connect` is the gate: same cohort + both `can_participate` + not blocked +
  **a shared PEER activity** (`shares_activity` excludes supervisory guardians, mirroring `voting_members`) + cohort
  allowed by `CONNECTIONS_ALLOWED_COHORTS` (**all cohorts by default, each strictly within its own cohort; UNASSIGNED
  never; cross-age structurally impossible via the same-cohort gate; children additionally need parental consent +
  guardian-observable messaging** — the old CHILD hard-wall was deliberately removed 2026-05-30, see ADR-0002). A
  deliberate **mutual opt-in** (request→accept; a reciprocal pending auto-accepts), re-gated at
  accept time. **Discovery is SEARCH-ONLY** (`search_connectable` needs a query and returns only peer co-members in your
  cohort) — there is **no "people you may know" suggestions feed** and **no attendance/“met-N-times”/reliability** stored
  or shown (eligibility is derived live — no behavioural rollup). `request_connection` is **idempotent** (a repeat never
  re-notifies) and **rate-limited** (`CONNECTIONS_REQUEST_RATE_LIMIT`) — no post-decline pestering. `open_conversation`
  requires an accepted connection then reuses `messaging.start_direct` (never bypasses messaging's own gate). Web
  (`/connections/` + a co-member “connect” button, guardian viewers excluded) + a DRF `ConnectionViewSet`; the web
  `connection_request` uses `_safe_next` (open-redirect guard). New mutable `Notification.Kind` CONNECTION_REQUEST/ACCEPTED.
  Connections are enabled for **all cohorts within their own cohort** (`CONNECTIONS_ALLOWED_COHORTS`; cross-age stays
  structurally impossible via the same-cohort `can_connect` gate).
- **Communities (derived geo×activity-type discovery labels)** — a "community" (e.g. "Cluj-Napoca Football") is a
  **materialized SAVED-SEARCH with a human name**, NOT a room/roster/feed/chat. `communities.Community` pins one
  coordinate on each of two existing FK chains — the GEO axis (`Place.address_city` → an `Area`; finer PostGIS areas
  later) and the TAXONOMY axis (`ActivityType → category`). **Materialized PER COHORT** by the nightly
  `generate_communities` job (in `ops` `DUE_JOBS`) only above `COMMUNITY_MIN_ACTIVITIES` + `COMMUNITY_MIN_DAYS` + a
  **k-anonymity floor** counted as DISTINCT non-guardian peers of that cohort (a supervisory guardian never counts toward
  a minor slice) — so a child never sees the **existence** of a community built off adult activity. The single read
  primitive `community_activities(community, viewer)` **asserts `viewer.cohort == community.cohort`** then routes through
  the existing `social.visible_activities(viewer)` and only narrows it — there is no second read path. **Membership is
  never stored or shown** (no count, no roster — a serializer-allowlist test enforces it); ordering is deterministic
  (alphabetical/soonest-first, never hot/trending); the list is paginated; no community-digest notification. Activities
  map in by **predicate at read time** (nothing written on the Activity; place/type/cohort are immutable). The
  **private-contact wall is untouched** — co-presence in a community is **not** a shared activity, so it never enables
  `can_connect` (pinned by a test). Generic across all activity types (sports is just the launch slice). New Activity
  indexes `(cohort, activity_type)` / `(cohort, place)` keep the predicate + generator index-scan. Read surfaces are
  members-only (`IsAuthenticated`, web + DRF `CommunityViewSet`); deactivate-not-delete self-heals a dry spell.
- **Group-thread media (images + PDF, no video)** — `media.Attachment` (FK to `social.Post`) puts media IN the unified
  conversation, members-only. `attach_to_post` reuses the Photo pipeline: image **EXIF-strip + re-encode**
  (`validate_and_strip`, PNG/JPEG/WEBP only), **fail-closed** hash-blocklist scan on the original bytes (reject unless the
  scanner is effective when `MEDIA_REQUIRE_SCANNER`), the storage backend, and **signed, expiring, per-viewer,
  membership-scoped URLs** (`attachment_signed_url`/`resolve_attachment_token` → `AttachmentFileView`). **PDF is the only
  FILE type, ADULTS-ONLY** (`MEDIA_FILE_COHORTS`, never minors — images are allowed in any cohort thread) and is **always
  served as a forced download** (`Content-Disposition: attachment` + `nosniff`) so it can't execute inline. **Never in 1:1
  DMs** (E2EE = unscannable). `post_to_thread(..., allow_empty=True)` permits an attachment-only message; the web
  `activity_post` creates Post + Attachment in **one transaction** (a rejected scan rolls back the post). `can_view_attachment`
  re-checks `can_read_thread` + `post.is_hidden` + block-vs-uploader; only the author/staff can delete. No video — deferred
  pending a real video-CSAM-scanning decision.
- **One real person = one account (EUDI holder binding)** — `accounts.IdentityBinding` records a **keyed HMAC** of the EUDI
  wallet holder subject (never the raw subject — data-minimal) so the same credential can't assure two accounts.
  `bind_identity` (atomic, row-locked) is wired into `EUDIVerifyView` (**409** on a duplicate wallet) and web `register`; the
  link is `SET_NULL` so it **survives erasure**. Gated by `settings.IDENTITY_UNIQUENESS_ENFORCED` (**default off**) AND a proven
  holder key (`holder_proof == "verified"`), so the dev/sandbox flow is untouched. `AssuranceResult.holder_sub` is transient —
  deliberately **never** copied into `AgeAssurance.raw`.
- **Tiered account sanctions + authority referral** — three tiers on the existing `take_action`/`lift_expired_suspensions`/
  hash-chained `AuditLog`: pause (`SUSPEND`), **`TIMED_BAN`** (auto-reactivates on expiry, via the shared lift path), and
  lifetime `BAN` **plus** `accounts.BannedIdentity` (a holder-hash ledger so a lifetime ban **survives GDPR erasure** and blocks
  wallet re-registration → `IdentityBanned`/**403**). `create_authority_referral` records a referral (subject by **`public_id`**,
  impersonation-safe) pinned to its own audit entry; `referral_proof_pack` runs `verify_audit_chain` for a lawful request.
  **Deliberately silent to the subject** (tipping off a grooming/CSAM suspect can defeat an investigation); the accompanying ban
  still carries its DSA Art.17 notice. F19 self-record surfaces the subject's own sanctions (`is_sanction`).
- **Anonymous adult-only discovery (opt-out)** — logged-out outsiders can find **ADULT** activities and groups "looking for
  people", organiser **opt-out, default ON** (`Activity.is_publicly_listed`/`Group.is_publicly_listed`, a structural pin kept
  out of `ACTIVITY_EDITABLE_FIELDS`). **Three independent walls** make minor exposure impossible: the viewer-less
  `public_activities()`/`public_groups()` **hard-code `cohort=ADULT`**; `set_public_listing` refuses a non-adult object; and
  `create_activity`/`create_group` force the flag False for a minor owner. New `AllowAny` `PublicActivitiesView`/
  `PublicGroupsView` + a web `/discover/` page; card serializers expose **no owner PII**.
- **Self-only progression (the evolving avatar)** — a felt sense of "evolving" derived live from the one real-world signal
  (F22 `met_confirmed_at`), **stored nowhere new** (no model/migration). `social.self_confirmed_meetup_count` (self-only;
  **regresses on leave**) → `progression_level`/`progression_intensity` modulate a purely-visual `intensity` kwarg on
  `constellation_svg` (**`0.0` is byte-identical** to the base avatar). `recommendations.evolving_avatar_*` is shown only on
  self-surfaces (`/me` `MeSerializer` + the web "Your journey" card); others see the base avatar unless
  `settings.PROGRESSION_AVATAR_PUBLIC` (**default off**). No leaderboard, no cross-user comparison, no audit/streak nudges —
  upholds inv.2.
