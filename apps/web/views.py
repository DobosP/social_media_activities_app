"""Server-rendered web UI over the (API-first) backend. Views call the same domain
services the API uses, so the safety invariants (cohort isolation, consent gating,
membership-scoped media) hold identically here."""

import math

from django.contrib import messages
from django.contrib.auth import login
from django.contrib.auth.decorators import login_required
from django.core.exceptions import ValidationError
from django.http import Http404
from django.shortcuts import get_object_or_404, redirect, render
from django.utils import timezone
from django.views.decorators.http import require_POST

from apps.accounts.identity.registry import get_identity_provider
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance, can_participate
from apps.media.models import Photo
from apps.media.services import NotAuthorized, signed_url, thread_photos, upload_photo
from apps.notifications import services as notifications
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.recommendations import services as recs
from apps.recommendations.models import UserInterest
from apps.social import services as social
from apps.social.models import Activity, JoinVote, Membership
from apps.taxonomy.models import ActivityType

from .forms import ActivityForm, DonateForm, PostForm, RegisterForm


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
    return render(
        request,
        "web/home.html",
        {"recommended": recommended, "upcoming": upcoming, "mine": mine, **_nav_context(user)},
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
    return render(
        request,
        "web/place_detail.html",
        {"place": place, "meetups": meetups, **_nav_context(request.user)},
    )


# --- Activities ---------------------------------------------------------------------


def _visible_activity_or_404(user, pk) -> Activity:
    activity = get_object_or_404(
        Activity.objects.select_related("place", "activity_type", "owner", "thread"), pk=pk
    )
    if getattr(user, "is_staff", False) or (
        user.is_authenticated and social.can_see_activity(user, activity)
    ):
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

    posts = activity.thread.posts.select_related("author").all()
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
    return render(
        request,
        "web/profile.html",
        {
            "profile_user": user,
            "avatar_url": _avatar_url(user, user),
            "can_participate": can_participate(user),
            "interests": chosen,
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
