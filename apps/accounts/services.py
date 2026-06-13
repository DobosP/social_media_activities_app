import logging
import secrets
from datetime import timedelta
from math import ceil

from django.conf import settings
from django.db import transaction
from django.utils import timezone
from django.utils.translation import gettext_lazy as _

from .models import (
    COHORT_BY_AGE_BAND,
    AgeAssurance,
    AgeBand,
    Cohort,
    GuardianGuardrail,
    GuardianLinkInvite,
    GuardianRelationship,
    ParentalConsent,
    User,
)

logger = logging.getLogger(__name__)

# Friendlier labels for the age-proof "method" token shown to users (F14).
_METHOD_LABELS = {"openid4vp": "the EU Digital Identity wallet"}


def assign_cohort(age_band: str) -> str:
    return COHORT_BY_AGE_BAND.get(age_band, Cohort.UNASSIGNED)


def apply_assurance(user: User, result) -> AgeAssurance:
    """Persist an assurance result onto the user and record it. Does NOT by itself
    grant participation for minors — that still requires valid parental consent."""
    old_cohort = user.cohort
    user.age_band = result.age_band
    user.recompute_cohort()
    user.is_identity_verified = bool(result.verified)
    user.identity_verified_at = timezone.now() if result.verified else None
    user.save(update_fields=["age_band", "cohort", "is_identity_verified", "identity_verified_at"])
    # A cohort change on re-verification must evict the user from conversations pinned to
    # their OLD cohort (cohort isolation): every such conversation is now cross-cohort for
    # them. (First verification from UNASSIGNED has no prior conversations to clean.)
    if user.cohort != old_cohort and old_cohort != Cohort.UNASSIGNED:
        from apps.messaging.services import remove_user_from_conversations
        from apps.social.services import remove_user_from_groups

        remove_user_from_conversations(user, reason="cohort_changed")
        # The user's standing groups were all pinned to their OLD cohort, so every one is now
        # cross-cohort: evict them (the read-time cohort wall in can_read_thread/group_roster also
        # fails closed, but eviction keeps rosters/feeds clean).
        remove_user_from_groups(user, reason="cohort_changed")
    return AgeAssurance.objects.create(
        user=user,
        provider=result.provider,
        method=result.method,
        age_band=result.age_band,
        expires_at=result.expires_at,
        raw=result.raw,
    )


def minor_onboarding_enabled() -> bool:
    """Whether this deployment permits onboarding minors (guardian-linking + consent).
    OFF in production by default until a real parental-responsibility trust anchor exists
    (the mutual-click guardian link is not verifiable proof of a parent-child relationship).
    See settings.ALLOW_MINOR_ONBOARDING and docs/AUDIT_STRESS_2026-05-29.md (L-GUARDIAN)."""
    return getattr(settings, "ALLOW_MINOR_ONBOARDING", True)


def has_valid_parental_consent(user: User) -> bool:
    return any(consent.is_valid() for consent in user.parental_consents.all())


def is_assurance_current(user: User) -> bool:
    """Identity verification is only valid while the *latest* age-assurance proof is
    unexpired. A proof with no expiry never lapses; an expired proof means the user must
    re-verify — so a child who ages out of a band, or whose attestation has gone stale,
    can no longer participate (join/post/chat) until they re-verify. (Cohort is not
    recomputed here; it is re-derived on the next successful assurance.)

    Falls back to the denormalized ``is_identity_verified`` flag when no assurance row
    exists (e.g. a staff/legacy account verified out-of-band)."""
    if not user.is_identity_verified:
        return False
    latest = AgeAssurance.objects.filter(user=user).order_by("-verified_at", "-id").first()
    if latest is None:
        return True
    return latest.expires_at is None or latest.expires_at > timezone.now()


