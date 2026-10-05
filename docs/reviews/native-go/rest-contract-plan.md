# Bounded remaining REST contract plan

Valid until: the referenced serving code, accepted policies or frozen declarations change — then re-triage.

Read-only triage on qualified source `c27a99fd71051cfe4575e3db9faa008111c12f12`, 2026-10-06.
No new tests, serving edits, producer-pin changes or HTML parity campaign were run for this plan.
Baseline qualification is [the aggregate](restart-checkpoint.json):705 fresh native tests/all21 lanes.
The finite transport batch maps one existing export case and closes three social wire cases; worker
social47/0skip and focused schema proofs pass, with root final21 qualification pending.
The ledger's1003 claimed/1668 unresolved entries count frozen assertions, not missing endpoints.
Go already serves the ordinary HTTP/toolchain paths; full Python reference retirement remains separate.

## Social REST:26 declarations

All26 IDs below have prefix `apps/social/tests/test_api.py::`; three are now mapped and23 remain. The frozen file hash matches the
inventory. Routes exist in `services/server/internal/social/http.go`. No complete mapping of
these legacy REST assertion sets was confirmed from the existing qualified service/HTML tests.

| Frozen declaration | Remaining exact REST obligation |
|---|---|
| test_activities_require_auth | Literal legacy GET refusal; qualified v1 authentication coverage is narrower. |
| test_activity_description_too_long_rejected | Mapped: current REST400 description string-array key, no write; both API prefixes. |
| test_arrived_action_marks_membership | HTTP200 plus persisted non-null arrival. |
| test_arrived_ignores_on_behalf_of | Guardian404 and unchanged ward arrival. |
| test_create_and_list_activity | POST201/cohort, GET exactly one result. |
| test_join_and_vote_flow | Join201/id and full legacy flow; existing HTTP votes are partial. |
| test_list_is_cohort_scoped | Child REST list results=[], not only service detail refusal. |
| test_mine_membership_list_is_bounded | Seven persisted owner rows, configured cap3. |
| test_non_owner_cannot_patch_activity | Nonmember PATCH403, title unchanged. |
| test_owner_can_cancel_via_api | POST200 and persisted cancelled. |
| test_owner_can_edit_activity_via_patch | PATCH200 and persisted new title. |
| test_post_body_too_long_rejected | Mapped: current REST400 body string-array key, zero-write and exact4000 HTTP201 acceptance. |
| test_post_requires_membership | Outsider POST403, owner POST201. |
| test_posts_cannot_be_ghostwritten_on_behalf_of | Guardian403 and zero ghostwritten rows. |
| test_rsvp_invalid_intent_is_400 | Exact REST400. |
| test_rsvp_non_member_forbidden | Same-cohort nonmember403. |
| test_rsvp_returns_live_count | REST200, going1/total2/minimum-null. |
| test_thread_posts_get_requires_membership | Populated-thread outsider403, owner200. |
| test_thread_posts_list_is_bounded | Legacy200, cap5, newest-N/oldest-first order. |
| test_transit_action_sets_status | POST200, response/persisted on_my_way. |
| test_transit_ignores_on_behalf_of | Guardian404, ward transit unchanged. |
| test_transit_invalid_status_is_forbidden | Mapped: unknown value HTTP403 after current visibility/member/window checks; state unchanged. |
| test_v1_mine_membership_list_is_cursor_paginated | Seven rows, limit2/results2/nonempty cursor. |
| test_v1_mine_membership_list_query_count_is_constant | Actual API trace≤4, ten results. |
| test_v1_thread_posts_are_cursor_paginated | Exact envelope, page bodies9–11/6–8. |
| test_v1_thread_posts_query_count_is_constant | Actual API trace≤8, ten results. |

Generic error origin remains `internal/platform/platform.go` Error. The finite batch preserves
field errors only for activity/post create and maps the service's unknown-transit-value marker only
in that REST action. Its400 schema alternatives are narrowly operation-scoped. Remaining groups:
lifecycle/admission, identity/presence/RSVP, and pagination/query-ceiling assertions.
Reuse qualified activities/vote/thread/safety, serializer, co-member and policy tests where their
actual assertions match. Preserve accepted co-organizer, co-member, guardian and safety replacements.

## Booking:existing REST, incomplete assertion mappings

