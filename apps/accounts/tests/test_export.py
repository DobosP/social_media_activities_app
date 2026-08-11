"""GDPR Art. 20 (data portability) export: the authenticated user's own data as JSON,
plus the guardian-for-ward variant. Verifies the export is scoped to the requester (or
their ward), discloses no other user's PII, and exposes the expected sections."""

import pytest
from django.contrib.gis.geos import Point
from django.urls import reverse
from rest_framework.test import APIClient

from apps.accounts.export import build_user_export
from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance, grant_parental_consent, link_guardian
from apps.donations.models import Donation
from apps.places.models import Place
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _activity(owner, slug):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"cat-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"at-{slug}", name="Football", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2030-06-01T10:00Z"
    )


def test_build_user_export_has_expected_sections():
    user = _user("exp1")
    export = build_user_export(user)
    assert set(export) == {
        "schema_version",
        "generated_at",
        "profile",
        "age_assurance",
        "consents",
        "guardianships",
        "memberships",
        "owned_activities",
        "owned_groups",
        "group_memberships",
        "thread_posts",
        "donations",
        "api_access",
        "safety_record",  # W4-F22
        "blocks",  # W4-F22
        "privacy_settings",  # W4-F22
        "own_sentiment_actions",  # ADR-0029
    }
    assert export["schema_version"] == 5  # DSA Art.17 provenance/scope fix bumped 4 -> 5
    assert export["own_sentiment_actions"] == {"reactions": [], "dissents": [], "concerns": []}
    # W10 disclosure: token METADATA only — the export must never contain a key.
    assert export["api_access"] == {"api_token_issued": False, "issued_at": None}
    assert export["profile"]["username"] == "exp1"
    assert export["profile"]["cohort"] == "adult"
    # The proven band is exported, but never a birthdate or other identifying data.
    assert export["profile"]["age_band"] == AgeBand.ADULT
    assert "birth_date" not in str(export)
    assert export["age_assurance"][0]["provider"] == "dev"


def test_build_user_export_includes_activity_membership_and_donations():
    user = _user("exp2")
    _activity(user, "exp2")
    Donation.objects.create(
        donor=user,
        amount_cents=500,
        provider="stripe",
        status=Donation.Status.COMPLETED,
        external_ref="ext-1",
    )
    export = build_user_export(user)

    assert len(export["owned_activities"]) == 1
    assert export["owned_activities"][0]["title"] == "Game"
    # create_activity makes the owner a member.
    assert any(m["role"] == "owner" for m in export["memberships"])
    assert export["donations"]["completed_count"] == 1
    assert export["donations"]["completed_total_cents"] == 500
    # No payment-card data is ever stored, so it cannot leak here.
    assert "card" not in str(export["donations"]).lower()


def test_build_user_export_includes_consent_and_guardianship():
    guardian = _user("g_exp", AgeBand.ADULT)
    ward = _user("w_exp", AgeBand.UNDER_16)
    link_guardian(guardian, ward)
    grant_parental_consent(guardian, ward)

    ward_export = build_user_export(ward)
    assert ward_export["consents"]["as_minor"][0]["status"] == "active"
    assert ward_export["guardianships"]["guarded_by"][0]["guardian_public_id"] == str(
        guardian.public_id
    )

    guardian_export = build_user_export(guardian)
    assert guardian_export["guardianships"]["as_guardian_of"][0]["ward_public_id"] == str(
        ward.public_id
    )


# --- API ---


def test_me_export_requires_auth():
    assert APIClient().get(reverse("me-export")).status_code in (401, 403)


def test_me_export_returns_own_data():
    user = _user("api_exp")
    client = APIClient()
    client.force_authenticate(user)
    resp = client.get(reverse("me-export"))
    assert resp.status_code == 200
    assert resp.json()["profile"]["username"] == "api_exp"


def test_ward_export_allowed_for_guardian():
    guardian = _user("g_api", AgeBand.ADULT)
    ward = _user("w_api", AgeBand.UNDER_16)
    link_guardian(guardian, ward)
    grant_parental_consent(guardian, ward)

    client = APIClient()
    client.force_authenticate(guardian)
    resp = client.get(reverse("ward-export", args=[ward.public_id]))
    assert resp.status_code == 200
    assert resp.json()["profile"]["username"] == "w_api"


def test_ward_export_denied_for_non_guardian():
    stranger = _user("s_api", AgeBand.ADULT)
    ward = _user("w_api2", AgeBand.UNDER_16)

    client = APIClient()
    client.force_authenticate(stranger)
    resp = client.get(reverse("ward-export", args=[ward.public_id]))
    assert resp.status_code == 403


