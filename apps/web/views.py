"""Server-rendered web UI over the (API-first) backend. Views call the same domain
services the API uses, so the safety invariants (cohort isolation, consent gating,
membership-scoped media) hold identically here."""

import math
from datetime import datetime, timedelta

from django.conf import settings
from django.contrib import messages
from django.contrib.auth import login, logout
from django.contrib.auth.decorators import login_required
from django.contrib.auth.views import LoginView
from django.core.cache import cache
from django.core.exceptions import ValidationError
from django.db import transaction
from django.http import Http404
from django.shortcuts import get_object_or_404, redirect, render
from django.utils import timezone
from django.utils.crypto import get_random_string
from django.utils.http import url_has_allowed_host_and_scheme
from django.views.decorators.http import require_POST

from apps.accounts.identity.base import IdentityVerificationError
from apps.accounts.identity.registry import get_identity_provider
from apps.accounts.models import AgeBand, GuardianRelationship, User
from apps.accounts.services import (
    accept_guardian_link_invite,
    apply_assurance,
    assurance_provenance,
    can_participate,
    create_guardian_link_invite,
    decline_guardian_link_invite,
    guardianship_capabilities,
    is_guardian_of,
    minor_onboarding_enabled,
    pending_guardian_invites_for,
    revoke_guardian,
)
from apps.events.models import Event
from apps.media.models import Photo
from apps.media.services import (
    MediaError,
    NotAuthorized,
    signed_url,
    thread_photos,
    upload_photo,
)
from apps.notifications import services as notifications
from apps.notifications.models import Notification
from apps.places.filters import PlaceFilter
from apps.places.models import Place
from apps.places.services import (
    accessibility_facts,
    accessibility_facts_display,
    get_access_preference,
    matches_access_preference,
    partner_for_place,
    set_access_preference,
)
from apps.recommendations import services as recs
from apps.recommendations.models import UserInterest
from apps.safety import services as safety
from apps.safety.models import Block
from apps.social import services as social
from apps.social.models import Activity, JoinVote, Membership, Post
from apps.taxonomy.models import ActivityType

from .forms import (
    _DT_FORMATS,
    ActivityEditForm,
    ActivityForm,
    DonateForm,
    PlaceProposeForm,
    PostForm,
    RegisterForm,
    ReportForm,
)

# --- Connections (find + reconnect with people you've shared real activities with) ------


@login_required
def connections_page(request):
    """Your connections + pending requests, and a SEARCH box (query-only, no suggestions feed).
    Search is restricted to people you've shared an activity with, in your own cohort."""
    from apps.connections import services as connections

    if not connections.is_enabled_for(request.user):
        messages.info(request, "Connections aren't available for your account yet.")
        return redirect("home")
    query = request.GET.get("q", "")
    return render(
        request,
        "web/connections.html",
        {
            "connections": connections.connections_for(request.user),
            "incoming": connections.pending_incoming(request.user),
            "outgoing": connections.pending_outgoing(request.user),
            "query": query,
            "results": connections.search_connectable(request.user, query),
            **_nav_context(request.user),
        },
    )


@login_required
@require_POST
def connection_request(request):
    from apps.connections import services as connections

    target = get_object_or_404(User, public_id=request.POST.get("public_id"))
    try:
        connections.request_connection(request.user, target)
        messages.success(request, "Connection request sent.")
    except connections.ConnectionError as exc:
        messages.error(request, _msg(exc))
    # _safe_next rejects an off-site/MITM'd ?next (open-redirect guard), like block_user_view.
    return redirect(_safe_next(request, "connections"))


@login_required
@require_POST
def connection_respond(request, pk):
    from apps.connections import services as connections
    from apps.connections.models import Connection

    conn = get_object_or_404(Connection, pk=pk)
    accept = request.POST.get("accept") == "1"
    try:
        connections.respond_to_connection(request.user, conn, accept=accept)
        messages.success(request, "Connected." if accept else "Request declined.")
    except connections.ConnectionError as exc:
        messages.error(request, _msg(exc))
    return redirect("connections")


@login_required
@require_POST
def connection_withdraw(request, pk):
    from apps.connections import services as connections
    from apps.connections.models import Connection

    conn = get_object_or_404(Connection, pk=pk)
    try:
        connections.withdraw_request(request.user, conn)
    except connections.ConnectionError as exc:
        messages.error(request, _msg(exc))
    return redirect("connections")


@login_required
@require_POST
def connection_remove(request):
    from apps.connections import services as connections

    target = get_object_or_404(User, public_id=request.POST.get("public_id"))
    connections.remove_connection(request.user, target)
    messages.success(request, "Connection removed.")
    return redirect("connections")


@login_required
@require_POST
def connection_message(request):
    """One tap from a connection into the existing E2EE messaging."""
    from apps.connections import services as connections

    target = get_object_or_404(User, public_id=request.POST.get("public_id"))
    try:
        connections.open_conversation(request.user, target)
        return redirect("messages")
    except connections.ConnectionError as exc:
        messages.error(request, _msg(exc))
        return redirect("connections")


def _msg(exc) -> str:
    if isinstance(exc, ValidationError):
        return "; ".join(exc.messages)
    return str(exc)


def _order_feed_by_location(qs, params):
    """Order an activity feed closest-first when the page carries ?near_lon/&near_lat
    (opt-in "near me"), else soonest-first. Coordinates are used only for this query and
    are never stored (privacy: no location tracking). Returns (queryset, near_active)."""
    from apps.discovery.proximity import apply_proximity

    qs, point = apply_proximity(qs, params, field="place__location")
    if point is None:
        qs = qs.order_by("starts_at")
    return qs, point is not None


def _nav_context(user):
    if user.is_authenticated:
        from apps.connections.services import is_enabled_for as connections_enabled

        return {
            "unread_notifications": notifications.unread_count(user),
            # Show the ward-side "Guardians" link only to users who actually have one (F13).
            "has_guardians": GuardianRelationship.objects.filter(
                ward=user, status=GuardianRelationship.Status.ACTIVE
            ).exists(),
            "connections_enabled": connections_enabled(user),
        }
    return {}


def _avatar_url(viewer, target_user):
    photo = Photo.objects.filter(uploader=target_user, kind=Photo.Kind.PROFILE).first()
    if photo is None:
        return None
    try:
        return signed_url(photo, viewer)
    except NotAuthorized:
        return None


# --- Auth ---------------------------------------------------------------------------


