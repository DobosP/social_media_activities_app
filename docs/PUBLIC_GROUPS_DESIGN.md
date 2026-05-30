# Public Groups — design spec (for next-run implementation)

> Produced by a multi-agent design workflow on 2026-05-31 (independent architecture
> proposals → judged → synthesized → adversarial child-safety critique). This is the
> ready-to-build spec. Build it on a branch following the standard per-feature cycle
> (branch → test → adversarial-review workflow → fix → merge --no-ff → push).

**Product decision (locked by the user):** ALL cohorts can join groups, but MINORS
(child + teen) stay ROSTER-LESS — adults see the roster/count, minors never see a member
list/count/who-is-here (only the feed + a moderated group thread). The connection/member
count-visibility toggle folds into this work.

---

## Candidate ranking (design workflow)

- **reuse-thread** — total 39, child_safety 8/10
  - biggest weakness: The guardian-observer mechanism is the single point where the design breaks its own "one read gate" promise and is also the most safety-load-bearing net-new code. Because an adult guardian's cohort is ADULT, the reused can_read_thread (cohort wall at services.py L626) correctly REJECTS them from a CHILD group thread — so the candidate must introduce a SECOND read path, can_observe_group_thread, plus persistent observer GroupMembership rows and auto-prune machinery (drop_guardian_observers_for-style on ward-leave, revoke, and age-out). For a one-off activity thread a guardian's presence is naturally bounded by the meetup; for a STANDING, persistently-joinable group a child returns to, a single missed prune (ward leaves but observer row lingers, or a revoke that doesn't fan out) leaves an adult continuously reading a children's space — exactly the adult<->minor exposure inv.3 forbids. The candidate leans on the messaging-observer precedent, but that precedent's prune correctness is itself subtle (_prune_orphaned_guardians / drop_guardian_observers_for must both fire on every relevant transition), and replicating it for an open-join, long-lived group multiplies the transitions that must each remove the observer. The read-time cohort re-check fails closed for peers but NOT for guardians (they're admitted via the carve-out by design), so the guardian path has no equivalent fail-closed backstop — its safety rests entirely on the prune fan-out being exhaustive. This carve-out, not the roster rule, is where this design can actually leak.
- **community-joinable** — total 8, child_safety 8/10
  - biggest weakness: The guardian-on-minor-group mechanism is the weakest and least-grounded piece, and it is the one that most directly touches child safety. The candidate says to "directly copy" messaging.add_guardian_observer / _child_wards_in, but that pattern is hardcoded CHILD-only (apps/messaging/services.py L378-389: `user__cohort=Cohort.CHILD`), while the design puts guardian observers on TEEN groups too — an extension with no existing template that the candidate hand-waves. Worse, a guardian observer is a SANCTIONED cross-cohort PRESENCE (an adult reading a minor thread). On a one-off Activity that adult sees one ephemeral meetup; on a PERSISTENT, openly-joinable standing group the same observer seat becomes a durable, standing adult read-window into a minor space — exactly the "highest-leverage grooming surface" the candidate flags elsewhere, now applied to the observer chair itself. Combined with minor_onboarding_enabled() being OFF in prod by default (accounts/services.py L54-59, commit f2448a3), the entire minor-group + minor-guardian apparatus — the design's largest complexity and risk surface — is effectively dead code at launch, yet the candidate never scopes minor groups behind that same flag or confronts the standing-honeypot risk that even a staff-curated, openly-joinable minor group lets any same-cohort minor slowly reconstruct a roster by accumulating post-author identities over time.
- **first-class** — total 7, child_safety 9/10
  - biggest weakness: The roster-less-for-minors rule is product-arbitrary, not a real safety boundary, and partly collides with invariant 2 (no vanity metrics). I verified that today every cohort — including CHILD/TEEN — already sees an UNCONDITIONAL member count on every activity card (social/serializers.py L23/L56-60, get_member_count gated by nothing). A Group is, by the design's own framing, just a persistent cohort-pinned Activity. So a minor in a group already sees per-post authorship in the moderated GroupPost thread AND can see member counts on every activity that group surfaces — the only thing hidden is the *aggregate group roster/count*. That hides almost nothing a determined minor cannot reconstruct from the activity feed + post authorship, while the count it adds for ADULTS is precisely the kind of standing-membership vanity number invariant 2 warns against (a stored-membership "Cluj Football has 312 members" surface is closer to engagement-maxxing than anything in the current codebase, which deliberately has NO per-user reliability/attendance and NO browse-all). The design treats "minors roster-less" as the headline safety win, but the true safety guarantees (cohort wall, no adult↔minor private contact, guardian read-only, age-out eviction) come entirely from the cohort-pinning + can_connect-untouched + apply_assurance-eviction machinery — all of which would hold even if the roster were hidden from EVERYONE. So the cohort-asymmetric roster is the weakest, most justify-by-fiat part: it adds the only net-new count/vanity surface in the app, for adults only, to solve a problem (minor enumeration) that the existing thread-authorship + activity-count surfaces already leak, making the rule feel like a product compromise dressed as a safety invariant. Secondary: "first-class new app deliberately mirroring (not reusing) the thread gate union" doubles the hardened-gate surface area — the very divergence-risk the One-Thread collapse was built to eliminate — and is mitigated only by a copied source-guard test, not by structural reuse.

---

## Recommended design

I now have full grounding. The synthesis is clear. Here is the recommended design.

---

# RECOMMENDED DESIGN — Public Groups

## 0. Synthesis decisions (what I took, grafted, and fixed)

**Base = `reuse-thread` (winner, 39/8).** It reuses the *one* hardened thread stack instead of cloning it, which is the whole reason the One-Thread collapse exists. I keep that.

**Three fixes grafted in:**

1. **Fix the winner's biggest weakness (the guardian carve-out is the only non-fail-closed read path).** I *remove the standing adult-guardian observer from minor group threads entirely.* This is the single most load-bearing net-new safety code in the winner, its prune fan-out is the leak surface, and it is unnecessary: a Group is a discovery shell over the existing **Activity** thread. Guardians already get read-only oversight of the actual *meetups* a ward joins (activity threads, `/wards/` manifest, arrival pings) through the existing, already-audited activity machinery. A standing group's own thread does **not** need an adult observer seat — the per-meetup oversight is where the child actually does the in-person activity, and it is naturally bounded. So **the group thread is peer-only (same-cohort members), with NO guardian carve-out, so `can_read_thread`'s cohort wall stays the single, fail-closed read gate verbatim.** This deletes `can_observe_group_thread`, observer `GroupMembership` rows, and all the prune fan-out the winner's reviewer flagged as the leak. (Product decision flag P1 below.)

2. **Fix the third candidate's correct objection (an adult-only stored-membership "312 members" count is a net-new vanity surface that invariant 2 warns against).** I make the count rule **derived and ADULT-gated** as the winner specifies — but with two hardenings the third candidate's critique demands: (a) the count is **never a header badge / never a card / never "Cluj Football has N members"** — it appears **only inside the roster panel an adult member already opened** (`group_roster` returns the list; the count is `len()` of that same list — they are the *same* gated read, not two surfaces), and (b) I also **close the pre-existing leak** the third candidate found: `ActivitySerializer.get_member_count` (social/serializers.py L56-60) currently leaks a count to **every** cohort including CHILD/TEEN. That gets brought under the same cohort rule. This makes the platform-wide rule uniform *and* removes the only existing vanity-count surface rather than adding a worse one. (Product decision flag P2 — whether adults see a count at all, or only the roster list.)