def test_ward_export_unknown_user_is_404():
    import uuid

    guardian = _user("g_api3", AgeBand.ADULT)
    client = APIClient()
    client.force_authenticate(guardian)
    resp = client.get(reverse("ward-export", args=[uuid.uuid4()]))
    assert resp.status_code == 404


# --- W2-F32: the user's own thread words in their takeout ------------------------------------


def _thread_setup(owner_name, other_name, slug):
    from apps.social.models import Membership

    owner = _user(owner_name)
    other = _user(other_name)
    activity = _activity(owner, slug)
    Membership.objects.create(
        activity=activity, user=other, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return owner, other, activity


def test_thread_posts_exports_only_the_users_own_words():
    from apps.social import services as social

    owner, other, activity = _thread_setup("tp_owner", "tp_other", "tp")
    social.post_to_thread(owner, activity, "my plan: bring snacks")
    theirs = social.post_to_thread(other, activity, "another members private words")
    social.post_to_thread(owner, activity, "replying to you", reply_to=theirs.id)
    social.post_announcement(owner, activity, "owner announcement here")

    export = build_user_export(owner)
    posts = export["thread_posts"]["items"]
    bodies = [p["body"] for p in posts]
    assert "my plan: bring snacks" in bodies
    assert "replying to you" in bodies
    assert any(p["is_announcement"] and p["body"] == "owner announcement here" for p in posts)
    # HARD exclusion: another member's words never appear — not as a post, nor a reply snippet.
    assert "another members private words" not in str(export)
    # Strict allowlist shape (no reply_to/shared-target/attachment-bytes/author/thread internals).
    assert set(posts[0]) == {
        "thread_kind",
        "thread_id",
        "thread_title",
        "body",
        "status",
        "is_announcement",
        "edited",
        "had_attachment",
        "created_at",
    }
    assert posts[0]["thread_kind"] == "activity" and posts[0]["thread_id"] == activity.id
    assert all(p["status"] == "visible" for p in posts)
    assert export["thread_posts"]["total"] == len(posts)
    assert export["thread_posts"]["truncated"] is False


def test_own_hidden_post_exports_as_neutral_removed_marker():
    """An admin-manual-hidden post (is_hidden=True, no is_author_deleted, no ModerationAction row —
    the PostAdmin escape-hatch path) still gets the neutral '[removed]' marker: release requires
    the author's own delete provenance, which this row never has."""
    from apps.social import services as social

    owner, _other, activity = _thread_setup("tp_h_owner", "tp_h_other", "tph")
    p = social.post_to_thread(owner, activity, "this got moderated")
    p.is_hidden = True
    p.save(update_fields=["is_hidden"])
    export = build_user_export(owner)
    items = export["thread_posts"]["items"]
    bodies = [r["body"] for r in items]
    assert "this got moderated" not in bodies  # the original text is not disclosed
    assert "[removed]" in bodies  # a neutral marker, never a moderator identity/reason
    row = next(r for r in items if r["body"] == "[removed]")
    assert row["status"] == "removed"


def test_thread_posts_empty_for_a_user_who_never_posted():
    user = _user("tp_silent")
    export = build_user_export(user)
    assert export["thread_posts"] == {"items": [], "total": 0, "truncated": False}
    assert export["schema_version"] == 5


def test_thread_posts_covers_group_threads_with_name_fallback():
    # Locks the activity-XOR-group branch: a group thread has no .title, so owner.name is used.
    from apps.communities.models import Area
    from apps.social import services as social

    staff = User.objects.create_user(username="tp_staff", password="pw", is_staff=True)
    apply_assurance(staff, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    area = Area.objects.create(city="Cluj-Napoca", slug="cluj-exp", name="Cluj-Napoca")
    cat = ActivityCategory.objects.create(slug="cat-grp", name="Sport")
    atype = ActivityType.objects.create(slug="at-grp", name="Football", category=cat)
    group = social.create_group(staff, area=area, title="Cluj Runners", activity_type=atype)
    social.post_to_thread(staff, group, "group welcome post")

    posts = build_user_export(staff)["thread_posts"]["items"]
    row = next(p for p in posts if p["body"] == "group welcome post")
    assert row["thread_kind"] == "group"
    assert row["thread_id"] == group.id
    assert row["thread_title"] == "Cluj Runners"  # group.name (no .title) via the fallback


def test_shared_post_target_content_never_leaks_into_thread_posts():
    from apps.social import services as social

    owner, _other, activity = _thread_setup("tp_s_owner", "tp_s_other", "tps")
    place = Place.objects.create(
        name="X", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug="cat-shr", name="Sport")
    atype = ActivityType.objects.create(slug="at-shr", name="Football", category=cat)
    shared = social.create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="SHARED_TARGET_SECRET",
        starts_at="2030-06-01T10:00Z",
    )
    social.post_to_thread(owner, activity, "look here", share_activity=shared)

    posts = build_user_export(owner)["thread_posts"]["items"]
    assert any(p["body"] == "look here" for p in posts)
    # The shared activity's content is NOT pulled into the post projection (only the post's own
    # body + its thread title). (Its title appears under owned_activities — that's the user's own.)
    assert "SHARED_TARGET_SECRET" not in str(posts)


# --- DSA Art.17 provenance fix: own words released to their own author (F3), newest-kept +
# --- honest truncation (F4) -------------------------------------------------------------------


def test_author_deleted_post_body_is_exported_to_owner():
    # F3: the author's OWN withdrawn words are still on disk and are their own personal data —
    # withholding them from their own Art.15/20 export was never justified by the moderator-
    # identity rationale. FAILS on pre-fix code (body redacted to "[removed]").
    from apps.social.services import delete_own_post

    owner, _other, activity = _thread_setup("tp_ad_owner", "tp_ad_other", "tpad")
    from apps.social import services as social

    post = social.post_to_thread(owner, activity, "my own withdrawn words")
    delete_own_post(owner, post)

    posts = build_user_export(owner)["thread_posts"]["items"]
    row = next(p for p in posts if p["thread_id"] == activity.id and p["created_at"])
    assert row["body"] == "my own withdrawn words"
    assert row["status"] == "deleted_by_you"


def test_moderator_removed_post_stays_redacted():
    # A genuine platform REMOVE (never author-initiated) stays redacted exactly as before.
    from apps.safety.models import ModerationAction, ReasonCode
    from apps.safety.services import take_action

    owner, _other, activity = _thread_setup("tp_rm_owner", "tp_rm_other", "tprm")
    mod = _user("tp_rm_mod")
    from apps.social import services as social

    post = social.post_to_thread(owner, activity, "reported words")
    take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)

    posts = build_user_export(owner)["thread_posts"]["items"]
    row = next(p for p in posts if p["body"] == "[removed]")
    assert row["status"] == "removed"


