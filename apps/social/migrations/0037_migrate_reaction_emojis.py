# Data migration for ADR-0029: PostReaction.emoji is widened from a legacy emoji glyph to a facet
# slug. Existing rows are best-effort seeded onto the new appreciation facets:
#   👍/🙏 → helped_me · ❤️ → felt_welcome · 🎉/👏 → made_me_smile
# Because two old glyphs can collapse onto one slug, a user may end up with two rows for the same
# (post, slug), which would violate uq_post_reaction. We keep the EARLIEST such row and delete the
# colliders. Rows with an unrecognised emoji (none should exist — the set was fixed) are left as-is.
#
# IRREVERSIBLE (paired with 0036): the reverse is a documented no-op — the original glyphs cannot be
# reconstructed from the slugs (multiple glyphs collapsed onto one slug and colliders were deleted),
# so a downgrade would leave slug values in a re-narrowed column. Roll forward, never back.

from django.db import migrations

EMOJI_TO_SLUG = {
    "👍": "helped_me",
    "🙏": "helped_me",
    "❤️": "felt_welcome",
    "🎉": "made_me_smile",
    "👏": "made_me_smile",
}


def migrate_emojis_to_slugs(apps, schema_editor):
    PostReaction = apps.get_model("social", "PostReaction")
    seen = set()  # (post_id, user_id, slug) already kept
    to_delete = []
    # Earliest-first so the kept row is the oldest reaction when two glyphs merge onto one slug.
    for reaction in PostReaction.objects.order_by("created_at", "id").iterator():
        slug = EMOJI_TO_SLUG.get(reaction.emoji)
        if slug is None:
            continue  # unrecognised legacy value — leave untouched
        key = (reaction.post_id, reaction.user_id, slug)
        if key in seen:
            to_delete.append(reaction.id)
            continue
        seen.add(key)
        if reaction.emoji != slug:
            reaction.emoji = slug
            reaction.save(update_fields=["emoji"])
    if to_delete:
        PostReaction.objects.filter(id__in=to_delete).delete()


def noop(apps, schema_editor):
    # Irreversible: the original glyphs cannot be reconstructed from the slugs.
    pass


class Migration(migrations.Migration):
    dependencies = [
        ("social", "0036_alter_postreaction_emoji_postconcernstate_and_more"),
    ]

    operations = [
        migrations.RunPython(migrate_emojis_to_slugs, noop),
    ]
