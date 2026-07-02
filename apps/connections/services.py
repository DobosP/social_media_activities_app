"""Connections domain logic — the single place the safety gates live (cohort isolation,
shared-activity precondition, blocking, minors-off). Both the web views and the DRF views call
these, so the gates hold identically on both surfaces.

Design notes tied to the hard invariants:
- DISCOVERY IS SEARCH-ONLY, NOT SUGGESTED. ``search_connectable`` requires a query and returns
  matches only among people you've shared an activity with; there is deliberately no
  "people you may know" feed (that would be an engagement pattern + a targeting surface).
- CONNECTIONS ARE COHORT-ISOLATED. ``can_connect`` requires the same cohort AND a shared
  activity (activities are cohort-pinned), so an adult<->minor connection is impossible — the
  same guarantee ``messaging.can_message`` makes. All cohorts are enabled by default, each
  within its own cohort; UNASSIGNED is never allowed (see ``_allowed_cohorts``).
- NO BEHAVIOURAL ROLLUP. Eligibility is derived live from ``social.Membership``; the
  Connection row stores no counts / "met N times" / reliability.
"""

from django.conf import settings
from django.db import transaction
from django.db.models import Q
from django.utils import timezone

from apps.accounts.models import Cohort, User
from apps.accounts.services import can_participate

from .models import Connection


class ConnectionError(Exception):
    """Base for expected, user-facing connection-domain errors."""


class NotEligible(ConnectionError):
    """The pair fails the connect gate (cohort / blocking / no shared activity / minors-off)."""


class InvalidState(ConnectionError):
    """The target connection isn't in a state that permits this action."""


def _allowed_cohorts() -> set:
    """Which cohorts may use connections, WITHIN their own cohort. Cross-age connection stays
    impossible regardless — ``can_connect`` requires the SAME cohort, so this never opens an
    adult<->minor path; it only governs whether each age group can connect among its own peers.
    All ages are enabled by default (children inherit the participation/consent gate and the
    messaging guardian-observer on any resulting chat); UNASSIGNED is never allowed."""
    allowed = set(getattr(settings, "CONNECTIONS_ALLOWED_COHORTS", (Cohort.ADULT,)))
    allowed.discard(Cohort.UNASSIGNED)
    return allowed


def _peer_activity_ids(user):
    """Activity ids where ``user`` is a PEER participant — a current member who is NOT a
    supervisory guardian (mirrors social.voting_members). A guardian's read-only supervisory
    membership must NOT establish a 'shared activity' for connections: it would draw a connect
    affordance toward a child's activity and let two co-supervising guardians connect via it."""
    from apps.social.models import Membership

    return (
        Membership.objects.filter(user=user, state=Membership.State.MEMBER)
        .exclude(role=Membership.Role.GUARDIAN)
        .values("activity_id")
    )


def shares_activity(a, b) -> bool:
    """True iff a and b are both PEER members (not supervisory guardians) of at least one shared
    Activity — the in-person-meetup precondition. Derived at call time (no stored rollup). A
    member who LEFT (state=REMOVED) no longer counts."""
    from apps.social.models import Membership

    return (
        Membership.objects.filter(
            user=b, state=Membership.State.MEMBER, activity_id__in=_peer_activity_ids(a)
        )
        .exclude(role=Membership.Role.GUARDIAN)
        .exists()
    )


def can_connect(a, b) -> bool:
    """The single hard gate, modelled on messaging.can_message + a shared-activity precondition.
    Same cohort, both verified+consented, not blocked either way, not self, cohort allowed by
    launch policy, AND a real shared activity. An adult<->minor connection is impossible."""
    if not a or not b or a.id == b.id:
        return False
    if not getattr(a, "is_authenticated", False) or not b.is_active:
        return False
    if a.cohort == Cohort.UNASSIGNED or b.cohort == Cohort.UNASSIGNED:
        return False
    if a.cohort != b.cohort:
        return False
    if a.cohort not in _allowed_cohorts() or b.cohort not in _allowed_cohorts():
        return False
    if not can_participate(a) or not can_participate(b):
        return False
    from apps.safety.services import is_blocked

    if is_blocked(a, b):
        return False
    return shares_activity(a, b)