def test_remove_then_self_delete_stays_redacted():
    # Precedence guard: a standing platform REMOVE wins over the author's later self-delete — the
    # naive "is_author_deleted always releases" implementation would wrongly export this body.
    from apps.safety.models import ModerationAction, ReasonCode
    from apps.safety.services import take_action
    from apps.social.services import delete_own_post

    owner, _other, activity = _thread_setup("tp_rs_owner", "tp_rs_other", "tprs")
    mod = _user("tp_rs_mod")
    from apps.social import services as social

    post = social.post_to_thread(owner, activity, "removed then self-deleted")
    take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    delete_own_post(owner, post)

    posts = build_user_export(owner)["thread_posts"]["items"]
    row = next(p for p in posts if p["body"] == "[removed]")
    assert row["status"] == "removed"
    assert "removed then self-deleted" not in str(posts)


def test_lifted_remove_after_author_delete_exports_body():
    # After the REMOVE is lifted by a granted appeal, the post stays hidden ONLY because of the
    # author's own act — the export is the one place the vindicated user still gets their words.
    from apps.safety.models import ModerationAction, ReasonCode
    from apps.safety.services import file_appeal, resolve_appeal, take_action
    from apps.social.services import delete_own_post

    owner, _other, activity = _thread_setup("tp_lr_owner", "tp_lr_other", "tplr")
    mod = _user("tp_lr_mod")
    from apps.social import services as social

    post = social.post_to_thread(owner, activity, "vindicated words")
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    delete_own_post(owner, post)
    appeal = file_appeal(owner, action, "wrong decision")
    resolve_appeal(mod, appeal, grant=True)

    post.refresh_from_db()
    assert post.is_hidden is True  # the author's own deletion still holds it hidden

    posts = build_user_export(owner)["thread_posts"]["items"]
    row = next(p for p in posts if p["thread_id"] == activity.id)
    assert row["body"] == "vindicated words"
    assert row["status"] == "deleted_by_you"