def assurance_provenance(user: User) -> dict | None:
    """Read-only provenance of the user's age proof, for the F14 profile panel. Returns ONLY
    the proven band, method, provider and timestamps plus a derived status — NEVER any
    identity/DOB/raw-attestation PII. Reuses is_assurance_current() for the validity gate so
    the panel can never drift from what actually governs participation.

    Returns None when there is nothing to show (no assurance row and not verified)."""
    latest = AgeAssurance.objects.filter(user=user).order_by("-verified_at", "-id").first()
    if latest is None:
        if not user.is_identity_verified:
            return None
        # Verified out-of-band (e.g. legacy/staff) with no assurance row.
        return {
            "has_row": False,
            "is_current": True,
            "band_display": user.get_age_band_display(),
            "provider": None,
            "method": None,
            "verified_at": None,
            "expires_at": None,
            "status": "no_expiry",
            "expires_soon": False,
            "days_left": None,
        }
    is_current = is_assurance_current(user)
    # Map a valid band to its label; never render an unknown/invalid value as a "proof".
    band_display = dict(AgeBand.choices).get(latest.age_band, "")
    reminder_days = getattr(settings, "REVERIFY_REMINDER_DAYS", 14)
    days_left = None
    if latest.expires_at is not None:
        days_left = max(0, ceil((latest.expires_at - timezone.now()).total_seconds() / 86400))
    # Status order matters: an expired proof must never be mislabelled "expiring".
    if latest.expires_at is None:
        status, expires_soon = "no_expiry", False
    elif not is_current:
        status, expires_soon = "expired", False
    elif days_left <= reminder_days:
        status, expires_soon = "expiring", True
    else:
        status, expires_soon = "current", False
    return {
        "has_row": True,
        "provider": latest.provider,
        "method": _METHOD_LABELS.get(latest.method, latest.method),
        "band_display": band_display,
        "verified_at": latest.verified_at,
        "expires_at": latest.expires_at,
        "is_current": is_current,
        "expires_soon": expires_soon,
        "days_left": days_left,
        "status": status,
    }


def _active_guardians(ward: User) -> list:
    """The ward's currently-ACTIVE guardians (keyed strictly on an ACTIVE GuardianRelationship —
    never a loose flag), for safety fan-outs. Mirrors the mark_arrived guardian-ping idiom."""
    return [
        rel.guardian
        for rel in GuardianRelationship.objects.filter(
            ward=ward, status=GuardianRelationship.Status.ACTIVE
        ).select_related("guardian")
    ]


@transaction.atomic
def _pause_lapsed_minor(minor: User, latest: AgeAssurance) -> None:
    """Evict a minor whose age proof has LAPSED from cohort-pinned rosters/conversations and send a
    one-time SYSTEM 'paused — re-verify' notice. is_assurance_current already fails closed at every
    action gate; this is the ACTIVE cleanup so a lapsed minor doesn't linger in a roster until they
    next act. Idempotent: evictions no-op once removed, and the EXPIRED marker stops re-notify."""
    from apps.messaging.services import remove_user_from_conversations
    from apps.notifications.models import Notification
    from apps.notifications.services import notify
    from apps.social.services import remove_user_from_groups

    remove_user_from_groups(minor, reason="assurance_expired")
    remove_user_from_conversations(minor, reason="assurance_expired")
    latest.reverify_notice = AgeAssurance.ReverifyNotice.EXPIRED
    latest.save(update_fields=["reverify_notice"])
    notify(
        minor,
        Notification.Kind.SYSTEM,
        str(_("Your age verification has expired")),
        body=str(_("Re-verify your age to keep joining and chatting.")),
        url="/verify-age/",
    )


@transaction.atomic
def _nudge_reverify_soon(minor: User, latest: AgeAssurance) -> None:
    """Send a one-time SYSTEM 'expiring soon' nudge to a minor AND each ACTIVE guardian, so they
    re-verify before the proof lapses. The SOON marker makes it at-most-once per proof."""
    from apps.notifications.models import Notification
    from apps.notifications.services import notify

    notify(
        minor,
        Notification.Kind.SYSTEM,
        str(_("Your age verification is expiring soon")),
        body=str(_("Re-verify your age soon to keep joining and chatting.")),
        url="/verify-age/",
    )
    for guardian in _active_guardians(minor):
        notify(
            guardian,
            Notification.Kind.SYSTEM,
            str(_("Your ward's age verification is expiring soon")),
            body=str(_("Your ward needs to re-verify their age soon to keep participating.")),
            url="/verify-age/",
        )
    latest.reverify_notice = AgeAssurance.ReverifyNotice.SOON
    latest.save(update_fields=["reverify_notice"])


