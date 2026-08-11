"""Migration 0039 backfill: recover author-delete provenance from the audit log.

The ``is_author_deleted`` guard is only retroactive if posts self-deleted BEFORE the field
existed are marked too — otherwise a granted appeal still republishes historical content its
author withdrew. Provenance is recoverable because ``delete_own_post`` has always written a
``post.self_deleted`` audit row. The backfill function is exercised directly (the repo has no
migration-executor harness); it takes the app registry, so the real one is a valid caller.
"""

from importlib import import_module

import pytest
from django.apps import apps as real_apps
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.services import record_audit
from apps.social.models import Post
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

# The module name starts with a digit, so it is only reachable via importlib.
backfill_author_deleted = import_module(
    "apps.social.migrations.0039_post_is_author_deleted"
).backfill_author_deleted

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="bf-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="bf-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at=timezone.now()
    )


def test_backfill_marks_historically_self_deleted_posts_only():
    author = _user("bf_author")
    activity = _activity(author)
    self_deleted = Post.objects.create(thread=activity.thread, author=author, body="withdrawn")
    mod_removed = Post.objects.create(thread=activity.thread, author=author, body="removed")
    untouched = Post.objects.create(thread=activity.thread, author=author, body="fine")

    # Simulate the pre-migration world: the audit row exists, the column does not yet reflect it.
    record_audit("post.self_deleted", actor=author, target=self_deleted)
    record_audit("moderation.action", actor=author, target=mod_removed)
    Post.objects.filter(pk__in=[self_deleted.pk, mod_removed.pk]).update(
        is_hidden=True, is_author_deleted=False
    )

    backfill_author_deleted(real_apps, None)

    assert Post.objects.get(pk=self_deleted.pk).is_author_deleted is True
    assert Post.objects.get(pk=mod_removed.pk).is_author_deleted is False
    assert Post.objects.get(pk=untouched.pk).is_author_deleted is False


def test_backfill_ignores_audit_rows_targeting_other_models():
    # target_ref is "<app>.<model>:<pk>", so the model prefix is what makes a pk a POST pk.
    # An audit row pointing at a non-post target must flag nothing, even though its pk is a
    # perfectly plausible Post pk.
    author = _user("bf_author2")
    activity = _activity(author)
    Post.objects.create(thread=activity.thread, author=author, body="fine")
    record_audit("post.self_deleted", actor=author, target=activity)

    backfill_author_deleted(real_apps, None)

    assert Post.objects.filter(is_author_deleted=True).count() == 0
