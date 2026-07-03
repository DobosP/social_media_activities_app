# ADR-0007: Mobile photo activity cards

Date: 2026-07-03
Status: accepted

## Decision
Allow one contextual cover photo per activity on discovery cards, including mobile card decks and public adult activity cards, while keeping photos tied to activity visibility. Do not add short video, public galleries, public user photo feeds, vanity counts, like/pass tracking, swipe telemetry, or engagement ranking. Cover upload and serving must reuse the existing media safety pipeline: scanner/legal gates fail closed, EXIF/GPS metadata is stripped, size/dimension caps apply, blobs use the storage abstraction, and audit events are recorded.

## Context / why
Paul explicitly chose to change the old absolute text-first invariant so the mobile activity deck can be photo-heavy without becoming engagement social media. The cover is contextual to the meetup, not a user feed or gallery: authenticated viewers may see it only when the owning activity passes `visible_activities(user)`, and anonymous viewers may see it only when the owning activity passes `public_activities()` (adult-only public listing). Swipe/throw gestures remain navigation over a bounded result set; they never POST like/pass/swipe events or feed an engagement ranker.

Why not require covers at activity creation in this slice: existing rows, tests, fixtures, and imports would need migration/backfill and a separate product gate. Cards therefore always expose a `visual`: an activity cover when available and viewable, otherwise a generated accent fallback.

## Consequences
Mobile and web discovery cards can lead with a real activity cover photo while the app still forbids short video, public galleries, public user photo feeds, likes/pass tracking, vanity metrics, and engagement-maxxing. Cover visibility is revoked automatically when the activity is hidden, blocked by cohort/blocking gates, cancelled/past for public discovery, or no longer public. A future "cover required at creation" policy needs its own product gate, backfill/migration plan, and tests.