def run_reverify_sweep(*, now=None) -> dict:
    """F6: proactively pause/nudge minors on a stale age proof — ACTIVE enforcement of EUDI expiry,
    which is otherwise only checked lazily at action time (is_assurance_current). For each CHILD/
    TEEN minor, look at their LATEST proof: if it has LAPSED, evict them from cohort-pinned
    rosters/conversations + a one-time SYSTEM notice; if it is EXPIRING within the reminder window,
    a one-time SYSTEM nudge to them + their ACTIVE guardians. Reads only band/expiry, never DOB. The
    per-proof sent-marker makes every notice at-most-once. Evictions are CAPPED per tick and the cap
    is AUDITED, so a clock-skew / mass-expiry event can never silently evict a whole cohort."""
    from apps.safety.services import record_audit

    now = now or timezone.now()
    reminder = getattr(settings, "REVERIFY_REMINDER_DAYS", 14)
    cap = getattr(settings, "REVERIFY_SWEEP_BATCH", 1000)
    soon_cutoff = now + timedelta(days=reminder)

    nudged = paused = newly_expired = 0
    minors = User.objects.filter(
        cohort__in=[Cohort.CHILD, Cohort.TEEN], is_identity_verified=True
    ).order_by("id")
    for minor in minors.iterator():
        try:
            latest = AgeAssurance.objects.filter(user=minor).order_by("-verified_at", "-id").first()
            if latest is None or latest.expires_at is None:
                continue
            if latest.expires_at <= now:
                # Already paused on a prior tick: skip WITHOUT counting it — counting the standing
                # backlog would make the mass-expiry guard a permanent nightly false alarm.
                if latest.reverify_notice == AgeAssurance.ReverifyNotice.EXPIRED:
                    continue
                newly_expired += 1  # a NOT-yet-handled lapse — the anomaly metric
                if paused >= cap:
                    continue  # eviction cap reached this tick (rest processed next tick)
                _pause_lapsed_minor(minor, latest)
                paused += 1
            elif latest.expires_at <= soon_cutoff:
                if latest.reverify_notice == AgeAssurance.ReverifyNotice.NONE:
                    _nudge_reverify_soon(minor, latest)
                    nudged += 1
        except Exception:  # noqa: BLE001 — one bad minor must not starve the rest of the cohort
            logger.exception("reverify_sweep: skipping minor %s after an error", minor.pk)

    if newly_expired > cap:
        # Anomalous burst of NEWLY-lapsed proofs in one tick (e.g. a provider or clock-skew bug) —
        # evictions are capped above; surface it loudly for a human. Keyed on NEW (not standing)
        # expiries, so steady-state accumulation of already-paused minors never trips the alarm.
        record_audit("accounts.reverify_mass_expiry_guard", newly_expired=newly_expired, cap=cap)
    record_audit(
        "accounts.reverify_swept", nudged=nudged, paused=paused, newly_expired=newly_expired
    )
    return {"nudged": nudged, "paused": paused, "newly_expired": newly_expired}


def can_participate(user: User) -> bool:
    """The gate D3/D4 uses: identity-verified with a *current* (unexpired) age
    assurance, and (if under 16) a valid parental consent on file."""
    if not is_assurance_current(user):
        return False
    if user.requires_parental_consent:
        return has_valid_parental_consent(user)
    return True


@transaction.atomic
def link_guardian(guardian: User, ward: User, *, relationship="parent", consent=None):
    """Record (or re-activate) an account-level guardianship link guardian → ward."""
    if guardian.id == ward.id:
        raise ValueError("A user cannot be their own guardian.")
    if guardian.cohort != Cohort.ADULT:
        # A guardian is an adult protector of a minor; never a child/teen/unassigned user.
        raise ValueError("A guardian must be a verified adult.")
    link, _ = GuardianRelationship.objects.update_or_create(
        guardian=guardian,
        ward=ward,
        defaults={
            "relationship": relationship,
            "consent": consent,
            "status": GuardianRelationship.Status.ACTIVE,
        },
    )
    return link


def pending_guardian_invites_for(ward: User):
    """Open, unexpired guardian-link invites awaiting this ward's response."""
    return GuardianLinkInvite.objects.filter(
        ward=ward, status=GuardianLinkInvite.Status.PENDING, expires_at__gt=timezone.now()
    ).select_related("guardian")


