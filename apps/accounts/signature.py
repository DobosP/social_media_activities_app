"""Avatar-style (signature avatar) domain services — ADR-0027.

A user picks which avatar *generation* renders their picture ("Choose your avatar style").
Internally each picked style carries a uniqueness guarantee: the canonical base render
(fixed size + fixed SVG id-namespace, intensity 0) is fingerprinted and held UNIQUE in
Postgres; a genuine collision bumps ``salt`` and re-rolls the seeded layout. The realistic
collision surface is the zero-interest identicon space (~11.8M variants — birthday-visible
at city scale); n>=1 renders already differ per unique username, so the registry is the
provable floor, not a fix for a common event.

Hard product rules (do not relax — reviewed against invariants #2/#3/#4):
* No collectible framing anywhere: no "minted"/"certificate"/serial numbers/dates on any
  surface, and the fingerprint is never serialized out or written to the audit log (the
  audit chain is permanent — a username-derived hash there would survive Art.17 erasure).
* Every generation is available to every user, always. Generations are NEVER unlocked by
  participation/progression — the chosen style is publicly visible through the render, so
  gating would leak activity.

Imports are lazy in both directions with ``recommendations.services`` (which needs this
module's remint hook, while this module needs its ``interest_graph``) — precedent:
``accounts/serializers.py`` lazy-imports the same stack.
"""

import hashlib
import logging

from django.db import IntegrityError, transaction
from django.utils.translation import gettext_lazy as _

from apps.safety.services import record_audit

from .avatars import (
    CANONICAL_PX,
    DEFAULT_GENERATION,
    FINGERPRINT_UID,
    GENERATIONS,
    render_generation,
    signature_seed,
)
from .models import SignatureAvatar

logger = logging.getLogger(__name__)

# Salt values tried before giving up. Exhaustion is astronomically unlikely (each salt
# re-rolls the full seeded layout); the cap is a runaway backstop, not an expected path.
MAX_SALT_ATTEMPTS = 16


class AvatarStyleError(Exception):
    """A style pick that cannot be honoured (unknown generation, or — theoretically —
    salt exhaustion). Message is user-safe."""


def _canonical_fingerprint(user, generation: int, salt: int) -> str:
    """sha256 of the canonical base render. Fixed px + fixed id-namespace + intensity 0, so
    byte-identical *visuals* fingerprint identically across users (the seed-derived uid would
    otherwise make every fingerprint trivially unique and the registry meaningless)."""
    from apps.recommendations.services import interest_graph

    nodes, edges = interest_graph(user)
    seed = signature_seed(user.username, generation, salt)
    svg = render_generation(
        generation,
        seed,
        nodes,
        edges,
        px=CANONICAL_PX,
        intensity=0.0,
        _uid_override=FINGERPRINT_UID,
    )
    return hashlib.sha256(svg.encode("utf-8")).hexdigest()


def _placeholder_fingerprint(user) -> str:
    """A per-user unique, non-visual placeholder so the row can be INSERTed before the salt
    loop runs. Never collides across users (public_id is unique); if a crash strands one, the
    next pick/refresh overwrites it and renders are unaffected (they read generation+salt)."""
    return hashlib.sha256(f"pending|{user.public_id}".encode()).hexdigest()


def _assign_unique_fingerprint(row, user) -> None:
    """Find a salt whose canonical fingerprint is free and save it on the (locked) ``row``.
    The row's CURRENT salt is tried first so an unchanged-visuals refresh is a true no-op and
    an established (collision-bumped) salt is never reset by an unrelated interest edit —
    layout continuity (review LOW). Each save attempt sits in its own savepoint: an
    IntegrityError inside an atomic block otherwise poisons the whole transaction. Because the
    caller holds the row lock and the row already exists, an IntegrityError here can only be
    the fingerprint UNIQUE constraint — the one that means "try another salt"."""
    order = [row.salt, *(s for s in range(MAX_SALT_ATTEMPTS) if s != row.salt)]
    for salt in order:
        fp = _canonical_fingerprint(user, row.generation, salt)
        if fp == row.fingerprint and row.salt == salt:
            return  # unchanged (e.g. an interest edit that didn't alter the visual)
        clash = SignatureAvatar.objects.filter(fingerprint=fp).exclude(user=user).exists()
        if clash:
            continue
        row.salt = salt
        row.fingerprint = fp
        try:
            with transaction.atomic():
                row.save(update_fields=["generation", "salt", "fingerprint", "updated_at"])
            return
        except IntegrityError:
            # Lost the race for this fingerprint to a concurrent mint — try the next salt.
            continue
    raise AvatarStyleError(_("Could not generate a unique picture; please try again."))


@transaction.atomic
def set_avatar_style(user, generation: int) -> SignatureAvatar:
    """Pick the avatar generation that renders this user's picture. Creates or updates their
    single SignatureAvatar row, guaranteeing fingerprint uniqueness via salt retry. Audited
    with the generation number ONLY (never the fingerprint — see module docstring)."""
    try:
        generation = int(generation)
    except (TypeError, ValueError):
        raise AvatarStyleError(_("Unknown avatar style.")) from None
    if generation not in GENERATIONS:
        raise AvatarStyleError(_("Unknown avatar style."))

    # get_or_create resolves the concurrent-first-pick race on the OneToOne internally (its
    # nested atomic catches the user_id IntegrityError and re-fetches); the placeholder
    # fingerprint is per-user unique so the INSERT itself can't collide on fingerprint.
    row, _created = SignatureAvatar.objects.get_or_create(
        user=user,
        defaults={"generation": generation, "fingerprint": _placeholder_fingerprint(user)},
    )
    row = SignatureAvatar.objects.select_for_update().get(pk=row.pk)
    if row.generation == generation and row.fingerprint == _canonical_fingerprint(
        user, generation, row.salt
    ):
        return row  # no-op re-pick: keep the existing salt/layout stable, skip the audit
    row.generation = generation
    _assign_unique_fingerprint(row, user)
    # Row lock before the global audit-tail lock — consistent order with every other service.
    record_audit("avatar.style_changed", actor=user, target=user, generation=generation)
    return row


def refresh_avatar_fingerprint(user) -> SignatureAvatar | None:
    """Re-fingerprint after the render inputs changed (interest edits). STRICT no-op for
    users without a row (seeding/imports call set_interests and must not create picks).
    Not audited: an interest edit is already its own event, and auditing here would put the
    global audit-tail lock on every interest save. Salt exhaustion (astronomically unlikely)
    is swallowed: an avatar-registry hiccup must never abort an unrelated interest edit —
    the row keeps its prior (still-unique) fingerprint until the next pick/refresh."""
    row = SignatureAvatar.objects.select_for_update().filter(user=user).first()
    if row is None:
        return None
    try:
        _assign_unique_fingerprint(row, user)
    except AvatarStyleError:
        logger.warning("avatar fingerprint refresh exhausted salts; keeping prior fingerprint")
    return row


def avatar_style_info(user) -> dict:
    """The self-surface payload for the style picker. Deliberately free of fingerprints,
    dates, and serials (no collectible framing) — just what's picked and what's available."""
    row = SignatureAvatar.objects.filter(user=user).only("generation").first()
    # An unknown (deprecated) pick reads as the default rather than KeyError-ing /me.
    current = row.generation if row and row.generation in GENERATIONS else DEFAULT_GENERATION
    return {
        "generation": current,
        "generation_name": str(GENERATIONS[current]["name"]),
        "available": [
            {"generation": g, "name": str(entry["name"])} for g, entry in GENERATIONS.items()
        ],
    }
