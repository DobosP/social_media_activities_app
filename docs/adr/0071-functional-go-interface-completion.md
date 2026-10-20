# ADR-0071 — Grouped Go rendering for the remaining functional interfaces

Status: Accepted architecture; feature implementation under qualification, no GUI milestone landing.
Date: 2026-10-21

## Context

Paul prioritized Cat and Social on 2026-10-20 and deferred further Teacher rewrites until retained modules are chosen (programme ADR-0033/E12). His functional rebuild decision (programme ADR-0032) allows a coherent new interface without reproducing every old template. Social's native Go services already implement these workflows, and its twenty SPA modules remain available. The finite route inventory distinguishes working interfaces, unused references and genuinely selected presentation work.

## Decision

Use cached `html/template` views over copied, typed, already authorized producer results for these thirty-nine template destinations:

| Family | Destinations |
|---|---|
| Personal collections (2) | My meetups, venue checks |
| Support and public information (5) | Donate, transparency, campaigns, partners, own donations |
| Recurring activities and interest (7) | Series list/detail/form, gauges list/detail/form/conversion |
| Account and trust (10) | Person/card fragment, activity log, safety record, privacy data, age verification, account deletion, reporting, privacy policy, terms |
| Public discovery (7) | Anonymous home, community graph, discovery, map, place detail, event detail, open data |
| Activity and venue workflows (8) | Activity create/edit/detail/safe exit, place proposal/claim/pending, messages |

The templates are parsed once and retain the existing base design, localization, display preferences and external script hooks. Projections perform no domain query, authorization union, ranking, quorum calculation or provider call. Existing services continue to authorize every action. A supplied empty CSRF string remains presentation data; token minting and POST admission stay at the existing server boundary. Legacy capability-marker omission keeps the existing reference branch. Additive form metadata copies authoritative field definitions, values and choices beside the retained compatibility widgets; allocation is limited to the selected form families. Shared PostForm metadata is opt-in through the activity constructor; existing group forms keep their prior projection. Hydrated mention metadata is likewise activity-only.

The shared response boundary preserves main's LOCAL POC disclosure on cached native full pages. Source access, TDM labels, public facts, licensing, provenance, private fields and publication eligibility remain producer decisions. Legal pages retain their draft and pre-launch placeholders. Identity, consent, evidence and erasure policies are unchanged; no provider or real-child activation follows this work.

Restore the retired Inbox bookmark as its original authenticated redirect and connect four exact public metadata paths to their existing download producers. These bindings add no policy, query or domain transition.

Place detail displays the actual supplied pending-confirmation count and presents the actual hydrated activity records and nullable venue votes. Blank venue names receive the existing generic accessible label while retaining the record and link. Exactly validated decorative SVG bytes render in an image boundary. Native community graph assets reproducibly extract two static CSS literals into a stylesheet while preserving the original vendor file and enforced CSP. Scoped accessibility styles and explicit form labels retain the existing controls and hooks.

## Verification and retained references

Current registered-route tests deny fallback to all thirty-nine legacy template names and exercise real producers in an isolated PostgreSQL fixture. Synthetic desktop exports use the ordinary current renderer and its actual enforced CSP headers in both supported languages. Frontend contracts, type checking, build and the existing bundle cap continue to apply to all twenty SPA modules. Qualification results belong to STATUS and the compact completion record.

Historical source, nonce, form and capture predicates retain their exact original assertions and inputs through test-only Pongo references and strict complete-source inverses. Physical current file tuples remain current. The references cover their original unflagged fixtures; current LOCAL POC rendering is checked separately. New pages do not replace historical response baselines.

Compatibility fallbacks for existing SPA destinations, original templates and unused route references remain available. The optional templ-kit programme and formal S3 milestone acceptance are separate. Feature publication does not authorize landing Social main, pushing app main, production, government/provider access or the held account-deletion meetup-purge ruling.