@transaction.atomic
def create_guardian_link_invite(
    guardian: User, ward: User, *, relationship: str = "parent"
) -> GuardianLinkInvite:
    """A verified adult invites a (minor) ward to confirm a guardianship link.

    The link is NOT created here — the ward must accept (see accept_guardian_link_invite),
    so the relationship requires both parties to act. Raises ValueError on any precondition
    failure."""
    if not minor_onboarding_enabled():
        raise ValueError("Minor onboarding is disabled on this deployment.")
    if guardian.id == ward.id:
        raise ValueError("A user cannot be their own guardian.")
    if guardian.cohort != Cohort.ADULT or not can_participate(guardian):
        raise ValueError("Only a verified adult can invite a ward.")
    if ward.cohort not in (Cohort.CHILD, Cohort.TEEN):
        # Minor-only: also rejects ADULT and the UNASSIGNED (unverified/unknown-age) cohort.
        raise ValueError("A guardian link can only target a minor account.")
    if is_guardian_of(guardian, ward):
        raise ValueError("You are already this user's guardian.")
    ttl_days = getattr(settings, "GUARDIAN_INVITE_TTL_DAYS", 7)
    # Idempotent per pair: refresh the open invite rather than stacking duplicates
    # (the partial unique constraint also enforces at most one PENDING invite per pair).
    invite, _ = GuardianLinkInvite.objects.update_or_create(
        guardian=guardian,
        ward=ward,
        status=GuardianLinkInvite.Status.PENDING,
        defaults={
            "relationship": relationship,
            "token": secrets.token_urlsafe(24),
            "expires_at": timezone.now() + timedelta(days=ttl_days),
        },
    )
    from apps.safety.services import record_audit

    record_audit("guardian.link_invited", actor=guardian, target=ward)
    return invite


def accept_guardian_link_invite(ward: User, token: str) -> GuardianRelationship:
    """The ward accepts a pending invite, creating the guardianship link. Re-validates the
    inviter is still a *currently-verified* adult at accept time."""
    # Defense-in-depth: a stale PENDING invite from before onboarding was disabled must not
    # be acceptable while the gate is off (mirrors create_guardian_link_invite/consent).
    if not minor_onboarding_enabled():
        raise ValueError("Minor onboarding is disabled on this deployment.")
    from apps.safety.services import record_audit

    # The expiry-marking and the link-creation are in SEPARATE atomic blocks: marking an
    # invite EXPIRED and then raising inside one transaction would roll back the EXPIRED
    # write, so we commit that status, then raise outside the block.
    with transaction.atomic():
        invite = (
            GuardianLinkInvite.objects.select_for_update()
            .filter(token=token, status=GuardianLinkInvite.Status.PENDING)
            .first()
        )
        if invite is None:
            raise ValueError("No such pending invite.")
        if invite.ward_id != ward.id:
            raise ValueError("This invite is addressed to a different user.")
        expired = invite.expires_at <= timezone.now()
        if expired:
            invite.status = GuardianLinkInvite.Status.EXPIRED
            invite.responded_at = timezone.now()
            invite.save(update_fields=["status", "responded_at"])
    if expired:
        raise ValueError("This invite has expired.")

    # One atomic unit for the link + its audit entry (record_audit takes a row lock, so it
    # MUST run inside a transaction — outside one it raises on PostgreSQL). Re-validate the
    # inviter is still a current verified adult (link_guardian only checks the cohort).
    with transaction.atomic():
        if invite.guardian.cohort != Cohort.ADULT or not can_participate(invite.guardian):
            raise ValueError("The inviting guardian is no longer a verified adult.")
        link = link_guardian(invite.guardian, ward, relationship=invite.relationship)
        invite.status = GuardianLinkInvite.Status.ACCEPTED
        invite.responded_at = timezone.now()
        invite.save(update_fields=["status", "responded_at"])
        record_audit("guardian.link_accepted", actor=ward, target=invite.guardian)
    return link


@transaction.atomic
def decline_guardian_link_invite(ward: User, token: str) -> None:
    """The ward declines (or revokes) a pending invite addressed to them."""
    invite = (
        GuardianLinkInvite.objects.select_for_update()
        .filter(token=token, status=GuardianLinkInvite.Status.PENDING, ward=ward)
        .first()
    )
    if invite is None:
        raise ValueError("No such pending invite.")
    invite.status = GuardianLinkInvite.Status.DECLINED
    invite.responded_at = timezone.now()
    invite.save(update_fields=["status", "responded_at"])


