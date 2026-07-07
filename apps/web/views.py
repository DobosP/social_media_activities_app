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
from django.http import Http404, JsonResponse
from django.shortcuts import get_object_or_404, redirect, render
from django.urls import reverse
from django.utils import timezone
from django.utils.crypto import get_random_string
from django.utils.http import url_has_allowed_host_and_scheme, urlencode
from django.views.decorators.http import require_POST

from apps.accounts.identity.base import IdentityVerificationError
from apps.accounts.identity.registry import get_identity_provider
from apps.accounts.models import AgeBand, Cohort, GuardianRelationship, User
from apps.accounts.services import (
    IdentityAlreadyBound,
    IdentityBanned,
    accept_guardian_link_invite,
    apply_assurance,
    assurance_provenance,
    bind_identity,
    can_participate,
    create_guardian_link_invite,
    decline_guardian_link_invite,
    guardianship_capabilities,
    minor_onboarding_enabled,
    pending_guardian_invites_for,
    retention_disclosure,
    revoke_guardian,
    set_guardian_guardrail,
)
from apps.events.models import Event
from apps.media.models import Photo
from apps.media.services import (
    MediaError,
    NotAuthorized,
    activity_visual,
    signed_url,
    thread_photos,
    upload_photo,
)
from apps.notifications import services as notifications
from apps.notifications.models import Notification
from apps.places.filters import PlaceFilter
from apps.places.models import Place, PlaceActivity
from apps.places.services import (
    accessibility_facts,
    accessibility_facts_display,
    get_access_preference,
    is_child_safe_venue,
    matches_access_preference,
    partner_for_place,
    set_access_preference,
    sort_by_access_match,
)
from apps.recommendations import services as recs
from apps.recommendations.models import UserInterest
from apps.safety import services as safety
from apps.safety.models import Block, ModerationAction
from apps.saved_searches import services as saved_searches
from apps.saved_searches.models import SavedSearch
from apps.social import services as social
from apps.social.models import (
    Activity,
    ActivitySeries,
    Group,
    GroupMembership,
    GroupQuestionPrompt,
    JoinVote,
    Membership,
    Post,
)
from apps.taxonomy.models import ActivityType
from apps.web import views_spa

from .forms import (
    _DT_FORMATS,
    ActivityEditForm,
    ActivityForm,
    DonateForm,
    GaugeConvertForm,
    GaugeForm,
    GroupCreateForm,
    NextInstanceNoteForm,
    PlaceProposeForm,
    PostForm,
    RegisterForm,
    ReportForm,
    SeriesForm,
)

# --- Communities (derived geo x activity-type discovery labels) -------------------------


def _can_create_group(user) -> bool:
    """UI-visibility mirror of create_group's gate (the service still enforces it):
    staff always; a non-staff user only when GROUPS_ALLOW_USER_CREATED is on AND their
    cohort is in the GROUPS_USER_CREATION_COHORTS hard wall (a minor can never own one)."""
    if user.is_staff:
        return True
    if not getattr(settings, "GROUPS_ALLOW_USER_CREATED", False):
        return False
    return user.cohort in getattr(settings, "GROUPS_USER_CREATION_COHORTS", ["adult"])


@login_required
def communities_page(request):
    """W5: the ONE communities surface — joinable standing groups and auto-detected
    communities side by side (they live on the same area×type coordinate; users
    shouldn't have to learn two nouns). Both lists stay behind their own chokepoints
    (visible_groups / visible_communities); no counts, no 'trending'/'hot'."""
    from django.core.paginator import Paginator

    from apps.communities import services as communities

    qs = communities.visible_communities(request.user)
    page = Paginator(qs, 30).get_page(request.GET.get("page"))
    groups_page = Paginator(social.visible_groups(request.user), 30).get_page(
        request.GET.get("gpage")
    )
    if views_spa.spa_enabled():
        return views_spa.communities_spa(
            request, page=page, groups_page=groups_page, can_create=_can_create_group(request.user)
        )
    return render(
        request,
        "web/communities.html",
        {
            "page": page,
            "groups_page": groups_page,
            "can_create": _can_create_group(request.user),
            **_nav_context(request.user),
        },
    )


@login_required
def community_graph_page(request):
    """The 3D community-graph navigator. Server-renders the list as the no-JS fallback, then
    progressively enhances into the rotatable WebGL graph (cohort-walled via the same
    visible_communities chokepoint as everywhere else)."""
    from apps.communities import services as communities

    return render(
        request,
        "web/communities_graph.html",
        {
            "communities": communities.visible_communities(request.user),
            **_nav_context(request.user),
        },
    )


@login_required
def community_detail(request, slug):
    """A community's upcoming activities — the existing cohort-filtered feed narrowed to this
    (area x type). Cohort-walled (404 for a cross-cohort/unpublished slug); NO roster, NO
    member count, NO contact button — just activity cards."""
    from apps.communities import services as communities

    community = communities.community_by_slug(slug, request.user)
    if community is None:
        raise Http404("No such community.")
    limit = getattr(settings, "COMMUNITY_ACTIVITIES_PAGE_SIZE", 100)
    activities = list(
        communities.community_activities(community, request.user)
        .select_related("place", "activity_type", "owner", "cover")
        .prefetch_related("place__corrections")[:limit]  # F20: _activity_card display_name
    )
    # Read-time linkage: if a standing GROUP exists on the same (cohort, area, type/category)
    # coordinate AND is visible to this viewer, offer a "join the standing group" link (name only,
    # no count). Sourced from visible_groups, so a child can never discover an adult group this way.
    linked_group = social.linked_group_for_community(community, request.user)
    if views_spa.spa_enabled():
        return views_spa.community_detail_spa(
            request, community=community, activities=activities, linked_group=linked_group
        )
    return render(
        request,
        "web/community_detail.html",
        {
            "community": community,
            "activities": activities,
            "linked_group": linked_group,
            **_nav_context(request.user),
        },
    )


# --- Public Groups (persistent, cohort-pinned, joinable standing groups) ----------------


@login_required
def group_list(request):
    """W5: groups and communities are ONE discovery surface now — the old standalone
    /groups/ list lands on it (group detail/join/leave URLs are unchanged)."""
    return redirect("communities")


@login_required
def group_create(request):
    """Create a standing group. Staff may always; a non-staff adult only when
    GROUPS_ALLOW_USER_CREATED is on. The service enforces every cohort rule (minor groups are
    staff-curated only and dark behind ALLOW_MINOR_ONBOARDING)."""
    if not (request.user.is_staff or getattr(settings, "GROUPS_ALLOW_USER_CREATED", False)):
        messages.error(request, "Group creation isn't available for your account yet.")
        return redirect("groups")
    if request.method == "POST":
        form = GroupCreateForm(request.POST)
        if form.is_valid():
            from apps.communities.services import _ensure_city_area

            area = _ensure_city_area(form.cleaned_data["city"])
            try:
                group = social.create_group(
                    request.user,
                    area=area,
                    title=form.cleaned_data["title"],
                    activity_type=form.cleaned_data["activity_type"],
                    description=form.cleaned_data.get("description", ""),
                    cohort=form.cleaned_data.get("cohort") or None,
                )
            except social.SocialError as exc:
                messages.error(request, _msg(exc))
            else:
                messages.success(request, "Group created - you're the owner.")
                return redirect("group_detail", pk=group.pk)
    else:
        # W5 "start a group from a chat": validated GET prefill (the F40 pattern) so an
        # activity thread / conversation can seed city + type. setdefault-style — typed
        # input is never overwritten; bad values are simply dropped.
        initial = {}
        if city := (request.GET.get("city") or "").strip()[:128]:
            initial["city"] = city
        if type_slug := request.GET.get("type"):
            seeded_type = ActivityType.objects.filter(slug=type_slug, is_active=True).first()
            if seeded_type:
                initial["activity_type"] = seeded_type
        form = GroupCreateForm(initial=initial)
    return render(request, "web/group_form.html", {"form": form, **_nav_context(request.user)})


@login_required
def group_detail(request, pk):
    """A standing group: its info, the upcoming activities in its coordinate, and (for members) the
    moderated thread. The ROSTER PANEL is shown ONLY to an eligible ADULT member (group_roster);
    minors and non-members never see a roster or count. Cohort-walled 404 via visible_groups."""
    user = request.user
    group = social.group_by_id(pk, user)
    if group is None:
        raise Http404("No such group.")
    my_membership = group.memberships.filter(user=user).first()
    is_member = my_membership is not None and my_membership.state == GroupMembership.State.MEMBER
    is_owner = group.owner_id == user.id

    # F30: a minor-group MEMBER (never the staff curator/owner) may send the organiser one of a
    # fixed set of questions — the only inbound voice in an otherwise announcement-only thread. The
    # control is shown only when the service would accept it; the service re-gates on submit.
    can_ask = (
        is_member
        and not is_owner
        and group.cohort in (Cohort.CHILD, Cohort.TEEN)
        and group.status == Group.Status.ACTIVE
        and can_participate(user)
        # Mirror the service's defence-in-depth recipient check so a legacy/misconfigured minor
        # group never renders a control the service would then reject.
        and group.is_staff_curated
        and group.owner.is_staff
    )

    # The SOLE who-is-here surface: None for minors / non-members (no count either).
    roster = social.group_roster(group, user)
    # Upcoming activities in the group's (area x type/category) coordinate — discovery, not roster.
    feed = list(
        social.group_feed_activities(group, user)
        .select_related("place", "activity_type", "owner", "cover")
        .prefetch_related("place__corrections")[:50]  # F20: _activity_card display_name
    )

    # The moderated thread (members who pass the read gate). @mentions are NOT resolved on group
    # threads (no name autocomplete/enumeration); minor group threads are announcement-only. The
    # adult staff CURATOR of a minor group can't pass the cohort wall (can_read_thread), so they get
    # the same staff read-bypass as activity_detail — they need to see their own announcements.
    announcements, posts, can_post = [], [], False
    show_thread = (is_member and social.can_read_thread(user, group)) or user.is_staff
    if show_thread:
        announcements = list(
            group.thread.posts.filter(is_hidden=False, is_announcement=True)
            .select_related("author")
            .order_by("-created_at")[:50]
        )
        posts, _has_older, _cursor = social.thread_page(group)
        group_links = social.thread_allows_links(group)  # adult cohort only
        for p in posts:
            p.body_html = social.highlight_mentions(p.body, {}, allow_links=group_links)
            for r in p.replies.all():
                r.body_html = social.highlight_mentions(r.body, {}, allow_links=group_links)
        # Peer posting: a current member of a non-minor group who passes the read gate. Minor group
        # threads are announcement-only (the gate lives in post_to_thread); a staff non-member or
        # the minor-group curator posts nothing here — the curator broadcasts via the announce form.
        can_post = (
            is_member
            and group.cohort not in (Cohort.CHILD, Cohort.TEEN)
            and social.can_read_thread(user, group)
        )
    post_form = PostForm() if can_post else None
    return render(
        request,
        "web/group_detail.html",
        {
            "group": group,
            "is_member": is_member,
            "is_owner": is_owner,
            "roster": roster,
            "feed": feed,
            "announcements": announcements,
            "posts": posts,
            "show_thread": show_thread,
            "can_post": can_post,
            "post_form": post_form,
            # The owner (for an adult group, a peer; for a minor group, the staff curator) is the
            # only one who may broadcast — independent of the cohort-walled thread read.
            "can_announce": is_owner,
            # F30: minor-group inbound-question control + the fixed prompt set.
            "can_ask": can_ask,
            "question_prompts": GroupQuestionPrompt.choices,
            **_nav_context(user),
        },
    )


@login_required
@require_POST
def group_join(request, pk):
    try:
        social.join_group(request.user, pk)
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("group_detail", pk=pk)


@login_required
@require_POST
def group_leave(request, pk):
    try:
        social.leave_group(request.user, pk)
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("group_detail", pk=pk)


@login_required
@require_POST
def group_post(request, pk):
    group = social.group_by_id(pk, request.user)
    if group is None:
        raise Http404("No such group.")
    form = PostForm(request.POST)
    if not form.is_valid() or not (form.cleaned_data.get("body") or "").strip():
        messages.error(request, "Type a message.")
        return redirect("group_detail", pk=pk)
    try:
        social.post_to_thread(
            request.user,
            group,
            form.cleaned_data["body"],
            reply_to=form.cleaned_data.get("reply_to"),
        )
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("group_detail", pk=pk)


@login_required
@require_POST
def group_announce(request, pk):
    group = social.group_by_id(pk, request.user)
    if group is None:
        raise Http404("No such group.")
    body = (request.POST.get("body") or "").strip()
    if not body:
        messages.error(request, "Write an announcement.")
        return redirect("group_detail", pk=pk)
    try:
        social.post_announcement(request.user, group, body)
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("group_detail", pk=pk)


@login_required
@require_POST
def group_ask(request, pk):
    """F30 — a minor-group member sends one fixed-enum question to the staff organiser. No
    Post is written and only the organiser is notified; the answer (if any) is a group-wide
    announcement, never a private reply. All gating lives in the service."""
    from django.utils.translation import gettext

    group = social.group_by_id(pk, request.user)
    if group is None:
        raise Http404("No such group.")
    try:
        delivered = social.group_ask_organiser(request.user, group, request.POST.get("prompt"))
        if delivered:
            messages.success(
                request,
                gettext(
                    "Your question was sent to the organiser. They'll answer everyone here in an "
                    "announcement, not privately."
                ),
            )
        else:
            # The organiser has question alerts turned off (we don't reveal that — just stay
            # honest about non-delivery and point anything urgent at the safety button).
            messages.info(
                request,
                gettext(
                    "Your question couldn't be delivered to the organiser right now. If it's "
                    "urgent or you feel unsafe, use the safety button instead."
                ),
            )
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("group_detail", pk=pk)