def _client_ip(request) -> str:
    """Best-effort real client IP. Honour X-Forwarded-For only behind the number of
    trusted proxies the deployment declares (settings.NUM_PROXIES); otherwise the XFF
    header is attacker-controlled and would let a single host evade per-IP lockout by
    spoofing it. With NUM_PROXIES=0 we always trust REMOTE_ADDR."""
    num_proxies = getattr(settings, "NUM_PROXIES", 0)
    if num_proxies:
        forwarded = request.META.get("HTTP_X_FORWARDED_FOR", "")
        parts = [p.strip() for p in forwarded.split(",") if p.strip()]
        if len(parts) >= num_proxies:
            # The right-most NUM_PROXIES entries are appended by our own proxies; the
            # entry just before them is the closest IP we can trust.
            return parts[-num_proxies]
    return request.META.get("REMOTE_ADDR", "") or "unknown"


def _login_attempt_key(username: str, ip: str) -> str:
    return f"web:login-failures:{(username or '').lower()}:{ip}"


class ThrottledLoginView(LoginView):
    """Stock Django login with an app-layer brute-force brake: too many failed attempts
    for the same (username, real client IP) within a rolling window locks that pair out.
    Stock ``LoginView`` is not a DRF view, so DRF throttles never apply to it; this gives
    the web login the per-IP brake the API already has, with no new dependency."""

    template_name = "web/login.html"

    @property
    def _limit(self) -> int:
        return getattr(settings, "LOGIN_FAILURE_LIMIT", 10)

    @property
    def _window(self) -> int:
        return getattr(settings, "LOGIN_FAILURE_WINDOW_SECONDS", 900)

    def _attempt_key(self) -> str:
        # AuthenticationForm posts the identifier under "username" regardless of the
        # user model's USERNAME_FIELD.
        username = self.request.POST.get("username", "")
        return _login_attempt_key(username, _client_ip(self.request))

    def _is_locked_out(self, key) -> bool:
        return (cache.get(key) or 0) >= self._limit

    def _record_failure(self, key) -> None:
        # Fixed-window counter; the first failure seeds the TTL so the window expires.
        try:
            cache.incr(key)
        except ValueError:
            cache.set(key, 1, self._window)

    def post(self, request, *args, **kwargs):
        if self._is_locked_out(self._attempt_key()):
            form = self.get_form()
            form.add_error(
                None,
                "Too many failed login attempts. Please wait a few minutes and try again.",
            )
            # Do not count the lock-out response itself as another failed attempt.
            self._locked_out = True
            return self.form_invalid(form)
        return super().post(request, *args, **kwargs)

    def form_valid(self, form):
        # A successful login clears the counter for this (username, IP) pair.
        cache.delete(self._attempt_key())
        return super().form_valid(form)

    def form_invalid(self, form):
        if not getattr(self, "_locked_out", False):
            self._record_failure(self._attempt_key())
        return super().form_invalid(form)


def register(request):
    if request.user.is_authenticated:
        return redirect("home")
    if request.method == "POST":
        form = RegisterForm(request.POST)
        if form.is_valid():
            data = form.cleaned_data
            if User.objects.filter(username=data["username"]).exists():
                form.add_error("username", "That username is taken.")
            else:
                # Account creation + age assurance must succeed or fail together: a provider
                # error must never leave an orphan, un-verified account behind. If the
                # configured identity provider (e.g. EUDI in prod) cannot establish an age
                # band here, roll back the half-built account and send the user back to
                # the sign-up form to retry age verification, rather than 500-ing on the
                # primary onboarding path. (verify_age itself is login-gated, so we can't
                # send a not-yet-authenticated registrant straight there.)
                try:
                    with transaction.atomic():
                        user = User.objects.create_user(
                            username=data["username"], password=data["password"]
                        )
                        user.display_name = data["display_name"]
                        user.save(update_fields=["display_name"])
                        result = get_identity_provider().verify(user, age_band=data["age_band"])
                        apply_assurance(user, result)
                except IdentityVerificationError:
                    messages.error(
                        request,
                        "We couldn't verify your age automatically. Your account wasn't "
                        "created - please try again and complete age verification.",
                    )
                    return redirect("register")
                login(request, user)
                if data["age_band"] == AgeBand.UNDER_16 and not can_participate(user):
                    messages.info(
                        request,
                        "Welcome! As an under-16 you can browse, but a parent/guardian must "
                        "approve your account before you can join or organize activities.",
                    )
                else:
                    messages.success(request, f"Welcome, {user.display_name or user.username}!")
                return redirect("home")
    else:
        form = RegisterForm()
    return render(request, "web/register.html", {"form": form})


# --- Home / discovery ---------------------------------------------------------------


def home(request):
    if not request.user.is_authenticated:
        return render(request, "web/landing.html")
    user = request.user
    recommended = recs.recommend_activities(user, limit=8)
    # F17: an HONEST reason per card, computed from the viewer's OWN declared interests (one
    # query, no per-card N+1). "matches your interest in X" is emitted only when the activity's
    # type is actually in that set; cold-start (no distance) is labelled "soonest first"; an
    # uncategorised similarity keeps the genuine "% match".
    interest_names = dict(
        UserInterest.objects.filter(user=user).values_list(
            "activity_type__slug", "activity_type__name"
        )
    )
    for a in recommended:
        distance = getattr(a, "rec_distance", None)
        if distance is not None:
            a.match_pct = max(0, min(100, round((1 - float(distance)) * 100)))
        if distance is None:  # `is None`, never falsy — a perfect match is distance 0.0
            a.rec_reason = "soonest first"
        elif a.activity_type.slug in interest_names:
            a.rec_reason = f"matches your interest in {interest_names[a.activity_type.slug]}"
    beginners_only = request.GET.get("beginners") == "true"
    upcoming_qs = (
        social.visible_activities(user)
        .filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
        .select_related("place", "activity_type", "owner")
    )
    if beginners_only:
        upcoming_qs = upcoming_qs.filter(beginners_welcome=True)
    upcoming_qs, near_active = _order_feed_by_location(upcoming_qs, request.GET)
    upcoming = upcoming_qs[:20]
    # "Your activities" shows only live meetups: a cancelled/completed one shouldn't sit in
    # the active list pulling members toward a meetup that isn't happening (F1 lifecycle).
    mine = (
        social.visible_activities(user)
        .filter(
            memberships__user=user,
            memberships__state=Membership.State.MEMBER,
            status=Activity.Status.OPEN,
        )
        .select_related("place", "activity_type")
        .distinct()
        .order_by("starts_at")
    )
    events = (
        Event.objects.filter(starts_at__gte=timezone.now())
        .select_related("place", "activity_type")
        .order_by("starts_at")[:6]
    )
    return render(
        request,
        "web/home.html",
        {
            "recommended": recommended,
            "upcoming": upcoming,
            "mine": mine,
            "events": events,
            "near_active": near_active,
            "beginners_only": beginners_only,
            "guardian_invites": list(pending_guardian_invites_for(user)),
            **_nav_context(user),
        },
    )