@transaction.atomic
def revoke_guardian(guardian: User, ward: User) -> None:
    GuardianRelationship.objects.filter(guardian=guardian, ward=ward).update(
        status=GuardianRelationship.Status.REVOKED
    )
    # Revoking the guardianship must also revoke the parental consent this guardian granted
    # (W2-11), otherwise can_participate(ward) stays True off a consent whose authorizing
    # relationship no longer exists. "No guardian -> no consent."
    ParentalConsent.objects.filter(
        minor=ward,
        guardian_identifier=str(guardian.public_id),
        status=ParentalConsent.Status.ACTIVE,
    ).update(status=ParentalConsent.Status.REVOKED, revoked_at=timezone.now())
    # End any messaging observer presence the (now-revoked) guardianship justified, so an
    # adult cannot keep reading a child's E2EE conversation after the relationship ends.
    from apps.messaging.services import drop_guardian_observers_for, remove_user_from_conversations

    drop_guardian_observers_for(guardian, ward)
    # If revoking this guardian's consent leaves the ward unable to participate (no other
    # active consent), evict them from conversations too — consistent with
    # revoke_parental_consent. A co-guardian's still-valid consent keeps them in.
    if not can_participate(ward):
        remove_user_from_conversations(ward, reason="guardian_revoked")
        from apps.social.services import remove_user_from_groups

        remove_user_from_groups(ward, reason="guardian_revoked")


def is_guardian_of(guardian: User, ward: User) -> bool:
    return GuardianRelationship.objects.filter(
        guardian=guardian, ward=ward, status=GuardianRelationship.Status.ACTIVE
    ).exists()


# --- F7: guardian-set participation guardrails ---------------------------------------
# A guardian turns all-or-nothing consent into a few conservative, child-read-only limits.
# Each maps to an honest fact can_join already checks; guardrails only ever NARROW access.


def _clean_hour(value) -> int | None:
    """Normalise an optional 0-23 hour. Empty -> None (no limit). Raises ValueError on junk so
    a bad form value can never silently become 'no limit' (fail-closed at the input boundary)."""
    if value is None or value == "":
        return None
    try:
        hour = int(value)
    except (TypeError, ValueError):
        raise ValueError("Latest start hour must be a whole number between 0 and 23.") from None
    if not 0 <= hour <= 23:
        raise ValueError("Latest start hour must be between 0 and 23.")
    return hour


def _clean_cap(value) -> int | None:
    """Normalise an optional open-meetup cap. Empty -> None (no cap)."""
    if value is None or value == "":
        return None
    try:
        cap = int(value)
    except (TypeError, ValueError):
        raise ValueError("The open-meetup limit must be a whole number.") from None
    if not 1 <= cap <= 50:
        raise ValueError("The open-meetup limit must be between 1 and 50.")
    return cap


@transaction.atomic
def set_guardian_guardrail(
    guardian: User,
    ward: User,
    *,
    supervised_only: bool = False,
    latest_start_hour=None,
    max_open_joins=None,
) -> GuardianGuardrail:
    """Create/update this guardian's guardrail for a CHILD ward. Gated strictly on an ACTIVE
    GuardianRelationship with a CHILD ward; audited inside the transaction. Inputs are
    normalised/validated fail-closed (junk raises, never silently becomes 'no limit')."""
    rel = (
        GuardianRelationship.objects.select_for_update()
        .filter(guardian=guardian, ward=ward, status=GuardianRelationship.Status.ACTIVE)
        .first()
    )
    if rel is None:
        raise ValueError("You are not a registered guardian of this user.")
    if ward.cohort != Cohort.CHILD:
        # Guardrails map to children's-meetup facts; teens self-manage (mirrors arrival pings).
        raise ValueError("Participation limits apply to children's accounts only.")
    hour = _clean_hour(latest_start_hour)
    cap = _clean_cap(max_open_joins)
    rail, _created = GuardianGuardrail.objects.update_or_create(
        relationship=rel,
        defaults={
            "supervised_only": bool(supervised_only),
            "latest_start_hour": hour,
            "max_open_joins": cap,
        },
    )
    from apps.safety.services import record_audit

    record_audit(
        "guardian.guardrail_set",
        actor=guardian,
        target=ward,
        supervised_only=bool(supervised_only),
        latest_start_hour=hour,
        max_open_joins=cap,
    )
    return rail


