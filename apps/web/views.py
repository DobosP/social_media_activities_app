"""Server-rendered web UI over the (API-first) backend. Views call the same domain
services the API uses, so the safety invariants (cohort isolation, consent gating,
membership-scoped media) hold identically here."""

import math

from django.conf import settings
from django.contrib import messages
from django.contrib.auth import login, logout
from django.contrib.auth.decorators import login_required
from django.core.exceptions import ValidationError
from django.http import Http404
from django.shortcuts import get_object_or_404, redirect, render
from django.utils import timezone
from django.utils.crypto import get_random_string
from django.views.decorators.http import require_POST

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

from .forms import ActivityForm, DonateForm, PostForm, RegisterForm, ReportForm


def _msg(exc) -> str:
    if isinstance(exc, ValidationError):
        return "; ".join(exc.messages)
    return str(exc)


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
                user = User.objects.create_user(
                    username=data["username"], password=data["password"]
                )
                user.display_name = data["display_name"]
                user.save(update_fields=["display_name"])
                result = get_identity_provider().verify(user, age_band=data["age_band"])
                apply_assurance(user, result)
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
    upcoming = (
        social.visible_activities(user)
        .filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
        .select_related("place", "activity_type", "owner")
        .order_by("starts_at")[:20]
    )
    mine = (
        social.visible_activities(user)
        .filter(memberships__user=user, memberships__state=Membership.State.MEMBER)
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
        .order_by("starts_at")
    )
    return render(
        request,
        "web/activities.html",
        {"activities": activities, **_nav_context(request.user)},
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

    posts = activity.thread.posts.filter(is_hidden=False).select_related("author")
    photos = []
    if is_member or user.is_staff:
        try:
            photos = list(thread_photos(user, activity.thread))
        except NotAuthorized:
            photos = []
        for photo in photos:
            photo.url = signed_url(photo, user)

    return render(
        request,
        "web/activity_detail.html",
        {
            "activity": activity,
            "members": members,
            "is_member": is_member,
            "is_owner": activity.owner_id == user.id,
            "my_membership": my_membership,
            "pending": pending,
            "posts": posts,
            "photos": photos,
            "post_form": PostForm(),
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
    ward_users = User.objects.filter(
        guardians__guardian=request.user,
        guardians__status=GuardianRelationship.Status.ACTIVE,
    ).distinct()
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


@login_required
@require_POST
def block_user_view(request, pk):
    target = get_object_or_404(User, pk=pk)
    try:
        safety.block_user(request.user, target)
        messages.success(request, f"Blocked {target.display_name or target.username}.")
    except ValueError as exc:
        messages.error(request, _msg(exc))
    return redirect(request.POST.get("next") or "home")


@login_required
@require_POST
def unblock_user_view(request, pk):
    target = get_object_or_404(User, pk=pk)
    safety.unblock_user(request.user, target)
    messages.success(request, f"Unblocked {target.display_name or target.username}.")
    return redirect(request.POST.get("next") or "profile")


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
