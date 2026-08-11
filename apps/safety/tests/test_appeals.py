"""DSA Art.17 redress: reachable statement of reasons + the internal appeal (file / resolve).

The pre-auth web surface is covered in apps/web/tests/test_account_restricted.py; here we cover the
services (self-scope, idempotency, reversal) and the logged-in DRF surface.
"""

import datetime as dt

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.models import ModerationAction, ModerationAppeal, ReasonCode
from apps.safety.services import (
    AppealError,
    file_appeal,
    resolve_appeal,
    restriction_statement_for,
    safety_record_for,
    take_action,
)
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, staff=False, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name, is_staff=staff)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="ap-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="ap-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at=timezone.now()
    )


# --- statement of reasons -----------------------------------------------------------------


def test_statement_for_suspended_user_is_self_scoped():
    mod, user = _user("ap_mod", staff=True), _user("ap_user")
    future = timezone.now() + dt.timedelta(days=3)
    action = take_action(
        mod, user, ModerationAction.Action.SUSPEND, ReasonCode.HARASSMENT, expires_at=future
    )
    st = restriction_statement_for(user)
    assert st is not None
    assert st["action_id"] == action.id
    assert st["is_lifetime"] is False
    assert st["lifts_at"] == future
    assert st["can_appeal"] is True
    assert "ap_mod" not in str(st)  # no moderator identity


def test_statement_none_for_active_user():
    assert restriction_statement_for(_user("ap_active")) is None


def test_statement_none_for_self_deactivated_account():
    # is_active=False but NOT from moderation → reveal no (false) moderation detail.
    user = _user("ap_selfoff")
    user.is_active = False
    user.save(update_fields=["is_active"])
    assert restriction_statement_for(user) is None