def guardrail_for(guardian: User, ward: User) -> GuardianGuardrail | None:
    """This guardian's own guardrail on the ward (for pre-filling the edit form / legibility),
    only while the link is ACTIVE."""
    return (
        GuardianGuardrail.objects.filter(
            relationship__guardian=guardian,
            relationship__ward=ward,
            relationship__status=GuardianRelationship.Status.ACTIVE,
        )
        .select_related("relationship")
        .first()
    )


def effective_guardrail(ward: User) -> dict | None:
    """The STRICTEST guardrail across ALL of the ward's currently-ACTIVE guardians, combined
    fail-closed: supervised_only if ANY guardian requires it, the EARLIEST latest_start_hour,
    and the SMALLEST max_open_joins. A guardian with no guardrail row never loosens another's
    limit. Returns None when no active guardrail applies (the common case → no enforcement)."""
    rails = list(
        GuardianGuardrail.objects.filter(
            relationship__ward=ward,
            relationship__status=GuardianRelationship.Status.ACTIVE,
        )
    )
    if not rails:
        return None
    hours = [r.latest_start_hour for r in rails if r.latest_start_hour is not None]
    caps = [r.max_open_joins for r in rails if r.max_open_joins is not None]
    return {
        "supervised_only": any(r.supervised_only for r in rails),
        "latest_start_hour": min(hours) if hours else None,
        "max_open_joins": min(caps) if caps else None,
    }


@transaction.atomic
def erase_user(actor: User, target: User) -> None:
    """GDPR Art.17 right-to-erasure (W1-5). Permanently deletes `target` and everything
    that cascades from the account (memberships, photos[blob-cleanup signal], messaging
    participation, consents, guardianships). Only the user themselves or an active guardian
    of the target may erase the account; anyone else raises ValueError.

    The erasure is audited BEFORE deletion (so the tamper-evident log records that it
    happened) using the target's public_id, since the row itself is about to disappear."""
    if not (actor.id == target.id or is_guardian_of(actor, target)):
        raise ValueError("You are not authorized to erase this account.")

    from apps.safety.services import record_audit

    # If the target is a guardian, erasing them must NOT leave a ward able to participate
    # off a consent whose guardian no longer exists. ParentalConsent references the guardian
    # by a string identifier (not an FK), so the CASCADE that removes the GuardianRelationship
    # rows would otherwise orphan an ACTIVE consent. revoke_guardian does the full cleanup
    # (revoke that guardian's consent, drop its observer presence, evict the now-ineligible
    # ward from conversations) before the rows cascade away.
    for ward_id in list(
        GuardianRelationship.objects.filter(
            guardian=target, status=GuardianRelationship.Status.ACTIVE
        ).values_list("ward_id", flat=True)
    ):
        revoke_guardian(target, User.objects.get(id=ward_id))

    # Remove the user from their conversations and DELETE their authored E2EE ciphertext.
    # Message.sender is SET_NULL, so target.delete() alone would leave the user's messages
    # (and recipients' wrapped keys) decryptable in others' histories — not true erasure.
    from apps.messaging.models import Message
    from apps.messaging.services import remove_user_from_conversations

    remove_user_from_conversations(target, reason="account_erased")
    Message.objects.filter(sender=target).delete()

    # The user's owned Groups CASCADE-delete with the account (like owned Activities + their
    # threads). Audit each destruction FIRST so a (possibly moderation-hidden, evidence-bearing)
    # group is never destroyed SILENTLY — the hash-chained log keeps a permanent, traceable record
    # of what went, even though the rows themselves are erased (target_ref is a string, not an FK).
    from apps.social.models import Group

    for g in Group.objects.filter(owner=target):
        record_audit(
            "group.owner_erased",
            actor=actor,
            target=g,
            cohort=g.cohort,
            is_hidden=g.is_hidden,
            status=g.status,
        )

    # erased_public_id (a UUID pseudonym) is sufficient to record the event; we do NOT keep
    # the username in the permanent log after erasure.
    record_audit("account.erased", actor=actor, erased_public_id=str(target.public_id))
    target.delete()