def are_connected(a, b) -> bool:
    return (
        Connection.objects.filter(status=Connection.Status.ACCEPTED)
        .filter(Q(requester=a, addressee=b) | Q(requester=b, addressee=a))
        .exists()
    )


def _related_user_ids(user) -> set:
    """User ids the viewer already has an OPEN (pending/accepted) connection row with, so the
    search never re-surfaces them. Declined/withdrawn/removed are NOT open, so a fresh request
    is allowed again."""
    open_states = [Connection.Status.PENDING, Connection.Status.ACCEPTED]
    rows = Connection.objects.filter(status__in=open_states).filter(
        Q(requester=user) | Q(addressee=user)
    )
    ids = set()
    for c in rows:
        ids.add(c.requester_id)
        ids.add(c.addressee_id)
    return ids


def search_connectable(user, query, *, limit=20):
    """SEARCH-ONLY discovery: look up people you've shared an activity with, by name. Returns []
    for an empty/short query — there is NO browse-all / suggestions feed. Results are restricted
    to the same cohort, exclude blocked + already-related users, and are a best-effort surface;
    ``request_connection`` re-applies the full hard gate."""
    query = (query or "").strip()
    if len(query) < 2 or user.cohort not in _allowed_cohorts():
        return []
    from apps.safety.services import blocked_user_ids
    from apps.social.models import Membership

    # Peer co-members (NOT supervisory guardians) of my peer activities — computed on a single
    # Membership row so the role/state/activity conditions apply to the SAME membership.
    co_member_ids = (
        Membership.objects.filter(
            activity_id__in=_peer_activity_ids(user), state=Membership.State.MEMBER
        )
        .exclude(role=Membership.Role.GUARDIAN)
        .values_list("user_id", flat=True)
    )
    pool = (
        User.objects.filter(id__in=co_member_ids, cohort=user.cohort, is_active=True)
        .filter(Q(display_name__icontains=query) | Q(username__icontains=query))
        .exclude(id=user.id)
        .distinct()
    )
    excluded = blocked_user_ids(user) | _related_user_ids(user)
    return [u for u in pool[: limit * 3] if u.id not in excluded][:limit]


@transaction.atomic
def request_connection(requester, addressee) -> Connection:
    """Send (or auto-accept) a connection request. A reciprocal pending request auto-accepts
    (no awkward double-handshake). Re-applies the full gate at request time."""
    from apps.notifications.models import Notification
    from apps.notifications.services import notify
    from apps.safety.services import allow_action, record_audit

    if not can_connect(requester, addressee):
        raise NotEligible("You can't connect with this person.")
    if are_connected(requester, addressee):
        raise InvalidState("You're already connected.")
    existing = Connection.objects.filter(requester=requester, addressee=addressee).first()
    if existing is not None and existing.status == Connection.Status.PENDING:
        return existing  # idempotent: a repeat request never re-notifies (no pestering loop)
    reverse = Connection.objects.filter(
        requester=addressee, addressee=requester, status=Connection.Status.PENDING
    ).first()
    if reverse is not None:  # they already asked you — accept it
        reverse.status = Connection.Status.ACCEPTED
        reverse.decided_at = timezone.now()
        reverse.save(update_fields=["status", "decided_at"])
        record_audit("connection.accepted", actor=requester, target=addressee)
        _notify_accepted(reverse, by=requester)
        return reverse
    # A genuinely new (or resurrected-after-decline) request: rate-limit it so a declined
    # request can't be replayed into unbounded "X would like to connect" notices.
    limit = getattr(settings, "CONNECTIONS_REQUEST_RATE_LIMIT", 20)
    window = getattr(settings, "CONNECTIONS_REQUEST_RATE_WINDOW_SECONDS", 3600)
    if not allow_action(requester, "connection_request", limit=limit, window_seconds=window):
        raise NotEligible("You're sending connection requests too quickly; slow down.")
    conn, _ = Connection.objects.update_or_create(
        requester=requester,
        addressee=addressee,
        defaults={"status": Connection.Status.PENDING, "decided_at": None},
    )
    record_audit("connection.requested", actor=requester, target=addressee)
    notify(
        addressee,
        Notification.Kind.CONNECTION_REQUEST,
        f"{_name(requester)} would like to connect",
        url="/connections/",
    )
    return conn


