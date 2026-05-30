"""Server-rendered web UI over the (API-first) backend. Views call the same domain
services the API uses, so the safety invariants (cohort isolation, consent gating,
membership-scoped media) hold identically here."""

import math

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
    can_participate,
    create_guardian_link_invite,
    decline_guardian_link_invite,
    pending_guardian_invites_for,
)
from apps.events.models import Event
from apps.media.models import Photo
from apps.media.services import NotAuthorized, signed_url, thread_photos, upload_photo
from apps.notifications import services as notifications
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.recommendations import services as recs
from apps.recommendations.models import UserInterest
from apps.safety import services as safety
from apps.safety.models import Block
from apps.social import services as social
from apps.social.models import Activity, JoinVote, Membership
from apps.taxonomy.models import ActivityType

from .forms import (
    ActivityEditForm,
    ActivityForm,
    DonateForm,
    PostForm,
    RegisterForm,
    ReportForm,
)


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
        return {"unread_notifications": notifications.unread_count(user)}
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
    for a in recommended:
        distance = getattr(a, "rec_distance", None)
        if distance is not None:
            a.match_pct = max(0, min(100, round((1 - float(distance)) * 100)))
    upcoming_qs = (
        social.visible_activities(user)
        .filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
        .select_related("place", "activity_type", "owner")
    )
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
            "guardian_invites": list(pending_guardian_invites_for(user)),
            **_nav_context(user),
        },
    )


def places_map(request):
    return render(request, "web/places.html", _nav_context(request.user))


def place_detail(request, pk):
    place = get_object_or_404(Place.objects.prefetch_related("place_activities__activity"), pk=pk)
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
    return render(
        request,
        "web/place_detail.html",
        {"place": place, "meetups": meetups, "events": events, **_nav_context(request.user)},
    )


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
    activities, near_active = _order_feed_by_location(activities, request.GET)
    return render(
        request,
        "web/activities.html",
        {"activities": activities, "near_active": near_active, **_nav_context(request.user)},
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

    thread_posts = list(activity.thread.posts.filter(is_hidden=False).select_related("author"))
    # Owner announcements (F11) pin above the ordinary thread, newest-first.
    announcements = [p for p in reversed(thread_posts) if p.is_announcement]
    posts = [p for p in thread_posts if not p.is_announcement]
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
    return render(
        request,
        "web/activity_detail.html",
        {
            "activity": activity,
            "members": members,
            "is_member": is_member,
            "is_owner": is_owner,
            "is_open": activity.status == Activity.Status.OPEN,
            "my_membership": my_membership,
            "pending": pending,
            "announcements": announcements,
            "posts": posts,
            "photos": photos,
            "post_form": PostForm(),
            "my_guardians": my_guardians,
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
    activity = _visible_activity_or_404(request.user, pk)
    form = PostForm(request.POST)
    if form.is_valid():
        try:
            social.post_to_thread(request.user, activity, form.cleaned_data["body"])
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
            "interests": chosen,
            "blocked": blocked,
            **_nav_context(user),
        },
    )


@login_required
@require_POST
def avatar_upload(request):
    upload = request.FILES.get("image")
    if upload is not None:
        try:
            upload_photo(request.user, Photo.Kind.PROFILE, upload.read())
            messages.success(request, "Profile picture updated.")
        except ValueError as exc:
            messages.error(request, _msg(exc))
    return redirect("profile")


# --- Notifications ------------------------------------------------------------------


@login_required
def notifications_list(request):
    items = Notification.objects.filter(recipient=request.user)[:50]
    return render(request, "web/notifications.html", {"items": items, **_nav_context(request.user)})


@login_required
@require_POST
def notifications_read_all(request):
    notifications.mark_all_read(request.user)
    return redirect("notifications")


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
    from apps.donations.services import DonationError, start_donation

    if request.method == "POST":
        form = DonateForm(request.POST)
        if form.is_valid():
            cents = int(form.cleaned_data["amount"] * 100)
            try:
                _donation, checkout_url = start_donation(request.user, cents)
            except DonationError as exc:
                messages.error(request, _msg(exc))
            else:
                return redirect(checkout_url)
    else:
        form = DonateForm()
    return render(request, "web/donate.html", {"form": form, **_nav_context(request.user)})


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
    return render(
        request,
        "web/wards.html",
        {"wards": ward_users, **_nav_context(request.user)},
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