Booking has18 frozen declarations,2 covered/16 unresolved. `/api/booking` and `/api/v1/booking`
create/list/detail/cancel/options/providers routes exist. Qualified booking7 tests are foundations.
Exact unresolved IDs have prefix `apps/booking/tests/`:

| ID | Remaining assertion or reviewed replacement |
|---|---|
| test_api.py::test_create_and_list_and_cancel | Created ID is present in list. |
| test_api.py::test_options_endpoint | provider=deeplink. |
| test_api.py::test_providers_endpoint | Both builtin slugs through actual endpoint. |
| test_api.py::test_requires_auth | Anonymous options refusal. |
| test_services.py::test_booking_options_default_deeplink | provider plus bookable_in_app=false. |
| test_services.py::test_booking_options_with_info | Configured link and instructions fixture. |
| test_services.py::test_create_deeplink_booking_is_pending | Stored nonempty deep-link roundtrip. |
| test_services.py::test_create_realtime_booking_confirms | Status/provider/external-reference tuple. |
| test_services.py::test_non_participant_denied | Exact frozen fresh-user scenario; stronger existing denial evidence retained. |
| test_services.py::test_booking_for_activity_requires_membership | Owner/member positive and outsider negative together; guardian policy retained. |
| test_providers.py::test_demo_rest_create_and_cancel | Successful real adapter cancel path/body. |
| test_providers.py::test_demo_rest_provider_failure_maps_to_booking_error | Network-error/outward mapping, retaining qualified503/non-replay. |
| test_providers.py::test_demo_rest_unconfigured_raises | Exact empty base-URL refusal. |
| test_providers.py::test_registry_returns_builtins | Native map/slugs replace Python class instances; preserve source ID. |
| test_providers.py::test_registry_unknown_raises | Governed failure replaces KeyError; reviewed replacement. |
| test_providers.py::test_deeplink_cannot_create | Non-realtime capability skips remote create; pending local record replacement. |

The covered cancellation/other-owner refusal IDs remain. Do not claim new endpoints are needed for
these booking rows; add only missing assertions or explicit accepted replacement evidence.

## Export and public finance

- Existing exact match now mapped after independent metadata review: `apps/accounts/tests/test_export.py::test_build_user_export_includes_activity_membership_and_donations`
  matches every frozen assertion in qualified `TestCasePort2ExportActivityDonationsAndSharedTargetBoundary`
  (`internal/accounts/export_erasure_case_port2_test.go`). No new endpoint/test is required for that row.
- `apps/accounts/tests/test_export.py::test_thread_posts_helper_query_count_is_flat`: existing whole-export
  growth test allows+2; the source helper requires exact equality across sizes. Assert that equality without raising its bound.
- `apps/donations/tests/test_f26_campaign_closeout.py::test_linked_spend_rows_attach_untagged_excluded`:
  qualified ledger test checks a linked row but omits its exact category.
- `apps/donations/tests/test_w4_f24_civic_outcomes.py::test_partner_name_regated_to_public_at_read_time`:
  SQL checks both partner flags; current qualified test withdraws is_active, while the source requires is_verified=false.
- `apps/donations/tests/test_f42_partner_campaign.py::{test_credit_does_not_expose_donor_pii,test_partner_website_renders_as_a_sanitised_link,test_malicious_partner_website_is_never_a_live_link,test_non_public_partner_is_not_credited}`:
  preserve IDs/hashes and separate HTML href/credit obligations from any reviewed JSON replacement.

Concrete transport gap: donations.Register exposes start/mine/total/webhook. Campaigns, closeouts,
spend, in-kind, civic outcomes, anchors and partners exist in qualified Ledger but reach paymentView;
a complete public-finance REST DTO/route remains. Specify link sanitization, exact spend category,
both partner re-gates and donor-name privacy before exposing that projection. Domain assertions
cannot establish complete REST projection parity.

## Order and scope

1. Retain the exact export mapping and its source IDs/provenance and qualified c27 receipt.
2. Complete root source qualification for the three repaired response contracts;23 social assertion sets remain.
3. Close booking REST assertions and reviewed adapter replacements.
4. Specify/qualify public-finance REST projection and exact export-helper query equality.
5. Triage relevant REST media/account-safety rows against existing code/tests similarly.

Source qualification may land under ADR-0040 without claiming REST assertion closure or reference
retirement. No blanket1672-case rewrite, broader HTML parity, archived generic V1 producer client
wiring or producer-pin bump follows from this plan. First-deployment reviews remain mandatory.