@transaction.atomic
def respond_to_connection(addressee, connection, *, accept: bool) -> Connection:
    """The addressee accepts or declines a pending request. Re-gates on accept (catches a
    cohort change / new block / consent lapse between request and accept)."""
    from apps.safety.services import record_audit

    if connection.addressee_id != addressee.id:
        raise NotEligible("That isn't your request to answer.")
    if connection.status != Connection.Status.PENDING:
        raise InvalidState("This request is no longer pending.")
    if accept:
        if not can_connect(addressee, connection.requester):
            raise NotEligible("You can't connect with this person.")
        connection.status = Connection.Status.ACCEPTED
        connection.decided_at = timezone.now()
        connection.save(update_fields=["status", "decided_at"])
        record_audit("connection.accepted", actor=addressee, target=connection.requester)
        _notify_accepted(connection, by=addressee)
    else:
        connection.status = Connection.Status.DECLINED
        connection.decided_at = timezone.now()
        connection.save(update_fields=["status", "decided_at"])
    return connection


@transaction.atomic
def withdraw_request(requester, connection) -> Connection:
    if connection.requester_id != requester.id:
        raise NotEligible("That isn't your request to withdraw.")
    if connection.status != Connection.Status.PENDING:
        raise InvalidState("This request is no longer pending.")
    connection.status = Connection.Status.WITHDRAWN
    connection.decided_at = timezone.now()
    connection.save(update_fields=["status", "decided_at"])
    return connection


@transaction.atomic
def remove_connection(user, other) -> None:
    """Symmetric unfriend — either party can sever an accepted connection."""
    from apps.safety.services import record_audit

    rows = Connection.objects.filter(status=Connection.Status.ACCEPTED).filter(
        Q(requester=user, addressee=other) | Q(requester=other, addressee=user)
    )
    n = rows.update(status=Connection.Status.REMOVED, decided_at=timezone.now())
    if n:
        record_audit("connection.removed", actor=user, target=other)


def connections_for(user):
    """The user's accepted connections (the OTHER person), filtered by blocking at read time."""
    from apps.safety.services import blocked_user_ids

    qs = (
        Connection.objects.filter(status=Connection.Status.ACCEPTED)
        .filter(Q(requester=user) | Q(addressee=user))
        .select_related("requester", "addressee")
    )
    blocked = blocked_user_ids(user)
    out = []
    for c in qs:
        other = c.addressee if c.requester_id == user.id else c.requester
        if other.id not in blocked:
            out.append(other)
    return out


def pending_incoming(user):
    return (
        Connection.objects.filter(addressee=user, status=Connection.Status.PENDING)
        .select_related("requester")
        .order_by("-created_at")
    )


def pending_outgoing(user):
    return (
        Connection.objects.filter(requester=user, status=Connection.Status.PENDING)
        .select_related("addressee")
        .order_by("-created_at")
    )


def open_conversation(user, other):
    """One tap from a connection into the EXISTING E2EE messaging. Requires an accepted
    connection; never bypasses messaging's own gate."""
    if not are_connected(user, other):
        raise NotEligible("Connect with this person before starting a chat.")
    from apps.messaging.services import start_direct

    return start_direct(user, other)


def is_enabled_for(user) -> bool:
    """Whether the connections feature is available to this user (cohort allowed by policy)."""
    return getattr(user, "is_authenticated", False) and user.cohort in _allowed_cohorts()


def related_user_ids(user) -> set:
    """Public accessor: user ids the viewer already has an open (pending/accepted) connection
    with — so a 'Connect' button isn't shown for someone already connected/requested."""
    return _related_user_ids(user)


def _name(user) -> str:
    return user.display_name or user.username


def _notify_accepted(connection, *, by) -> None:
    from apps.notifications.models import Notification
    from apps.notifications.services import notify

    other = connection.requester if by.id == connection.addressee_id else connection.addressee
    notify(
        other,
        Notification.Kind.CONNECTION_ACCEPTED,
        f"{_name(by)} accepted your connection",
        url="/connections/",
    )