def places_map(request):
    return render(request, "web/places.html", _nav_context(request.user))


def places_list(request):
    """F16: a server-rendered, JS-free text list of places (works with no map/tiles), mirroring
    the public places API's filtering + proximity. Public data; no per-user location stored."""
    from apps.discovery.proximity import apply_proximity
    from apps.places.services import public_places

    # F25: hide pending user-proposed places from the public list.
    qs = public_places(Place.objects.prefetch_related("place_activities__activity"))
    # .distinct() is load-bearing: PlaceFilter joins place_activities for ?activity/?min_confidence
    # and would otherwise multiply rows (mirrors PlaceViewSet.get_queryset).
    qs = PlaceFilter(request.GET, queryset=qs).qs.distinct()
    qs, point = apply_proximity(qs, request.GET)  # field="location" (default), like the API
    if point is None:
        qs = qs.order_by("name", "id")
    capped = list(qs[:200])
    # F15 compose: attach the venue's positive accessibility facts as terse badges per row.
    for p in capped:
        p.access_tags = [
            r for r in accessibility_facts_display(p) if r["state"] in ("true", "limited")
        ]
    return render(
        request,
        "web/places_list.html",
        {
            "places": capped,
            "near_active": point is not None,
            "truncated": len(capped) == 200,
            "filters": {
                "activity": request.GET.get("activity", ""),
                "city": request.GET.get("city", ""),
                "source": request.GET.get("source", ""),
            },
            **_nav_context(request.user),
        },
    )


def place_detail(request, pk):
    from apps.places.services import public_places

    place = get_object_or_404(Place.objects.prefetch_related("place_activities__activity"), pk=pk)
    # F25: a still-pending user place is viewable ONLY by its proposer or staff (404 otherwise),
    # so the quorum isn't bypassed and the public never sees it before it's published.
    proposal = getattr(place, "proposal", None)
    is_public = public_places().filter(pk=place.pk).exists()
    is_proposer = (
        request.user.is_authenticated
        and proposal is not None
        and proposal.proposer_id == request.user.id
    )
    if not (is_public or request.user.is_staff or is_proposer):
        raise Http404("No place matches the given query.")
    pending = proposal is not None and proposal.status != proposal.Status.PUBLISHED
    meetups = []
    if request.user.is_authenticated:
        meetups = (
            social.visible_activities(request.user)
            .filter(place=place, status=Activity.Status.OPEN, starts_at__gte=timezone.now())
            .select_related("activity_type", "owner")
            .order_by("starts_at")
        )
    events = (
        Event.objects.filter(place=place, starts_at__gte=timezone.now())
        .select_related("activity_type")
        .order_by("starts_at")
    )
    # F15: honest accessibility facts derived from OSM tags, plus a soft match badge when the
    # viewer has set an access preference (get_access_preference returns None for anonymous).
    pref = get_access_preference(request.user)
    access_match = matches_access_preference(accessibility_facts(place), pref)
    # F26: activity edges with per-viewer vote summaries; F28: open-now status from parsed hours,
    # downgraded to "unverified" when recent reports say the posted hours are wrong.
    from apps.places.edges import edge_vote_summary
    from apps.places.services import open_now_status

    edges = [
        pa for pa in place.place_activities.all() if not pa.is_disputed or request.user.is_staff
    ]
    for edge in edges:
        edge.summary = edge_vote_summary(edge, request.user)
    can_contribute = is_public and request.user.is_authenticated and can_participate(request.user)
    return render(
        request,
        "web/place_detail.html",
        {
            "place": place,
            "meetups": meetups,
            "events": events,
            "edges": edges,
            "can_contribute": can_contribute,
            "open_now": open_now_status(place),
            "access_facts": accessibility_facts_display(place),
            "access_match": access_match,
            "has_access_pref": pref is not None,
            "partner": partner_for_place(place),
            "pending_proposal": proposal if pending else None,
            **_nav_context(request.user),
        },
    )


@login_required
def place_propose(request):
    """F25: add a venue OSM missed. Creates a pending user place + a co-creation proposal."""
    if not can_participate(request.user):
        messages.error(
            request, "You need to be verified (and consented, if a minor) to add a place."
        )
        return redirect("profile")
    if request.method == "POST":
        form = PlaceProposeForm(request.POST)
        if form.is_valid():
            d = form.cleaned_data
            try:
                proposal = social.propose_place_with_venue(
                    request.user,
                    name=d["name"],
                    lon=d["lon"],
                    lat=d["lat"],
                    activity_type=d["activity_type"],
                    allow_nearby=d["allow_nearby"],
                )
            except social.DuplicatePlace as exc:
                if exc.soft:
                    messages.warning(
                        request,
                        f"A place already exists very close ({exc.place_name}). If yours is "
                        "different, tick 'Add anyway' and resubmit.",
                    )
                else:
                    messages.error(request, f"That place already exists: {exc.place_name}.")
                    return redirect("place_detail", pk=exc.place_id)
            except social.SocialError as exc:
                messages.error(request, _msg(exc))
            else:
                messages.success(
                    request, "Thanks! Your place is pending — neighbours can now confirm it."
                )
                return redirect("place_detail", pk=proposal.place_id)
    else:
        form = PlaceProposeForm()
    return render(request, "web/place_propose.html", {"form": form, **_nav_context(request.user)})


@login_required
def places_pending(request):
    """F25: user places awaiting confirmation by other verified neighbours (counts only — no
    proposer/confirmer identity is shown)."""
    return render(
        request,
        "web/places_pending.html",
        {"proposals": social.pending_proposals_for(request.user), **_nav_context(request.user)},
    )