@login_required
@require_POST
def group_archive(request, pk):
    group = social.group_by_id(pk, request.user)
    if group is None:
        raise Http404("No such group.")
    try:
        social.archive_group(request.user, group)
        messages.success(request, "Group archived.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("groups")


# --- Connections (find + reconnect with people you've shared real activities with) ------


@login_required
def connections_page(request):
    """Your connections + pending requests, and a SEARCH box (query-only, no suggestions feed).
    Search is restricted to people you've shared an activity with, in your own cohort."""
    from apps.connections import services as connections
    from apps.recommendations.services import attach_interest_nodes

    if not connections.is_enabled_for(request.user):
        messages.info(request, "Connections aren't available for your account yet.")
        return redirect("home")
    query = request.GET.get("q", "")
    conns = connections.connections_for(request.user)
    # W4: a filter WITHIN your own connections (already-loaded, read-gated list — plain
    # name match, no new query surface) + pagination so the list stays manageable at scale.
    conn_query = (request.GET.get("cq") or "").strip()
    if conn_query:
        needle = conn_query.lower()
        conns = [
            u
            for u in conns
            if needle in (u.display_name or "").lower() or needle in u.username.lower()
        ]
    from django.core.paginator import Paginator

    conn_page = Paginator(conns, 24).get_page(request.GET.get("page"))
    results = list(connections.search_connectable(request.user, query))
    # Batch-load interests so the constellation avatars in these lists don't N+1 (one query total);
    # the |avatar_uri filter then renders each from the cached nodes. incoming/outgoing show names.
    attach_interest_nodes(list(conn_page.object_list) + results)
    if views_spa.spa_enabled():
        return views_spa.connections_spa(
            request,
            connections=conn_page.object_list,
            conn_page=conn_page,
            conn_query=conn_query,
            conn_total=len(conns),
            incoming=connections.pending_incoming(request.user),
            outgoing=connections.pending_outgoing(request.user),
            query=query,
            results=results,
        )
    return render(
        request,
        "web/connections.html",
        {
            "connections": conn_page.object_list,
            "conn_page": conn_page,
            "conn_query": conn_query,
            "conn_total": len(conns),
            "incoming": connections.pending_incoming(request.user),
            "outgoing": connections.pending_outgoing(request.user),
            "query": query,
            "results": results,
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


def _share_targets(user, *, exclude_pk=None):
    """W6: the threads the viewer could share something into — their own current
    activities (peer memberships only; the post gate re-checks everything)."""
    if not user.is_authenticated:
        return []
    qs = (
        social.visible_activities(user)
        .filter(
            memberships__user=user,
            memberships__state=Membership.State.MEMBER,
            status=Activity.Status.OPEN,
        )
        .exclude(memberships__user=user, memberships__role=Membership.Role.GUARDIAN)
        .distinct()
        .order_by("starts_at")
    )
    if exclude_pk:
        qs = qs.exclude(pk=exclude_pk)
    return list(qs[:50])


@login_required
@require_POST
def share_to_thread(request):
    """W6 'share content': post an activity / venue / event card into one of YOUR
    activity threads. Everything rides the single hardened write path post_to_thread —
    the share target is validated there (cohort / F25 public-place gates) and the card
    is re-gated at render time."""
    kind = request.POST.get("kind")
    obj_id = request.POST.get("obj_id")
    target_pk = request.POST.get("target")
    # obj_id must be a real pk too (review W1-6): a missing/garbage obj_id would make
    # _validate_share a no-op and allow_empty=True would then create a fully EMPTY post.
    if (
        kind not in ("activity", "place", "event")
        or not str(target_pk).isdigit()
        or not str(obj_id).isdigit()
    ):
        raise Http404("Bad share request.")
    target = _visible_activity_or_404(request.user, int(target_pk))
    note = (request.POST.get("note") or "").strip()[:280]
    share_kwargs = {f"share_{kind}": obj_id}
    try:
        post = social.post_to_thread(request.user, target, note, allow_empty=True, **share_kwargs)
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
        return redirect(_safe_next(request, "home"))
    messages.success(request, "Shared to the conversation.")
    return redirect(f"/activities/{target.pk}/#post-{post.pk}")


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


# DSA Art.17 pre-auth redress surface ------------------------------------------------------
# A SUSPEND/TIMED_BAN/BAN sets is_active=False, so the in-app statement-of-reasons is unreachable
# (the user can't log in to read it). This lets them prove credentials WITHOUT a session to read
# WHY, and to CONTEST it. The appeal form is authorised by a signed, short-lived, single-purpose
# token, so the password is entered once and no session is ever granted.
_RESTRICTION_APPEAL_SALT = "safety.restriction-appeal"
_RESTRICTION_APPEAL_MAX_AGE = 1800  # 30 min to write a contest after viewing the statement


def _restricted_statement_context(user, signer, *, error=None):
    """Build the template context for a credential-verified restricted user: their allowlisted
    statement of reasons plus (only when still contestable) a signed appeal-authorisation token."""
    statement = safety.restriction_statement_for(user)
    ctx = {"error": error}
    if statement is None:
        # is_active=False but not due to a current moderation restriction (e.g. self-deactivated):
        # reveal no moderation detail.
        ctx["inactive_no_moderation"] = True
        return ctx
    ctx["statement"] = statement
    if statement["can_appeal"]:
        ctx["appeal_token"] = signer.sign(f"{user.pk}:{statement['action_id']}")
    return ctx


def account_restricted(request):
    """DSA Art.17 redress for a restricted (is_active=False) account: prove credentials to read the
    statement of reasons and contest it, WITHOUT being granted a session. Credential checks share
    the login brute-force lockout and are user-enumeration-safe; the appeal is token-authorised."""
    from django.core import signing

    if request.user.is_authenticated and request.user.is_active:
        return redirect("home")

    signer = signing.TimestampSigner(salt=_RESTRICTION_APPEAL_SALT)
    limit = getattr(settings, "LOGIN_FAILURE_LIMIT", 10)
    window = getattr(settings, "LOGIN_FAILURE_WINDOW_SECONDS", 900)

    # --- Appeal submission (token-authorised; no credentials, no session) ---
    if request.method == "POST" and request.POST.get("appeal_token"):
        statement = request.POST.get("statement", "")
        try:
            payload = signer.unsign(
                request.POST["appeal_token"], max_age=_RESTRICTION_APPEAL_MAX_AGE
            )
            user_pk, action_pk = (int(x) for x in payload.split(":"))
        except (signing.BadSignature, ValueError):
            messages.error(request, "Your session expired. Please sign in again to contest.")
            return redirect("account_restricted")
        user = User.objects.filter(pk=user_pk).first()
        action = ModerationAction.objects.filter(pk=action_pk).first()
        if user is None or action is None:
            messages.error(request, "Your session expired. Please sign in again to contest.")
            return redirect("account_restricted")
        try:
            safety.file_appeal(user, action, statement)
        except safety.AppealError as exc:
            # Re-render the statement with the error and a fresh token (file_appeal self-scopes).
            return render(
                request,
                "web/account_restricted.html",
                _restricted_statement_context(user, signer, error=str(exc)),
            )
        return render(request, "web/account_restricted.html", {"appeal_filed": True})

    # --- Credential verification → show the statement of reasons ---
    if request.method == "POST":
        username = request.POST.get("username", "")
        password = request.POST.get("password", "")
        key = _login_attempt_key(username, _client_ip(request))
        if (cache.get(key) or 0) >= limit:
            return render(
                request,
                "web/account_restricted.html",
                {"error": "Too many attempts. Please wait a few minutes and try again."},
            )
        user = User.objects.filter(username=username).first()
        if user is None:
            # Run the hasher anyway to equalise timing (mitigate username enumeration), then fail.
            User().set_password(password)
            valid = False
        else:
            valid = user.check_password(password)
        if not valid:
            try:
                cache.incr(key)  # count on the SHARED login key — can't bypass the login lockout
            except ValueError:
                cache.set(key, 1, window)
            return render(
                request,
                "web/account_restricted.html",
                {"error": "We couldn't verify those details."},
            )
        cache.delete(key)  # successful proof clears the failure counter
        if user.is_active:
            # Not restricted — reveal nothing; send them to normal login.
            return render(request, "web/account_restricted.html", {"active_account": True})
        return render(
            request, "web/account_restricted.html", _restricted_statement_context(user, signer)
        )

    return render(request, "web/account_restricted.html", {})


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
                        # One real person = one account: refuse a duplicate wallet before the
                        # account is committed (no-op unless uniqueness enforcement is on and
                        # the provider proves holder-key possession).
                        bind_identity(user, result)
                        apply_assurance(user, result)
                except IdentityVerificationError:
                    messages.error(
                        request,
                        "We couldn't verify your age automatically. Your account wasn't "
                        "created - please try again and complete age verification.",
                    )
                    return redirect("register")
                except IdentityBanned:
                    messages.error(
                        request,
                        "This identity is not permitted to register an account.",
                    )
                    return redirect("register")
                except IdentityAlreadyBound:
                    messages.error(
                        request,
                        "An account already exists for this verified identity. Each person "
                        "may hold only one account - please sign in instead.",
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


def _attach_activity_visuals(activities, viewer):
    items = list(activities)
    for activity in items:
        activity.visual = activity_visual(activity, viewer)
    return items


def home(request):
    from apps.web.structured_data import ld_json, site_ld

    site_data = ld_json(site_ld(request))
    if not request.user.is_authenticated:
        return render(request, "web/landing.html", {"structured_data": site_data})
    user = request.user
    from apps.discovery.proximity import parse_point

    # F5: request-only proximity. Used to re-rank the recommended strip toward reachable venues,
    # then discarded — never stored. Radius defaults to the discovery API's 10 km when a point is
    # given (recommend_activities needs BOTH a point and a radius to apply the hard distance cut).
    near_point = parse_point(request.GET)
    radius_m = None
    if near_point is not None:
        raw_radius = request.GET.get("radius_m")
        try:
            radius_m = float(raw_radius) if raw_radius else 10000.0
        except (TypeError, ValueError):
            radius_m = 10000.0
    # W2: one shared feed composition (recommended + interest-matched events + group
    # updates) — the same build_home_feed the mobile feed API serves, so web and API
    # always show the same items for the same honest F17 reasons.
    from apps.discovery.services import build_home_feed

    feed = build_home_feed(user, near_point=near_point, radius_m=radius_m, limit=8)
    recommended = feed["recommended"]
    # W3-F10: the cold-start sliver — a user with NO declared interests yet — gets honest one-tap
    # starter toggles (the types with real upcoming local supply), shown above the soonest-first
    # fallback strip. Gated strictly on zero DECLARED interests (not on an empty `recommended`,
    # which cold-starts to a non-empty soonest-first list) because the home quick-pick submits ONLY
    # the ticked types -> set_interests REPLACES; a user who already declared some uses the additive
    # interests page instead, so their set can never be wiped here.
    starter_types = []
    if not recs.get_interests(user).exists():
        starter_types = recs.suggest_starter_interests(user)
    # W3-F11: a distinct, always-on "welcomes beginners" strip (already deduped against
    # `recommended` in build_home_feed). Its promoted cards are excluded from the soonest-first
    # `upcoming` list below, so a newcomer sees beginner options up top without a card shown twice.
    beginners = feed["beginners"]
    beginners_ids = [a.id for a in beginners]
    beginners_only = request.GET.get("beginners") == "true"
    upcoming_qs = (
        social.visible_activities(user)
        .filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
        .exclude(id__in=beginners_ids)  # W3-F11: not a third copy of the promoted strip cards
        .select_related("place", "activity_type", "owner", "cover")
        .prefetch_related("place__corrections")  # F20: display_name without per-card N+1
    )
    if beginners_only:
        upcoming_qs = upcoming_qs.filter(beginners_welcome=True)
    upcoming_qs, near_active = _order_feed_by_location(upcoming_qs, request.GET)
    upcoming = _attach_activity_visuals(upcoming_qs[:20], user)
    # "Your activities" shows only live meetups: a cancelled/completed one shouldn't sit in
    # the active list pulling members toward a meetup that isn't happening (F1 lifecycle).
    mine = (
        social.visible_activities(user)
        .filter(
            memberships__user=user,
            memberships__state=Membership.State.MEMBER,
            status=Activity.Status.OPEN,
        )
        .select_related("place", "activity_type", "cover")
        .prefetch_related("place__corrections")  # F20
        .distinct()
        .order_by("starts_at")
    )
    recommended = _attach_activity_visuals(recommended, user)
    beginners = _attach_activity_visuals(beginners, user)
    mine = _attach_activity_visuals(mine, user)
    if views_spa.spa_enabled():
        # ADR-0016: same computed feed, React presentation. Legacy render below
        # stays the default until the SOCIAL_REACT_UI switch flips.
        return views_spa.home_spa(
            request,
            recommended=recommended,
            starter_types=starter_types,
            beginners=beginners,
            upcoming=upcoming,
            mine=mine,
            events=feed["events"],
            group_updates=feed["group_updates"],
            near_active=near_active,
            beginners_only=beginners_only,
            guardian_invites=list(pending_guardian_invites_for(user)),
        )
    return render(
        request,
        "web/home.html",
        {
            "structured_data": site_data,
            "recommended": recommended,
            "starter_types": starter_types,
            "beginners": beginners,
            "upcoming": upcoming,
            "mine": mine,
            "events": feed["events"],
            "group_updates": feed["group_updates"],
            "near_active": near_active,
            "beginners_only": beginners_only,
            "guardian_invites": list(pending_guardian_invites_for(user)),
            **_nav_context(user),
        },
    )


def _places_map_categories(city=""):
    from apps.places.services import public_places

    places = public_places(Place.objects.all())
    if city:
        places = places.filter(address_city__iexact=city)
    edges = (
        PlaceActivity.objects.filter(place__in=places, is_disputed=False)
        .select_related("activity__category__parent")
        .order_by("activity__category__parent__name", "activity__category__name")
    )
    categories = {}
    for edge in edges:
        category = edge.activity.category
        top = category.parent or category
        categories.setdefault(top.slug, {"slug": top.slug, "name": top.name})
    return sorted(categories.values(), key=lambda row: row["name"].casefold())


def places_map(request):
    city = request.GET.get("city", "")
    return render(
        request,
        "web/places.html",
        {
            "categories": _places_map_categories(city),
            **_nav_context(request.user),
        },
    )


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
    # F32: a SOFT needs-aware nudge — stably float venues that CONFIRM the viewer's stated access
    # needs to the top, without hiding anything (no-op for an anonymous/no-need viewer). Applied
    # AFTER the distance/name ordering + materialisation, so it composes rather than replaces.
    pref = get_access_preference(request.user)
    capped = sort_by_access_match(capped, pref)
    # F15 compose: attach the venue's positive accessibility facts as terse badges per row, plus a
    # subtle "matches your needs" marker on the rows the nudge promoted.
    for p in capped:
        p.access_tags = [
            r for r in accessibility_facts_display(p) if r["state"] in ("true", "limited")
        ]
        p.access_match = matches_access_preference(accessibility_facts(p), pref) == "match"
    from apps.web.structured_data import itemlist_ld, ld_json, place_entries

    # ItemList of the listed venues so an answer engine can extract them (public places only).
    structured_data = ld_json(itemlist_ld(place_entries(capped), request)) if capped else None
    # A filtered/proximity result page is thin/duplicate/personalised — keep it out of the index;
    # the canonical unfiltered list stays indexable. Still crawled (follow) for its links.
    filtered = point is not None or any(request.GET.get(k) for k in ("activity", "city", "source"))
    list_filters = {
        "activity": request.GET.get("activity", ""),
        "city": request.GET.get("city", ""),
        "source": request.GET.get("source", ""),
    }
    if views_spa.spa_enabled():
        return views_spa.places_spa(
            request,
            places=capped,
            filters=list_filters,
            near_active=point is not None,
            truncated=len(capped) == 200,
            filtered=filtered,
            structured_data=structured_data,
        )
    return render(
        request,
        "web/places_list.html",
        {
            "places": capped,
            "near_active": point is not None,
            "truncated": len(capped) == 200,
            "structured_data": structured_data,
            "filtered": filtered,
            "filters": list_filters,
            **_nav_context(request.user),
        },
    )


def place_detail(request, pk, slug=None):
    from apps.places.services import public_places
    from apps.web.seo import absolute_url, place_path

    place = get_object_or_404(
        Place.objects.prefetch_related("place_activities__activity", "corrections"), pk=pk
    )
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
            .select_related("place", "activity_type", "owner", "cover")
            .prefetch_related("place__corrections")  # F20: _activity_card display_name
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
    from apps.places.services import open_now_status, place_attribution, venue_facts_detail

    edges = [
        pa for pa in place.place_activities.all() if not pa.is_disputed or request.user.is_staff
    ]
    for edge in edges:
        edge.summary = edge_vote_summary(edge, request.user)
    can_contribute = is_public and request.user.is_authenticated and can_participate(request.user)
    # F20: pending name/address corrections (counts only) — applied at read time via display_*.
    from apps.places.services import pending_corrections

    corrections = pending_corrections(place, request.user)
    # F19: crowd venue facts (OSM-first, crowd overlay) + a SOFT kid badge (never hides a place).
    from apps.places.services import KID_FACT_KEYS

    venue_fact_rows = venue_facts_detail(place, request.user)
    has_kid_facts = any(
        row["state"] == "true" and row["key"] in KID_FACT_KEYS for row in venue_fact_rows
    )
    # W3-F15: read-aloud plain-language brief — composed from facts already in context (the free
    # accessibility dict-read + the already-fetched venue_fact_rows), so no new query.
    from apps.places.services import place_plain_brief

    place_brief = place_plain_brief(place, venue_fact_rows=venue_fact_rows)
    # schema.org JSON-LD for crawlers/answer engines — only on a publicly-visible venue (a
    # pending F25 place is viewable solely by its proposer/staff and must not be advertised).
    structured_data = None
    breadcrumb_data = None
    related_city = None
    related_landings = []
    # SEO: the bare /places/<pk>/ and any decorative slug all render 200; the canonical <link>
    # (+ og:url, sitemap, JSON-LD, internal links) point at the keyword-rich slugged path, so
    # search engines consolidate to one URL with no redirect. Pending places keep the default
    # (their name must not leak into a URL).
    canonical_override = absolute_url(place_path(place) if is_public else request.path, request)
    if is_public:
        from django.utils.translation import gettext

        from apps.web.landing import related_landings_for_place
        from apps.web.structured_data import breadcrumb_ld, ld_json, place_ld

        # Embed the venue's upcoming public events in the Place JSON-LD ("what's on at X").
        structured_data = ld_json(place_ld(place, request, events=events))
        breadcrumb_data = ld_json(
            breadcrumb_ld(
                [
                    {"name": gettext("Home"), "url": "/"},
                    {"name": gettext("Places"), "url": reverse("places_list")},
                    {"name": place.display_name, "url": place_path(place)},
                ],
                request,
            )
        )
        # Internal links to the city×activity landing pages this venue belongs to (no dead links).
        related_city, related_landings = related_landings_for_place(place)
    return render(
        request,
        "web/place_detail.html",
        {
            "place": place,
            "structured_data": structured_data,
            "breadcrumb_data": breadcrumb_data,
            "related_city": related_city,
            "related_landings": related_landings,
            "canonical_url": canonical_override,
            "place_brief": place_brief,
            "meetups": meetups,
            "events": events,
            "edges": edges,
            "can_contribute": can_contribute,
            "open_now": open_now_status(place),
            "corrections": corrections,
            "venue_facts": venue_fact_rows,
            "has_kid_facts": has_kid_facts,
            "access_facts": accessibility_facts_display(place),
            "access_match": access_match,
            "has_access_pref": pref is not None,
            "partner": partner_for_place(place),
            "attribution_credit": place_attribution(place),
            "pending_proposal": proposal if pending else None,
            # W6: share this venue into one of the viewer's activity chats (public only).
            "share_targets": _share_targets(request.user) if is_public else [],
            "share_kind": "place",
            "share_obj_id": place.pk,
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
                # ADR-0019 §4: when the proposal started from the organizer form, bounce
                # straight back there with the new place preselected — an ADULT organiser
                # may convene at their own pending proposal immediately (create_activity
                # re-gates), so "the place doesn't exist yet" no longer derails creating
                # the meetup. A fixed allowlisted target, never an open ?next= redirect.
                if request.POST.get("return_to") == "organize":
                    return redirect(f"{reverse('activity_create')}?place={proposal.place_id}")
                return redirect("place_detail", pk=proposal.place_id)
    else:
        form = PlaceProposeForm()
    return render(
        request,
        "web/place_propose.html",
        {
            "form": form,
            "return_to": (
                request.GET.get("return") if request.GET.get("return") == "organize" else ""
            ),
            **_nav_context(request.user),
        },
    )


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
def fact_vote(request, pk):
    """F19: confirm/dispute a concrete venue fact (drinking water, toilets, fenced, ...). The
    service gates verified+consented + public-place + rate-limit and is idempotent per fact."""
    from apps.places.services import PlacesError, vote_on_fact

    place = get_object_or_404(Place, pk=pk)
    try:
        vote_on_fact(
            request.user,
            place,
            request.POST.get("fact_key", ""),
            request.POST.get("value") == "yes",
        )
        messages.success(request, "Thanks - your feedback was recorded.")
    except PlacesError as exc:
        messages.error(request, str(exc))
    return redirect("place_detail", pk=pk)


@login_required
@require_POST
def place_correction_propose(request, pk):
    """F20: suggest a corrected venue name/address (opens a quorum correction)."""
    from apps.places.services import PlacesError, propose_place_correction

    place = get_object_or_404(Place, pk=pk)
    try:
        propose_place_correction(
            request.user,
            place,
            field=request.POST.get("field", ""),
            proposed_value=request.POST.get("proposed_value", ""),
        )
        messages.success(request, "Thanks - others can now confirm your correction.")
    except PlacesError as exc:
        messages.error(request, str(exc))
    return redirect("place_detail", pk=pk)


@login_required
@require_POST
def place_correction_confirm(request, pk, correction_id):
    """F20: confirm a pending correction; a quorum publishes it (applied at read time)."""
    from apps.places.models import PlaceCorrection
    from apps.places.services import InvalidState, NotEligible, confirm_place_correction

    correction = get_object_or_404(PlaceCorrection, pk=correction_id, place_id=pk)
    try:
        confirm_place_correction(request.user, correction)
        messages.success(request, "Thanks - your confirmation was recorded.")
    except (InvalidState, NotEligible) as exc:
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


@login_required
@require_POST
def place_closure_report(request, pk):
    """W3-F13: report that a venue is gone / permanently closed. A quorum hides it from discovery
    and blocks new meetups there (via public_places)."""
    from apps.places.services import NotEligible, file_closure_report

    place = get_object_or_404(Place, pk=pk)
    try:
        result = file_closure_report(request.user, place)
    except NotEligible as exc:
        messages.error(request, str(exc))
    else:
        if result is None:
            messages.info(request, "Thanks - we already have your report for this place.")
        else:
            messages.success(request, "Thanks - we'll hide this venue if others agree it's gone.")
    return redirect("place_detail", pk=pk)


@login_required
@require_POST
def place_closure_reset(request, pk):
    """W3-F13: staff reset of accumulated closure reports (wrongly reported / reopened venue)."""
    from apps.places.services import clear_closure_reports

    if not request.user.is_staff:
        raise Http404("Not found.")
    place = get_object_or_404(Place, pk=pk)
    clear_closure_reports(place, moderator=request.user)
    messages.success(request, "Closure reports cleared.")
    return redirect("place_detail", pk=pk)


# --- Activities ---------------------------------------------------------------------


def _visible_activity_or_404(user, pk) -> Activity:
    activity = get_object_or_404(
        Activity.objects.select_related(
            "place", "activity_type", "owner", "thread"
        ).prefetch_related("place__corrections"),
        pk=pk,
    )
    # Staff/moderators may still open removed content (for review/appeal); members may not.
    if getattr(user, "is_staff", False):
        return activity
    if user.is_authenticated and social.can_see_activity(user, activity) and not activity.is_hidden:
        return activity
    raise Http404("No activity matches the given query.")


@login_required
def activity_list(request):
    beginners_only = request.GET.get("beginners") == "true"
    query = (request.GET.get("q") or "").strip()
    near_active = False
    did_you_mean = None
    if query:
        # W1 search: one bounded, gate-identical path (visible_activities inside). W2-F1 now
        # resolves the type via slug + RO/EN aliases + a synonym walk, so seeded vocabulary
        # matches. Materialise the bounded result so we can offer an honest "did you mean X?"
        # (trigram) only when it found nothing — never auto-applied, never a dead-end suggestion.
        activities = list(
            social.search_activities(
                request.user, query, beginners=beginners_only
            ).prefetch_related("place__corrections")  # F20
        )
        if not activities:
            did_you_mean = social.search_did_you_mean(request.user, query)
    else:
        activities = (
            social.visible_activities(request.user)
            .filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
            .select_related("place", "activity_type", "owner", "cover")
            .prefetch_related("place__corrections")  # F20: _activity_card display_name
        )
        if beginners_only:
            activities = activities.filter(beginners_welcome=True)
        activities, near_active = _order_feed_by_location(activities, request.GET)
    # Pre-encode the suggestion as a query string (a type name may contain a space or '&');
    # blocktrans can't apply |urlencode in its body, so build it here (W2-F1 review).
    did_you_mean_q = urlencode({"q": did_you_mean}) if did_you_mean else ""
    # Two browse modes over the SAME cohort-gated list: "list" (compact scannable rows,
    # the calm default) and "cards" (a focused visual deck - one meetup at a time).
    # The mode is PRESENTATION-ONLY: it never changes which activities are visible, never records a
    # swipe, never ranks people. Both are bounded + paginated (no infinite scroll); the card deck
    # moves within a page client-side (browse-modes.js, progressive enhancement) and the pager
    # advances to the next page. Cards may show one cover photo or a generated accent; unknown
    # value -> "list".
    from django.core.paginator import Paginator

    view_mode = "cards" if request.GET.get("view") == "cards" else "list"
    # .get_page() clamps out-of-range / non-int pages safely; one page bounds the rendered DOM.
    page_obj = Paginator(activities, 24).get_page(request.GET.get("page"))
    activities = _attach_activity_visuals(page_obj, request.user)
    # Carry the current filters (minus page/view) so the view-mode + pager links don't drop them.
    base_params = request.GET.copy()
    for k in ("view", "page"):
        base_params.pop(k, None)
    base_qs = base_params.urlencode()
    if views_spa.spa_enabled():
        return views_spa.browse_spa(
            request,
            activities=activities,
            page_obj=page_obj,
            view_mode=view_mode,
            query=query,
            did_you_mean=did_you_mean,
            did_you_mean_q=did_you_mean_q,
            near_active=near_active,
            beginners_only=beginners_only,
            base_qs=base_qs,
        )
    return render(
        request,
        "web/activities.html",
        {
            "activities": activities,
            "near_active": near_active,
            "beginners_only": beginners_only,
            "query": query,
            "did_you_mean": did_you_mean,
            "did_you_mean_q": did_you_mean_q,
            "view_mode": view_mode,
            "page_obj": page_obj,
            "base_qs": base_qs,
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
        # Reactions: anonymous, COUNTLESS — only the distinct emojis present + the viewer's own.
        rx = social.reactions_for_posts(all_rendered, user)
        # @mention roster computed ONCE (not per post) so highlighting the stream stays one query.
        roster = social.mention_roster(activity)
        allow_links = social.thread_allows_links(activity)  # adult cohort only
        for p in all_rendered:
            p.attachment_list = by_post.get(p.id, [])
            slot = rx.get(p.id, {"present": [], "mine": set()})
            p.reaction_present = slot["present"]
            p.reaction_mine = slot["mine"]
            # @mentions + safe markdown (escaped first; only real peers highlight; minors never
            # get autolinked URLs).
            p.body_html = social.highlight_mentions(p.body, roster, allow_links=allow_links)
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
    # F22: a co-organiser shares the operational tools (edit/cancel/announce) via is_organizer;
    # grant/revoke/transfer stay owner-only and are gated separately in the template.
    is_organizer = social.is_organizer(user, activity)
    can_manage_organizers = is_owner and activity.cohort == Cohort.ADULT
    # F29: LIVE supervision state (never a stored flag) + the owner's add-supervisor candidates.
    supervised = activity.supervised
    supervisor_present = social.active_supervisor_present(activity) if supervised else False
    owner_supervisor_candidates = []
    if is_owner and supervised and not supervisor_present:
        seated_ids = set(
            activity.memberships.filter(role=Membership.Role.GUARDIAN)
            .exclude(state=Membership.State.REMOVED)
            .values_list("user_id", flat=True)
        )
        owner_supervisor_candidates = [
            rel.guardian
            for rel in GuardianRelationship.objects.filter(
                ward=user, status=GuardianRelationship.Status.ACTIVE
            ).select_related("guardian")
            if rel.guardian_id not in seated_ids
        ]
    # Communities this activity belongs to (its area x type/category), as a discovery affordance.
    from apps.communities.services import communities_for_activity

    activity_communities = communities_for_activity(activity)
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
    # W2-F9: ephemeral "on my way / running late" cue. Forward-only, so a member only ever sees
    # the buttons for states they haven't reached yet (mirrors set_transit_status's monotonic gate).
    my_transit = (
        my_membership.transit_status
        if (is_member and my_membership)
        else Membership.TransitStatus.NONE
    )
    can_say_on_my_way = is_member and my_transit == Membership.TransitStatus.NONE
    can_say_running_late = is_member and my_transit in (
        Membership.TransitStatus.NONE,
        Membership.TransitStatus.ON_MY_WAY,
    )
    # W3-F3: the "heading home" departure bookend, shown only to a CHILD member within the
    # end-relative window (the service re-checks). my_departure renders the confirmation line.
    my_departure = my_membership.departing_at if (is_member and my_membership) else None
    can_mark_departing = (
        is_member
        and user.cohort == Cohort.CHILD
        and not my_departure
        and social.departure_window_open(activity)
    )
    # W4-F30: a member may declare they're bringing a personal support person (ADULTS-ONLY at
    # launch). Not capacity-counted; the organiser sees only a logistical count.
    my_brings_support = (
        my_membership.brings_support_person if (is_member and my_membership) else False
    )
    can_set_support = (
        is_member
        and not viewer_is_guardian  # a supervisory guardian is not a participant bringing support
        and user.cohort in social.support_companions_allowed_cohorts()
    )
    # F35: the "catch up" digest, only for someone who can already see the thread. The numeric
    # summary inside it is cohort-gated by the viewer (None for minors — see thread_digest).
    digest = social.thread_digest(activity, user) if (is_member or user.is_staff) else None
    # F39: a self-dismissing first-timer welcome banner, shown to the new joiner for a window
    # after they were welcomed (then it simply ages out — no mutating GET).
    welcome_ttl = timedelta(days=getattr(settings, "F39_WELCOME_BANNER_TTL_DAYS", 7))
    show_welcome = bool(
        is_member
        and my_membership
        and my_membership.welcomed_at
        and timezone.now() - my_membership.welcomed_at <= welcome_ttl
    )
    # W6 "search into chat": members may search this thread's plaintext posts (the
    # service re-gates on can_read_thread; E2EE DMs are unsearchable by design).
    thread_query = (request.GET.get("tq") or "").strip()
    thread_results = None
    if thread_query and (is_member or user.is_staff) and social.can_read_thread(user, activity):
        thread_results = list(social.search_thread_posts(user, activity, thread_query))
    # W4-F11: a calm inline prompt to the organiser when the meetup has hit its go-quorum (it's
    # actually happening) but still has no meeting point — caught on the page they already visit,
    # no notification, no job. Reuses the live attendance snapshot; self-suppresses once set.
    rsvp_summary = social.attendance_summary(activity)
    meeting_point_needed = is_organizer and social.quorum_locked_without_meeting_point(
        activity, rsvp_summary
    )
    # F33: the shared contact-leak ruleset + a translated confirm message, emitted to the
    # client composer (members only — that's the only surface with a compose form). The
    # ruleset is the SAME one the server policy uses (apps.chat.presend), so the two can't
    # drift; the nudge is advisory and dismissible — it never blocks a post.
    from django.utils.translation import gettext

    from apps.chat.presend import client_ruleset

    presend_nudge = {
        "rules": client_ruleset(),
        "message": gettext(
            "This looks like it might share contact details or a plan to meet one-to-one. "
            "To keep everyone safe — especially younger members — try to keep coordination "
            "inside the meetup. Post it anyway?"
        ),
    }
    # Config for the live thread client (static/js/thread-chat.js), passed via json_script so it is
    # XSS-safe. reactUrlTemplate carries a numeric sentinel the client swaps for a real post id (a
    # live post has no server-reversed URL of its own). All UI copy is translated server-side here.
    thread_chat_config = {
        "threadId": activity.thread.id,
        "meId": user.id,
        "reactUrlTemplate": reverse("activity_post_react", args=[activity.pk, 987654321]),
        "emojis": social.allowed_reactions(),
        "i18n": {
            "reply": gettext("Reply"),
            "react": gettext("react"),
            "edited": gettext("(edited)"),
            "replyingTo": gettext("Replying to"),
            "messageSent": gettext("Message sent."),
            "newAnnouncement": gettext("New announcement posted."),
            "newMessages": gettext("New messages"),
            "livePaused": gettext("Live updates paused — reload to catch up."),
            "typingOne": gettext("%(name)s is typing…"),
            "typingTwo": gettext("%(a)s and %(b)s are typing…"),
            "typingMany": gettext("Several people are typing…"),
            "justNow": gettext("just now"),
        },
    }
    return render(
        request,
        "web/activity_detail.html",
        {
            "activity": activity,
            "members": members,
            "is_member": is_member,
            # W2-F27: plain-language read-aloud brief; member-only logistics included only for a
            # member (reuses the same is_member signal the page already computed).
            "meetup_brief": social.plain_meetup_brief(activity, is_member=is_member),
            # W2-F34: a calm "who can see this" line for the composer (shown member-only, where the
            # composer renders). peer_count is cohort-suppressed (None for minors) in the service.
            "thread_audience": social.thread_audience_summary(user, activity)
            if is_member
            else None,
            "is_owner": is_owner,
            "is_organizer": is_organizer,
            "can_manage_organizers": can_manage_organizers,
            "activity_communities": activity_communities,
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
            "reaction_emojis": social.allowed_reactions(),
            "thread_chat_config": thread_chat_config,
            "reply_target": reply_target,
            "photos": photos,
            "post_form": post_form,
            # Disappear options shown in the composer — MINORS (child/teen) never see a sub-day
            # option (their floor is 24h); adults may also pick 1 hour.
            "ephemeral_options": (
                [("3600", "1 hour"), ("86400", "1 day"), ("604800", "1 week")]
                if activity.cohort == Cohort.ADULT
                else [("86400", "1 day"), ("604800", "1 week")]
            ),
            "my_guardians": my_guardians,
            "my_arrival": my_arrival,
            "my_transit": my_transit,
            "my_departure": my_departure,
            "can_mark_departing": can_mark_departing,
            "my_brings_support": my_brings_support,
            "can_set_support": can_set_support,
            "can_say_on_my_way": can_say_on_my_way,
            "can_say_running_late": can_say_running_late,
            "arrival_window_open": is_member and social.arrival_window_open(activity),
            "rsvp_summary": rsvp_summary,
            "meeting_point_needed": meeting_point_needed,
            "met_summary": social.met_confirmation_summary(activity),
            "my_met_confirmed": bool(my_membership.met_confirmed_at)
            if (is_member and my_membership)
            else False,
            "can_join": social.can_join(user, activity),
            # F9: transparency chip — this CHILD meetup is at an approved public venue type.
            "child_safe_venue": (
                activity.cohort == Cohort.CHILD and is_child_safe_venue(activity.place)
            ),
            # F29: LIVE supervision state + the owner's seat-a-guardian affordance.
            "supervised": supervised,
            "supervisor_present": supervisor_present,
            "owner_supervisor_candidates": owner_supervisor_candidates,
            # W5: "start a standing group like this" prefill affordance (members only;
            # the service re-enforces the real creation gate).
            "can_create_group": is_member and _can_create_group(user),
            # W6: share THIS activity into another of the viewer's chats + in-thread search.
            "share_targets": _share_targets(user, exclude_pk=activity.pk) if is_member else [],
            "share_kind": "activity",
            "share_obj_id": activity.pk,
            "thread_query": thread_query,
            "thread_results": thread_results,
            "presend_nudge": presend_nudge,
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
    from apps.places.services import public_places

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
    # W4-F5: "set up another like this" — seed from a meetup the organiser already ran. The
    # ownership gate lives in social.draft_from_activity (a ?from= pointing at someone else's
    # activity injects nothing); setdefault never overwrites an explicit GET param, and
    # create_activity re-validates every seeded value on submit, so a clone can't escape the gate.
    from_id = request.GET.get("from", "")
    if from_id.isdigit():
        source = Activity.objects.filter(pk=from_id).first()
        if source is not None:
            for field, value in social.draft_from_activity(request.user, source).items():
                if value not in (None, ""):
                    initial.setdefault(field, value)
    # F36: seed an editable draft title/description from the chosen type/place/time (composes
    # with the F40 prefill). setdefault only fills EMPTY slots — never overwrites the user's
    # input, and the POST path is untouched.
    if initial.get("activity_type"):
        atype = ActivityType.objects.filter(pk=initial["activity_type"], is_active=True).first()
        if atype is not None:
            place_obj = (
                public_places(Place.objects.filter(pk=initial["place"])).first()
                if initial.get("place")
                else None
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
        form = ActivityForm(request.POST, user=request.user)
        if form.is_valid():
            try:
                activity = social.create_activity(request.user, **form.cleaned_data)
            except social.SocialError as exc:
                messages.error(request, _msg(exc))
            else:
                messages.success(request, "Activity created - you're the owner.")
                return redirect("activity_detail", pk=activity.pk)
    else:
        form = ActivityForm(initial=initial, user=request.user)
    return render(request, "web/activity_form.html", {"form": form, **_nav_context(request.user)})


# --- F4: recurring series (web) ------------------------------------------------------
# A series is an owner-management template, not a meetup: only its owner sees/manages it
# (peers discover the spawned activities via the normal cohort feed).


def _visible_series_or_404(user, pk) -> ActivitySeries:
    series = get_object_or_404(
        ActivitySeries.objects.select_related("place", "activity_type", "owner"), pk=pk
    )
    if getattr(user, "is_staff", False):
        return series
    if user.is_authenticated and series.owner_id == user.id:
        return series
    raise Http404("No series matches the given query.")


@login_required
def series_list(request):
    """The signed-in user's own recurring series."""
    series = social.visible_series(request.user).order_by("status", "next_starts_at")
    return render(request, "web/series_list.html", {"series": series, **_nav_context(request.user)})


@login_required
def series_create(request):
    if not social.can_create_activity(request.user):
        messages.error(
            request,
            "You need to be verified (and, if a minor, have parental consent) and in a cohort "
            "to organize activities.",
        )
        return redirect("profile")
    if request.method == "POST":
        form = SeriesForm(request.POST, user=request.user)
        if form.is_valid():
            try:
                series = social.create_series(request.user, **form.cleaned_data)
            except social.SocialError as exc:
                messages.error(request, _msg(exc))
            else:
                messages.success(
                    request,
                    "Recurring series created - the next meetup is scheduled automatically.",
                )
                return redirect("series_detail", pk=series.pk)
    else:
        form = SeriesForm(user=request.user)
    return render(request, "web/series_form.html", {"form": form, **_nav_context(request.user)})


@login_required
def series_detail(request, pk):
    series = _visible_series_or_404(request.user, pk)
    return render(
        request,
        "web/series_detail.html",
        {
            "series": series,
            "is_owner": series.owner_id == request.user.id,
            "instances": series.instances.order_by("-starts_at")[:10],
            # W2-F14: the one-shot "heads-up for the next meetup" note, pre-filled with what's set.
            "next_note_form": NextInstanceNoteForm(
                initial={"next_instance_note": series.next_instance_note}
            ),
            **_nav_context(request.user),
        },
    )


@login_required
@require_POST
def series_set_next_note(request, pk):
    """W2-F14: owner stages a one-shot note for the next spawned instance (then auto-cleared)."""
    series = _visible_series_or_404(request.user, pk)
    form = NextInstanceNoteForm(request.POST)
    if not form.is_valid():
        messages.error(request, "That note is too long (max 500 characters).")
        return redirect("series_detail", pk=pk)
    try:
        social.set_next_instance_note(request.user, series, form.cleaned_data["next_instance_note"])
        messages.success(request, "Saved - it'll be added to the next meetup, then cleared.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("series_detail", pk=pk)


@login_required
@require_POST
def series_pause(request, pk):
    series = _visible_series_or_404(request.user, pk)
    try:
        social.pause_series(request.user, series)
        messages.success(request, "Series paused - no new meetups will be scheduled.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("series_detail", pk=pk)


@login_required
@require_POST
def series_resume(request, pk):
    series = _visible_series_or_404(request.user, pk)
    try:
        social.resume_series(request.user, series)
        messages.success(request, "Series resumed.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("series_detail", pk=pk)


@login_required
@require_POST
def series_end(request, pk):
    series = _visible_series_or_404(request.user, pk)
    try:
        social.end_series(request.user, series)
        messages.success(request, "Series ended. Already-scheduled meetups still stand.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("series_detail", pk=pk)


@login_required
def activity_edit(request, pk):
    """Owner edits an OPEN, not-yet-started activity (F2). The venue routes through the
    audited move_activity path (ADR-0019 §4 — every member is notified); type stays locked."""
    activity = _visible_activity_or_404(request.user, pk)
    if not social.is_organizer(request.user, activity):  # F22: owner OR co-organiser
        messages.error(request, "Only the organiser can edit this activity.")
        return redirect("activity_detail", pk=pk)
    if request.method == "POST":
        form = ActivityEditForm(request.POST, user=request.user)
        if form.is_valid():
            fields = dict(form.cleaned_data)
            new_place = fields.pop("place", None)
            try:
                # Venue first: if the move is refused by the venue gates, NOTHING is
                # applied — an edit whose destination is invalid shouldn't half-land.
                if new_place is not None and new_place.pk != activity.place_id:
                    social.move_activity(request.user, activity, place=new_place)
                social.update_activity(request.user, activity, **fields)
            except social.SocialError as exc:
                messages.error(request, _msg(exc))
            else:
                messages.success(request, "Activity updated.")
                return redirect("activity_detail", pk=pk)
    else:
        form = ActivityEditForm(
            user=request.user,
            initial={
                "place": activity.place_id,
                "title": activity.title,
                "description": activity.description,
                "starts_at": activity.starts_at,
                "ends_at": activity.ends_at,
                "capacity": activity.capacity,
                "min_to_go": activity.min_to_go,
                "meeting_point": activity.meeting_point,
                "what_to_bring": activity.what_to_bring,
                "organizer_note": activity.organizer_note,
                # Must prefill (F41): required=False would wipe the stored note on edit.
                "first_time_note": activity.first_time_note,
                "cost_band": activity.cost_band,
                "cost_amount": activity.cost_amount,
                "cost_note": activity.cost_note,
                "difficulty": activity.difficulty,
                "accessibility_notes": activity.accessibility_notes,
                "beginners_welcome": activity.beginners_welcome,
            },
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
def activity_add_supervisor(request, pk):
    """F29: the (CHILD) owner seats one of their own verified guardians as the read-only supervisor.
    Thin wrapper over social.add_guardian, which enforces is_guardian_of(guardian, owner) + adult +
    guardian_accompanied, and settles any join that was waiting on supervision."""
    activity = _visible_activity_or_404(request.user, pk)
    guardian = _member_from_post(request)
    if guardian is None:
        messages.error(request, "Choose a guardian to add.")
        return redirect("activity_detail", pk=pk)
    try:
        social.add_guardian(request.user, activity, guardian)
        messages.success(request, "Your guardian was added as supervisor.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_set_supervision(request, pk):
    """F29: the owner turns the supervising-guardian requirement on/off — a guarded toggle, NOT the
    edit path (supervised is deliberately absent from ACTIVITY_EDITABLE_FIELDS)."""
    activity = _visible_activity_or_404(request.user, pk)
    want = request.POST.get("supervised") == "on"
    try:
        social.set_activity_supervision(request.user, activity, want)
        messages.success(
            request,
            "Supervision is now required." if want else "Supervision is no longer required.",
        )
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


def _member_from_post(request):
    """Resolve a target member User from a posted integer user_id, None-safe (a non-numeric id
    never reaches the ORM as an invalid-literal 500)."""
    uid = request.POST.get("user_id", "")
    return User.objects.filter(pk=uid).first() if uid.isdigit() else None


@login_required
@require_POST
def activity_grant_coorg(request, pk):
    """F22: the owner grants a current member co-organiser rights (the service enforces owner-only +
    adult-cohort-only + same-cohort-member)."""
    activity = _visible_activity_or_404(request.user, pk)
    member = _member_from_post(request)
    if member is None:
        messages.error(request, "Pick a member to make a co-organiser.")
        return redirect("activity_detail", pk=pk)
    try:
        social.grant_co_organizer(request.user, activity, member)
        messages.success(request, "Co-organiser added.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_revoke_coorg(request, pk):
    """F22: the owner removes a member's co-organiser rights."""
    activity = _visible_activity_or_404(request.user, pk)
    member = _member_from_post(request)
    if member is None:
        messages.error(request, "Pick a co-organiser to remove.")
        return redirect("activity_detail", pk=pk)
    try:
        social.revoke_co_organizer(request.user, activity, member)
        messages.success(request, "Co-organiser removed.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_transfer_owner(request, pk):
    """F22: the owner hands the activity over to a current member."""
    activity = _visible_activity_or_404(request.user, pk)
    member = _member_from_post(request)
    if member is None:
        messages.error(request, "Pick a member to hand the activity to.")
        return redirect("activity_detail", pk=pk)
    try:
        social.transfer_ownership(request.user, activity, member)
        messages.success(request, "You handed this activity over - they're now the organiser.")
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
def activity_support_companion(request, pk):
    """W4-F30: a member toggles whether they're bringing a personal support person. Not
    capacity-counted; no location, no new contact — the organiser sees only a logistical count."""
    activity = _visible_activity_or_404(request.user, pk)
    try:
        social.set_support_companion(request.user, activity, request.POST.get("brings") == "on")
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
def activity_transit(request, pk):
    """A member shares an ephemeral "on my way" / "running late" cue (W2-F9); the group (and a
    child's guardian) are quietly told once per state. No location, no note — just the tap."""
    activity = _visible_activity_or_404(request.user, pk)
    status = (request.POST.get("status") or "").strip()
    try:
        social.set_transit_status(request.user, activity, status)
        messages.success(request, "Thanks - your group has been told.")
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("activity_detail", pk=pk)


@login_required
@require_POST
def activity_departing(request, pk):
    """A CHILD member self-declares "I'm heading home" (W3-F3); only their active guardian(s)
    are quietly told. No location, no note — just the tap. The departure bookend to arrival."""
    activity = _visible_activity_or_404(request.user, pk)
    try:
        social.mark_departing(request.user, activity)
        messages.success(request, "Thanks - the grown-ups who look after you have been told.")
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
                ping=form.cleaned_data.get("ping", False),
            )
            if data is not None:
                ttl = form.cleaned_data.get("disappear") or None
                media.attach_to_post(
                    request.user,
                    post,
                    filename=upload.name,
                    data=data,
                    ttl_seconds=int(ttl) if ttl else None,
                )
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
def activity_post_react(request, pk, post_id):
    """Toggle the viewer's own emoji reaction on a thread post (anonymous, no count). Returns JSON
    for the live (fetch) client so it can update chips without a reload; redirects for the no-JS
    form POST. The live update for OTHER members rides the toggle's on-commit reaction broadcast."""
    activity = _visible_activity_or_404(request.user, pk)
    post = get_object_or_404(Post, pk=post_id, thread=activity.thread)
    wants_json = request.headers.get("X-Requested-With") == "fetch"
    try:
        social.toggle_reaction(request.user, post, request.POST.get("emoji", ""))
    except social.SocialError as exc:
        if wants_json:
            return JsonResponse({"ok": False, "detail": _msg(exc)}, status=400)
        messages.error(request, _msg(exc))
        return redirect(f"{reverse('activity_detail', args=[pk])}#post-{post_id}")
    if wants_json:
        # Echo back THIS viewer's resulting state (present = anonymous distinct emojis; mine = the
        # viewer's own toggles) so the reactor's own chips update immediately, before/without the
        # broadcast. Never a count, never a who-list.
        slot = social.reactions_for_posts([post], request.user).get(
            post.id, {"present": [], "mine": set()}
        )
        return JsonResponse({"ok": True, "present": slot["present"], "mine": sorted(slot["mine"])})
    return redirect(f"{reverse('activity_detail', args=[pk])}#post-{post_id}")


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
    # W3-F10: honest "popular near you right now" starter toggles — the types with real upcoming
    # local supply that the user hasn't declared yet. Shown above the full picker; excluded from the
    # category groups below so each type's checkbox appears exactly once (never ranked/labelled by a
    # nearby-count — that would be the inv.2 vanity metric).
    starter = recs.suggest_starter_interests(request.user)
    starter_slugs = {t.slug for t in starter}
    # W4: compact category-grouped picker — types arrive grouped so the template renders
    # one chip-section per category instead of one flat wall of checkboxes.
    options = (
        ActivityType.objects.filter(is_active=True)
        .select_related("category")
        .order_by("category__name", "name")
    )
    groups: dict = {}
    for t in options:
        if t.slug in starter_slugs:
            continue  # surfaced once, in the starter highlight above
        groups.setdefault(t.category, []).append(t)
    if views_spa.spa_enabled():
        return views_spa.interests_spa(
            request,
            groups=[(cat, types) for cat, types in groups.items()],
            chosen=chosen,
            chosen_count=len(chosen),
            starter=starter,
        )
    return render(
        request,
        "web/interests.html",
        {
            "groups": [(cat, types) for cat, types in groups.items()],
            "chosen": chosen,
            "chosen_count": len(chosen),
            "starter": starter,
            **_nav_context(request.user),
        },
    )


@login_required
def topic_preferences(request):
    """The user's own hand on the suggestion algorithm (inv.2): pick which TOPICS (top-level
    taxonomy categories) the feed should steer toward. STATED, never inferred from behaviour; a
    SOFT signal that only re-orders + honestly labels cohort-visible suggestions and NEVER hides
    anything. Reads/writes only request.user."""
    from apps.taxonomy.models import ActivityCategory

    if request.method == "POST":
        recs.set_topic_preferences(request.user, request.POST.getlist("topics"))
        messages.success(request, "Your topics were saved.")
        return redirect("topic_preferences")
    if views_spa.spa_enabled():
        return views_spa.topics_spa(
            request,
            categories=ActivityCategory.objects.filter(parent__isnull=True).order_by("name"),
            chosen=set(recs.topic_preference_slugs(request.user)),
        )
    return render(
        request,
        "web/topic_preferences.html",
        {
            # Top-level categories only — picking "sport" already covers its sub-types via the
            # shared category-ancestry walk, so the picker stays short and calm.
            "categories": ActivityCategory.objects.filter(parent__isnull=True).order_by("name"),
            "chosen": set(recs.topic_preference_slugs(request.user)),
            **_nav_context(request.user),
        },
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
            needs_hearing_loop=request.POST.get("needs_hearing_loop") == "on",
            prefers_quiet=request.POST.get("prefers_quiet") == "on",
        )
        messages.success(request, "Your access preferences were saved.")
        return redirect("access_preferences")
    if views_spa.spa_enabled():
        return views_spa.access_spa(request, pref=get_access_preference(request.user))
    return render(
        request,
        "web/access_preferences.html",
        {"pref": get_access_preference(request.user), **_nav_context(request.user)},
    )


# --- F3: saved-search alerts (web) ---------------------------------------------------


@login_required
def saved_searches_page(request):
    """Opt-in, save-only: list the user's saved searches + a create form. No suggestions feed."""
    if not saved_searches.can_save(request.user):
        messages.info(request, "Verify your account to save searches and get match alerts.")
        return redirect("home")
    from apps.social.models import ActivityInterest
    from apps.taxonomy.models import ActivityCategory

    if views_spa.spa_enabled():
        return views_spa.saved_searches_spa(
            request,
            items=saved_searches.saved_searches_for(request.user),
            activity_types=ActivityType.objects.filter(is_active=True).order_by("name"),
            categories=ActivityCategory.objects.all().order_by("name"),
            cost_bands=Activity.CostBand.choices,
            coarse_windows=ActivityInterest.CoarseWindow.choices,
        )
    return render(
        request,
        "web/saved_searches.html",
        {
            "items": saved_searches.saved_searches_for(request.user),
            "activity_types": ActivityType.objects.filter(is_active=True).order_by("name"),
            "categories": ActivityCategory.objects.all().order_by("name"),
            "cost_bands": Activity.CostBand.choices,
            "coarse_windows": ActivityInterest.CoarseWindow.choices,
            **_nav_context(request.user),
        },
    )


@login_required
@require_POST
def saved_search_create(request):
    from apps.taxonomy.models import ActivityCategory

    at_id = (request.POST.get("activity_type") or "").strip()
    cat_id = (request.POST.get("category") or "").strip()
    city = (request.POST.get("city") or "").strip()
    activity_type = None
    if at_id:
        activity_type = (
            ActivityType.objects.filter(pk=at_id, is_active=True).first()
            if at_id.isdigit()
            else None
        )
        if activity_type is None:
            activity_type = ActivityType.objects.filter(slug=at_id, is_active=True).first()
    category = None
    if cat_id:
        category = ActivityCategory.objects.filter(pk=cat_id).first() if cat_id.isdigit() else None
        if category is None:
            category = ActivityCategory.objects.filter(slug=cat_id).first()
    try:
        saved_searches.create_saved_search(
            request.user,
            activity_type=activity_type,
            category=category,
            city=city,  # the service resolves an Area after its anti-abuse gates
            beginners=request.POST.get("beginners") == "on",
            cost_band=request.POST.get("cost_band") or "",
            coarse_window=request.POST.get("coarse_window") or "",
        )
        messages.success(
            request, "Search saved — we'll alert you when a matching activity appears."
        )
    except saved_searches.SavedSearchError as exc:
        messages.error(request, _msg(exc))
    return redirect(_safe_next(request, "saved_searches"))


@login_required
@require_POST
def saved_search_delete(request, pk):
    ss = get_object_or_404(SavedSearch, pk=pk, user=request.user)  # owner-scoped lookup
    saved_searches.delete_saved_search(request.user, ss)
    messages.success(request, "Saved search removed.")
    return redirect(_safe_next(request, "saved_searches"))


# --- Hubs (consolidated navigation landings) ----------------------------------------
# Presentation-only: these wrap existing, individually-gated pages so the redesigned
# nav can group them. Each hub is a thin landing — it adds NO new data path or gate;
# the real work still happens in the underlying views/services (cohort isolation,
# consent, blocking unchanged).


@login_required
def you_hub(request):
    """The 'You' account & settings overview — a single home for the personal pages
    that used to be scattered across the top nav. Pure links; nav_badges() supplies
    has_guardians / connections_enabled for the conditional cards."""
    if views_spa.spa_enabled():
        return views_spa.you_spa(request, nav=_nav_context(request.user))
    return render(request, "web/you.html", _nav_context(request.user))


@login_required
def organize(request):
    """W2-F5: the organizer console — every activity/series/group the viewer runs, each tagged
    with the concrete action it needs now. Read-only; every row links into the existing
    edit/admit/announce screens (the service performs nothing)."""
    console = social.organizer_console(request.user)
    if views_spa.spa_enabled():
        return views_spa.organize_spa(request, **console)
    return render(request, "web/organize.html", {**console, **_nav_context(request.user)})


@login_required
def inbox_hub(request):
    """Retired Inbox entry point. A typed/bookmarked /inbox/ URL lands on notifications."""
    return redirect("notifications")


@login_required
def settings_hub(request):
    """W3: the single Settings page. Everything that used to crowd the top bar lives
    here — language, display, notifications, access needs, privacy/data controls,
    age verification, guardians, and the account dangers (export / delete). Pure
    links + the language form; every linked control keeps its own view and gates."""
    from rest_framework.authtoken.models import Token

    # W10 disclosure (review W1-1): if an API token exists, the user can SEE it here
    # and revoke it with their session — losing the device no longer means an
    # invisible, irrevocable credential.
    api_token = Token.objects.filter(user=request.user).first()
    if views_spa.spa_enabled():
        from django.conf import settings as dj_settings
        from django.utils.translation import get_language

        return views_spa.settings_spa(
            request,
            nav=_nav_context(request.user),
            api_token_created=api_token.created if api_token else None,
            languages=dj_settings.LANGUAGES,
            current_language=get_language(),
        )
    return render(
        request,
        "web/settings.html",
        {
            "api_token_created": api_token.created if api_token else None,
            **_nav_context(request.user),
        },
    )


@login_required
@require_POST
def api_token_revoke(request):
    """Session-authenticated revoke of the account's API token (W10) — the recovery
    path when the device holding the token is lost or shared."""
    from rest_framework.authtoken.models import Token

    deleted, _ = Token.objects.filter(user=request.user).delete()
    if deleted:
        messages.success(request, "API access has been revoked on all devices.")
    else:
        messages.info(request, "There was no API access to revoke.")
    return redirect("settings")


# --- Profile ------------------------------------------------------------------------


@login_required
def profile(request):
    user = request.user
    from apps.connections import services as connections

    chosen = list(
        UserInterest.objects.filter(user=user)
        .select_related("activity_type")
        .values_list("activity_type__name", flat=True)
    )
    blocked = [b.blocked for b in Block.objects.filter(blocker=user).select_related("blocked")]
    if views_spa.spa_enabled():
        return views_spa.profile_spa(
            request,
            nav=_nav_context(user),
            avatar_url=_avatar_url(user, user),
            can_participate=can_participate(user),
            provenance=assurance_provenance(user),
            interests=chosen,
            blocked=blocked,
            connections=connections.connections_for(user),
            pending_in=(
                connections.pending_incoming(user) if connections.is_enabled_for(user) else []
            ),
            progression=social.progression_summary(user),
            journey_avatar=_journey_avatar(user),
        )
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
            "connections": connections.connections_for(user),
            "pending_in": connections.pending_incoming(user)
            if connections.is_enabled_for(user)
            else [],
            # Phase 4: SELF-ONLY progression. profile() renders only request.user, so the "your
            # journey" card never shows another person's count (no other-user profile path sees it).
            "progression": social.progression_summary(user),
            "journey_avatar": _journey_avatar(user),
            **_nav_context(user),
        },
    )


def _journey_avatar(user):
    from apps.recommendations.services import evolving_avatar_data_uri

    return evolving_avatar_data_uri(user, px=120)


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
    if views_spa.spa_enabled():
        return views_spa.notifications_spa(request, items=items)
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
    if views_spa.spa_enabled():
        return views_spa.notification_preferences_spa(request, rows=rows)
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
    from apps.connections import services as connections
    from apps.recommendations.services import attach_interest_nodes, interest_avatar_data_uri

    # Your connections (people you've met) — offered as quick "start a chat" shortcuts so you
    # don't have to type a username. Same-cohort by construction; messaging re-gates can_message.
    conn_users = connections.connections_for(request.user)
    # Same generated avatar (interest constellation) the rest of the UI shows; batch the interest
    # load across the connections + me so the chips don't N+1.
    attach_interest_nodes(conn_users + [request.user])
    conns = [
        {
            "public_id": str(u.public_id),
            "username": u.username,
            "display_name": u.display_name or u.username,
            "avatar": interest_avatar_data_uri(u),
        }
        for u in conn_users
    ]
    config = {
        "me": {
            "public_id": str(request.user.public_id),
            "username": request.user.username,
            "display_name": request.user.display_name or request.user.username,
            "avatar": interest_avatar_data_uri(request.user),
        },
        "connections": conns,
        # The fixed reaction set (same as the thread). In E2EE chat a reaction is an encrypted
        # message the client renders as who+what — the server never sees the emoji.
        "reaction_emojis": social.allowed_reactions(),
    }
    return render(
        request,
        "web/messages.html",
        {
            "messaging_config": config,
            # W5: "start a standing group" entry point from the chat surface (the
            # service still enforces the real creation gate).
            "can_create_group": _can_create_group(request.user),
            **_nav_context(request.user),
        },
    )


# --- Donations ----------------------------------------------------------------------


def donate(request):
    from apps.donations.models import Campaign
    from apps.donations.services import DonationError, cost_anchors, start_donation

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
    return render(
        request,
        "web/donate.html",
        {"form": form, "cost_anchors": cost_anchors(), **_nav_context(request.user)},
    )


def transparency(request):
    """F29: public 'where the money goes' — donations received and staff-entered spending by
    category, as two clearly separate aggregate sections (never an 'X of Y goal' bar)."""
    from apps.donations.services import (
        civic_outcomes,
        completed_total_cents,
        in_kind_by_category,
        spend_by_category,
        spend_total_cents,
    )

    return render(
        request,
        "web/transparency.html",
        {
            "currency": "EUR",
            "civic_outcomes": civic_outcomes(),  # W4-F24 — staff prose, its own section
            "raised_cents": completed_total_cents("EUR"),
            "spend_rows": spend_by_category("EUR"),
            "spend_total_cents": spend_total_cents("EUR"),
            "in_kind_rows": in_kind_by_category("EUR"),  # W3-F20 — its own separate section
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
    """F34: public list of active earmark campaigns with a calm, static progress bar.
    W2-F26: plus a neutral close-out section for completed campaigns with a published outcome."""
    from apps.donations.services import (
        active_campaigns_with_progress,
        completed_campaigns_with_outcomes,
    )

    return render(
        request,
        "web/campaigns.html",
        {
            "campaigns": active_campaigns_with_progress(),
            "completed_campaigns": completed_campaigns_with_outcomes(),
            **_nav_context(request.user),
        },
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
    from apps.communities.models import Area
    from apps.communities.services import _area_place_q
    from apps.events.services import search_events, upcoming_events

    query = (request.GET.get("q") or "").strip()
    activity = request.GET.get("activity")
    # W4-F14: optional city-Area narrowing — the SAME address_city predicate SavedSearch/Group/
    # Community use (no coordinate stored). Resolves by slug; an unknown slug -> no filter.
    area_slug = request.GET.get("area")
    area = Area.objects.filter(slug=area_slug).first() if area_slug else None
    if query:
        # The type filter composes with search — never silently dropped while the
        # "Filtered by X" banner shows (review W1-28).
        events = search_events(query, activity_slug=activity)
    else:
        # upcoming_events applies the F25 pending-place gate (the API HappeningView always
        # had it; the web list previously leaked a pending venue's name through an event).
        events = upcoming_events().order_by("starts_at")
        if activity:
            events = events.filter(activity_type__slug=activity)
    # W4-F14: narrow to the selected city Area (composes with q/activity; a null-place event is
    # correctly dropped once an area is chosen — no NULL-IN leak). Order stays soonest-first.
    if area is not None:
        events = events.filter(_area_place_q(area))
    from apps.web.structured_data import event_entries, itemlist_ld, ld_json

    events = list(events[:100])
    # ItemList so an answer engine can extract the visible list directly (public events only).
    structured_data = ld_json(itemlist_ld(event_entries(events), request)) if events else None
    # A filtered/search result page is thin/duplicate — keep it out of the index (the canonical
    # unfiltered list stays indexable). The page is still crawled (follow) for its links.
    filtered = bool(query or activity or area_slug)
    if views_spa.spa_enabled():
        return views_spa.events_spa(
            request,
            events=events,
            query=query,
            activity=activity,
            areas=Area.objects.order_by("name"),
            area=area.slug if area else "",
            area_name=area.name if area else "",
            filtered=filtered,
            structured_data=structured_data,
        )
    return render(
        request,
        "web/events.html",
        {
            "events": events,
            "activity": activity,
            "query": query,
            "areas": Area.objects.order_by("name"),
            "area": area.slug if area else "",
            "area_name": area.name if area else "",
            "structured_data": structured_data,
            "filtered": filtered,
            **_nav_context(request.user),
        },
    )


# --- Public city×activity "things to do" landing pages (SEO) --------------------------------
# Login-free, open-data only: every query routes through public_places()/upcoming_events(), so a
# cohort activity, a minor, or a pending venue can never appear. Empty combos 404 (no thin pages).


def things_to_do_index(request):
    from apps.web.landing import available_landings
    from apps.web.seo import cache_public

    combos = available_landings()
    cities = {}
    for area, activity_type in combos:
        cities.setdefault(area, []).append(activity_type)
    grouped = sorted(cities.items(), key=lambda kv: kv[0].name)
    if views_spa.spa_enabled():
        return cache_public(views_spa.landing_index_spa(request, grouped=grouped), request)
    return cache_public(
        render(
            request,
            "web/landing_index.html",
            {"grouped": grouped, **_nav_context(request.user)},
        ),
        request,
    )


def things_to_do_city(request, area_slug):
    from apps.communities.models import Area
    from apps.web.landing import available_landings
    from apps.web.seo import cache_public

    area = get_object_or_404(Area, slug=area_slug, is_active=True)
    activities = [t for a, t in available_landings() if a.pk == area.pk]
    if not activities:
        raise Http404("Nothing here yet.")
    if views_spa.spa_enabled():
        return cache_public(
            views_spa.landing_city_spa(request, area=area, activities=activities), request
        )
    return cache_public(
        render(
            request,
            "web/landing_city.html",
            {"area": area, "activities": activities, **_nav_context(request.user)},
        ),
        request,
    )


def things_to_do(request, area_slug, activity_slug):
    from django.utils.translation import gettext

    from apps.communities.models import Area
    from apps.taxonomy.models import ActivityType
    from apps.web.landing import landing_supply
    from apps.web.seo import cache_public
    from apps.web.structured_data import breadcrumb_ld, event_entries, itemlist_ld, ld_json

    area = get_object_or_404(Area, slug=area_slug, is_active=True)
    activity_type = get_object_or_404(ActivityType, slug=activity_slug, is_active=True)
    places, events = landing_supply(area, activity_type)
    places = list(places)
    events = list(events[:50])
    if not (places or events):
        raise Http404("Nothing here yet.")
    # ItemList of the upcoming events so answer engines can extract the list directly.
    structured_data = ld_json(itemlist_ld(event_entries(events), request)) if events else None
    breadcrumb_data = ld_json(
        breadcrumb_ld(
            [
                {"name": gettext("Home"), "url": "/"},
                {"name": gettext("Things to do"), "url": reverse("things_to_do_index")},
                {"name": area.name, "url": reverse("things_to_do_city", args=[area.slug])},
                {
                    "name": activity_type.name,
                    "url": reverse("things_to_do", args=[area.slug, activity_type.slug]),
                },
            ],
            request,
        )
    )
    if views_spa.spa_enabled():
        return cache_public(
            views_spa.landing_detail_spa(
                request,
                area=area,
                activity_type=activity_type,
                places=places,
                events=events,
                structured_data=structured_data,
                breadcrumb_data=breadcrumb_data,
            ),
            request,
        )
    return cache_public(
        render(
            request,
            "web/landing_detail.html",
            {
                "area": area,
                "activity_type": activity_type,
                "places": places,
                "events": events,
                "structured_data": structured_data,
                "breadcrumb_data": breadcrumb_data,
                **_nav_context(request.user),
            },
        ),
        request,
    )


def event_detail(request, pk, slug=None):
    # Same F25 gate as the list: an event at a still-unpublished user-proposed place
    # must not disclose that place (or its name) on the detail page either. Mirror
    # place_detail's carve-out (review W1-13): the place's PROPOSER and staff may still
    # open it, so the pending-place flow doesn't dead-end.
    # F21: annotate the recent-report count at query time (one query, no wasted prefetch) so
    # event_reliability reads the annotation instead of re-counting per request.
    from datetime import timedelta

    from django.db.models import Count, Q

    from apps.events.services import events_with_public_places

    decay = getattr(settings, "EVENT_REPORT_DECAY_SECONDS", 14 * 24 * 3600)
    cutoff = timezone.now() - timedelta(seconds=decay)
    event = get_object_or_404(
        Event.objects.select_related("place", "activity_type").annotate(
            recent_report_n=Count("reports", filter=Q(reports__created_at__gte=cutoff))
        ),
        pk=pk,
    )
    is_public_event = events_with_public_places().filter(pk=event.pk).exists()
    if not is_public_event:
        proposal = getattr(event.place, "proposal", None) if event.place_id else None
        is_proposer = (
            request.user.is_authenticated
            and proposal is not None
            and proposal.proposer_id == request.user.id
        )
        if not (request.user.is_staff or is_proposer):
            raise Http404("No event matches the given query.")
    # F21: read-time accuracy flag from crowd reports + the member report affordance. The report
    # form shows only on a PUBLIC event (mirrors the event_report gate — no form that would 404).
    from apps.events.services import event_attribution, event_reliability
    from apps.web.seo import absolute_url, event_path

    # SEO: canonical points at the keyword-rich slugged path (bare/decorative-slug URLs all 200);
    # a pending event keeps the request path so a hidden place's name can't leak via the slug.
    canonical_override = absolute_url(
        event_path(event) if is_public_event else request.path, request
    )
    # schema.org Event JSON-LD — only on a public event (a pending-place event is proposer/
    # staff-only and must not be advertised to crawlers).
    structured_data = None
    breadcrumb_data = None
    related_landing = None
    if is_public_event:
        from django.utils.translation import gettext

        from apps.web.landing import landing_for_event
        from apps.web.structured_data import breadcrumb_ld, event_ld, ld_json

        structured_data = ld_json(event_ld(event, request))
        breadcrumb_data = ld_json(
            breadcrumb_ld(
                [
                    {"name": gettext("Home"), "url": "/"},
                    {"name": gettext("Events"), "url": reverse("events_list")},
                    {"name": event.title, "url": event_path(event)},
                ],
                request,
            )
        )
        # Internal link to the city×activity landing this event belongs to (always live supply).
        related_landing = landing_for_event(event)
    return render(
        request,
        "web/event_detail.html",
        {
            "event": event,
            "structured_data": structured_data,
            "breadcrumb_data": breadcrumb_data,
            "related_landing": related_landing,
            "canonical_url": canonical_override,
            "event_reliability": event_reliability(event),
            "attribution_credit": event_attribution(event),
            "can_report_event": (
                is_public_event and request.user.is_authenticated and can_participate(request.user)
            ),
            # W4-F12: offer "convene a gauge around this" only on a PUBLIC event (whose place can
            # seed a gauge) to a user who can actually create one.
            "can_convene": (
                is_public_event
                and request.user.is_authenticated
                and can_participate(request.user)
                and event.place_id is not None
            ),
            "share_targets": _share_targets(request.user),
            "share_kind": "event",
            "share_obj_id": event.pk,
            **_nav_context(request.user),
        },
    )


@login_required
@require_POST
def event_report(request, pk):
    """F21: report that an event has changed (cancelled / moved / wrong time)."""
    from apps.events.services import events_with_public_places, file_event_report

    # F25 gate: you can only report an event you can actually SEE — never one at a still-pending
    # user-proposed place (mirrors the event_detail visibility gate; no report-on-invisible).
    event = get_object_or_404(events_with_public_places(), pk=pk)
    try:
        result = file_event_report(request.user, event, request.POST.get("kind", ""))
    except (PermissionError, ValueError) as exc:
        messages.error(request, str(exc))
    else:
        if result is None:
            messages.info(request, "Thanks - we already have your report for this event.")
        else:
            messages.success(request, "Thanks - we'll flag the event if others agree.")
    return redirect("event_detail", pk=pk)


@login_required
@require_POST
def event_report_reset(request, pk):
    """F21: staff reset of accumulated change-reports for an event."""
    from apps.events.services import clear_event_reports

    if not request.user.is_staff:
        raise Http404("Not found.")
    event = get_object_or_404(Event, pk=pk)
    clear_event_reports(event, moderator=request.user)
    messages.success(request, "Event reports cleared.")
    return redirect("event_detail", pk=pk)


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
        # Surface symmetry with register / EUDIVerifyView: one real person = one account. A real
        # wallet that proves holder-key possession binds here too (and a banned or already-bound
        # wallet is refused BEFORE the age is applied); the sandbox proves no key, so it's a no-op.
        try:
            bind_identity(request.user, result)
        except IdentityBanned:
            messages.error(request, "This identity is not permitted to verify on this account.")
            return redirect("profile")
        except IdentityAlreadyBound:
            messages.error(
                request,
                "That verified identity is already linked to another account. Each person may "
                "hold only one account.",
            )
            return redirect("profile")
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
    from apps.taxonomy.models import ActivityCategory

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
                # Mirror the read-time wall every other surface enforces (visible_activities /
                # _coorg_eligible / can_read_thread): never surface an activity whose cohort no
                # longer matches the ward (a stale membership after a cohort change) or one a
                # moderator has hidden. Without these, F18's free-text logistics would leak.
                cohort=ward.cohort,
                is_hidden=False,
            )
            .select_related("place", "activity_type")
            .order_by("starts_at")
            .distinct()
        )
        # W4-F2: annotate each meetup with the LIVE supervision state, computed at render time
        # (never the static a.supervised flag), so the chip can't falsely reassure a parent that an
        # adult is present after the supervisor has left. supervision_satisfied is the same live
        # predicate the join-settle gate uses (GUARDIAN-role seat keyed on is_guardian_of OWNER).
        # W4-F4: a "why this venue is child-approved" credit (CHILD wards only — the F9 child-venue
        # gate is CHILD-only). None when the venue reads 'unknown' at render time -> no credit.
        from apps.places.services import child_venue_rationale

        for meetup in ward.meetups:
            meetup.supervision_live = social.supervision_satisfied(meetup)
            meetup.child_venue_rationale = (
                child_venue_rationale(meetup.place) if ward.cohort == Cohort.CHILD else None
            )
        # F13: the legible can/cannot boundary for this guardianship, from the real rules.
        # caps already carries this guardian's F7 guardrail values (for pre-filling the form).
        ward.caps = guardianship_capabilities(request.user, ward)
        # W4-F1: an honest dry-run of what the CURRENT combined guardrails allow — "could join N of
        # the next M upcoming meetups". CHILD-only (None otherwise); reuses the real gate fns so it
        # can't drift, and explains a too-tight combination instead of looking like a broken app.
        ward.guardrail_preview = social.guardrail_preview(ward)
        # The ward's CURRENT chosen feed topics (SOFT suggestion steering) so the guardian's
        # "topics this child's feed leans toward" form pre-checks them. Distinct from the HARD
        # category envelope in caps above — this never restricts what the ward can join. CHILD-only,
        # mirroring the F7 guardrail scope (the write path re-checks the same).
        ward.can_set_topics = ward.cohort == Cohort.CHILD
        ward.topic_slugs = set(recs.topic_preference_slugs(ward))
    return render(
        request,
        "web/wards.html",
        {
            "wards": ward_users,
            "minor_onboarding": minor_onboarding_enabled(),
            "guardrail_hours": range(24),
            # W3-F1: (ISO day, short label) for the family-calendar weekday checkboxes.
            "guardrail_weekdays": [
                (1, "Mon"),
                (2, "Tue"),
                (3, "Wed"),
                (4, "Thu"),
                (5, "Fri"),
                (6, "Sat"),
                (7, "Sun"),
            ],
            # W3-F2: top-level activity categories (slug, name) for the allowlist checkboxes. A
            # ticked top category covers its whole subtree (the gate walks the type's ancestry).
            "guardrail_categories": list(
                ActivityCategory.objects.filter(parent__isnull=True)
                .order_by("name")
                .values_list("slug", "name")
            ),
            **_nav_context(request.user),
        },
    )


def _my_upcoming_meetups(user):
    """The viewer's OWN upcoming meetups behind the same read wall the wards manifest enforces:
    OPEN + future + admitted MEMBER + CURRENT cohort + never moderator-hidden. The single source of
    truth for the /my-meetups/ page (F38) and the self-only /account/calendar.ics download (W3-F18)
    — so neither can ever surface a cancelled / hidden / past / stale-cross-cohort meetup, nor a
    CHILD's future place+time outside the cohort/consent wall."""
    return (
        Activity.objects.filter(
            memberships__user=user,
            memberships__state=Membership.State.MEMBER,
            status=Activity.Status.OPEN,
            starts_at__gte=timezone.now(),
            cohort=user.cohort,
            is_hidden=False,
        )
        .select_related("place", "activity_type")
        # F20: place.display_name reads applied corrections — prefetch so the list is O(1) queries.
        .prefetch_related("place__corrections")
        .order_by("starts_at")
        .distinct()
    )


@login_required
def my_meetups(request):
    """F38: the viewer's OWN upcoming meetups (time, place, meeting point) + the guardians they
    can turn to — a lean, no-JS page a member can read en route. A service worker serves it
    NETWORK-FIRST and falls back to the last-cached copy offline (with a freshness stamp)."""
    now = timezone.now()
    meetups = list(_my_upcoming_meetups(request.user))
    # The viewer's own safe-exit guardians (names only — no contact details), readable offline.
    my_guardians = [
        rel.guardian
        for rel in GuardianRelationship.objects.filter(
            ward=request.user, status=GuardianRelationship.Status.ACTIVE
        ).select_related("guardian")
    ]
    return render(
        request,
        "web/my_meetups.html",
        {
            "meetups": meetups,
            "my_guardians": my_guardians,
            # Baked into the HTML, so the offline (cached) copy honestly shows WHEN it was saved.
            "generated_at": now,
            **_nav_context(request.user),
        },
    )


@login_required
def my_venues(request):
    """W4-F18: a self-only data-quality digest — for the meetups the viewer is GOING to, flag any
    whose venue currently reads crowd-reported-closed, unverified-hours, or has a pending crowd
    correction, so they can check before heading out. Pure read: reuses _my_upcoming_meetups
    verbatim (so it can never surface another member's meetup — a guardian sees their OWN, never a
    ward's) and composes only existing overlay reads. A PAGE only — never a job or notification."""
    from apps.places.services import venue_quality_flags

    seen = set()
    venues = []
    for activity in _my_upcoming_meetups(request.user):
        place = activity.place
        if place is None or place.id in seen:
            continue
        seen.add(place.id)
        flags = venue_quality_flags(place)
        if flags:  # only surface venues that actually have a concern
            venues.append({"place": place, "flags": flags})
    return render(
        request,
        "web/my_venues.html",
        {"venues": venues, **_nav_context(request.user)},
    )


def _ics_escape(text) -> str:
    r"""RFC 5545 text escaping for SUMMARY/LOCATION: backslash FIRST, then semicolon, comma, and
    every newline → literal ``\n`` (a raw newline would otherwise break the line-based grammar)."""
    return (
        (text or "")
        .replace("\\", "\\\\")
        .replace(";", "\\;")
        .replace(",", "\\,")
        .replace("\r\n", "\\n")
        .replace("\n", "\\n")
        .replace("\r", "\\n")
    )


def _ics_fold(line: str) -> str:
    """RFC 5545 §3.1 content-line folding: no content line exceeds 75 OCTETS; a continuation begins
    with CRLF + a single space (stripped on unfold). Split on UTF-8 octet boundaries so a multibyte
    character is never severed (a SUMMARY/LOCATION can hold non-ASCII)."""
    raw = line.encode("utf-8")
    if len(raw) <= 75:
        return line
    chunks, start, limit = [], 0, 75
    while start < len(raw):
        end = min(start + limit, len(raw))
        while end < len(raw) and (raw[end] & 0xC0) == 0x80:  # never split a continuation byte
            end -= 1
        chunks.append(raw[start:end].decode("utf-8"))
        start, limit = end, 74  # continuation lines lose one octet to the leading space
    return "\r\n ".join(chunks)


def _build_calendar(activities, *, host: str) -> str:
    """Build a minimal RFC 5545 VCALENDAR (one VEVENT per meetup) as a pure standard-library
    string. Times are emitted in UTC (the stored tz-aware value, suffixed Z); every calendar client
    converts them back to the reader's local zone. CRLF line breaks + 75-octet folding per spec."""
    import datetime as _dt

    def _utc(value) -> str:
        return value.astimezone(_dt.UTC).strftime("%Y%m%dT%H%M%SZ")

    stamp = _utc(timezone.now())
    lines = [
        "BEGIN:VCALENDAR",
        "VERSION:2.0",
        f"PRODID:-//{host}//meetups//EN",
        "CALSCALE:GREGORIAN",
        "METHOD:PUBLISH",
    ]
    for a in activities:
        lines += [
            "BEGIN:VEVENT",
            f"UID:meetup-{a.id}@{host}",
            f"DTSTAMP:{stamp}",
            f"DTSTART:{_utc(a.starts_at)}",
        ]
        if a.ends_at:
            lines.append(f"DTEND:{_utc(a.ends_at)}")
        lines.append(f"SUMMARY:{_ics_escape(a.title)}")
        lines.append(f"LOCATION:{_ics_escape(a.place.display_name)}")
        lines.append("END:VEVENT")
    lines.append("END:VCALENDAR")
    return "\r\n".join(_ics_fold(line) for line in lines) + "\r\n"


@login_required
def my_calendar(request):
    """W3-F18: a self-only, one-time .ics DOWNLOAD of the viewer's OWN upcoming meetups (place +
    time + type) so they can drop them into a phone/desktop calendar — a real show-up + dignity win
    given the app has no push. Session-authenticated and served as a file attachment (mirrors
    account_export's GET pattern), deliberately NOT a tokenized subscribable feed: a long-lived
    secret URL fetched by an external calendar client with no session would disclose a member's —
    possibly a CHILD's — future place+time outside the cohort/consent wall. Behind the same read
    wall as /my-meetups/ (``_my_upcoming_meetups``)."""
    from django.http import HttpResponse

    payload = _build_calendar(_my_upcoming_meetups(request.user), host=request.get_host())
    resp = HttpResponse(payload, content_type="text/calendar; charset=utf-8")
    resp["Content-Disposition"] = f'attachment; filename="my-meetups-{request.user.public_id}.ics"'
    return resp


# F38: a minimal, root-scoped service worker. Served at /sw.js (root => scope "/" with no extra
# header needed; we set Service-Worker-Allowed defensively). NETWORK-FIRST for /my-meetups/ so a
# live cancel/edit is always preferred; the cached copy is only a last-resort offline fallback.
# Caches NOTHING else authenticated. Purged on logout + on a user switch (see base.html), so a
# shared/borrowed phone never serves one member's saved meetups to another.
_SERVICE_WORKER_JS = """\
'use strict';
const CACHE = 'mz-meetups-v1';
const PAGE = '/my-meetups/';
const ASSETS = ['/static/css/base.css'];

self.addEventListener('install', (e) => {
  self.skipWaiting();
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(ASSETS).catch(() => {})));
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys.filter((k) => k.startsWith('mz-meetups') && k !== CACHE).map((k) => caches.delete(k))
      )
    ).then(() => self.clients.claim())
  );
});

// The page postMessages this on logout / user-switch — drop the on-device copy immediately.
self.addEventListener('message', (e) => {
  if (e.data && e.data.type === 'purge') {
    e.waitUntil(caches.delete(CACHE));
  }
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  const isPage = url.pathname === PAGE;
  const isAsset = ASSETS.indexOf(url.pathname) !== -1;
  if (!isPage && !isAsset) return; // manage ONLY the meetups page + its stylesheet

  if (isPage) {
    // NETWORK-FIRST: always prefer the live page (so a cancellation shows); cache the fresh copy;
    // only fall back to the cached copy when the network is unavailable.
    e.respondWith(
      fetch(req)
        .then((resp) => {
          if (resp && resp.ok) {
            const copy = resp.clone();
            caches.open(CACHE).then((c) => c.put(PAGE, copy));
          }
          return resp;
        })
        .catch(() => caches.match(PAGE))
    );
    return;
  }
  // The stylesheet: serve the cached copy at once (fast, keeps the offline page styled) but
  // refresh it in the background when online, so a deployed CSS change reaches the user on their
  // next online load (stale-while-revalidate) rather than being pinned until CACHE is bumped.
  e.respondWith(
    caches.match(req).then((hit) => {
      const live = fetch(req)
        .then((resp) => {
          if (resp && resp.ok) {
            const copy = resp.clone();
            caches.open(CACHE).then((c) => c.put(req, copy));
          }
          return resp;
        })
        .catch(() => hit);
      return hit || live;
    })
  );
});
"""


def service_worker(request):
    """Serve the F38 service worker at the site root so its scope is '/'. A plain view (WhiteNoise
    can't set headers, and /static/ would limit the scope) returning the JS with the right
    content type. No auth: the SW script itself is non-sensitive; the data it caches is fetched
    per-session and purged on logout."""
    from django.http import HttpResponse

    resp = HttpResponse(_SERVICE_WORKER_JS, content_type="text/javascript")
    resp["Service-Worker-Allowed"] = "/"
    resp["Cache-Control"] = "no-cache"  # always re-check the SW script itself for updates
    return resp


def _active_ward_or_none(guardian, ward_pk):
    """The actor's CURRENTLY-ACTIVE ward by pk, resolved in a SINGLE query — so a non-guardian
    gets an identical response whether or not the pk exists. Avoids a 404-vs-redirect
    user-enumeration oracle on child accounts (cf. the deliberately non-distinguishing
    guardian_invite_create). Returns None when the actor is not this user's active guardian."""
    return User.objects.filter(
        pk=ward_pk,
        guardians__guardian=guardian,
        guardians__status=GuardianRelationship.Status.ACTIVE,
    ).first()


@login_required
@require_POST
def guardian_guardrail_set(request, ward_pk):
    """A guardian sets conservative participation limits on a CHILD ward (F7). Reuses
    accounts.set_guardian_guardrail, which re-checks the ACTIVE guardianship + CHILD cohort and
    audits in-transaction. Guardrails only ever NARROW the ward's access (see can_join)."""
    ward = _active_ward_or_none(request.user, ward_pk)
    if ward is None:
        messages.error(request, "You are not this user's guardian.")
        return redirect("wards")
    # Throttle: each save writes an audit row; mirror the guardian-invite throttle.
    if not safety.allow_action(
        request.user,
        "guardian_guardrail",
        limit=getattr(settings, "GUARDIAN_GUARDRAIL_RATE_LIMIT", 30),
        window_seconds=getattr(settings, "GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS", 3600),
    ):
        messages.error(request, "Too many updates; please try again later.")
        return redirect("wards")
    try:
        set_guardian_guardrail(
            request.user,
            ward,
            supervised_only=request.POST.get("supervised_only") == "on",
            latest_start_hour=request.POST.get("latest_start_hour", ""),
            max_open_joins=request.POST.get("max_open_joins", ""),
            # W3-F1: weekday checkboxes (getlist) + the earliest-start-hour bookend.
            allowed_weekdays=request.POST.getlist("allowed_weekdays"),
            earliest_start_hour=request.POST.get("earliest_start_hour", ""),
            # W3-F2: activity-category allowlist checkboxes (getlist; none ticked = any type).
            allowed_categories=request.POST.getlist("allowed_categories"),
        )
        messages.success(request, "Participation limits saved.")
    except ValueError as exc:
        messages.error(request, _msg(exc))
    return redirect("wards")


@login_required
@require_POST
def ward_topics_set(request, ward_pk):
    """A guardian sets the TOPICS their CHILD ward's suggestion feed steers toward — "the
    responsible person controls the feed". Same ACTIVE-guardian gate + throttle as the
    participation limits. This is a SOFT signal (re-orders + honestly labels suggestions, never
    hides anything); the HARD child-safety category ENVELOPE is the separate guardrail allowlist
    on this same /wards/ page. The ward may also adjust their own topics — this is shared control,
    not a lockout (a soft preference, not a safety gate)."""
    ward = _active_ward_or_none(request.user, ward_pk)
    if ward is None:
        messages.error(request, "You are not this user's guardian.")
        return redirect("wards")
    # Guardian-set feed steering is for CHILD wards only — the same scope as the F7 guardrails
    # (a teen self-manages; an aged-up adult with a lingering ACTIVE link is excluded here too).
    if ward.cohort != Cohort.CHILD:
        messages.error(request, "This setting is only available for a child's account.")
        return redirect("wards")
    if not safety.allow_action(
        request.user,
        "guardian_ward_topics",
        limit=getattr(settings, "GUARDIAN_GUARDRAIL_RATE_LIMIT", 30),
        window_seconds=getattr(settings, "GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS", 3600),
    ):
        messages.error(request, "Too many updates; please try again later.")
        return redirect("wards")
    recs.set_topic_preferences(ward, request.POST.getlist("topics"))
    messages.success(request, "Suggested topics saved.")
    return redirect("wards")


@login_required
@require_POST
def guardian_revoke(request, ward_pk):
    """A guardian ends a guardianship link (F13). Reuses accounts.revoke_guardian, which also
    revokes that guardian's consent grant and drops any messaging-observer presence."""
    ward = _active_ward_or_none(request.user, ward_pk)
    if ward is None:
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
    elif target_type == "post":
        # A thread post — in an activity OR a group thread. Resolve the owner generically (a group
        # thread's .activity is None) so a member (incl. a minor in a group thread) can report a
        # post directly; can_see_activity is a cohort check both an Activity and a Group satisfy.
        post = Post.objects.filter(pk=target_id).first()
        if post and (user.is_staff or social.can_see_activity(user, post.thread.owner_object)):
            return post, (post.author.display_name or post.author.username)
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


@login_required
@require_POST
def activity_unsafe(request, pk):
    """F8: the one-tap "I feel unsafe" button on the safe-exit card. Files a real moderation report
    against the activity and (for a CHILD) alerts the active guardians — see
    safety.file_unsafe_report. Member-only and never the owner (mirrors the card's own gate), so it
    can't be used as a drive-by report endpoint beyond the detailed /report/ slow path."""
    activity = _visible_activity_or_404(request.user, pk)
    membership = activity.memberships.filter(
        user=request.user, state=Membership.State.MEMBER
    ).first()
    # Member, not the owner, and not a supervisory GUARDIAN seat (mirrors the card gate + the
    # thread-write services' GUARDIAN exclusion — a guardian uses their own channels).
    if (
        membership is None
        or activity.owner_id == request.user.id
        or membership.role == Membership.Role.GUARDIAN
    ):
        messages.error(request, "That option isn't available here.")
        return redirect("activity_detail", pk=pk)
    try:
        result = safety.file_unsafe_report(request.user, activity)
    except safety.RateLimited:
        # Stay reassuring even when rate-limited: a scared child must never get a scolding error.
        messages.success(
            request,
            "We've got your earlier alert and a moderator is looking. If you're in danger right "
            "now, tell a trusted adult or call your local emergency number.",
        )
        return redirect("activity_detail", pk=pk)
    # Reassurance reflects exactly what happened: only claim a guardian was told when one actually
    # was (file_unsafe_report alerts guardians only for a CHILD, and excludes blocked ones).
    if result.repeat:
        messages.success(
            request,
            "Thanks — we already have your alert and a moderator is looking. You can leave this "
            "activity any time.",
        )
    elif result.guardians_alerted:
        messages.success(
            request,
            "Thank you for telling us. A moderator has been alerted, and the grown-ups who look "
            "after you have been told. You can leave this activity any time.",
        )
    else:
        messages.success(
            request,
            "Thank you for telling us. A moderator has been alerted. You can leave this activity "
            "any time.",
        )
    return redirect("activity_detail", pk=pk)


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


@login_required
@require_POST
def activity_listing_toggle(request, pk):
    """Organiser opt-IN toggle for anonymous discovery of an ADULT activity (default OFF)."""
    activity = get_object_or_404(Activity, pk=pk)
    listed = request.POST.get("listed") == "1"
    try:
        social.set_public_listing(request.user, activity, listed)
        messages.success(
            request,
            "This activity is now visible to people who aren't logged in."
            if listed
            else "This activity is now hidden from logged-out visitors.",
        )
    except (social.NotAMember, social.InvalidState) as exc:
        messages.error(request, _msg(exc))
    return redirect(_safe_next(request, "home"))


@login_required
@require_POST
def group_listing_toggle(request, pk):
    """Organiser opt-IN toggle for anonymous discovery of an ADULT group (default OFF)."""
    group = get_object_or_404(Group, pk=pk)
    listed = request.POST.get("listed") == "1"
    try:
        social.set_public_listing(request.user, group, listed)
        messages.success(
            request,
            "This group is now visible to people who aren't logged in."
            if listed
            else "This group is now hidden from logged-out visitors.",
        )
    except (social.NotAMember, social.InvalidState) as exc:
        messages.error(request, _msg(exc))
    return redirect(_safe_next(request, "home"))


def discover(request):
    """Public, logged-out discovery of ADULT activities and groups looking for people. Sources
    from the cohort=ADULT-walled social.public_activities()/public_groups() so a minor's meetup
    or group can never appear here. Open to everyone (no login required)."""
    from apps.discovery.views import MAX_RESULTS

    activities = list(social.public_activities()[:MAX_RESULTS])
    groups = list(social.public_groups()[:MAX_RESULTS])
    return render(
        request,
        "web/discover.html",
        {"activities": activities, "groups": groups},
    )


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


@login_required
@require_POST
def safety_record_appeal(request):
    """F19/DSA Art.17: file a contest against a decision shown on /my-safety-record/ (logged-in
    path — e.g. a content removal while the account is still active). Self-scoped via file_appeal;
    a decision affecting someone else 404s rather than revealing it exists."""
    action_id = request.POST.get("action_id", "")
    statement = request.POST.get("statement", "")
    action = ModerationAction.objects.filter(pk=action_id).first() if action_id.isdigit() else None
    if action is None or safety._affected_user(action.target) != request.user:
        raise Http404("No such decision.")
    try:
        safety.file_appeal(request.user, action, statement)
        messages.success(request, "Thanks - your contest was received. We'll review it.")
    except safety.AppealError as exc:
        messages.error(request, str(exc))
    return redirect("safety_record")


@login_required
def activity_log(request):
    """F34: a read-only, plain-language list of the safety-relevant actions you took, drawn from the
    tamper-evident audit log. Strictly self-scoped (safety.audit_log_for keys on actor_ref); each
    row is an allowlisted {label, when} — no raw event code, target, or payload reaches the page."""
    return render(
        request,
        "web/activity_log.html",
        {
            "entries": safety.audit_log_for(request.user),
            **_nav_context(request.user),
        },
    )


@login_required
def my_privacy(request):
    """F36: a single self-only "what we know about you" front door. It only re-renders already-
    built, strictly self-scoped reads (band-only age provenance, muted-kind list, capped safety-
    record counts, interest/donation counts) and deep-links each category to its existing control,
    plus honest negative-space statements. No user_id param, no new data — it cannot widen exposure
    beyond what those services already permit."""
    from apps.donations.models import Donation

    user = request.user
    record = safety.safety_record_for(user)
    muted_labels = sorted(
        Notification.Kind(value).label
        for value in notifications.get_muted_kinds(user)
        if value in Notification.Kind.values
    )
    access = get_access_preference(user)
    access_set = bool(
        access
        and (access.needs_step_free or access.needs_accessible_toilet or access.prefers_quiet)
    )
    return render(
        request,
        "web/my_privacy.html",
        {
            "provenance": assurance_provenance(user),
            "access_set": access_set,
            "muted_labels": muted_labels,
            "interests_count": UserInterest.objects.filter(user=user).count(),
            "donations_count": Donation.objects.filter(donor=user).count(),
            # Counts mirror exactly what the linked /my-safety-record/ page shows (same cap).
            "decisions_count": len(record["decisions"]),
            "reports_count": len(record["reports"]),
            # W10 disclosure: a device may hold API access; the revoke lives in Settings.
            "api_token_exists": _api_token_exists(user),
            # W3-F16: how long each category of the user's data is kept (durations only).
            "retention": retention_disclosure(user),
            **_nav_context(user),
        },
    )


def _api_token_exists(user) -> bool:
    from rest_framework.authtoken.models import Token

    return Token.objects.filter(user=user).exists()


def display_preferences(request):
    """F12: choose a display theme (auto/light/dark/high-contrast), text size, and motion. Stored in
    FUNCTIONAL cookies that apply to everyone (no login, no per-user data); the display_preferences
    context processor reads them back onto <html>. No-JS friendly (a plain form + submit)."""
    from django.utils.translation import gettext

    from apps.web.context_processors import (
        MOTION_COOKIE,
        MOTIONS,
        TEXT_COOKIE,
        TEXT_SIZES,
        THEME_COOKIE,
        THEMES,
    )

    if request.method == "POST":
        resp = redirect("display_preferences")
        one_year = 60 * 60 * 24 * 365
        for cookie, allowed in (
            (THEME_COOKIE, THEMES),
            (TEXT_COOKIE, TEXT_SIZES),
            (MOTION_COOKIE, MOTIONS),
        ):
            value = request.POST.get(cookie, "")
            if value in allowed:  # ignore anything off the allowlist (no garbage cookies)
                resp.set_cookie(
                    cookie,
                    value,
                    max_age=one_year,
                    samesite="Lax",
                    secure=request.is_secure(),
                )
        messages.success(request, gettext("Your display settings were saved."))
        return resp
    # The current selections come from the context processor (display_theme/text/motion), so the
    # template just marks the matching option — works for signed-in and signed-out visitors alike.
    return render(request, "web/display_preferences.html", _nav_context(request.user))


def privacy(request):
    return render(request, "web/privacy.html", _nav_context(request.user))


def terms(request):
    return render(request, "web/terms.html", _nav_context(request.user))


# --- GDPR self-service account deletion (right to erasure) ---------------------------


@login_required
def account_export(request):
    """F35: one-click GDPR Art.20 download of the user's OWN data as a JSON file. Self-scoped —
    reuses the hardened build_user_export (the same payload as the DRF /api/accounts/me/export/
    endpoint), served as a file attachment so the browser saves it instead of rendering inline.
    No card/payment data is stored, so none can leak; only the requesting user's own data."""
    import json

    from django.http import HttpResponse

    from apps.accounts.export import build_user_export

    payload = json.dumps(build_user_export(request.user), indent=2, ensure_ascii=False)
    resp = HttpResponse(payload, content_type="application/json")
    resp["Content-Disposition"] = f'attachment; filename="my-data-{request.user.public_id}.json"'
    return resp


@login_required
def account_delete(request):
    """Let a user erase their own account (GDPR Art. 17). On GET, show an honest counts-only
    preview of exactly what erasure destroys and the one audit pseudonym that lawfully survives
    (F33) — this also fixes the my_privacy "Delete my account" link, which previously GET-hit a
    POST-only endpoint and 405'd. On POST, delegate to the accounts domain service (which enforces
    the real erasure/retention rules), then log the user out and return them to the landing page."""
    # Imported lazily: these are owned by the accounts domain service.
    from apps.accounts.services import erase_user, erasure_preview

    if request.method == "POST":
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

    preview = erasure_preview(request.user, request.user)
    return render(request, "web/account_delete.html", {"preview": preview})


# --- F27: gauge-interest (ephemeral proto-meetups) web --------------------------------


@login_required
def gauges(request):
    """Active gauges in the viewer's cohort — count-only cards. The ephemeral sibling of the
    standing Groups list."""
    user = request.user
    threshold = social.interest_threshold()
    items = []
    for g in social.visible_gauges(user):
        count = social.interest_count(g)
        items.append(
            {
                "gauge": g,
                "ready": count >= threshold,
                "remaining": max(threshold - count, 0),
                "mine": g.proposer_id == user.id,
            }
        )
    return render(request, "web/gauges.html", {"items": items, **_nav_context(user)})


@login_required
def gauge_create(request):
    if not social.can_create_activity(request.user):
        messages.error(
            request,
            "You need to be verified (and, if a minor, have parental consent) and in a cohort "
            "to start a gauge.",
        )
        return redirect("gauges")
    if request.method == "POST":
        form = GaugeForm(request.POST)
        if form.is_valid():
            try:
                gauge = social.propose_interest(
                    request.user,
                    place=form.cleaned_data["place"],
                    activity_type=form.cleaned_data["activity_type"],
                    coarse_window=form.cleaned_data["coarse_window"],
                )
            except social.SocialError as exc:
                messages.error(request, _msg(exc))
            else:
                messages.success(request, "Gauge started — peers can now signal interest.")
                return redirect("gauge_detail", pk=gauge.pk)
    else:
        # W4-F12: "convene around this event" — seed the gauge from a real public event the browser
        # found but where no meetup has formed. Validate ?event= through events_with_public_places
        # (so a pending F25 venue can never seed a gauge); seed place + type only and let the user
        # pick their availability window. propose_interest re-validates everything on submit.
        initial = {}
        event_id = request.GET.get("event", "")
        if event_id.isdigit():
            from apps.events.services import events_with_public_places

            event = events_with_public_places().filter(pk=event_id).first()
            if event is not None:
                if event.place_id:
                    initial["place"] = event.place_id
                if event.activity_type_id:
                    initial["activity_type"] = event.activity_type_id
        form = GaugeForm(initial=initial)
    return render(request, "web/gauge_form.html", {"form": form, **_nav_context(request.user)})


@login_required
def gauge_detail(request, pk):
    user = request.user
    gauge = social.gauge_by_id(pk, user)
    if gauge is None:
        raise Http404("No such gauge.")
    count = social.interest_count(gauge)
    threshold = social.interest_threshold()
    return render(
        request,
        "web/gauge_detail.html",
        {
            "gauge": gauge,
            "count": count,
            "threshold": threshold,
            "remaining": max(threshold - count, 0),
            "ready": count >= threshold,
            "is_proposer": gauge.proposer_id == user.id,
            "viewer_interested": gauge.interested_users.filter(id=user.id).exists(),
            **_nav_context(user),
        },
    )


@login_required
@require_POST
def gauge_interested(request, pk):
    gauge = social.gauge_by_id(pk, request.user)
    if gauge is None:
        raise Http404("No such gauge.")
    try:
        social.mark_interested(request.user, gauge)
    except social.SocialError as exc:
        messages.error(request, _msg(exc))
    return redirect("gauge_detail", pk=pk)


@login_required
@require_POST
def gauge_uninterested(request, pk):
    gauge = social.gauge_by_id(pk, request.user)
    if gauge is None:
        raise Http404("No such gauge.")
    social.unmark_interested(request.user, gauge)
    return redirect("gauge_detail", pk=pk)


@login_required
def gauge_convert(request, pk):
    gauge = social.gauge_by_id(pk, request.user)
    # Only the proposer may convert; a non-proposer (or a converted/expired gauge, which
    # gauge_by_id already filters out) gets a 404 — never a usable convert form.
    if gauge is None or gauge.proposer_id != request.user.id:
        raise Http404("No such gauge.")
    if request.method == "POST":
        form = GaugeConvertForm(request.POST)
        if form.is_valid():
            try:
                activity = social.convert_to_activity(
                    request.user,
                    gauge,
                    title=form.cleaned_data["title"],
                    starts_at=form.cleaned_data["starts_at"],
                    ends_at=form.cleaned_data.get("ends_at"),
                    description=form.cleaned_data.get("description", ""),
                )
            except social.SocialError as exc:
                messages.error(request, _msg(exc))
            else:
                messages.success(
                    request, "Your gauge is now a real meetup — interested peers were invited."
                )
                return redirect("activity_detail", pk=activity.pk)
    else:
        form = GaugeConvertForm()
    return render(
        request,
        "web/gauge_convert.html",
        {"form": form, "gauge": gauge, **_nav_context(request.user)},
    )


@login_required
def spa_preview(request):
    """Phase-1 React pipeline proof (DEBUG-only URL): Aurora design preview."""
    return render(request, "web/spa_preview.html", _nav_context(request.user))
