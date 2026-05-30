"""F26: crowd confirm/dispute of place<->activity edges. The vote tally lives in
ActivityEdgeVote (ingest never touches it); a quorum of confirms promotes an INFERRED edge to
origin=CONFIRMED (which ingest's PROTECTED_ORIGINS then won't overwrite); a quorum of disputes
sets the ingest-safe is_disputed flag, and read surfaces hide disputed edges. Moderators reverse."""

from django.conf import settings
from django.db import transaction

from apps.accounts.services import can_participate

from .models import DEFAULT_EDGE_QUORUM, ActivityEdgeVote, PlaceActivity


class EdgeError(Exception):
    """Expected, user-facing edge-vote error."""


class NotEligible(EdgeError):
    """User fails the verified+consented gate."""


class InvalidEdge(EdgeError):
    """The edge/place is not in a state that permits voting."""


def _quorum() -> int:
    return getattr(settings, "EDGE_QUORUM", DEFAULT_EDGE_QUORUM)


@transaction.atomic
def vote_on_edge(user, edge, vote) -> PlaceActivity:
    """Record (or change) one user's confirm/dispute of an edge, then re-evaluate it."""
    from .services import edge_is_publicly_visible

    if not can_participate(user):
        raise NotEligible("Verified, consented participation is required to vote on a place.")
    if not edge_is_publicly_visible(edge):
        raise InvalidEdge("This place isn't published yet.")
    if vote not in ActivityEdgeVote.Vote.values:
        raise InvalidEdge("Invalid vote.")
    ActivityEdgeVote.objects.update_or_create(edge=edge, user=user, defaults={"vote": vote})
    _reevaluate_edge(edge)
    return edge


def _reevaluate_edge(edge) -> None:
    """Re-tally and apply promotion/hide. MUST run inside an atomic block. Disputes are weighed
    BEFORE confirms (accuracy-first), and only an INFERRED edge is auto-hidden/promoted — a
    CONFIRMED (ingest-protected) edge is only ever reversed by a moderator (no crowd-griefing)."""
    n = _quorum()
    confirms = edge.edge_votes.filter(vote=ActivityEdgeVote.Vote.CONFIRM).count()
    disputes = edge.edge_votes.filter(vote=ActivityEdgeVote.Vote.DISPUTE).count()
    fields = set()
    if disputes >= n and edge.origin == PlaceActivity.Origin.INFERRED and not edge.is_disputed:
        edge.is_disputed = True
        fields.add("is_disputed")
    elif confirms >= n and edge.origin == PlaceActivity.Origin.INFERRED:
        edge.origin = PlaceActivity.Origin.CONFIRMED
        edge.is_disputed = False
        fields.update({"origin", "is_disputed"})
    if fields:
        edge.save(update_fields=[*fields, "updated_at"])


def edge_vote_summary(edge, user=None) -> dict:
    """Counts + the viewer's own vote ONLY — never a voter list (no identity exposure)."""
    my_vote = None
    if user is not None and getattr(user, "is_authenticated", False):
        row = edge.edge_votes.filter(user=user).first()
        my_vote = row.vote if row else None
    return {
        "confirms": edge.edge_votes.filter(vote=ActivityEdgeVote.Vote.CONFIRM).count(),
        "disputes": edge.edge_votes.filter(vote=ActivityEdgeVote.Vote.DISPUTE).count(),
        "required": _quorum(),
        "is_confirmed": edge.origin == PlaceActivity.Origin.CONFIRMED,
        "is_disputed": edge.is_disputed,
        "my_vote": my_vote,
    }


@transaction.atomic
def moderator_reverse_edge(moderator, edge, *, action) -> PlaceActivity:
    """Staff reversal. 'demote' -> origin back to INFERRED AND delete ALL votes (so a surviving
    confirm tally can't immediately re-promote); 'restore' -> clear the dispute hide + its
    votes; 'reset' -> clear hide + delete all votes, leaving origin as-is."""
    if not moderator.is_staff:
        raise NotEligible("Only staff may reverse an edge.")
    if action == "demote":
        edge.origin = PlaceActivity.Origin.INFERRED
        edge.is_disputed = False
        edge.edge_votes.all().delete()
        edge.save(update_fields=["origin", "is_disputed", "updated_at"])
    elif action == "restore":
        edge.is_disputed = False
        edge.edge_votes.filter(vote=ActivityEdgeVote.Vote.DISPUTE).delete()
        edge.save(update_fields=["is_disputed", "updated_at"])
    elif action == "reset":
        edge.is_disputed = False
        edge.edge_votes.all().delete()
        edge.save(update_fields=["is_disputed", "updated_at"])
    else:
        raise InvalidEdge("Unknown moderator action.")
    from apps.safety.services import record_audit

    record_audit(f"place.edge_{action}", actor=moderator, target=edge.place)
    return edge