@login_required
@require_POST
def place_confirm(request, proposal_id):
    """F25: one-tap confirmation of a pending place by another verified user."""
    from apps.social.models import UserPlaceProposal

    proposal = get_object_or_404(UserPlaceProposal, pk=proposal_id)
    try:
        social.confirm_place(request.user, proposal)
        messages.success(request, "Thanks for confirming this place.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("places_pending")


@login_required
@require_POST
def edge_vote(request, pk, edge_id):
    """F26: confirm or dispute that a place supports an activity."""
    from apps.places.edges import EdgeError, vote_on_edge
    from apps.places.models import PlaceActivity

    edge = get_object_or_404(PlaceActivity, pk=edge_id, place_id=pk)
    try:
        vote_on_edge(request.user, edge, request.POST.get("vote"))
        messages.success(request, "Thanks - your feedback was recorded.")
    except EdgeError as exc:
        messages.error(request, str(exc))
    return redirect("place_detail", pk=pk)


@login_required
@require_POST
def place_open_now_report(request, pk):
    """F28: report that a venue's posted opening hours are wrong (closed when it said open)."""
    from apps.places.services import NotEligible, file_open_now_report

    place = get_object_or_404(Place, pk=pk)
    try:
        result = file_open_now_report(request.user, place)
    except NotEligible as exc:
        messages.error(request, str(exc))
    else:
        if result is None:
            messages.info(request, "Thanks - we already have your report for this place.")
        else:
            messages.success(request, "Thanks - we'll flag the hours if others agree.")
    return redirect("place_detail", pk=pk)


@login_required
@require_POST
def place_open_now_reset(request, pk):
    """F28: staff reset of accumulated open-now reports for a place."""
    from apps.places.services import clear_open_now_reports

    if not request.user.is_staff:
        raise Http404("Not found.")
    place = get_object_or_404(Place, pk=pk)
    clear_open_now_reports(place, moderator=request.user)
    messages.success(request, "Opening-hours reports cleared.")
    return redirect("place_detail", pk=pk)


# --- Activities ---------------------------------------------------------------------


def _visible_activity_or_404(user, pk) -> Activity:
    activity = get_object_or_404(
        Activity.objects.select_related("place", "activity_type", "owner", "thread"), pk=pk
    )
    # Staff/moderators may still open removed content (for review/appeal); members may not.
    if getattr(user, "is_staff", False):
        return activity
    if user.is_authenticated and social.can_see_activity(user, activity) and not activity.is_hidden:
        return activity
    raise Http404("No activity matches the given query.")


@login_required
def activity_list(request):
    activities = (
        social.visible_activities(request.user)
        .filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
        .select_related("place", "activity_type", "owner")
    )
    beginners_only = request.GET.get("beginners") == "true"
    if beginners_only:
        activities = activities.filter(beginners_welcome=True)
    activities, near_active = _order_feed_by_location(activities, request.GET)
    return render(
        request,
        "web/activities.html",
        {
            "activities": activities,
            "near_active": near_active,
            "beginners_only": beginners_only,
            **_nav_context(request.user),
        },
    )


@login_required
def activity_detail(request, pk):
    user = request.user
    activity = _visible_activity_or_404(user, pk)
    members = social.current_members(activity).select_related("user")
    my_membership = activity.memberships.filter(user=user).first()
    is_member = my_membership is not None and my_membership.state == Membership.State.MEMBER

    pending = []
    if is_member:
        voting_count = social.voting_members(activity).count()
        needed = max(1, math.ceil(activity.join_threshold * voting_count))
        my_votes = {
            v.membership_id: v.approve
            for v in JoinVote.objects.filter(membership__activity=activity, voter=user)
        }
        for m in activity.memberships.filter(state=Membership.State.REQUESTED).select_related(
            "user"
        ):
            m.approvals = m.votes.filter(approve=True).count()
            m.needed = needed
            m.my_vote = my_votes.get(m.id)
            pending.append(m)

    # Owner announcements (F11) pin above the ordinary thread, newest-first (bounded).
    announcements = list(
        activity.thread.posts.filter(is_hidden=False, is_announcement=True)
        .select_related("author")
        .order_by("-created_at")[:50]
    )
    # F26/unification: one bounded, keyset-paginated window of top-level posts, each with its
    # non-hidden replies prefetched. can_read_thread is enforced via _visible_activity_or_404 +
    # is_member below, so the ?before= cursor can't leak across the membership wall.
    before = request.GET.get("before") if (is_member or user.is_staff) else None
    posts, has_older, older_cursor = social.thread_page(activity, before=before)
    # Inline media: attach each post's viewable attachments (with per-viewer signed URLs) so the
    # stream renders images/PDFs in the conversation without an N+1.
    if is_member or user.is_staff:
        from apps.media.services import attachments_for_posts

        all_rendered = [*posts, *(r for p in posts for r in p.replies.all())]
        by_post = attachments_for_posts(all_rendered, user)
        for p in all_rendered:
            p.attachment_list = by_post.get(p.id, [])
    # No-JS quote-reply: a "Reply" link is ?reply_to=<id>#compose; pre-target the compose form.
    reply_target = None
    rt = request.GET.get("reply_to")
    if rt and rt.isdigit() and is_member:
        reply_target = Post.objects.filter(
            pk=int(rt), thread=activity.thread, is_hidden=False, is_announcement=False
        ).first()
    post_form = PostForm(initial={"reply_to": reply_target.id} if reply_target else None)
    photos = []
    if is_member or user.is_staff:
        try:
            photos = list(thread_photos(user, activity.thread))
        except NotAuthorized:
            photos = []
        for photo in photos:
            photo.url = signed_url(photo, user)

    # Safe-exit context (F5): the viewer's own already-linked guardians, named so a child
    # who feels unsafe knows who they can turn to. No contact details, no new link — just
    # the names of adults the platform already records as their guardian.
    my_guardians = [
        rel.guardian
        for rel in GuardianRelationship.objects.filter(
            ward=user, status=GuardianRelationship.Status.ACTIVE
        ).select_related("guardian")
    ]

    is_owner = activity.owner_id == user.id
    # Connections: show a "connect" button on co-members (only when the feature is enabled for
    # the viewer's cohort, and not for someone already connected/requested). Co-membership here
    # satisfies the shared-activity precondition; the service still re-applies the full gate.
    from apps.connections import services as connections

    # A supervisory guardian is not a peer and must never be offered a "connect" affordance
    # toward the (child-cohort) members they accompany.
    viewer_is_guardian = bool(my_membership and my_membership.role == Membership.Role.GUARDIAN)
    conn_enabled = is_member and not viewer_is_guardian and connections.is_enabled_for(user)
    conn_related_ids = connections.related_user_ids(user) if conn_enabled else set()
    my_arrival = my_membership.arrived_at if (is_member and my_membership) else None
    # F35: the "catch up" digest, only for someone who can already see the thread.
    digest = social.thread_digest(activity) if (is_member or user.is_staff) else None
    # F39: a self-dismissing first-timer welcome banner, shown to the new joiner for a window
    # after they were welcomed (then it simply ages out — no mutating GET).
    welcome_ttl = timedelta(days=getattr(settings, "F39_WELCOME_BANNER_TTL_DAYS", 7))
    show_welcome = bool(
        is_member
        and my_membership
        and my_membership.welcomed_at
        and timezone.now() - my_membership.welcomed_at <= welcome_ttl
    )
    return render(
        request,
        "web/activity_detail.html",
        {
            "activity": activity,
            "members": members,
            "is_member": is_member,
            "is_owner": is_owner,
            "conn_enabled": conn_enabled,
            "conn_related_ids": conn_related_ids,
            "is_open": activity.status == Activity.Status.OPEN,
            "is_completed": activity.status == Activity.Status.COMPLETED,
            "digest": digest,
            "show_welcome": show_welcome,
            "my_membership": my_membership,
            "pending": pending,
            "announcements": announcements,
            "posts": posts,
            "has_older": has_older,
            "older_cursor": older_cursor,
            "reply_target": reply_target,
            "photos": photos,
            "post_form": post_form,
            "my_guardians": my_guardians,
            "my_arrival": my_arrival,
            "arrival_window_open": is_member and social.arrival_window_open(activity),
            "rsvp_summary": social.attendance_summary(activity),
            "met_summary": social.met_confirmation_summary(activity),
            "my_met_confirmed": bool(my_membership.met_confirmed_at)
            if (is_member and my_membership)
            else False,
            "can_join": social.can_join(user, activity),
            **_nav_context(user),
        },
    )


@login_required
def activity_create(request):
    if not social.can_create_activity(request.user):
        messages.error(
            request,
            "You need to be verified (and, if a minor, have parental consent) and in a cohort "
            "to organize activities.",
        )
        return redirect("profile")
    initial = {}
    if request.GET.get("place"):
        initial["place"] = request.GET["place"]
    # F40: seed the form from an event the owner wants to convene around. Every GET value is
    # validated so a crafted URL can't 500 or inject an inactive type; the owner still edits
    # before submit, and create_activity pins the cohort, so there's no isolation impact.
    atype_id = request.GET.get("activity_type", "")
    if atype_id.isdigit() and ActivityType.objects.filter(pk=atype_id, is_active=True).exists():
        initial["activity_type"] = atype_id
    raw_starts = request.GET.get("starts_at", "")
    for fmt in _DT_FORMATS:
        try:
            initial["starts_at"] = datetime.strptime(raw_starts, fmt)
            break
        except ValueError:
            continue
    # F36: seed an editable draft title/description from the chosen type/place/time (composes
    # with the F40 prefill). setdefault only fills EMPTY slots — never overwrites the user's
    # input, and the POST path is untouched.
    if initial.get("activity_type"):
        atype = ActivityType.objects.filter(pk=initial["activity_type"], is_active=True).first()
        if atype is not None:
            place_obj = (
                Place.objects.filter(pk=initial["place"]).first() if initial.get("place") else None
            )
            draft = social.draft_activity_text(
                activity_type=atype,
                place=place_obj,
                starts_at=initial.get("starts_at"),
                cohort=request.user.cohort,
            )
            initial.setdefault("title", draft["title"])
            initial.setdefault("description", draft["description"])
    if request.method == "POST":
        form = ActivityForm(request.POST)
        if form.is_valid():
            try:
                activity = social.create_activity(request.user, **form.cleaned_data)
            except social.SocialError as exc:
                messages.error(request, _msg(exc))
            else:
                messages.success(request, "Activity created - you're the owner.")
                return redirect("activity_detail", pk=activity.pk)
    else:
        form = ActivityForm(initial=initial)
    return render(request, "web/activity_form.html", {"form": form, **_nav_context(request.user)})


@login_required
def activity_edit(request, pk):
    """Owner edits an OPEN, not-yet-started activity (F2). Place/type are not editable."""
    activity = _visible_activity_or_404(request.user, pk)
    if activity.owner_id != request.user.id:
        messages.error(request, "Only the organiser can edit this activity.")
        return redirect("activity_detail", pk=pk)
    if request.method == "POST":
        form = ActivityEditForm(request.POST)
        if form.is_valid():
            try:
                social.update_activity(request.user, activity, **form.cleaned_data)
            except social.SocialError as exc:
                messages.error(request, _msg(exc))
            else:
                messages.success(request, "Activity updated.")
                return redirect("activity_detail", pk=pk)
    else:
        form = ActivityEditForm(
            initial={
                "title": activity.title,
                "description": activity.description,
                "starts_at": activity.starts_at,
                "ends_at": activity.ends_at,
                "capacity": activity.capacity,
                "meeting_point": activity.meeting_point,
                "what_to_bring": activity.what_to_bring,
                "organizer_note": activity.organizer_note,
                "cost_band": activity.cost_band,
                "difficulty": activity.difficulty,
                "accessibility_notes": activity.accessibility_notes,
                "beginners_welcome": activity.beginners_welcome,
            }
        )
    return render(
        request,
        "web/activity_edit.html",
        {"form": form, "activity": activity, **_nav_context(request.user)},
    )


@login_required
@require_POST
def activity_cancel(request, pk):
    """Owner cancels the meetup (F1); current members are notified with the reason."""
    activity = _visible_activity_or_404(request.user, pk)
    try:
        social.cancel_activity(request.user, activity, reason=request.POST.get("reason", ""))
        messages.success(request, "Activity cancelled - members have been notified.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_announce(request, pk):
    """Owner posts a pinned announcement that notifies every member (F11)."""
    activity = _visible_activity_or_404(request.user, pk)
    form = PostForm(request.POST)
    if form.is_valid():
        try:
            social.post_announcement(request.user, activity, form.cleaned_data["body"])
            messages.success(request, "Announcement posted - members notified.")
        except social.SocialError as exc:
            messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_rsvp(request, pk):
    """A member sets their transient go/no-go for this meetup (F20)."""
    activity = _visible_activity_or_404(request.user, pk)
    try:
        social.set_attendance_intent(request.user, activity, request.POST.get("intent"))
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_met(request, pk):
    """A participant confirms (or undoes) that a finished meetup happened (F22)."""
    activity = _visible_activity_or_404(request.user, pk)
    confirmed = request.POST.get("met") != "no"
    try:
        social.set_met_confirmed(request.user, activity, confirmed)
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_arrived(request, pk):
    """A member self-declares "I've arrived" (F3); their group (and a child's guardian) are
    quietly told. No location, no note — just the tap."""
    activity = _visible_activity_or_404(request.user, pk)
    try:
        social.mark_arrived(request.user, activity)
        messages.success(request, "Thanks - your group has been told you're here.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_join(request, pk):
    activity = _visible_activity_or_404(request.user, pk)
    try:
        social.request_to_join(request.user, activity)
        messages.success(request, "Join request sent - current members will vote on it.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_leave(request, pk):
    activity = _visible_activity_or_404(request.user, pk)
    try:
        if social.leave_activity(request.user, activity):
            messages.success(request, "You left this activity.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def membership_vote(request, pk, membership_id):
    activity = _visible_activity_or_404(request.user, pk)
    membership = get_object_or_404(Membership, pk=membership_id, activity=activity)
    approve = request.POST.get("vote") == "approve"
    try:
        social.cast_vote(request.user, membership, approve)
        messages.success(request, "Vote recorded.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_post(request, pk):
    from apps.media import services as media

    activity = _visible_activity_or_404(request.user, pk)
    form = PostForm(request.POST, request.FILES)
    if not form.is_valid():
        messages.error(request, "Your message couldn't be posted.")
        return redirect("activity_detail", pk=pk)
    body = form.cleaned_data["body"]
    upload = form.cleaned_data.get("attachment")
    if not (body or "").strip() and upload is None:
        messages.error(request, "Type a message or attach a file.")
        return redirect("activity_detail", pk=pk)
    data = None
    if upload is not None:
        if upload.size > media._attachment_max_bytes():
            messages.error(request, "That file is too large.")
            return redirect("activity_detail", pk=pk)
        data = upload.read()
    try:
        # Post + attachment are ONE transaction: if the scan rejects the file, the post is
        # rolled back too (no message without its file), and the on_commit live broadcast fires
        # only after both exist.
        with transaction.atomic():
            post = social.post_to_thread(
                request.user,
                activity,
                body,
                reply_to=form.cleaned_data.get("reply_to"),
                allow_empty=data is not None,
            )
            if data is not None:
                media.attach_to_post(request.user, post, filename=upload.name, data=data)
    except (social.SocialError, media.MediaError) as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_post_edit(request, pk, post_id):
    """Author edits their own thread message in place (the 'edited' marker is derived)."""
    activity = _visible_activity_or_404(request.user, pk)
    post = get_object_or_404(Post, pk=post_id, thread=activity.thread)
    body = (request.POST.get("body") or "").strip()
    if not body:
        messages.error(request, "A message can't be empty.")
        return redirect("activity_detail", pk=pk)
    try:
        social.edit_post(request.user, post, body)
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_post_delete(request, pk, post_id):
    """Author soft-deletes their own thread message (row retained for audit/appeal)."""
    activity = _visible_activity_or_404(request.user, pk)
    post = get_object_or_404(Post, pk=post_id, thread=activity.thread)
    try:
        social.delete_own_post(request.user, post)
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_photo(request, pk):
    activity = _visible_activity_or_404(request.user, pk)
    upload = request.FILES.get("image")
    if upload is not None:
        try:
            upload_photo(request.user, Photo.Kind.THREAD, upload.read(), thread=activity.thread)
            messages.success(request, "Photo added to the thread.")
        except (NotAuthorized, ValueError) as exc:
            messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


# --- Interests & recommendations ----------------------------------------------------


@login_required
def interests(request):
    if request.method == "POST":
        slugs = request.POST.getlist("interests")
        recs.set_interests(request.user, slugs)
        messages.success(request, "Your interests were saved.")
        return redirect("home")
    chosen = set(
        UserInterest.objects.filter(user=request.user).values_list("activity_type__slug", flat=True)
    )
    options = (
        ActivityType.objects.filter(is_active=True).select_related("category").order_by("name")
    )
    return render(
        request,
        "web/interests.html",
        {"options": options, "chosen": chosen, **_nav_context(request.user)},
    )


@login_required
def access_preferences(request):
    """F15: the user's OWN stated accessibility needs — a setting they choose, never inferred
    or tracked. Reads/writes only request.user (no cross-user surface)."""
    if request.method == "POST":
        set_access_preference(
            request.user,
            needs_step_free=request.POST.get("needs_step_free") == "on",
            needs_accessible_toilet=request.POST.get("needs_accessible_toilet") == "on",
            prefers_quiet=request.POST.get("prefers_quiet") == "on",
        )
        messages.success(request, "Your access preferences were saved.")
        return redirect("access_preferences")
    return render(
        request,
        "web/access_preferences.html",
        {"pref": get_access_preference(request.user), **_nav_context(request.user)},
    )


# --- Profile ------------------------------------------------------------------------


@login_required
def profile(request):
    user = request.user
    chosen = list(
        UserInterest.objects.filter(user=user)
        .select_related("activity_type")
        .values_list("activity_type__name", flat=True)
    )
    blocked = [b.blocked for b in Block.objects.filter(blocker=user).select_related("blocked")]
    return render(
        request,
        "web/profile.html",
        {
            "profile_user": user,
            "avatar_url": _avatar_url(user, user),
            "can_participate": can_participate(user),
            "provenance": assurance_provenance(user),
            "interests": chosen,
            "blocked": blocked,
            **_nav_context(user),
        },
    )


@login_required
@require_POST
def avatar_upload(request):
    # Brake the avatar-upload path so the profile-image uniqueness check can't be queried at
    # scale to brute-force which images are in use (an enumeration oracle on a child platform).
    if not safety.allow_action(
        request.user,
        "avatar_upload",
        limit=getattr(settings, "AVATAR_UPLOAD_RATE_LIMIT", 20),
        window_seconds=getattr(settings, "AVATAR_UPLOAD_RATE_WINDOW_SECONDS", 3600),
    ):
        messages.error(request, "Too many avatar changes; please try again later.")
        return redirect("profile")
    upload = request.FILES.get("image")
    if upload is not None:
        try:
            upload_photo(request.user, Photo.Kind.PROFILE, upload.read())
            messages.success(request, "Profile picture updated.")
        except (ValueError, MediaError) as exc:
            # MediaError covers a duplicate image (DuplicateProfileImage) and a failed scan.
            messages.error(request, _msg(exc))
    return redirect("profile")


# --- Notifications ------------------------------------------------------------------


@login_required
def notifications_list(request):
    items = list(Notification.objects.filter(recipient=request.user)[:50])
    for n in items:
        n.why = notifications.why_reason(n.kind)
    return render(request, "web/notifications.html", {"items": items, **_nav_context(request.user)})


@login_required
@require_POST
def notifications_read_all(request):
    notifications.mark_all_read(request.user)
    return redirect("notifications")


@login_required
def notification_preferences(request):
    """Per-kind mute + 'why you got this'. DSA notices (moderation/system) aren't listed —
    they can never be muted."""
    from apps.notifications.models import MUTABLE_KINDS

    if request.method == "POST":
        notifications.set_muted_kinds(request.user, request.POST.getlist("muted"))
        messages.success(request, "Notification settings saved.")
        return redirect("notification_preferences")
    muted = notifications.get_muted_kinds(request.user)
    rows = [
        {
            "value": k.value,
            "label": k.label,
            "reason": notifications.why_reason(k),
            "muted": k.value in muted,
        }
        for k in MUTABLE_KINDS
    ]
    return render(
        request,
        "web/notification_preferences.html",
        {"rows": rows, **_nav_context(request.user)},
    )


# --- Secure messaging (E2EE, client-side crypto) ------------------------------------


@login_required
def messages_page(request):
    """Shell for the end-to-end-encrypted messenger. All crypto happens in the
    browser (see static/js/e2ee-messaging.js); this view only renders the page and
    hands the client the current user's public identity so it can address itself."""
    config = {
        "me": {
            "public_id": str(request.user.public_id),
            "username": request.user.username,
            "display_name": request.user.display_name or request.user.username,
        }
    }
    return render(
        request,
        "web/messages.html",
        {"messaging_config": config, **_nav_context(request.user)},
    )


# --- Donations ----------------------------------------------------------------------


def donate(request):
    from apps.donations.models import Campaign
    from apps.donations.services import DonationError, start_donation

    if request.method == "POST":
        form = DonateForm(request.POST)
        if form.is_valid():
            cents = int(form.cleaned_data["amount"] * 100)
            try:
                _donation, checkout_url = start_donation(
                    request.user, cents, campaign=form.cleaned_data["campaign"]
                )
            except DonationError as exc:
                messages.error(request, _msg(exc))
            else:
                return redirect(checkout_url)
    else:
        # F34: pre-select a campaign when arriving from /campaigns/ via ?campaign=<slug>.
        initial = {}
        slug = request.GET.get("campaign")
        if slug:
            initial["campaign"] = Campaign.objects.filter(slug=slug, is_active=True).first()
        form = DonateForm(initial=initial)
    return render(request, "web/donate.html", {"form": form, **_nav_context(request.user)})


def transparency(request):
    """F29: public 'where the money goes' — donations received and staff-entered spending by
    category, as two clearly separate aggregate sections (never an 'X of Y goal' bar)."""
    from apps.donations.services import (
        completed_total_cents,
        spend_by_category,
        spend_total_cents,
    )

    return render(
        request,
        "web/transparency.html",
        {
            "currency": "EUR",
            "raised_cents": completed_total_cents("EUR"),
            "spend_rows": spend_by_category("EUR"),
            "spend_total_cents": spend_total_cents("EUR"),
            **_nav_context(request.user),
        },
    )


@login_required
def my_donations(request):
    """F29: the donor's OWN donations with plain receipts. Self-only by construction; no
    card/payment data is stored or shown."""
    from apps.donations.models import Donation

    donations = Donation.objects.filter(donor=request.user).order_by("-created_at")
    return render(
        request,
        "web/my_donations.html",
        {"donations": donations, **_nav_context(request.user)},
    )


def campaigns(request):
    """F34: public list of active earmark campaigns with a calm, static progress bar."""
    from apps.donations.services import active_campaigns_with_progress

    return render(
        request,
        "web/campaigns.html",
        {"campaigns": active_campaigns_with_progress(), **_nav_context(request.user)},
    )


def partners_list(request):
    """F37: public list of verified civic partners — text-only acknowledgement, not advertising."""
    from apps.places.services import verified_partners

    return render(
        request,
        "web/partners.html",
        {"partners": list(verified_partners()), **_nav_context(request.user)},
    )


# --- Events (public: places + happenings) -------------------------------------------


def events_list(request):
    events = (
        Event.objects.filter(starts_at__gte=timezone.now())
        .select_related("place", "activity_type")
        .order_by("starts_at")
    )
    activity = request.GET.get("activity")
    if activity:
        events = events.filter(activity_type__slug=activity)
    return render(
        request,
        "web/events.html",
        {"events": events[:100], "activity": activity, **_nav_context(request.user)},
    )


def event_detail(request, pk):
    event = get_object_or_404(Event.objects.select_related("place", "activity_type"), pk=pk)
    return render(request, "web/event_detail.html", {"event": event, **_nav_context(request.user)})


# --- EUDI Wallet age verification (in-page; sandbox demo wallet) ---------------------


@login_required
def verify_age(request):
    sandbox = getattr(settings, "EUDI_SANDBOX", False)
    if request.method == "POST":
        if not sandbox:
            messages.error(request, "EU wallet verification is not configured on this server.")
            return redirect("profile")
        from apps.accounts.identity.base import IdentityVerificationError
        from apps.accounts.identity.eudi.issuer import issue_age_credential
        from apps.accounts.identity.providers.eudi import EUDIWalletProvider

        choice = request.POST.get("age", "adult")
        nonce = get_random_string(24)
        token = issue_age_credential(
            audience=settings.EUDI_CLIENT_ID,
            nonce=nonce,
            age_over_16=choice in ("16_17", "adult"),
            age_over_18=choice == "adult",
            subject=str(request.user.public_id),
        )
        presentation = {
            "vp_token": token,
            "nonce": nonce,
            "audience": settings.EUDI_CLIENT_ID,
        }
        try:
            result = EUDIWalletProvider().verify(request.user, presentation=presentation)
        except IdentityVerificationError as exc:
            messages.error(request, f"Verification failed: {exc}")
            return redirect("verify_age")
        apply_assurance(request.user, result)
        messages.success(
            request,
            f"Age verified via the EU wallet - you're in the "
            f"'{request.user.get_cohort_display()}' cohort.",
        )
        return redirect("profile")
    return render(
        request,
        "web/verify_age.html",
        {
            "sandbox": sandbox,
            "verified": can_participate(request.user),
            **_nav_context(request.user),
        },
    )


# --- Guardian / wards (account-level guardianship) ----------------------------------


@login_required
def wards(request):
    ward_users = list(
        User.objects.filter(
            guardians__guardian=request.user,
            guardians__status=GuardianRelationship.Status.ACTIVE,
        ).distinct()
    )
    now = timezone.now()
    for ward in ward_users:
        # Read-only "where/when is my child meeting" manifest (F6): real place, time and
        # activity type only — never the thread, peers, or chat. Scoped to live, upcoming
        # meetups the ward has actually been admitted to.
        ward.meetups = list(
            Activity.objects.filter(
                memberships__user=ward,
                memberships__state=Membership.State.MEMBER,
                status=Activity.Status.OPEN,
                starts_at__gte=now,
            )
            .select_related("place", "activity_type")
            .order_by("starts_at")
            .distinct()
        )
        # F13: the legible can/cannot boundary for this guardianship, from the real rules.
        ward.caps = guardianship_capabilities(request.user, ward)
    return render(
        request,
        "web/wards.html",
        {
            "wards": ward_users,
            "minor_onboarding": minor_onboarding_enabled(),
            **_nav_context(request.user),
        },
    )


@login_required
@require_POST
def guardian_revoke(request, ward_pk):
    """A guardian ends a guardianship link (F13). Reuses accounts.revoke_guardian, which also
    revokes that guardian's consent grant and drops any messaging-observer presence."""
    ward = get_object_or_404(User, pk=ward_pk)
    # revoke_guardian does not itself re-check the actor is the guardian — guard here.
    if not is_guardian_of(request.user, ward):
        messages.error(request, "You are not this user's guardian.")
        return redirect("wards")
    try:
        revoke_guardian(request.user, ward)
        messages.success(request, "Guardianship ended.")
    except ValueError as exc:
        messages.error(request, _msg(exc))
    return redirect("wards")


@login_required
def my_guardians(request):
    """Ward-side legibility panel (F13): who my guardians are and exactly what each can and
    cannot see about me. Read-only — there is no ward-initiated unlink (a child severing
    oversight is a safety decision, not a UI toggle)."""
    guardians = []
    for rel in (
        GuardianRelationship.objects.filter(
            ward=request.user, status=GuardianRelationship.Status.ACTIVE
        )
        .select_related("guardian")
        .order_by("guardian__display_name")
    ):
        guardian = rel.guardian
        guardian.caps = guardianship_capabilities(guardian, request.user)
        guardians.append(guardian)
    return render(
        request,
        "web/guardianship.html",
        {"guardians": guardians, **_nav_context(request.user)},
    )


@login_required
@require_POST
def guardian_invite_create(request):
    """A verified adult invites a minor (by username) to confirm a guardianship link.

    Rate-limited, and the outcome is deliberately non-distinguishing (same message whether
    or not the account exists / is a minor / is already linked) so the form can't be used
    to enumerate or classify child accounts. The ward sees the pending request in-app on
    their home page — no code is shared out-of-band."""
    generic = (
        "If that account exists and is eligible, a guardianship request was sent. "
        "They'll see it on their home page to accept."
    )
    if not safety.allow_action(
        request.user,
        "guardian_invite",
        limit=getattr(settings, "GUARDIAN_INVITE_RATE_LIMIT", 20),
        window_seconds=getattr(settings, "GUARDIAN_INVITE_RATE_WINDOW_SECONDS", 3600),
    ):
        messages.error(request, "Too many requests; please try again later.")
        return redirect("wards")
    ward = User.objects.filter(username=(request.POST.get("ward_username") or "").strip()).first()
    if ward is not None:
        try:
            create_guardian_link_invite(
                request.user,
                ward,
                relationship=(request.POST.get("relationship") or "parent").strip(),
            )
        except ValueError:
            pass  # swallow to keep the outcome indistinguishable
    messages.success(request, generic)
    return redirect("wards")


@login_required
@require_POST
def guardian_invite_accept(request, token):
    """The invited ward confirms the guardianship link."""
    try:
        accept_guardian_link_invite(request.user, token)
        messages.success(request, "Guardian link confirmed.")
    except ValueError as exc:
        messages.error(request, str(exc))
    return redirect("home")


@login_required
@require_POST
def guardian_invite_decline(request, token):
    try:
        decline_guardian_link_invite(request.user, token)
        messages.success(request, "Invite declined.")
    except ValueError as exc:
        messages.error(request, str(exc))
    return redirect("home")


# --- Safety: reporting & blocking (wires apps/safety into the UI) --------------------


def _resolve_report_target(user, target_type, target_id):
    if target_type == "activity":
        activity = Activity.objects.filter(pk=target_id).first()
        if activity and (user.is_staff or social.can_see_activity(user, activity)):
            return activity, activity.title
    elif target_type == "user":
        person = User.objects.filter(pk=target_id).first()
        if person:
            return person, (person.display_name or person.username)
    return None, None


@login_required
def report(request):
    target_type = request.GET.get("type") or request.POST.get("type")
    target_id = request.GET.get("id") or request.POST.get("id")
    target, label = _resolve_report_target(request.user, target_type, target_id)
    if target is None:
        raise Http404("Nothing to report.")
    if request.method == "POST":
        form = ReportForm(request.POST)
        if form.is_valid():
            safety.file_report(
                request.user, target, form.cleaned_data["reason"], form.cleaned_data["detail"]
            )
            messages.success(request, "Thanks - your report was sent to the moderation team.")
            if target_type == "activity":
                return redirect("activity_detail", pk=target.pk)
            return redirect("home")
    else:
        form = ReportForm()
    return render(
        request,
        "web/report.html",
        {
            "form": form,
            "target_type": target_type,
            "target_id": target_id,
            "target_label": label,
            **_nav_context(request.user),
        },
    )


def _safe_next(request, default: str) -> str:
    """Return the posted ``next`` target only if it points back at this site, else the
    given named-route default. Prevents an open redirect through an attacker-supplied
    ``next`` on the block/unblock POST."""
    candidate = request.POST.get("next")
    if candidate and url_has_allowed_host_and_scheme(
        candidate,
        allowed_hosts={request.get_host()},
        require_https=request.is_secure(),
    ):
        return candidate
    return default


@login_required
@require_POST
def block_user_view(request, pk):
    target = get_object_or_404(User, pk=pk)
    try:
        safety.block_user(request.user, target)
        messages.success(request, f"Blocked {target.display_name or target.username}.")
    except ValueError as exc:
        messages.error(request, _msg(exc))
    return redirect(_safe_next(request, "home"))


@login_required
@require_POST
def unblock_user_view(request, pk):
    target = get_object_or_404(User, pk=pk)
    safety.unblock_user(request.user, target)
    messages.success(request, f"Unblocked {target.display_name or target.username}.")
    return redirect(_safe_next(request, "profile"))


# --- Transparency: privacy & terms (W1-8) -------------------------------------------
# Static legal pages. Copy is DRAFT placeholder text and must be reviewed/finalised by
# the DPO/legal before launch; see the templates' leading banner.


@login_required
def safety_record(request):
    """F19: a user's own DSA Art.16/17 transparency page — moderation decisions about them and
    the status of reports they filed. Read-only and strictly self-scoped."""
    record = safety.safety_record_for(request.user)
    return render(
        request,
        "web/safety_record.html",
        {
            "decisions": record["decisions"],
            "reports": record["reports"],
            **_nav_context(request.user),
        },
    )


def privacy(request):
    return render(request, "web/privacy.html", _nav_context(request.user))


def terms(request):
    return render(request, "web/terms.html", _nav_context(request.user))


# --- GDPR self-service account deletion (right to erasure) ---------------------------


@login_required
@require_POST
def account_delete(request):
    """Let a user erase their own account (GDPR Art. 17). Delegates to the accounts
    domain service, which enforces the actual erasure/retention rules, then logs the
    user out and returns them to the public landing page."""
    # Imported lazily: erase_user is owned by the accounts domain service.
    from apps.accounts.services import erase_user

    try:
        erase_user(request.user, request.user)
    except (ValueError, PermissionError) as exc:
        messages.error(request, _msg(exc))
        return redirect("profile")
    logout(request)
    messages.success(
        request,
        "Your account and personal data have been deleted. We're sorry to see you go.",
    )
    return redirect("home")