def test_statement_lifetime_ban_has_no_lift_date():
    mod, user = _user("ap_mod2", staff=True), _user("ap_banned")
    take_action(mod, user, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    st = restriction_statement_for(user)
    assert st["is_lifetime"] is True
    assert st["lifts_at"] is None


# --- file_appeal --------------------------------------------------------------------------


def test_file_appeal_happy_path_trims_and_pends():
    mod, user = _user("fa_mod", staff=True), _user("fa_user")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    appeal = file_appeal(user, action, "  I did nothing wrong  ")
    assert appeal.status == ModerationAppeal.Status.PENDING
    assert appeal.statement == "I did nothing wrong"
    assert appeal.appellant == user


def test_file_appeal_rejects_empty_statement():
    mod, user = _user("fa_mod2", staff=True), _user("fa_user2")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    with pytest.raises(AppealError):
        file_appeal(user, action, "   ")
    assert ModerationAppeal.objects.count() == 0


def test_file_appeal_rejects_other_users_action():
    mod, user, other = _user("fa_mod3", staff=True), _user("fa_user3"), _user("fa_other3")
    action = take_action(mod, other, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    with pytest.raises(AppealError):
        file_appeal(user, action, "let me appeal someone else's sanction")
    assert ModerationAppeal.objects.count() == 0


def test_file_appeal_is_one_per_action():
    mod, user = _user("fa_mod4", staff=True), _user("fa_user4")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    file_appeal(user, action, "first")
    with pytest.raises(AppealError):
        file_appeal(user, action, "second")
    assert ModerationAppeal.objects.filter(action=action).count() == 1


# --- resolve_appeal -----------------------------------------------------------------------


def test_overturn_reactivates_account():
    mod, user = _user("re_mod", staff=True), _user("re_user")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    user.refresh_from_db()
    assert user.is_active is False
    appeal = file_appeal(user, action, "please review")
    resolve_appeal(mod, appeal, grant=True)
    user.refresh_from_db()
    action.refresh_from_db()
    appeal.refresh_from_db()
    assert user.is_active is True
    assert action.lifted_at is not None
    assert appeal.status == ModerationAppeal.Status.OVERTURNED


def test_uphold_keeps_restriction():
    mod, user = _user("re_mod2", staff=True), _user("re_user2")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    appeal = file_appeal(user, action, "please")
    resolve_appeal(mod, appeal, grant=False, notes="decision stands")
    user.refresh_from_db()
    appeal.refresh_from_db()
    assert user.is_active is False
    assert appeal.status == ModerationAppeal.Status.UPHELD


def test_overturn_does_not_reactivate_when_a_separate_ban_still_applies():
    mod, user = _user("re_mod3", staff=True), _user("re_user3")
    suspend = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    take_action(mod, user, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    appeal = file_appeal(user, suspend, "appeal the suspension")
    resolve_appeal(mod, appeal, grant=True)
    user.refresh_from_db()
    assert user.is_active is False  # the independent lifetime BAN still deactivates the account


def test_overturn_unhides_removed_content():
    mod, owner = _user("re_mod5", staff=True), _user("re_owner5")
    activity = _activity(owner)
    take_action(mod, activity, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    activity.refresh_from_db()
    assert activity.is_hidden is True
    action = ModerationAction.objects.get(action=ModerationAction.Action.REMOVE)
    appeal = file_appeal(owner, action, "this activity was fine")
    resolve_appeal(mod, appeal, grant=True)
    activity.refresh_from_db()
    assert activity.is_hidden is False


def test_overturn_does_not_republish_a_post_the_author_deleted():
    # Regression: a self-delete and a moderator REMOVE both set Post.is_hidden, so without
    # is_author_deleted provenance a granted appeal republished content the AUTHOR withdrew.
    # The action must still lift (the moderation record is genuinely reversed) — only the
    # un-hide is declined.
    from apps.social.services import delete_own_post, post_to_thread

    mod, owner = _user("adp_mod", staff=True), _user("adp_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    delete_own_post(owner, post)
    post.refresh_from_db()
    assert (post.is_hidden, post.is_author_deleted) == (True, True)

    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal = file_appeal(owner, action, "the removal was wrong")
    resolve_appeal(mod, appeal, grant=True)

    post.refresh_from_db()
    action.refresh_from_db()
    assert post.is_hidden is True  # the author's own deletion survives the reversal
    assert action.lifted_at is not None  # but the moderation decision IS reversed


def test_overturn_does_not_republish_when_author_deleted_after_the_removal():
    # The other order: moderator REMOVEs first, then the author deletes it themselves (reachable
    # — the delete views fetch the post without an is_hidden filter). The self-delete must still
    # register provenance on the already-hidden row, or the appeal republishes it.
    from apps.social.services import delete_own_post, post_to_thread

    mod, owner = _user("adp2_mod", staff=True), _user("adp2_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    delete_own_post(owner, post)
    post.refresh_from_db()
    assert post.is_author_deleted is True

    appeal = file_appeal(owner, action, "the removal was wrong")
    resolve_appeal(mod, appeal, grant=True)
    post.refresh_from_db()
    assert post.is_hidden is True


def test_self_delete_decides_on_locked_db_state_not_the_caller_s_stale_instance():
    # Race guard (deterministic stand-in for the interleaving): the delete views hand
    # delete_own_post an instance fetched outside any transaction, so a concurrent appeal
    # reversal can un-hide the row underneath it. If the branch decision were made on the stale
    # in-memory is_hidden=True, the call would take the "already hidden" path and only stamp
    # provenance — leaving the post PUBLICLY VISIBLE with is_author_deleted=True, the exact state
    # the provenance flag exists to prevent. Re-reading under select_for_update fixes the
    # decision to committed state.
    from apps.social.models import Post
    from apps.social.services import delete_own_post, post_to_thread

    owner = _user("stale_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    delete_own_post(owner, post)
    assert post.is_hidden is True  # the caller's instance now says hidden

    # Someone else un-hides the row (what a granted appeal used to do) — the instance is stale.
    Post.objects.filter(pk=post.pk).update(is_hidden=False, is_author_deleted=False)

    delete_own_post(owner, post)
    post.refresh_from_db()
    assert (post.is_hidden, post.is_author_deleted) == (True, True)


def test_overturn_still_unhides_a_post_the_author_never_deleted():
    # The provenance guard must not break the ordinary REMOVE reversal for posts.
    from apps.social.services import post_to_thread

    mod, owner = _user("adp3_mod", staff=True), _user("adp3_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "perfectly fine")
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal = file_appeal(owner, action, "this was fine")
    resolve_appeal(mod, appeal, grant=True)
    post.refresh_from_db()
    assert post.is_hidden is False


def test_resolve_is_idempotent():
    mod, user = _user("re_mod4", staff=True), _user("re_user4")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    appeal = file_appeal(user, action, "x")
    resolve_appeal(mod, appeal, grant=False)
    with pytest.raises(AppealError):
        resolve_appeal(mod, appeal, grant=True)


def test_overturned_ban_does_not_keep_account_locked_after_timed_lifts():
    # Regression (review MED): a user under a lifetime BAN + a future TIMED_BAN wins the BAN
    # appeal. _reverse_action stamps BAN.lifted_at but can't reactivate yet (TIMED_BAN still
    # active). When the TIMED_BAN later expires, lift_expired_suspensions must IGNORE the
    # already-lifted BAN and reactivate — otherwise the granted appeal is silently nullified.
    from apps.safety.services import lift_expired_suspensions

    mod, user = _user("ov_mod", staff=True), _user("ov_user")
    future = timezone.now() + dt.timedelta(days=1)
    ban = take_action(mod, user, ModerationAction.Action.BAN, ReasonCode.HARASSMENT)
    take_action(mod, user, ModerationAction.Action.TIMED_BAN, ReasonCode.SPAM, expires_at=future)
    appeal = file_appeal(user, ban, "the ban was wrong")
    resolve_appeal(mod, appeal, grant=True)
    user.refresh_from_db()
    assert user.is_active is False  # TIMED_BAN still in force, so not yet reactivated

    # The timed ban now elapses; the nightly batch must reactivate (BAN was overturned/lifted).
    timed = ModerationAction.objects.get(action=ModerationAction.Action.TIMED_BAN)
    timed.expires_at = timezone.now() - dt.timedelta(minutes=1)
    timed.save(update_fields=["expires_at"])
    assert lift_expired_suspensions() == 1
    user.refresh_from_db()
    assert user.is_active is True


def test_overturn_ban_releases_identity_ledger(settings):
    # Overturning a lifetime BAN must also lift the wallet from the identity-ban ledger, so the
    # vindicated person can register/recover again (closes the Art.17 BAN-overturn residual).
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    from apps.accounts.services import bind_identity, identity_is_banned

    mod, user = _user("rl_mod", staff=True), _user("rl_user")
    bind_identity(
        user,
        AssuranceResult(
            age_band=AgeBand.ADULT,
            verified=True,
            provider="eudi",
            method="openid4vp",
            holder_sub="holder-rl",
            raw={"age_over_16": True, "age_over_18": True, "holder_proof": "verified"},
        ),
    )
    ban = take_action(mod, user, ModerationAction.Action.BAN, ReasonCode.HARASSMENT)
    assert identity_is_banned("holder-rl") is True

    appeal = file_appeal(user, ban, "wrongly banned")
    resolve_appeal(mod, appeal, grant=True)
    user.refresh_from_db()
    assert user.is_active is True
    assert identity_is_banned("holder-rl") is False  # ledger released on overturn


def test_overturn_one_of_two_bans_keeps_wallet_on_ledger(settings):
    # The `not other_ban` guard: overturning ONE lifetime BAN must NOT release the ledger (or
    # reactivate) while a second, independent BAN still keys the same wallet/account.
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    from apps.accounts.services import bind_identity, identity_is_banned

    mod, user = _user("tb_mod", staff=True), _user("tb_user")
    bind_identity(
        user,
        AssuranceResult(
            age_band=AgeBand.ADULT,
            verified=True,
            provider="eudi",
            method="openid4vp",
            holder_sub="holder-tb",
            raw={"age_over_16": True, "age_over_18": True, "holder_proof": "verified"},
        ),
    )
    ban1 = take_action(mod, user, ModerationAction.Action.BAN, ReasonCode.HARASSMENT)
    # a second, independent lifetime ban on the same account
    take_action(mod, user, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    appeal = file_appeal(user, ban1, "contest the first ban")
    resolve_appeal(mod, appeal, grant=True)
    user.refresh_from_db()
    assert user.is_active is False  # the second BAN still deactivates
    assert identity_is_banned("holder-tb") is True  # wallet stays on the ledger


def test_overturn_for_minor_alerts_active_guardian():
    # An appeal outcome is a moderation outcome about the (CHILD) affected user — the W4-F3
    # symmetric guardian loop must fire on resolution, keyed on an ACTIVE GuardianRelationship.
    from apps.accounts.models import GuardianRelationship
    from apps.notifications.models import Notification

    mod = _user("gm_mod", staff=True)
    child = _user("gm_child", band=AgeBand.UNDER_16)
    guardian = _user("gm_guardian")
    GuardianRelationship.objects.create(
        guardian=guardian, ward=child, status=GuardianRelationship.Status.ACTIVE
    )
    action = take_action(mod, child, ModerationAction.Action.SUSPEND, ReasonCode.HARASSMENT)
    appeal = file_appeal(child, action, "please review")
    before = Notification.objects.filter(recipient=guardian).count()
    resolve_appeal(mod, appeal, grant=True)
    assert Notification.objects.filter(recipient=guardian).count() == before + 1


def test_safety_record_shows_appeal_status_and_action_id():
    mod, user = _user("sr_mod", staff=True), _user("sr_user")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    rec = safety_record_for(user)
    d = rec["decisions"][0]
    assert d["action_id"] == action.id
    assert d["can_appeal"] is True
    file_appeal(user, action, "contest")
    d2 = safety_record_for(user)["decisions"][0]
    assert d2["can_appeal"] is False
    assert d2["appeal_status_label"]


# --- ReversalOutcome: the reversal reports what it materially did -------------------------


def test_reverse_action_reports_left_hidden_author_deleted():
    from apps.safety.services import ReversalOutcome, _reverse_action
    from apps.social.services import delete_own_post, post_to_thread

    mod, owner = _user("ro_mod", staff=True), _user("ro_owner")
    activity = _activity(owner)
    # Decline branch: the author also deleted the post, so the un-hide is refused and flagged.
    post = post_to_thread(owner, activity, "my own words")
    delete_own_post(owner, post)
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    assert _reverse_action(action) == ReversalOutcome(
        reactivated=False, left_hidden_author_deleted=True
    )
    # Normal post un-hide: neither flag.
    post2 = post_to_thread(owner, activity, "perfectly fine")
    action2 = take_action(mod, post2, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    assert _reverse_action(action2) == ReversalOutcome(False, False)
    post2.refresh_from_db()
    assert post2.is_hidden is False
    # Account branch: reactivated True on a plain suspension...
    user = _user("ro_user")
    suspend = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    assert _reverse_action(suspend) == ReversalOutcome(True, False)
    # ...and False while an independent lifetime BAN still deactivates the account.
    user2 = _user("ro_user2")
    suspend2 = take_action(mod, user2, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    take_action(mod, user2, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    assert _reverse_action(suspend2) == ReversalOutcome(False, False)


def test_reverse_action_other_remove_not_flagged_author_deleted():
    # A SECOND un-lifted REMOVE keeps the post hidden — but that other decision has its own
    # record row and its own appeal, so THIS reversal must not claim the author-deleted decline.
    from apps.safety.services import ReversalOutcome, _reverse_action
    from apps.social.services import delete_own_post, post_to_thread

    mod, owner = _user("or_mod", staff=True), _user("or_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    delete_own_post(owner, post)
    first = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.SPAM)  # second, un-lifted
    assert _reverse_action(first) == ReversalOutcome(False, False)
    post.refresh_from_db()
    assert post.is_hidden is True


# --- truthful grant notification (the win that restores nothing) --------------------------


def test_grant_notification_says_content_stays_deleted_when_author_deleted():
    from apps.notifications.models import Notification
    from apps.social.services import delete_own_post, post_to_thread

    mod = _user("gn_mod", staff=True)
    # Order 1: the author deletes first, a moderator REMOVEs on top, the contest is granted.
    owner = _user("gn_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    delete_own_post(owner, post)
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal = file_appeal(owner, action, "the removal was wrong")
    resolve_appeal(mod, appeal, grant=True)
    note = (
        Notification.objects.filter(recipient=owner, kind=Notification.Kind.MODERATION)
        .order_by("-id")
        .first()
    )
    assert note.title == "Your appeal succeeded"
    assert "stays deleted" in note.body
    assert "has been removed" not in note.body

    # Order 2: REMOVE first, then the author deletes the already-hidden post, then contests.
    owner2 = _user("gn_owner2")
    activity2 = _activity(owner2)
    post2 = post_to_thread(owner2, activity2, "my own words too")
    action2 = take_action(mod, post2, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    delete_own_post(owner2, post2)
    appeal2 = file_appeal(owner2, action2, "the removal was wrong")
    resolve_appeal(mod, appeal2, grant=True)
    note2 = (
        Notification.objects.filter(recipient=owner2, kind=Notification.Kind.MODERATION)
        .order_by("-id")
        .first()
    )
    assert note2.title == "Your appeal succeeded"
    assert "stays deleted" in note2.body
    assert "has been removed" not in note2.body


def test_grant_notification_unchanged_for_normal_overturn():
    # The ordinary overturn keeps the existing copy verbatim (byte-equal).
    from apps.notifications.models import Notification
    from apps.social.services import post_to_thread

    mod, owner = _user("nn_mod", staff=True), _user("nn_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "perfectly fine")
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal = file_appeal(owner, action, "this was fine")
    resolve_appeal(mod, appeal, grant=True)
    note = Notification.objects.filter(recipient=owner, kind=Notification.Kind.MODERATION).latest(
        "id"
    )
    assert note.title == "Your appeal succeeded"
    assert note.body == (
        "We reviewed your contest of a moderation decision and reversed it. Any "
        "restriction from that decision has been removed."
    )


# --- delete-while-contesting refusal (F2) -------------------------------------------------


def test_delete_refused_while_contesting_the_removal():
    # A stale Delete click while the author's own contest of the hiding REMOVE is PENDING must
    # not silently forfeit the restore half of the remedy (the provenance stamp is permanent).
    from apps.social.services import NotEligible, delete_own_post, post_to_thread

    mod, owner = _user("rf_mod", staff=True), _user("rf_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal = file_appeal(owner, action, "the removal was wrong")
    with pytest.raises(NotEligible):
        delete_own_post(owner, post)
    post.refresh_from_db()
    appeal.refresh_from_db()
    assert post.is_author_deleted is False
    assert appeal.status == ModerationAppeal.Status.PENDING


def test_delete_allowed_again_after_appeal_decided():
    from apps.social.services import delete_own_post, post_to_thread

    mod = _user("da_mod", staff=True)
    # Granted: the post is visible again, so a normal delete works (no moderation flash case).
    owner = _user("da_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal = file_appeal(owner, action, "wrong")
    resolve_appeal(mod, appeal, grant=True)
    post.refresh_from_db()
    assert post.is_hidden is False
    result = delete_own_post(owner, post)
    assert result.was_moderation_hidden is False
    post.refresh_from_db()
    assert (post.is_hidden, post.is_author_deleted) == (True, True)
    # Upheld: the next delete takes the stamp-on-hidden path (and reports it to the views).
    owner2 = _user("da_owner2")
    activity2 = _activity(owner2)
    post2 = post_to_thread(owner2, activity2, "my own words too")
    action2 = take_action(mod, post2, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal2 = file_appeal(owner2, action2, "wrong")
    resolve_appeal(mod, appeal2, grant=False)
    result2 = delete_own_post(owner2, post2)
    assert result2.was_moderation_hidden is True
    post2.refresh_from_db()
    assert (post2.is_hidden, post2.is_author_deleted) == (True, True)


def test_pending_warn_appeal_does_not_block_delete():
    # The refusal predicate is REMOVE-scoped: a pending contest of a WARN on the same post
    # must not refuse the stamp on the (REMOVE-hidden, uncontested) row.
    from apps.social.services import delete_own_post, post_to_thread

    mod, owner = _user("pw_mod", staff=True), _user("pw_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    warn = take_action(mod, post, ModerationAction.Action.WARN, ReasonCode.SPAM)
    take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    file_appeal(owner, warn, "the warning was wrong")
    result = delete_own_post(owner, post)
    assert result.was_moderation_hidden is True
    post.refresh_from_db()
    assert post.is_author_deleted is True


# --- admin queue surfaces the author-deleted fact -----------------------------------------


@pytest.mark.django_db(transaction=True)
def test_admin_overturn_reports_stays_hidden(client):
    from django.urls import reverse

    from apps.social.services import delete_own_post, post_to_thread

    mod = _user("adm_mod", staff=True)
    mod.is_superuser = True
    mod.save(update_fields=["is_superuser"])
    owner = _user("adm_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    delete_own_post(owner, post)
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal = file_appeal(owner, action, "the removal was wrong")
    client.force_login(mod)
    # The change page carries the pre-decision content note.
    change = client.get(reverse("admin:safety_moderationappeal_change", args=[appeal.pk]))
    assert "overturning will not republish it" in change.content.decode()
    # The message must fire from the CHANGELIST ACTION itself: resolve_appeal re-reads the
    # appeal under lock and returns THAT instance, so the loop has to capture the return —
    # read off the queryset element, the transient outcome is absent and the notice vanishes.
    resp = client.post(
        reverse("admin:safety_moderationappeal_changelist"),
        {"action": "overturn", "_selected_action": [str(appeal.pk)]},
        follow=True,
    )
    body = resp.content.decode()
    assert (
        f"Appeal #{appeal.pk}: overturned; the author had deleted this content themselves, "
        "so it stays hidden." in body
    )
    post.refresh_from_db()
    assert post.is_hidden is True


def test_appeal_serializer_flags_author_deleted_content():
    from apps.social.models import Post
    from apps.social.services import delete_own_post, post_to_thread

    mod, owner = _user("sz_mod", staff=True), _user("sz_owner")
    activity = _activity(owner)
    deleted = post_to_thread(owner, activity, "my own words")
    delete_own_post(owner, deleted)
    a1 = take_action(mod, deleted, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    file_appeal(owner, a1, "wrong")
    kept = post_to_thread(owner, activity, "perfectly fine")
    a2 = take_action(mod, kept, ModerationAction.Action.REMOVE, ReasonCode.SPAM)
    file_appeal(owner, a2, "also wrong")
    # Author-deleted but operator-un-hidden (PostAdmin escape hatch): the post is LIVE, so the
    # queue must not warn that an overturn "will not republish" it (agrees with
    # _reverse_action, which only declines the un-hide while the post IS hidden).
    republished = post_to_thread(owner, activity, "deleted then republished")
    delete_own_post(owner, republished)
    a3 = take_action(mod, republished, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    Post.objects.filter(pk=republished.pk).update(is_hidden=False)
    file_appeal(owner, a3, "third")
    staff = APIClient()
    staff.force_authenticate(mod)
    rows = staff.get("/api/safety/moderation/appeals/").json()
    flags = {row["action"]: row["content_author_deleted"] for row in rows}
    assert flags == {a1.id: True, a2.id: False, a3.id: False}
    # The single-instance path (no list batch — e.g. ResolveAppealView's response) must agree.
    from apps.safety.serializers import ModerationAppealSerializer

    singles = {
        ap.action_id: ModerationAppealSerializer(ap).data["content_author_deleted"]
        for ap in ModerationAppeal.objects.filter(action__in=[a1, a2, a3])
    }
    assert singles == flags


def test_appeal_queue_serialisation_query_cost_is_flat():
    # The staff queue batches the content_author_deleted generic-FK resolve (one query per
    # content type over up to 200 rows — apps/safety/views.py), never one per appeal. "Flat"
    # is asserted directly: the SAME request cost at two queue sizes (a pinned exact N would
    # flake under randomized suite order / warm caches without proving flatness).
    from django.db import connection
    from django.test.utils import CaptureQueriesContext

    from apps.social.services import delete_own_post, post_to_thread

    mod = _user("qf_mod", staff=True)
    staff = APIClient()
    staff.force_authenticate(mod)

    def _appeal(tag):
        owner = _user(f"qf_{tag}")
        activity = _activity(owner)
        post = post_to_thread(owner, activity, "my own words")
        delete_own_post(owner, post)
        action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
        file_appeal(owner, action, "wrong")

    for i in range(2):
        _appeal(f"a{i}")
    staff.get("/api/safety/moderation/appeals/")  # warm ContentType + route caches
    with CaptureQueriesContext(connection) as small:
        assert len(staff.get("/api/safety/moderation/appeals/").json()) == 2
    for i in range(3):
        _appeal(f"b{i}")
    with CaptureQueriesContext(connection) as large:
        assert len(staff.get("/api/safety/moderation/appeals/").json()) == 5
    assert len(small) == len(large)


def test_admin_content_note_blank_for_unhidden_author_deleted_post():
    # An operator un-hide (PostAdmin) leaves is_author_deleted=True on a LIVE post; the note
    # would claim "will not republish" about content that is already republished, so the
    # is_hidden gate turns it off (matching _reverse_action's decline condition).
    from django.contrib import admin as django_admin

    from apps.safety.admin import ModerationAppealAdmin
    from apps.social.models import Post
    from apps.social.services import delete_own_post, post_to_thread

    mod, owner = _user("cn_mod", staff=True), _user("cn_owner")
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "my own words")
    delete_own_post(owner, post)
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal = file_appeal(owner, action, "wrong")
    model_admin = ModerationAppealAdmin(ModerationAppeal, django_admin.site)
    assert "will not republish" in model_admin.content_note(appeal)
    Post.objects.filter(pk=post.pk).update(is_hidden=False)
    # Re-fetch: the generic-FK target is cached on the in-memory action instance.
    appeal = ModerationAppeal.objects.get(pk=appeal.pk)
    assert model_admin.content_note(appeal) == ""


# --- DRF surface (logged-in) --------------------------------------------------------------


def test_drf_user_files_appeal_against_own_action():
    mod, user = _user("dr_mod", staff=True), _user("dr_user")
    # A WARN keeps the account active, so the user has a normal API session to contest with.
    action = take_action(mod, user, ModerationAction.Action.WARN, ReasonCode.SPAM)
    client = APIClient()
    client.force_authenticate(user)
    resp = client.post(
        "/api/safety/appeals/", {"action_id": action.id, "statement": "not spam"}, format="json"
    )
    assert resp.status_code == 201
    assert ModerationAppeal.objects.filter(action=action, appellant=user).exists()
    listing = client.get("/api/safety/appeals/")
    assert listing.status_code == 200
    assert len(listing.json()) == 1


def test_drf_cannot_appeal_anothers_action_404():
    mod, user, other = _user("dr_mod2", staff=True), _user("dr_user2"), _user("dr_other2")
    action = take_action(mod, other, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    client = APIClient()
    client.force_authenticate(user)
    resp = client.post(
        "/api/safety/appeals/", {"action_id": action.id, "statement": "x"}, format="json"
    )
    assert resp.status_code == 404
    assert ModerationAppeal.objects.count() == 0


def test_drf_resolve_requires_moderator():
    mod, user = _user("dr_mod3", staff=True), _user("dr_user3")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    appeal = file_appeal(user, action, "please")
    plain = APIClient()
    plain.force_authenticate(user)
    plain_resp = plain.post(f"/api/safety/moderation/appeals/{appeal.id}/resolve/", {"grant": True})
    assert plain_resp.status_code == 403
    staff = APIClient()
    staff.force_authenticate(mod)
    resp = staff.post(
        f"/api/safety/moderation/appeals/{appeal.id}/resolve/", {"grant": True}, format="json"
    )
    assert resp.status_code == 200
    user.refresh_from_db()
    assert user.is_active is True


def test_drf_moderator_queue_lists_appeals():
    mod, user = _user("dr_mod4", staff=True), _user("dr_user4")
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    file_appeal(user, action, "please")
    staff = APIClient()
    staff.force_authenticate(mod)
    resp = staff.get("/api/safety/moderation/appeals/?status=pending")
    assert resp.status_code == 200
    assert len(resp.json()) == 1
    # The plain appellant must not reach the staff queue.
    plain = APIClient()
    plain.force_authenticate(user)
    assert plain.get("/api/safety/moderation/appeals/").status_code == 403
