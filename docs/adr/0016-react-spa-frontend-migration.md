# ADR-0016: React SPA frontend migration ("Aurora Social" redesign)

Date: 2026-07-06
Status: accepted

## Decision

Migrate the web UI to a **React (Vite, TypeScript) SPA** using the shared **@roedu/ui**
design system with this app's bespoke **"Aurora Social"** theme (vibrant indigo/teal,
mobile-first, dark-mode native), in **four phases** on `claude/redesign-social-p*`
branches. Paul chose **full SPA** over a Django/React hybrid end-state: all screens —
including public SEO pages — eventually render in React.

Constraints that hold throughout:

- **No Node/SSR server** (fleet decision, roedu-ui ARCHITECTURE.md). Django serves the
  built bundle: `frontend/` → `vite build` → hashed assets + manifest in
  `static/frontend/` → collectstatic/WhiteNoise; the `spa_entry` template tag
  (apps/web/templatetags/spa_assets.py) injects them with the request's CSP nonce.
- **SEO/indexability** (public pages, from Phase 2): Django keeps serving the SPA
  *document* per route with the existing SEO head (canonical, meta_robots, OpenGraph,
  JSON-LD) plus a server-rendered content snapshot for crawlers/noscript that React
  replaces on hydration. Indexable URLs, titles, and structured data do not regress.
- **Strict CSP is a hard gate** (ADR-0014): no inline styles/scripts from React. @roedu/ui
  v0.3.0 is CSP-safe (its `useCspSafeStyle` hook writes via the CSSOM); app components
  must use the same mechanism.
- **Sensitive subsystems migrate LAST, restyle-only in this program**: E2EE messaging
  (crypto/IndexedDB/transport untouched), Leaflet maps, 3d-force-graph, donations,
  safety/report/legal flows. Their React migration is a separate future program.
- **One theme, two renderers during migration**: token VALUES are defined in
  `frontend/src/theme.ts` and mirrored into the legacy tokens in `static/css/base.css`
  (light, dark, contrast blocks). Django-rendered and React screens stay visually
  identical until the legacy layer retires.
- **@roedu/ui consumption**: committed tarball `frontend/vendor/roedu-ui-<ver>.tgz`
  (same pattern as cat_de_roman_esti) — no registry auth in CI/Docker.
- The scoped `ROEDU_API_KEY` stays server-side; the SPA only calls this app's own
  same-origin APIs with session auth + CSRF.

## Phases

1. **Foundation (this ADR's slice)**: CSP-safe @roedu/ui 0.3.0; Vite scaffold + Docker
   frontend build stage; Aurora tokens in both layers; Bricolage Grotesque display font
   (vendored, OFL); mobile bottom tab bar (server-rendered, JS-free); `.btn--light`
   defined; vendored Leaflet assets (CSP unpkg allowance kept — removing it is a
   follow-up hardening slice with test updates); inline-style cleanup batch 1;
   DEBUG-only `/app/preview/` pipeline proof.
2. **High-traffic screens** in React (P2a shipped): authenticated home, browse deck/list,
   organize — one Django view per screen serving BOTH the SPA shell (web/spa.html,
   `nonced_json_script` bootstrap island) and `?_data=1` JSON for client-side navigation
   (apps/web/views_spa.py builders reuse the legacy views' service results; display
   strings ship pre-translated/pre-formatted — no new msgids, no client i18n).
   Gated by the **`SOCIAL_REACT_UI` kill switch** (default OFF; dev ON): the legacy
   templates keep rendering — and the test suite keeps asserting them — until the flag
   flips per environment (same pattern as ro_teacher's `RO_TEACHER_REACT_UI`).
   **`/my-meetups/` deliberately stays server-rendered**: it is the F38 offline-saved
   safety page (meeting points readable with no signal); its delivery must not depend on
   SPA hydration. activity_detail.html split into five partials (header/safety/organizer/
   membership/thread — behavior-identical).
   **P2b (shipped)**: public SEO screens — events, places list, things-to-do
   index/city/detail — as React screens with full head parity (description, robots incl.
   noindex-on-filtered, JSON-LD, RSS alternates, breadcrumb data) plus a server-rendered
   crawler/noscript snapshot inside `#root` (`web/snapshots/*`, replaced on hydration).
   Public `?_data=1` payloads omit the CSRF token so `cache_public` keeps applying.
   **The activity_detail React shell is deferred to the sensitive-subsystems track**: it
   embeds the live thread + F33 pre-send safety nudge (script-order-sensitive) and ~25
   POST flows — exactly the "complex, migrate last" bucket.
3. **Profile/settings/communities (shipped)**: /you, /settings, profile, interests,
   topics, access, notifications + preferences, connections, saved searches, communities
   + community detail as React screens. Every P3 mutation is a classic POST that
   redirects with a flash (recon-verified), so screens render plain forms with the
   payload CSRF — no client mutation state. Account/inbox nav renders from one source
   (account_nav/you_tabs/inbox_tabs). Child-safety screens stay Django + restyle-only
   (class-level; wards nested-card hierarchy fixed). Actions whose pk rides in the URL
   path ship as `{pk}` template strings, not reverse() (which would NoReverseMatch).
4. **Sensitive subsystems restyled in place** (messaging, maps, graph, donations, legal).

## Why

The old UI is server-rendered with a real token system but mixed execution (142 inline
styles, undefined button variants, a 531-line activity detail template) and no build
pipeline. The fleet standardized on React + @roedu/ui with per-app themes (roedu-ui
ARCHITECTURE.md); cat_de_roman_esti shipped first and proved the pattern. Full recon
(codex-fleet, 2026-07-06) and the phased plan live in the redesign session log; the
product invariants (activity-first, no engagement patterns, child safety, WCAG AA,
Romanian copy via Django i18n) bind every phase.

## Consequences

- CI's `docker build .` now also builds the frontend (node stage) — a frontend compile
  error fails the image build by design.
- Local backend-only dev works without node: pages fall back gracefully (`spa_entry`
  renders a comment; only SPA routes need a built bundle).
- Until the legacy layer retires, color changes must be made in BOTH
  `frontend/src/theme.ts` and `static/css/base.css` (documented in both files).
- `style-src` stays free of `'unsafe-inline'`; anything that needs a dynamic style in
  React goes through `useCspSafeStyle`.
