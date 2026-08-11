from django.db import migrations, models

_CHUNK = 2000


def backfill_author_deleted(apps, schema_editor):
    """Mark posts their author had already self-deleted before the flag existed.

    Without this the fix is not retroactive: every historically self-deleted post is
    indistinguishable from a moderator REMOVE, so overturning a REMOVE on one would still
    republish content its author withdrew. The provenance is recoverable because
    ``delete_own_post`` has always written a ``post.self_deleted`` audit row whose
    ``target_ref`` is ``"social.post:<pk>"``.

    Read-only against AuditLog — the hash chain covers audit rows, which this never touches.
    ``event`` is unindexed, so this is a one-shot full scan of the audit table; acceptable for a
    single forward migration (and the table is small — production has never been provisioned).
    """
    AuditLog = apps.get_model("safety", "AuditLog")
    Post = apps.get_model("social", "Post")

    refs = AuditLog.objects.filter(event="post.self_deleted").values_list("target_ref", flat=True)
    batch = []
    for ref in refs.iterator(chunk_size=_CHUNK):
        prefix, _, pk = (ref or "").partition(":")
        if prefix != "social.post" or not pk.isdigit():
            continue
        batch.append(int(pk))
        # Flush per batch rather than collecting every pk first: the source scan is already
        # streamed, so holding the whole id set would be the only unbounded allocation here.
        if len(batch) >= _CHUNK:
            Post.objects.filter(pk__in=batch).update(is_author_deleted=True)
            batch.clear()
    if batch:
        Post.objects.filter(pk__in=batch).update(is_author_deleted=True)


def noop_reverse(apps, schema_editor):
    """Nothing to undo: RemoveField drops the column with the backfilled values."""


class Migration(migrations.Migration):
    dependencies = [
        ("social", "0038_postdissent_social_post_created_cd6036_idx"),
        # AuditLog is read by the backfill below.
        ("safety", "0001_initial"),
    ]

    operations = [
        migrations.AddField(
            model_name="post",
            name="is_author_deleted",
            field=models.BooleanField(default=False),
        ),
        migrations.RunPython(backfill_author_deleted, noop_reverse),
    ]