def test_admin_hidden_post_without_action_stays_redacted():
    # Fail-closed guard: an admin-manual-hide (is_hidden=True, no ModerationAction row, the
    # PostAdmin escape hatch) must stay redacted — release requires is_author_deleted too, which
    # this row never has. Would FAIL if the bare unlifted-REMOVE predicate replaced is_hidden.
    from apps.social.models import Post

    owner, _other, activity = _thread_setup("tp_am_owner", "tp_am_other", "tpam")
    from apps.social import services as social

    post = social.post_to_thread(owner, activity, "admin hidden directly")
    Post.objects.filter(pk=post.pk).update(is_hidden=True)

    posts = build_user_export(owner)["thread_posts"]["items"]
    row = next(p for p in posts if p["body"] == "[removed]")
    assert row["status"] == "removed"


def test_thread_posts_helper_query_count_is_flat():
    # F3: the helper's query count must not grow with the number of author-deleted posts in the
    # page (ONE bounded targets_with_unlifted_remove call, never per-row). Pinned as
    # same-count-at-two-Ns rather than a magic total, so an unrelated query-shape change can't
    # false-fail it while a per-row N+1 regression still would.
    from django.db import connection
    from django.test.utils import CaptureQueriesContext

    from apps.accounts import export as export_module
    from apps.social import services as social
    from apps.social.services import delete_own_post

    owner, _other, activity = _thread_setup("tp_qc_owner", "tp_qc_other", "tpqc")

    def add_deleted_posts(n):
        for i in range(n):
            p = social.post_to_thread(owner, activity, f"post {n}-{i}")
            delete_own_post(owner, p)

    def query_count():
        with CaptureQueriesContext(connection) as ctx:
            export_module._thread_posts(owner, for_self=True)
        return len(ctx)

    add_deleted_posts(1)
    baseline = query_count()
    add_deleted_posts(3)
    assert query_count() == baseline


def test_admin_hidden_then_self_deleted_exports_as_deleted_by_you():
    # Adjudicated PIN, not a fix: an admin manual hide (the PostAdmin escape hatch — is_hidden
    # only, no ModerationAction row) followed by the author's own delete is BYTE-IDENTICAL in
    # data to a plain self-delete (is_hidden + is_author_deleted, zero action rows), so no
    # export code could emit a distinct token — the body releases to its author as
    # "deleted_by_you". If the escape hatch ever grows a ModerationAction row (an owner
    # decision), this test should start failing and the docstring rule must be revisited.
    from apps.social import services as social
    from apps.social.models import Post
    from apps.social.services import delete_own_post

    owner, _other, activity = _thread_setup("tp_ah_owner", "tp_ah_other", "tpah")
    post = social.post_to_thread(owner, activity, "hidden then withdrawn")
    Post.objects.filter(pk=post.pk).update(is_hidden=True)
    post.refresh_from_db()
    delete_own_post(owner, post)

    items = build_user_export(owner)["thread_posts"]["items"]
    row = next(r for r in items if r["thread_id"] == activity.id)
    assert row["status"] == "deleted_by_you"
    assert row["body"] == "hidden then withdrawn"