3. **Fix the second/third candidates' parallel-stream divergence risk** by *not* building a parallel `GroupPost` stream. Reuse `social.Post` via a nullable `Thread.group` FK + an XOR constraint, exactly as the winner specifies. The single-creator test still passes (Posts only created in `social/services.py`).

**Net:** the winner's structure, minus its one leaky carve-out, plus the third candidate's count discipline and pre-existing-leak fix.

---

## 1. Final data model

All new models live in **`apps/social`** (same app as Thread/Post — Thread can FK them with no cross-app cycle; the single-creator test only forbids `Post.objects.create` outside `social/services.py`, and a `Group`/`GroupMembership` create is a different model).

### `social.Group` — persistent, Activity-SHAPED shell
Mirrors the duck-typed attributes `post_to_thread`/`can_read_thread` read off `activity`, so the gates work verbatim:

| Field | Type | Notes |
|---|---|---|
| `owner` | FK(User, **PROTECT**, related_name="owned_groups") | named `owner` so `safety._affected_user` (L155) resolves DSA Art.17 to the right person, and the block-vs-owner check (L534/L634) works verbatim. PROTECT not CASCADE — a staff-curated minor group must not vanish if the staff account is deleted; reassign on offboarding. |
| `area` | FK("communities.Area", PROTECT) | reuse the Community geo coordinate (derived from Place geometry only — never a user position; inv.4). |
| `activity_type` | FK("taxonomy.ActivityType", PROTECT, null=True) | TYPE tier. |
| `category` | FK("taxonomy.ActivityCategory", PROTECT) | always set (CATEGORY tier or the type's category). Same tier shape as Community. |
| `tier` | CharField(choices TYPE/CATEGORY) | mirrors `Community.Tier`. |
| `cohort` | CharField(choices=Cohort.choices) | **PINNED from `owner.cohort` at creation** (mirrors Activity L74-75). IMMUTABLE; the isolation boundary. NOT in any editable whitelist. |
| `title` | CharField(max_length=200) | |
| `description` | TextField(blank=True) | |
| `status` | CharField(choices ACTIVE/ARCHIVED, default ACTIVE) | supplies `activity.status`; ARCHIVED freezes the thread like `Activity.Status.CANCELLED`. |
| `is_hidden` | BooleanField(default=False) | `take_action` REMOVE (L218-220) hides uniformly; gates read `getattr(_, "is_hidden")`. |
| `is_staff_curated` | BooleanField(default=False) | |
| `created_at` / `updated_at` | DateTimeField | |

**Indexes / constraints:**
- `Index(cohort, status)` — `visible_groups` discovery scan (mirrors Activity L104).
- `Index(cohort, area, activity_type)`, `Index(cohort, category)` — discovery cell + Community-linkage lookup.
- `UniqueConstraint(cohort, area, activity_type)` partial `WHERE status='ACTIVE' AND activity_type IS NOT NULL`; `UniqueConstraint(cohort, area, category)` partial `WHERE status='ACTIVE' AND activity_type IS NULL` — at most one live group per coordinate per cohort (anti-spam-clone). Copy Community's tier `CheckConstraint`.

### `social.GroupMembership` — stored membership (template: messaging `Participant`, NOT activity join-by-vote)

| Field | Type | Notes |
|---|---|---|
| `group` | FK(Group, CASCADE, related_name="memberships") | |
| `user` | FK(User, CASCADE, related_name="group_memberships") | |
| `role` | CharField(choices OWNER/MEMBER) | **No GUARDIAN role** (deleted per Fix 1 — there is no group-thread observer). |
| `state` | CharField(choices MEMBER/LEFT/REMOVED, default MEMBER) | **No REQUESTED/INVITED** — groups are openly joinable. `join_group` admits straight to MEMBER. |
| `joined_at` | DateTimeField | |

**No** `attendance_intent` / `arrived_at` / `met_confirmed_at` / `last_read_at` — a standing group must never accrue per-user reliability/read-tracking history (inv.2).

**Indexes / constraints:**
- `UniqueConstraint(group, user)` (mirrors Membership L170 / Participant).
- `Index(group, state)` — powers `group_roster` / count.
- `Index(user, state)` — "my groups" + the cohort-change eviction sweep.

### `social.Thread` (MODIFIED — the ONLY structural change to the Post stream)
- Add `group = FK(Group, on_delete=CASCADE, null=True, related_name="thread")`.
- Make `activity` nullable.
- `CheckConstraint`: exactly one of (`activity`, `group`) non-null (XOR). Existing rows backfill cleanly (all have `activity`, `group` NULL).
- Add `@property owner_object(self): return self.activity or self.group` — the single duck-type bridge.

`Post` is **unchanged** (still FK to Thread), so reactions / mentions / attachments / digest / edit / delete attach for free, and the single-creator test still passes.

---

## 2. Single read + single write chokepoints (exact gate order)

### WRITE — `social.post_to_thread` (REUSED VERBATIM, two tiny generalizations)
The function already reads `activity.thread`, `.owner_id/.owner`, `.status`, `.is_hidden`, `.memberships`, `.cohort` — all of which `Group` now supplies with identical names. Two edits only:
- `current_members(activity)` (L520) → a dispatcher `thread_members(owner_obj)`: `activity.memberships.filter(state=MEMBER)` for an Activity; `group.memberships.filter(state=MEMBER)` for a Group.
- the CANCELLED check (L532) → `is_thread_frozen(owner_obj)`: `Activity.status==CANCELLED` OR `Group.status==ARCHIVED`.

**Gate order (identical on activity and group threads):**
1. current MEMBER of the owner_object (else `NotAMember`)
2. `role != GUARDIAN` (vestigial for groups since no GUARDIAN role exists there — harmless, keeps one code path)
3. `can_participate(author)` (lapsed consent/assurance)
4. not `owner_object.is_hidden`
5. not `is_thread_frozen(owner_object)` (CANCELLED / ARCHIVED)
6. not blocked-vs-owner (`author.id != owner_id and is_blocked`)
7. rate-limit (`allow_action`)
8. **MessagePolicy / CSAR scan** — non-negotiable: a public group thread is server-readable, so it routes through the same content scan as activity threads (unlike E2EE messaging, which can't be scanned).

`post_announcement` generalizes the same way for an owner/staff broadcast (and already excludes blocked pairs from fan-out). The **WebSocket consumer** needs only `select_related("activity", "group")` and `can_read_thread(user, thread.owner_object)`; `broadcast_post` already keys on `chat_{thread_id}` (not activity), so live delivery works untouched. `Post.objects.create` stays in `post_to_thread`.

### READ — `social.can_read_thread` (REUSED VERBATIM, passed `owner_object`)
Gate order, fail-closed at each step:
1. authenticated + `is_active`
2. not `owner_object.is_hidden`
3. **`user.cohort == owner_object.cohort`** ← the cohort wall, **now the single fail-closed read gate with no carve-out** (Fix 1). An aged-out or cross-cohort user is rejected at read time even if an eviction was missed.
4. `can_participate(user)`
5. live MEMBER (`group.memberships.filter(user, state=MEMBER).exists()`)
6. not blocked-vs-owner

Backs the web view, keyset history (`thread_page`), digest, and per-delivery 4403 — gate divergence structurally impossible.

### GROUP-ENTITY discovery (distinct from the thread) — `visible_groups(viewer)`
ACTIVE, not-hidden groups of `viewer.cohort` only; anon/UNASSIGNED → `.none()` (mirrors `visible_communities` L62-71). Every group-entity surface (web list/detail + DRF `GroupViewSet`, `IsAuthenticated`, **never AllowAny**) sources from this. `group_detail` 404s a cross-cohort/hidden group.

### JOIN / LEAVE — single write paths
`join_group(user, group)` `@transaction.atomic`: gate = in `visible_groups(viewer)` + `user.cohort == group.cohort` + `can_participate(user)` + `group.status==ACTIVE` + not `is_blocked(user, group.owner)` + creation-side eligibility for minors (consent re-check via `can_participate`) + rate-limit. Idempotent (re-join is a no-op, never re-notifies). `record_audit("group.joined")` inside the txn. `leave_group` sets `state=LEFT`, `record_audit("group.left")`. **No guardian prune needed** (no observers).

---

## 3. Roster / count visibility rule (per cohort) — THE rule, in ONE service

`group_roster(group, viewer) -> list[User] | None` is the **sole** "who is in this group" read path (mirrors `community_activities` being the sole activity-list path). Both the web template and `GroupSerializer` read **only** this and `group_member_count`.

```
def group_roster(group, viewer):
    if not viewer.is_authenticated or viewer.cohort != group.cohort:   # cohort wall FIRST
        return None
    if viewer.cohort in (Cohort.CHILD, Cohort.TEEN):
        return None                                                     # MINORS: nothing, full stop
    # ADULT only, and only if a live member:
    if not group.memberships.filter(user=viewer, state=MEMBER).exists():
        return None
    return list(group.memberships.filter(state=MEMBER)
                .exclude(user_id__in=blocked_user_ids(viewer))          # block-filtered both ways
                .select_related("user"))
```

`group_member_count(group, viewer) -> int | None` = `len(group_roster(...))` (the **same gated read**, not a second surface) — `None` for minors and non-members. **Never stored** on Group, never per-user history.

**Per-cohort outcome:**
- **ADULT, member:** roster list + derived count, **inside the roster panel only** (Fix 2: no header badge, no card, no "N members" string anywhere else).
- **ADULT, non-member / anon:** `None`.
- **CHILD / TEEN (member or not):** `None` for both, always, on every surface.

**What a minor sees in the group:** exactly the **activity feed** (a `community_activities`-style narrowed `visible_activities(viewer)` over `_area_place_q(group.area)` + type/category) **+ the moderated thread** (authored posts) **+ announcements**. **Per-message author bylines** are the only minor-visible identity — and they are **attribution, not enumeration**: a lurker who never posts is invisible; there is no count, no list, no who-else-here panel, no "X joined" notification, no presence/typing/online dots (already OUT of the thread design). A minor **can still report** a post author (per-message attribution is exactly enough for `file_report`), so safety is not weakened by roster-lessness.

**Regression tests (the kill-switch for the headline requirement):**
- Serializer-allowlist test (the `CommunitySerializer` pattern): `GroupSerializer` emits **no** `members`/`member_count`/`roster`/`participants`/`who_else` key for a CHILD or TEEN viewer; `group_roster(group, child)` is `None`.
- Web-context test: `group_detail` context has no `members`/`member_count` key for a minor.
- A test that an **ADULT non-member** also gets `None` (the count is member-gated, not just cohort-gated).

---

## 4. Count-toggle (folds into the roster rule)

There is **no per-user toggle**. Grep-confirmed there is no existing stored count-visibility setting; `connections_for` returns a list with no count; the only existing count is the unconditional `ActivitySerializer.get_member_count`. So:
- Visibility of roster+count is decided **entirely by the viewer's cohort** via `group_roster`/`group_member_count` — a structural rule, non-configurable. A configurable "show my count" would be a vanity/engagement lever and a misconfig hole for minors (inv.2/inv.3).
- **Connection-count folds in the same way:** `connections_for` keeps returning a list with no count for everyone; minors never see a connection count anywhere.
- **Pre-existing-leak fix (the third candidate's catch):** `ActivitySerializer.get_member_count` (social/serializers.py L56-60) is brought under the same helper so it returns `None` for CHILD/TEEN viewers — closing the only current count leak and making the rule platform-uniform. This is the one pre-existing surface the work refactors. *(Needs the same cohort context plumbed into `ActivitySerializer` — flag P2.)*

---

## 5. Creation / curation model (conservative default — inv.3)

- **CHILD or TEEN cohort group → STAFF-CURATED ONLY.** `is_staff_curated=True`. `create_group` raises `NotEligible` if a non-staff actor tries to create any minor-cohort group, and raises outright if `owner.cohort in (CHILD, TEEN)` (a minor can never own a group). Rationale: an openly-joinable, persistent, named space gathering minors by city+activity is a high-value grooming target — a human review gate on the *existence* of every minor space, matching how minor Communities are materialized by a vetted nightly job, not user-declared.
- **ADULT cohort group → self-creatable behind a flag** `GROUPS_ALLOW_USER_CREATED` (**default False at launch**; staff-curated everywhere first, matching the "minor onboarding disabled in prod by default" posture). When enabled: `cohort` pinned to `owner.cohort` (an adult can only ever create an ADULT group — cross-age structurally impossible), `can_create_activity`-level eligibility, creation rate-limit, `record_audit("group.created")`.
- **`GROUPS_USER_CREATION_COHORTS` hard-wall**: CHILD/TEEN can **never** be added even by misconfig (the `CONNECTIONS_ALLOWED_COHORTS` hard-wall pattern).
- Either way: owner auto-admitted as `GroupMembership(role=OWNER, state=MEMBER)` and `Thread(group=group)` created in the **same txn** (mirrors `create_activity`).
- Curation: staff can ARCHIVE (freeze) and `is_hidden` (REMOVE). **No hard delete** (audit/appeal retention).
- **Launch gating:** the **entire minor-group apparatus is dead code while `ALLOW_MINOR_ONBOARDING` is False in prod** (no minors exist to join). This is correct and intended — build it guarded, ship it dark, light it only when a real parental-responsibility trust anchor lands. *(Flag P3.)*

---

## 6. Guardian oversight (SIMPLIFIED — Fix 1)

**There is no standing group-thread observer seat.** Guardian oversight of a ward operates entirely through the **existing activity machinery**, unchanged:
- A ward discovers/joins actual **activities** via the group's feed → those flow through `visible_activities` → the existing `/wards/` manifest (place/time/type) shows them, and `mark_arrived` fans out arrival pings to **active** guardians (keyed on `GuardianRelationship.status=ACTIVE`) for a CHILD ward exactly as today.
- The group thread itself is **peer-only, same-cohort** — `can_read_thread`'s cohort wall (step 3) rejects an adult guardian with **no carve-out**, so the read gate stays the single fail-closed primitive. This *eliminates* the winner's most leak-prone net-new code: no `can_observe_group_thread`, no observer `GroupMembership` rows, no `_prune_orphaned_group_guardians` / `drop_group_guardians_for` fan-out that a single missed transition could leave an adult reading a children's space.
- **Legibility:** `guardianship_capabilities` (accounts L396-434) is left as-is for groups *or* gains a derived (no stored field) note that group **activities** appear in the existing manifest. No new private-contact channel is opened.

*Trade-off (flag P1):* a guardian cannot read their ward's *standing group chat*, only the ward's actual *meetup threads* + manifest. This is the conservative cut — it removes the durable adult read-window into a children's space the second candidate's reviewer flagged, at the cost of less continuous chat visibility. The product owner should confirm this is acceptable (it matches "guardians are read-only supervisors of in-person meetups," not "of all standing conversation").

---

## 7. Safety integrations (all reuse existing seams — no new safety primitives)

- **Cohort isolation:** `Group.cohort` pinned + immutable + re-checked at every read/write (§2) + on `apply_assurance` cohort change (below). A minor group contains only same-cohort minors; an adult literally cannot be in a minor group's roster.
- **Cohort-change eviction (the inv.3 requirement the winner flagged):** extend `apply_assurance` (accounts L40-43) — alongside the existing `remove_user_from_conversations`, add **`remove_user_from_groups(user, reason="cohort_changed")`** (direct copy of the messaging eviction): sets the user's MEMBER `GroupMembership` rows to REMOVED. Even a missed eviction fails closed because `can_read_thread`'s cohort re-check runs at read time.
- **Block:** `group_roster` excludes blocked pairs (even for adults); `post_announcement` fan-out excludes them; `can_read_thread`/`post_to_thread` keep the block-vs-owner check. A blocked pair never sees each other in a roster or each other's posts.
- **Report:** zero new code — `safety.Report`'s generic FK targets any model; `file_report(reporter, group_or_post, reason, detail)` works for free. GROOMING/CSAM/HARASSMENT reason codes already exist. A minor reports a post author with no roster needed.
- **Moderation:** `take_action` REMOVE sets `is_hidden=True` on the Group or its Post (works because both carry `is_hidden`); every group read surface excludes `is_hidden`. `_affected_user` resolves Group→`owner` / Post→`author` for the DSA Art.17 notice (FKs named correctly). SUSPEND/BAN deactivate the account, caught by `can_participate` at every group gate.
- **Audit:** every state-changing group service (create/join/leave/post/announce/moderate/archive) calls `record_audit(...)` **inside** its `@transaction.atomic` (it takes a row lock). New events: `group.created/joined/left/archived`. (No `observer_added` — there are no observers.)
- **Notifications:** one new **mutable** `Notification.Kind.GROUP_ANNOUNCEMENT` (owner/staff broadcast) through the single `notify()` choke point + a `WHY_REASONS` entry + a no-op `makemigrations notifications` (CLAUDE.md). Fan-out idempotent, blocked-pair-excluding, rate-limited. **Deliberately NO "X joined the group" / "people you may know" / member-growth notification** — that would be a roster-enumeration leak *and* engagement-maxxing (inv.2). MODERATION/SYSTEM stay non-mutable.

---

## 8. Relation to Communities (Groups sit ABOVE Communities — decoupled)

- A **Community stays a derived, per-cohort, count-free DISCOVERY LABEL** (its deactivate-not-delete nightly lifecycle + k-anon floor + serializer-allowlist test make it structurally non-joinable — confirmed by reading `generate_communities`; the second candidate's "make Community joinable" trap analysis is correct and rejected).
- A **Group is the explicitly-created, stored-membership SPACE** on the **same coordinate system** (reuses `communities.Area` + `taxonomy.ActivityType/Category`).
- **Linkage = read-time only, no new FK on either side** (decision: cleaner than the third candidate's `Community.linked_group` FK, because it keeps the nightly generator from ever touching Group and avoids the same-cohort-FK-constraint problem): when a published Community card renders for a viewer, query `visible_groups(viewer)` for a matching ACTIVE group in the same `(cohort, area, type-or-category)` cell; if one exists, show a "Join the standing group" link. Both ends are cohort-walled, so a child can never discover an adult group's existence via a community card. Surfaces the group **name** only — never membership/count.
- **Private-contact wall preserved identically:** co-membership in a Group is **NOT** a shared PEER activity, so it never satisfies `connections.shares_activity`/`can_connect`. **Test-pinned** (mirrors the community private-contact-wall test): being in the same Group does not make `can_connect(a,b)` true. Joining a group can never unlock an adult↔minor or any private DM.

---

## 9. Migration plan

1. `makemigrations social` — create `Group` + `GroupMembership` with the indexes/constraints in §1.
2. `makemigrations social` — ALTER `Thread`: add nullable `group` FK, make `activity` nullable, add XOR `CheckConstraint`. Existing rows backfill cleanly (no data migration). **Post unchanged.**
3. `makemigrations notifications` — `GROUP_ANNOUNCEMENT` Kind + `WHY_REASONS` (no-op schema change, required for the `makemigrations --check` gate).
4. `accounts` — no model change; `apply_assurance` gains the `remove_user_from_groups` call (code only).

Every chokepoint query is an index scan (`visible_groups` by cohort+status; `group_roster`/count by group+state; Community-linkage by cohort+area+type; thread read via `thread.group`). No new deps — Postgres-only, no ML (inv.6). CI gates kept green: `ruff`, `makemigrations --check`, `pytest` (new tests below), `docker build`, `pip-audit`.

---

## 10. Phased build order (smallest safe vertical slice first)

**Phase 0 — schema + read/write reuse, ADULT-only, staff-created, no roster yet.**
Models + Thread XOR migration; generalize `post_to_thread`/`can_read_thread`/consumer to `owner_object` (the two-line dispatcher + freeze helper); `create_group` (staff only), `join_group`/`leave_group`, `visible_groups`, `group_detail`. Web list/detail + `GroupViewSet`. Tests: single-creator (`Post.objects.create` still only in services.py), cohort-wall on read/write/join, gate-parity (group thread enforces the same union as an activity thread), `can_connect`-not-enabled-by-group. **This is a complete, shippable adult-only standing group with a thread and feed — no roster surface at all yet.**

**Phase 1 — roster/count rule + the count-leak fix.**
`group_roster`/`group_member_count` (ADULT-member-only, block-filtered); `GroupSerializer` + web template read only these. Bring `ActivitySerializer.get_member_count` under the same cohort helper. Serializer-allowlist + web-context regression tests (minor sees no roster/count; adult non-member sees none). This is where the **headline requirement** lands and is independently testable.

**Phase 2 — cohort-change eviction + minor-group curation (dark).**
`remove_user_from_groups` + the `apply_assurance` call; staff-curated minor-group creation path behind `ALLOW_MINOR_ONBOARDING` (ships dark in prod). Eviction + cohort-change tests.

**Phase 3 — announcements + Community linkage + polish.**
`post_announcement` generalization, `GROUP_ANNOUNCEMENT` notification, read-time "Join the standing group" link on Community cards, archive lifecycle, digest reuse.

Each phase is independently shippable and leaves every invariant intact.

---

## 11. Product decisions needed (flagged)

- **P1 — Guardian group-thread visibility.** Recommended: **no** standing adult observer in a minor group's *thread*; guardians see the ward's actual *meetup* threads + `/wards/` manifest + arrival pings only (removes the leakiest net-new code). Confirm this reduced visibility is acceptable, or accept the winner's heavier observer+prune machinery.
- **P2 — Does an ADULT see a member *count* at all,** or only the roster *list* (count = `len`)? Recommended: count only inside the already-opened roster panel, never as a standalone badge/card/string (avoids the inv.2 vanity surface). Also confirm plumbing cohort context into `ActivitySerializer` to fix the pre-existing activity-count leak.
- **P3 — Launch posture for minor groups.** Recommended: build guarded, ship dark behind `ALLOW_MINOR_ONBOARDING` (currently False in prod); light only with a real parental-responsibility trust anchor. Confirm minor groups are *not* in scope for the first launch.
- **P4 — One-live-group-per-coordinate** (the partial unique constraint) vs allowing multiple competing adult groups for the same city+type. Recommended: one (anti-spam-clone, matches Community's one-per-coordinate). Confirm.

**Relevant grounding files:** `/home/dobo/work/social_media_activities_app/apps/social/services.py` (L495-636 = the reused gates), `/home/dobo/work/social_media_activities_app/apps/social/serializers.py` (L56-60 = the pre-existing count leak to fix), `/home/dobo/work/social_media_activities_app/apps/communities/services.py` (the coordinate system + sole-read-path pattern), `/home/dobo/work/social_media_activities_app/apps/messaging/services.py` (L485-499 = `remove_user_from_conversations` to copy for `remove_user_from_groups`), `/home/dobo/work/social_media_activities_app/apps/accounts/services.py` (L28-43 = `apply_assurance` eviction call-site), `/home/dobo/work/social_media_activities_app/apps/safety/services.py` (L144-220 = `_affected_user` + `take_action`, confirming Group/Post plug in for free).

---

## Adversarial child-safety critique (must-fix list before/within build)

I now have full grounding on every seam the design reuses. I've confirmed the load-bearing facts:

1. `Thread.activity` is a **OneToOneField** with `related_name="thread"` — the design's `Group.thread` FK proposal is wrong-shaped.
2. `thread_digest` returns `member_count` and `attendance_summary` (`going`/`total`) — a count surface the design imports for minors in Phase 3.
3. `post_announcement` reads `activity.owner_id` / `activity.title` / `activity.thread` directly — needs duck-typing.
4. `broadcast_post` keys on `chat_{post.thread_id}` — works for groups.
5. `shares_activity` queries the `Membership` (Activity) table only — a `GroupMembership` cannot satisfy `can_connect` (good).
6. `with_counts` annotates `member_n`, which `ActivitySerializer.get_member_count` leaks unconditionally.
7. `notify()` has no cohort awareness — fan-out targeting is the caller's job.

Here is the adversarial audit.

---

# HOSTILE CHILD-SAFETY AUDIT — Public Groups design

Verdict up front: **GO-WITH-CHANGES.** The reuse-thread spine is sound and most claims check out against the code. But I found **4 critical** and several high/medium holes where the design either imports a count/enumeration surface it forgot it was importing, mis-shapes a model in a way that breaks the read gate, or leaves a minor-discovery path under-gated. None are unfixable; all must-fix items are concrete and small.

Legend: **C**=critical (ships a child-safety or cross-cohort breach), **H**=high, **M**=medium, **L**=low/hardening.

---

## (a) Ways a minor can see a roster / count / who-is-here

### C1 — `thread_digest` leaks `member_count` AND `going/total` to minors. The design re-uses it verbatim in Phase 3.
`social/services.py:1078` — `thread_digest` returns `"member_count": current_members(activity).count()` and `"going"/"total"` from `attendance_summary`. The design §10 Phase 3 says "digest reuse" and §3 says a minor's group surface is "the activity feed + the moderated thread + announcements" — but the **catch-up digest is part of the thread surface** and it hands a minor a live member count and a go/no-go tally. This is the *exact* headline-requirement breach (a minor seeing a count), smuggled in through a reused helper the design never re-examined.

- **Severity: C.**
- **Fix:** `thread_digest` must take a `viewer` and route its `member_count`/`going`/`total` through `group_member_count(...)`/the same cohort helper — returning `None` for CHILD/TEEN (and for any non-member). Add a regression test: `thread_digest(group_thread, child)["member_count"] is None`. This is **not** Phase-3 polish; it is part of the Phase-1 headline rule and must be pinned by the serializer-allowlist test, because the digest dict is a serialized surface too.

### C2 — Per-message author bylines + depth-1 quote-reply ARE a who-is-here enumeration for minors, and the design explicitly blesses them.
§3 calls bylines "attribution, not enumeration… a lurker who never posts is invisible." That is only half true. The reused thread carries **`reply_to` quote snippets derived live from the parent author** (`reply_snippet`), **@mention rendering** (`resolve_mentions` over `voting_members`), and **reactions**. A minor reading the group thread can enumerate every *active* member by name, can see "X replied to Y," and — via `_ping_mentioned`/`resolve_mentions` — the **@mention autocomplete/resolution surface lists members to mention**. That is a who-is-here panel by another name, available to a minor, for the *standing* (persistent, city-wide) group — a far larger and more durable namespace than a single 8-person meetup. The grooming-relevant fact ("who are the other kids in Cluj Football and what are their usernames") is fully recoverable.

- **Severity: C** (this is the core of the headline requirement and the design waves it through).
- **Fix:** This is a genuine product tension, not a code bug — bylines are needed to *report* an author. Resolve it explicitly:
  - **Mentions: disable `@mention` resolution on group threads for minors entirely** (pass an empty/`None` mention-candidate set when `owner_object` is a Group, or gate `_ping_mentioned`/`resolve_mentions` to Activity threads only). The autocomplete-style member-name surface must not exist in a minor group.
  - **Bylines: keep** (needed for `file_report`) but flag P5 (new): the product owner must accept that an active-poster namespace is visible to minors in a standing group. The honest mitigation is that the **group thread for a minor cohort should be heavily rate-limited / announcement-dominant** (closer to a feed than a chat) so the enumerable active set stays tiny — or minor group *threads* are owner/staff-broadcast-only (read-only feed for minors, no peer posting), which collapses the enumeration surface to zero. **Recommend: minor group threads are announcement-only (no peer posting); peer chat happens only in the bounded per-meetup activity thread.** This also matches the §6 guardian-oversight cut (guardians see meetup threads, not standing chat) and makes that cut consistent.

### M3 — `open_positions`/capacity on a group? If a Group ever gets a capacity, `open_positions` leaks a derived count.
The model table in §1 has no `capacity`, good — but `post_to_thread`'s reused serializer path and any `GroupSerializer` that copies `ActivitySerializer` fields would carry `open_positions`/`member_count` field names. The XOR-shared serializer must be a *separate* `GroupSerializer` with an explicit allowlist, never a subclass of `ActivitySerializer`.
- **Severity: M.** **Fix:** assert in the allowlist test that `GroupSerializer.Meta.fields` contains none of `{member_count, open_positions, member_n, participant_n, capacity}`.

---

## (b) Adult↔minor contact / cross-cohort leakage

### C4 — The `Thread.group` FK is mis-shaped: it must be OneToOne, and the duck-typed `owner_object.thread` accessor collides with the existing `related_name="thread"`.
`Thread.activity` is `OneToOneField(..., related_name="thread")` (`models.py:200`), so `activity.thread` is the reverse OneToOne. The design proposes `Group.thread = FK(... related_name="thread")` and a property `owner_object` returning `self.activity or self.group`. Two problems:
- **A plain FK lets multiple Threads point at one Group** → two threads for one group → the read gate (`group.memberships…`) is fine but `post_to_thread` reads `owner_object.thread` (singular). With a FK reverse manager, `group.thread` is a **RelatedManager, not a Thread** → `group.thread.posts` would raise / silently target the wrong object. The duck-type bridge breaks. **It must be `OneToOneField`** with `related_name="thread"` so `group.thread` is the object, mirroring Activity exactly.
- The design's `Thread.owner_object` property (`return self.activity or self.group`) is the bridge *from a Thread to its owner*, but `post_to_thread`/`can_read_thread` are called with the **owner object** (`activity`), and inside they read `owner_object.thread`. So you need the reverse-accessor name to be `thread` on **both** Activity and Group. With two OneToOnes both named `related_name="thread"` that's fine (different models). Confirm the migration sets it `OneToOneField`.
- **Severity: C** (a broken read gate is a cross-cohort leak: if `owner_object.thread` resolves wrong, `can_read_thread`'s `.memberships` check could run against the wrong object). **Fix:** `Thread.group = OneToOneField(Group, on_delete=CASCADE, null=True, related_name="thread")` + the XOR `CheckConstraint`. Add a test that `group.thread` returns a `Thread` instance.

### H5 — The cohort-wall in `can_read_thread` reads `activity.cohort`; a Group must expose `.cohort` AND `.is_hidden` AND `.owner_id` with identical semantics — verify `owner_id`, not just `owner`.
`can_read_thread` does `user.id != activity.owner_id and is_blocked(user, activity.owner)` (`:634`). Group has `owner = FK(...)` → `owner_id` exists, good. But `post_to_thread` also calls `current_members(activity)` → the design's `thread_members(owner_obj)` dispatcher. **Risk:** if the dispatcher is a `getattr`/`isinstance` branch and a future third thread-owner type is added, the fail-open default must be **deny** (raise), never "treat as activity." 
- **Severity: H.** **Fix:** `thread_members` and `is_thread_frozen` must `isinstance`-dispatch with an explicit `else: raise TypeError`/`NotAMember` (fail-closed), and a test that an unknown owner type cannot post.

### H6 — Cohort-change eviction (`remove_user_from_groups`) is necessary but the messaging template it copies prunes *guardian observers* — the design deleted observers, so the copy must NOT silently no-op the eviction itself.
The design copies `remove_user_from_conversations` (`messaging/services.py:486`), which both demotes the user **and** calls `_prune_orphaned_guardians`. The design says "no guardian prune needed." Fine — but the real risk is the **inverse**: `apply_assurance` (`accounts/services.py:40`) only fires eviction `if user.cohort != old_cohort and old_cohort != UNASSIGNED`. A user who ages CHILD→TEEN→ADULT triggers it; good. But **a consent *revocation* that flips `can_participate` to False does NOT change cohort** → `apply_assurance` eviction never fires → the user stays a `GroupMembership(state=MEMBER)` of their cohort group. `can_read_thread` step 4 (`can_participate`) catches reads/writes, so no thread leak — **but `group_roster` for an adult does NOT re-check `can_participate`**:

```
return list(group.memberships.filter(state=MEMBER)...)   # no can_participate filter
```

So a **consent-revoked / suspended member still appears in the adult roster** (and counts toward the count). For an adult group that's a stale-data bug; but combined with C7 below it's worse.
- **Severity: H.** **Fix:** `group_roster` must filter members through `can_participate` (or at minimum exclude `user.is_active=False`), exactly as the thread gate does. And `remove_user_from_groups` must ALSO be called from the consent-revocation path (`messaging` already wires `remove_user_from_conversations` there — find that call site and add the group eviction beside it), not only from `apply_assurance`'s cohort-change branch.

### C7 — `group_roster` excludes blocked pairs but NOT cross-cohort impossibility for the *displayed* users — and an aged-out member can linger in a minor roster… except minors never see a roster, so re-aim: an **adult who aged-IN from TEEN** can momentarily be a member of a TEEN group.
Walk it: a 17-y-o TEEN joins "Cluj Football TEEN." They turn 18 → re-verify → `apply_assurance` flips cohort TEEN→ADULT → eviction fires → `GroupMembership` set REMOVED. Good *if* eviction is wired. **But if the eviction sweep is missed or runs late** (the design itself says "even a missed eviction fails closed because `can_read_thread` re-checks"), the **roster read does NOT have that re-check**: `group_roster` filters only on `state=MEMBER` and `blocked_user_ids`, **not on `viewer.cohort == member.cohort`**. The roster is gated on the *viewer's* cohort, but it never re-verifies each *listed member's* current cohort. So a stale ADULT row (aged-out from TEEN but eviction missed) would be listed in a TEEN roster — except TEENs never see rosters, so the only viewer is… there is no adult in a TEEN group to view it. Net: contained **only** because minor rosters return `None` for everyone. The latent bug is real and would bite the instant the roster rule is ever relaxed.
- **Severity: C if the roster rule is ever softened; H today** (defence-in-depth gap). **Fix:** `group_roster` must filter listed members to `member.user.cohort == group.cohort` (a listed member whose cohort drifted is excluded), so the roster can never show an off-cohort user even with a missed eviction. One `.filter(user__cohort=group.cohort)` line — cheap, fail-closed, and it makes the roster gate symmetric with `can_read_thread`.

### M8 — Community→Group read-time linkage (§8) is cohort-walled on *both* ends, but the join is `(cohort, area, type-or-category)` matched at render — confirm the match query itself filters `cohort=viewer.cohort`, or a child community card could surface an adult group's *name*.
The design says "both ends are cohort-walled," and `visible_groups(viewer)` does filter `viewer.cohort`. The risk is an implementer writing the linkage as `Group.objects.filter(area=…, activity_type=…, status=ACTIVE)` (forgetting cohort) because the community is already cohort-correct. Since a community and a group of the *same* coordinate but *different* cohort can coexist (the unique constraint is per-cohort), a missing `cohort=` in the link query leaks the adult group's existence + name onto a child's community card.
- **Severity: M** (existence+name leak, no content). **Fix:** the linkage MUST source from `visible_groups(viewer)` (never raw `Group.objects`), pinned by a test: a CHILD viewing a child community whose coordinate also has an ADULT group sees no "standing group" link.

---

## (c) Predator creates / uses a group to target minors

### H9 — `is_staff_curated` is a boolean, not a gate. The actual gate is "non-staff cannot create a minor-cohort group" — but a STAFF account is the single point of failure, and there is no second-person review.
§5: minor-cohort group creation is staff-only. Good and conservative. But a Group is *persistent, openly-joinable, city+activity-scoped* — exactly the high-value target the design itself names. A single compromised/rogue staff account can mint a "Cluj-Napoca Under-13 Football" group and is auto-admitted as `OWNER` with thread-posting rights. Communities (the comparison the design leans on) are materialized by an **unattended nightly job with a k-anon floor** — *no human can will one into existence*. A staff-created group is strictly weaker.
- **Severity: H.** **Fix:** (1) minor-cohort group creation should require a **second-staff confirmation** (mirror the N-confirmers pattern from `confirm_place`, or a `staff_curated` review flag that a *different* staff member must flip to publish). (2) The creating staff member should **not** auto-join as a posting OWNER of a *minor* group — set `role=OWNER` for ownership/audit, but a minor group's thread should be **announcement-only** (ties to C2's recommendation), so no adult is peer-posting into a children's space even as "owner." (3) `record_audit("group.created")` must capture the staff actor for every minor group (the design includes this — keep it, and make minor-group creation un-mutable in the audit review queue).

### H10 — `owner = PROTECT` means a minor group's owner account can never be cleanly deleted/erased; GDPR erasure of a (rogue or resigned) staff owner is blocked.
§1 chose `PROTECT` "so a staff-curated minor group must not vanish if the staff account is deleted; reassign on offboarding." But the app has a **GDPR erasure** path (`ops`). `PROTECT` means erasing the owner **throws** until every owned group is reassigned. If offboarding reassignment is ever missed, erasure fails closed (acceptable) — but a *predator-staffer* whose account you urgently need to erase now blocks on manual reassignment of every group they own, during an incident.
- **Severity: H** (incident-response + GDPR friction). **Fix:** keep `PROTECT` but add an explicit `reassign_group_owner(group, new_owner)` service + an **offboarding/erasure hook** that auto-reassigns a departing staff owner's minor groups to a system/curation service account *before* erasure, with an audit row. Document that erasure of a group owner requires reassignment first (and provide the tool to do it atomically).

### M11 — No per-group join rate-limit distinct from posting; a predator can mass-join every group in a city to maximize reach.
§2 `join_group` has "rate-limit," good — but confirm it's a *distinct* action key (`"group_join"`) with a low cap, not sharing the `thread_post` bucket. Mass-joining is the reconnaissance step. Also: joining is *idempotent and silent* (no "X joined" notification — correct), but a predator joining 50 adult groups is itself a signal worth a soft moderation flag.
- **Severity: M.** **Fix:** dedicated `allow_action(user, "group_join", limit=…)`; optionally an audit-derived (not stored-per-user) anomaly the moderation queue can surface. Do **not** add a stored join-velocity counter (that's a behavioural rollup, inv.2).

---

## (d) Reintroduced vanity metric / engagement-maxxing

### C12 — The design's own headline fix (the adult roster `len()` count) IS a vanity surface unless the "panel-only, never a string" rule is enforced structurally — and §4's plan to "fix" `ActivitySerializer.get_member_count` actually *widens* a count to adults it currently shows.
Two parts:
- **(a)** The pre-existing `ActivitySerializer.get_member_count` (`serializers.py:56`) leaks a count to **every** cohort. The design fixes this to return `None` for CHILD/TEEN — good, that's a net safety improvement. **But** the design keeps the count for adults "to make the rule uniform." That preserves a per-activity member count for adults on the **public activity list** — which IS a "N people going" vanity/social-proof number on a discovery surface (inv.2: "no vanity metrics"). The current code's sin (count to everyone) is being *narrowed*, but the adult count on the activity feed remains a discovery-surface vanity metric. The honest move is to **remove `member_count` from `ActivitySerializer` entirely** (keep `open_positions`, which is functional capacity info, not social proof) — not to keep it adult-only.
- **(b)** For groups, §3 says the count appears "only inside the roster panel an adult already opened." There is **no structural enforcement** of "panel-only" — a `member_count` key in `GroupSerializer` JSON is readable by any client regardless of UI. "Only inside the panel" is a template convention, not a gate. An adult could read the count via the raw API on a group they're a member of even without opening the panel; worse, if `group_member_count` is computed from a queryset, an N-members number is one field away from becoming a card.
- **Severity: C for the principle** (this is the invariant-2 line the whole feature is supposed to respect, and the design's "fix" softens it). **Fix:** 
  - Drop `member_count` from `ActivitySerializer` outright (don't adult-gate it — *remove* it). Keep `open_positions`.
  - For groups: do **not** emit any numeric count field in `GroupSerializer` at all. The adult roster panel renders a **list** (names), server-side; if a count is shown it's `len()` of the already-rendered list in the template, never a serialized scalar the API exposes. The allowlist test forbids `member_count`/any `*_count`/`*_n` key in `GroupSerializer` for **all** cohorts, adults included. (This also resolves P2 cleanly: adults see the *roster list*, full stop; "count" is purely incidental visual `len()` of what's already on screen, never an independent number.)

### M13 — Reactions on a standing group thread become a low-grade engagement signal at scale.
Reused `toggle_reaction` is "anonymous, countless, no who-list" (`:802`) — genuinely clean. But on a *persistent city-wide* thread, even countless reactions create a feedback loop the per-meetup thread doesn't. Low risk, flagging for completeness.
- **Severity: L.** **Fix:** none required; the countless design holds. Confirm `post_reaction_emojis` stays countless on group threads (it will — same code path).

---

## (e) Loss of the hardened `post_to_thread` / `can_read_thread` gates

### H14 — Generalizing `current_members(activity)` → `thread_members(owner_obj)` and the CANCELLED check changes the body of `post_to_thread`; the "two tiny generalizations" still touch the single most safety-critical function and can drift.
The design is right that the gate *order* is preserved. The risk is the **single-creator test** and **gate-parity test**: after editing `post_to_thread`, the test "Post is only created in `post_to_thread`/`post_announcement`" must still pass, and a **new** "group thread enforces the identical union gate as an activity thread" parity test is needed. The design lists it in Phase 0 — keep it as a **hard gate**, and add: the GUARDIAN-role check (step 2) is described as "vestigial for groups." Vestigial code that *looks* load-bearing is a maintenance hazard — but **removing it would mean a future GroupMembership.role=GUARDIAN silently gains posting rights**. Keep the check; do **not** call it vestigial in the code comment — instead assert `GroupMembership` has no GUARDIAN role via a test, so the check staying is belt-and-suspenders, not dead code.
- **Severity: H** (gate integrity). **Fix:** (1) keep all 8 gate steps unconditionally; (2) add the gate-parity test as a merge blocker; (3) add a test that `GroupMembership.Role` has no `GUARDIAN` member, so step-2's group-side meaning is pinned.

### H15 — The WebSocket consumer hard-codes `select_related("activity")` and `thread.activity` in THREE places — the design says "needs only `select_related('activity','group')`" but the body still passes `thread.activity` to the gate.
`consumers.py:69,73,83,84,89` all reference `self.thread.activity` and `thread.activity`. The design's one-line note undersells it: **every** `thread.activity` in the consumer must become `thread.owner_object` (the new property), and `_persist` calls `post_to_thread_realtime(self.user, self.thread.activity, …)` → must pass `owner_object`. Miss any one and a group socket either 500s (fails closed, ok) or — worse — if `thread.activity` is `None` for a group thread, `can_read_thread(user, None)` returns `False` at `getattr(None,'is_hidden')`→False then `None.cohort`→**AttributeError**, tearing down the socket (fail-closed) but spamming errors. The real danger: an implementer "fixes" the crash by defaulting `owner_object = thread.activity or thread.group` in only *some* of the 4 sites.
- **Severity: H.** **Fix:** add `Thread.owner_object` property (already in §1) and replace **all** `thread.activity` references in the consumer + `broadcast_post` keying is already `post.thread_id` (safe). Add a consumer test connecting to a **group** thread (member ok, non-member 4403, cross-cohort 4403, aged-out per-delivery 4403).

### M16 — `reply_snippet` / `_validate_reply_to` reference `activity.thread.id`; on a group thread `_validate_reply_to(owner_obj, reply_to)` compares `parent.thread_id != activity.thread.id`.
`_validate_reply_to` (`:586`) takes `activity` and reads `activity.thread.id`. Under the dispatcher it receives `owner_obj`; `owner_obj.thread.id` resolves for a Group **iff** the OneToOne fix (C4) is applied. With a plain FK it breaks.
- **Severity: M** (subsumed by C4). **Fix:** covered by C4; add a reply-in-group-thread test.

---

## (f) Migration / under-gated read path

### H17 — Making `Thread.activity` nullable + adding the XOR constraint is a 2-step migration on a OneToOne; the existing OneToOne `unique` + the new XOR must not deadlock backfill, and `Thread.__str__` will crash.
`Thread.__str__` (`models.py:203`) does `return f"thread({self.activity})"` — for a group thread `self.activity` is `None` → renders `thread(None)` (harmless) but any admin/log that expects the activity crashes elsewhere. More importantly: making a OneToOne nullable is fine, but the **XOR `CheckConstraint`** must be added in the **same** migration as the column or a brief window allows a row with both null. Existing rows all have `activity` set → backfill clean (design is right). 
- **Severity: H** (migration correctness + a latent `__str__`/serializer crash that could fail-OPEN a read if an exception is swallowed). **Fix:** (1) `__str__` → `f"thread({self.owner_object})"`; (2) add the nullable change + XOR constraint in one migration; (3) the OneToOne on `group` gives you the "exactly one thread per group" half; the XOR gives "exactly one owner per thread" — keep both.

### C18 — `visible_groups` is `IsAuthenticated`, never `AllowAny` (design §2) — but the design does NOT state that the **DRF `GroupViewSet` queryset itself** is sourced from `visible_groups(request.user)`. A default `ModelViewSet` `queryset = Group.objects.all()` + `get_object()` would bypass the cohort wall on detail/retrieve.
This is the classic under-gated read path. The web `group_detail` is described as "404s a cross-cohort/hidden group," but a DRF `GroupViewSet` with a naive `queryset = Group.objects.all()` exposes `/api/.../groups/{id}/` for **any** group to **any** authenticated user — a CHILD could `GET` an ADULT group's title/description by id-guessing. The design *says* "every group-entity surface sources from `visible_groups`," but a `ModelViewSet`'s `get_queryset` is the one place this is forgotten 90% of the time.
- **Severity: C** (cross-cohort existence+content leak by id enumeration). **Fix:** `GroupViewSet.get_queryset(self)` returns `visible_groups(self.request.user)` — and a **test that a CHILD `GET /groups/{adult_group_id}/` is 404**, plus the same for `retrieve`, `list`, and any `@action`. Never set `queryset = Group.objects.all()` on the viewset class (a class-level `queryset` is used by the router for basename and can leak via `get_object` if `get_queryset` is overridden incompletely).

### M19 — `join_group` gate omits an explicit `not is_hidden` check; it relies on "in `visible_groups`" — but `visible_groups` filters `is_hidden=False`, so a hidden group can't be joined. Confirm, don't assume.
The gate is `in visible_groups + cohort + can_participate + status==ACTIVE + not blocked + rate-limit`. `visible_groups` excludes `is_hidden`, so a moderator-removed group is unjoinable. Good — but make `join_group` re-fetch the group **through `visible_groups(user)`** (not accept a passed-in `group` object directly), or a caller passing a hidden Group object bypasses the `is_hidden` filter.
- **Severity: M.** **Fix:** `join_group(user, group_id)` re-resolves via `visible_groups(user).filter(pk=group_id)` → 404/raise if absent, never trust a passed object.

---

## Cross-cutting confirmations (things the design got RIGHT — verified against code)

- **`can_connect` cannot be satisfied by group co-membership.** Confirmed: `shares_activity` (`connections/services.py:64`) queries `Membership` (the Activity table) via `_peer_activity_ids`; a `GroupMembership` row is invisible to it. The private-contact wall holds. ✓ (Keep the test-pin §8 promises.)
- **`broadcast_post` keys on `chat_{post.thread_id}`** (`:785`), not activity — live delivery works for group threads untouched. ✓
- **`notify()` non-mutable carve-out** (MODERATION/SYSTEM) is checked before any lookup (`:36`); a new mutable `GROUP_ANNOUNCEMENT` folds in cleanly, and the `makemigrations notifications` no-op is correctly required. ✓
- **`take_action` REMOVE / `_affected_user`** resolve via `is_hidden` + `owner`/`author` attrs (`:155`, `:218`) — Group (named `owner`) and Post (named `author`) plug in for free. ✓
- **`apply_assurance` eviction call-site** (`accounts/services.py:40`) is the right seam, and the cohort-change-only trigger is real — which is exactly why H6 (consent-revocation path) must be wired separately.

---

## FINAL VERDICT: **GO-WITH-CHANGES**

The architecture (reuse the one hardened thread, XOR the Thread owner, derive-not-store the roster, cohort-wall every gate) is correct and the design's instinct to *delete* the guardian observer carve-out is the right call — it removes the leakiest net-new code. But the design under-counts how much "count/enumeration" it imports through reused helpers, mis-shapes the Thread FK, and leaves the two most-forgotten under-gated read paths (DRF queryset, digest) unaddressed.

### MUST-FIX before any code merges (blockers):

1. **C1 — `thread_digest` must take a `viewer` and return `member_count`/`going`/`total` as `None` for minors + non-members.** It is a serialized count surface, in Phase 1, not Phase 3. Pin with a test.
2. **C2 — Minor group threads must not expose a member-enumeration surface.** Recommended: **minor-cohort group threads are announcement/owner-broadcast only (no peer posting), and `@mention` resolution is disabled for group threads.** Bylines stay only to enable reporting. Flag **P5** for product sign-off.
3. **C4 — `Thread.group` must be `OneToOneField` (not FK), `related_name="thread"`,** with the XOR `CheckConstraint` in the same migration. Test `group.thread` returns a Thread.
4. **C12 — No serialized member-count anywhere.** Remove `member_count` from `ActivitySerializer` entirely (keep `open_positions`); `GroupSerializer` emits no `*_count`/`*_n` field for **any** cohort. Roster is a server-rendered list; any count is template `len()`, never API JSON. Allowlist test for all cohorts.
5. **C18 — `GroupViewSet.get_queryset` returns `visible_groups(request.user)`;** never a class-level `Group.objects.all()`. Test CHILD `GET /groups/{adult_id}/` → 404 (list, retrieve, every action).
6. **C7/H6 — `group_roster` must filter listed members by `user__cohort == group.cohort` AND `can_participate` (or `is_active`),** so a missed eviction or consent revocation never shows an off-cohort/ineligible user; and wire `remove_user_from_groups` into the **consent-revocation** path, not only `apply_assurance`'s cohort-change branch.

### SHOULD-FIX before launch (high):

7. **H5/H14/H15 — fail-closed dispatch** (`thread_members`/`is_thread_frozen` `isinstance` with `else: raise`), keep all 8 `post_to_thread` gate steps, replace **all** `thread.activity` in the consumer with `thread.owner_object`, and add the **gate-parity** + **GroupMembership-has-no-GUARDIAN-role** + **group-socket 4403** tests as merge blockers.
8. **H9/H10 — minor-group creation needs second-staff review** (not a single staff click), the creating staff should not peer-post into a minor group, and add `reassign_group_owner` + an erasure/offboarding hook for the `PROTECT` owner.
9. **M8/M19 — Community→Group linkage and `join_group` must both source from `visible_groups(viewer)`,** never raw `Group.objects`. Tests for the cross-cohort linkage leak and hidden-group join.

### Product decisions to escalate:
- **P1** (guardian standing-thread visibility — design's recommended cut is sound; **becomes moot/clean if C2 makes minor group threads announcement-only**).
- **P2** — resolved by C12: adults see a **roster list**, no standalone count number anywhere (API or UI).
- **P3** — ship minor groups dark behind `ALLOW_MINOR_ONBOARDING`. Agree.
- **P5 (new)** — accept that *active posters'* bylines are visible to minors in a standing group (mitigated to near-zero if C2 makes minor threads announcement-only). Needs explicit sign-off.

Net: the spine ships. The six blockers are each a few lines + a test. Fix them and it's a **GO**.
