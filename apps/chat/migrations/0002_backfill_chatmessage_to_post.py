"""Backfill every realtime ChatMessage into the durable social.Post stream BEFORE the
ChatMessage table is dropped (thread-unification, "One Thread"). This closes the data-
visibility window: history must already live in Post before any read is repointed off chat.

Idempotent within a run and re-run-safe: it skips a (thread, author, body, created_at) tuple
that already exists as a Post, so a partial-then-retried migration never double-inserts. A
``redacted`` chat message lands as ``is_hidden=True`` (kept, never resurfaced); ordinary chat
messages become ordinary (non-announcement, top-level) Posts with their ORIGINAL timestamp.
"""

from django.db import migrations


def backfill(apps, schema_editor):
    ChatMessage = apps.get_model("chat", "ChatMessage")
    Post = apps.get_model("social", "Post")
    db = schema_editor.connection.alias

    created = 0
    for cm in ChatMessage.objects.using(db).order_by("created_at", "id").iterator():
        exists = (
            Post.objects.using(db)
            .filter(
                thread_id=cm.thread_id,
                author_id=cm.author_id,
                body=cm.body,
                created_at=cm.created_at,
            )
            .exists()
        )
        if exists:
            continue  # re-run safety: don't duplicate an already-backfilled message
        post = Post.objects.using(db).create(
            thread_id=cm.thread_id,
            author_id=cm.author_id,
            body=cm.body,
            is_announcement=False,
            is_hidden=cm.redacted,
            reply_to=None,
        )
        # created_at is auto_now_add and updated_at is auto_now, so both were stamped to the
        # migration-run time. Force BOTH back to the original chat timestamp: this preserves
        # message order AND keeps updated_at == created_at, so the unified stream's "edited"
        # marker (_is_edited) reads False for unedited imported history (it has no edit record).
        Post.objects.using(db).filter(pk=post.pk).update(
            created_at=cm.created_at, updated_at=cm.created_at
        )
        created += 1


def noop_reverse(apps, schema_editor):
    # One-way data move; the ChatMessage rows are retained until the next migration drops the
    # table, so a rollback simply leaves the backfilled Posts in place (harmless duplicates are
    # prevented on re-apply by the existence check above).
    pass


class Migration(migrations.Migration):
    dependencies = [
        ("chat", "0001_initial"),
        ("social", "0009_post_reply_to_post_social_post_thread__6db79a_idx"),
    ]

    operations = [migrations.RunPython(backfill, noop_reverse)]