def test_ward_export_withholds_wards_withdrawn_words_from_guardian():
    # MUST-FIX (all three reviewers independently): build_user_export(ward) is also called by
    # WardExportView, so F3's released body must be scoped to the DATA SUBJECT or a minor's
    # affirmatively withdrawn words flow to their guardian. The guardian is an explicit
    # read-only observer (docs/SAFETY.md) and the Art.17 statement of reasons already excludes
    # guardians, so the ward-export keeps the body "[removed]" — while the status token still
    # tells the guardian a post existed and was withdrawn.
    from apps.social import services as social
    from apps.social.models import Membership
    from apps.social.services import delete_own_post

    guardian = _user("we_guard", AgeBand.ADULT)
    ward = _user("we_ward", AgeBand.UNDER_16)
    link_guardian(guardian, ward)
    grant_parental_consent(guardian, ward)
    owner = _user("we_owner")
    activity = _activity(owner, "wexp")
    Membership.objects.create(
        activity=activity, user=ward, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    post = social.post_to_thread(ward, activity, "wards withdrawn words")
    delete_own_post(ward, post)

    client = APIClient()
    client.force_authenticate(guardian)
    resp = client.get(reverse("ward-export", args=[ward.public_id]))
    assert resp.status_code == 200
    items = resp.json()["thread_posts"]["items"]
    row = next(r for r in items if r["thread_id"] == activity.id)
    assert row["status"] == "deleted_by_you"  # the guardian learns a post was withdrawn...
    assert row["body"] == "[removed]"  # ...but never the withdrawn text
    assert "wards withdrawn words" not in resp.content.decode()

    # The other direction, pinned at the API surface: the ward's OWN export still releases
    # their own withdrawn words (F3's whole point).
    client.force_authenticate(ward)
    self_items = client.get(reverse("me-export")).json()["thread_posts"]["items"]
    self_row = next(r for r in self_items if r["thread_id"] == activity.id)
    assert self_row["status"] == "deleted_by_you"
    assert self_row["body"] == "wards withdrawn words"


def test_thread_posts_keeps_newest_when_over_cap(monkeypatch):
    # F4: the OLD ascending order_by()[:cap] kept the OLDEST posts and dropped the newest for a
    # prolific author — the exact defect class already fixed in safety_record_for. FAILS on
    # pre-fix code (would keep post-0/1/2 instead of post-2/3/4).
    from apps.accounts import export as export_module

    monkeypatch.setattr(export_module, "_THREAD_POSTS_CAP", 3)
    owner, _other, activity = _thread_setup("tp_cap_owner", "tp_cap_other", "tpcap")
    from apps.social import services as social

    for i in range(5):
        social.post_to_thread(owner, activity, f"post {i}")

    result = build_user_export(owner)["thread_posts"]
    bodies = [p["body"] for p in result["items"]]
    assert bodies == ["post 2", "post 3", "post 4"]  # newest 3, chronological order
    assert result["total"] == 5
    assert result["truncated"] is True


def test_thread_posts_under_cap_not_truncated():
    owner, _other, activity = _thread_setup("tp_uc_owner", "tp_uc_other", "tpuc")
    from apps.social import services as social

    social.post_to_thread(owner, activity, "only one")

    result = build_user_export(owner)["thread_posts"]
    assert result["total"] == len(result["items"]) == 1
    assert result["truncated"] is False


# --- W4-F22: complete-the-export (own DSA record + blocks + privacy settings) ---------


def test_export_includes_own_blocks():
    import json

    from apps.safety.services import block_user

    user = _user("exp_blk")
    target = _user("exp_blk_tgt")
    block_user(user, target)
    blocks = build_user_export(user)["blocks"]
    assert len(blocks) == 1
    assert blocks[0]["blocked"] == target.display_name
    assert blocks[0]["blocked_public_id"] == str(target.public_id)
    assert blocks[0]["created_at"]  # iso string, present
    json.dumps(build_user_export(user))  # plain-json export must stay serialisable


def test_export_safety_record_present_and_omits_moderator_identity():
    import json

    from apps.safety.models import ModerationAction, ReasonCode
    from apps.safety.services import take_action

    user = _user("exp_sr")
    moderator = _user("exp_mod_secret_handle")
    moderator.is_staff = True
    moderator.save(update_fields=["is_staff"])
    take_action(moderator, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)

    export = build_user_export(user)
    assert "decisions" in export["safety_record"]
    assert len(export["safety_record"]["decisions"]) >= 1
    # The moderator's identity must NEVER appear in the affected user's export (Art.17 detail is the
    # offender's; the moderator's handle is not the user's data).
    assert "exp_mod_secret_handle" not in json.dumps(export)


def test_export_safety_record_includes_own_filed_reports():
    from apps.safety.services import file_report

    reporter = _user("exp_rep")
    target = _user("exp_rep_tgt")
    from apps.safety.models import ReasonCode

    file_report(reporter, target, ReasonCode.SPAM)
    reports = build_user_export(reporter)["safety_record"]["reports"]
    assert len(reports) == 1  # the reporter's OWN filed report is portable


def test_export_includes_privacy_settings():
    from apps.notifications.models import Notification
    from apps.notifications.services import set_muted_kinds
    from apps.places.models import AccessPreference

    user = _user("exp_ps")
    set_muted_kinds(user, [Notification.Kind.EVENT_REMINDER])
    AccessPreference.objects.create(user=user, needs_step_free=True)

    ps = build_user_export(user)["privacy_settings"]
    assert "event_reminder" in ps["muted_notification_kinds"]
    assert ps["access_preferences"]["needs_step_free"] is True


def test_export_privacy_settings_handles_no_access_preference():
    user = _user("exp_ps_none")
    ps = build_user_export(user)["privacy_settings"]
    assert ps["muted_notification_kinds"] == []
    assert ps["access_preferences"] is None  # no row set yet
