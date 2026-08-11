"""targets_with_unlifted_remove: THE single implementation of "a standing (un-lifted) platform
REMOVE holds this content" — Reason B of the provenance model (Reason A is the author's own
``is_author_deleted``). The GDPR export and the media retention purge both key on this set,
layered ON the hidden flags (never substituting for ``is_hidden``: an admin manual hide has no
ModerationAction row). This matrix is the load-bearing contract for both consumers."""

import pytest
from django.contrib.contenttypes.models import ContentType
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.models import ModerationAction, ReasonCode
from apps.safety.services import (
    file_appeal,
    resolve_appeal,
    take_action,
    targets_with_unlifted_remove,
)
from apps.social.models import Activity, Post
from apps.social.services import create_activity, delete_own_post, post_to_thread
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, staff=False):
    u = User.objects.create_user(username=name, password="pw", display_name=name, is_staff=staff)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="ur-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="ur-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at=timezone.now()
    )


def test_targets_with_unlifted_remove_matrix():
    mod, owner = _user("ur_mod", staff=True), _user("ur_owner")
    activity = _activity(owner)

    # A standing REMOVE -> in the set.
    p_removed = post_to_thread(owner, activity, "removed")
    take_action(mod, p_removed, ModerationAction.Action.REMOVE, ReasonCode.OTHER)

    # A REMOVE lifted through a granted appeal -> NOT in the set (the platform let go).
    p_lifted = post_to_thread(owner, activity, "lifted")
    lifted_action = take_action(mod, p_lifted, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    resolve_appeal(mod, file_appeal(owner, lifted_action, "wrong"), grant=True)

    # REMOVE first, author deletes second -> STILL in the set (the platform's hold is
    # independent of the author's own act; this is the order the naive predicate gets wrong).
    p_both = post_to_thread(owner, activity, "both")
    take_action(mod, p_both, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    delete_own_post(owner, p_both)

    # Self-delete only (no action row) -> NOT in the set.
    p_self = post_to_thread(owner, activity, "self")
    delete_own_post(owner, p_self)

    # Admin-style manual hide (is_hidden with NO ModerationAction row) -> NOT in the set —
    # which is exactly why callers must layer this ON is_hidden, never replace it.
    p_admin = post_to_thread(owner, activity, "admin")
    Post.objects.filter(pk=p_admin.pk).update(is_hidden=True)

    # A WARN on the post -> NOT in the set (not a REMOVE).
    p_warn = post_to_thread(owner, activity, "warned")
    take_action(mod, p_warn, ModerationAction.Action.WARN, ReasonCode.SPAM)

    # Cross-content-type pk collision: a standing REMOVE on ANOTHER content type whose
    # target_id equals p_self's pk. The set is keyed on (target_type, target_id) — drop the
    # target_type filter from the query and this row leaks p_self into the set.
    ModerationAction.objects.create(
        moderator=mod,
        target_type=ContentType.objects.get_for_model(Activity),
        target_id=p_self.id,
        action=ModerationAction.Action.REMOVE,
        reason=ReasonCode.OTHER,
    )

    ids = [p_removed.id, p_lifted.id, p_both.id, p_self.id, p_admin.id, p_warn.id]
    # One batched call over every id -> exactly the standing-REMOVE subset.
    assert targets_with_unlifted_remove(Post, ids) == {p_removed.id, p_both.id}


def test_targets_with_unlifted_remove_empty_input_runs_no_query(django_assert_num_queries):
    # The empty-list short-circuit must avoid the query entirely (the common case for callers
    # with no candidate rows).
    with django_assert_num_queries(0):
        assert targets_with_unlifted_remove(Post, []) == set()
    with django_assert_num_queries(0):
        assert targets_with_unlifted_remove(Post, iter(())) == set()