@transaction.atomic
def grant_parental_consent(
    guardian: User, ward: User, *, scope="", expires_at=None
) -> ParentalConsent:
    """A verified adult guardian grants parental consent for their under-16 ward.

    Requires an existing active guardianship (established through the verified
    parental-consent identity flow). Activates/refreshes the ward's consent record so
    can_participate(ward) becomes True. Raises ValueError on any precondition failure.
    """
    if not minor_onboarding_enabled():
        raise ValueError("Minor onboarding is disabled on this deployment.")
    if not ward.requires_parental_consent:
        raise ValueError("This user does not require parental consent.")
    if guardian.cohort != Cohort.ADULT or not can_participate(guardian):
        raise ValueError("Only a verified adult guardian can grant consent.")
    if not is_guardian_of(guardian, ward):
        raise ValueError("You are not a registered guardian of this user.")
    consent, _ = ParentalConsent.objects.update_or_create(
        minor=ward,
        guardian_identifier=str(guardian.public_id),
        defaults={
            "status": ParentalConsent.Status.ACTIVE,
            "scope": scope,
            "granted_at": timezone.now(),
            "expires_at": expires_at,
            "revoked_at": None,
        },
    )
    GuardianRelationship.objects.filter(
        guardian=guardian, ward=ward, status=GuardianRelationship.Status.ACTIVE
    ).update(consent=consent)
    return consent


@transaction.atomic
def revoke_parental_consent(guardian: User, ward: User) -> int:
    """Revoke all active parental consents for `ward`. "No consent -> no access": the ward
    is removed from messaging conversations (write-path consent re-checks block any new
    participation across the app). Returns the number of consents revoked."""
    if not is_guardian_of(guardian, ward):
        raise ValueError("You are not a registered guardian of this user.")
    revoked = ParentalConsent.objects.filter(
        minor=ward, status=ParentalConsent.Status.ACTIVE
    ).update(status=ParentalConsent.Status.REVOKED, revoked_at=timezone.now())
    from apps.messaging.services import remove_user_from_conversations
    from apps.social.services import remove_user_from_groups

    remove_user_from_conversations(ward, reason="consent_revoked")
    # "No consent -> no access" applies to standing groups too. A consent revocation does NOT change
    # cohort, so apply_assurance's eviction never fires for it — this is the separate wiring (H6).
    remove_user_from_groups(ward, reason="consent_revoked")
    return revoked


def guardianship_capabilities(guardian: User, ward: User) -> dict:
    """What a guardianship link actually grants, computed from the real rules (F13). Pure,
    read-only; the legibility panels render exactly these booleans so the displayed can/cannot
    copy can never drift from enforcement. Every flag maps to a code fact:
      - can_see_manifest: the F6 read-only meetup manifest (place/time/type only).
      - can_get_arrival_pings: F3 arrival pings, CHILD wards only (teens self-manage).
      - can_observe_messaging: consent-gated, CHILD-only, read-only E2EE observer — and only
        if the guardian has actually set up secure messaging (a key); the panel phrases this
        conditionally rather than asserting it unconditionally.
      - can_grant_consent: only while the guardian is a currently-verified adult and minor
        onboarding is enabled.
    """
    rel = (
        GuardianRelationship.objects.filter(
            guardian=guardian, ward=ward, status=GuardianRelationship.Status.ACTIVE
        )
        .only("relationship")
        .first()
    )
    consent_active = any(
        c.is_valid()
        for c in ParentalConsent.objects.filter(
            minor=ward, guardian_identifier=str(guardian.public_id)
        )
    )
    is_child = ward.cohort == Cohort.CHILD
    # F7: this guardian's own participation guardrail (CHILD wards only) — surfaced so the F13
    # legibility panels render exactly what can_join enforces, never a claim that can drift.
    rail = guardrail_for(guardian, ward) if is_child else None
    return {
        "relationship": (rel.relationship if rel else "") or "guardian",
        "consent_active": consent_active,
        "requires_consent": ward.requires_parental_consent,
        "can_see_manifest": True,
        "can_get_arrival_pings": is_child,
        "can_observe_messaging": is_child and consent_active,
        "can_grant_consent": (
            ward.requires_parental_consent
            and minor_onboarding_enabled()
            and can_participate(guardian)
        ),
        "can_set_guardrails": is_child,
        "guardrail_supervised_only": bool(rail and rail.supervised_only),
        "guardrail_latest_start_hour": rail.latest_start_hour if rail else None,
        "guardrail_max_open_joins": rail.max_open_joins if rail else None,
    }
