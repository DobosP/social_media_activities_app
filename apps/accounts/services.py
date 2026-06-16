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


def _nudge_consent_renewal(minor: User, consent: ParentalConsent) -> None:
    """W3-F4: a one-time SYSTEM nudge to each ACTIVE guardian that a ward's parental consent is
    expiring soon, so they renew before it lapses (renewal is the guardian's action). NON-MUTABLE
    SYSTEM channel — an access-continuity / compliance notice (DSA Art.16), never silenceable, so
    a guardian can't mute the one warning that prevents their child being silently cut off. The
    per-consent SOON marker makes it at-most-once per term."""
    from apps.notifications.models import Notification
    from apps.notifications.services import notify

    for guardian in _active_guardians(minor):
        notify(
            guardian,
            Notification.Kind.SYSTEM,
            str(_("Your ward's parental consent is expiring soon")),
            body=str(_("Renew your consent soon so they can keep joining and chatting.")),
            url="/wards/",
        )
    consent.renewal_notice = ParentalConsent.RenewalNotice.SOON
    consent.save(update_fields=["renewal_notice", "updated_at"])


@transaction.atomic
def _pause_lapsed_consent(minor: User, lapsed_consents: list) -> None:
    """W3-F4: a CHILD ward's LAST valid parental consent has lapsed — evict them from cohort-pinned
    rosters/conversations (has_valid_parental_consent already fails closed at every action gate;
    this is the ACTIVE cleanup so a lapsed minor doesn't linger) and send a one-time SYSTEM notice
    to the minor AND each ACTIVE guardian (the guardian renews). Mirrors _pause_lapsed_minor. The
    ACTIVE->EXPIRED status flip on the lapsed consents is the handled-marker: the next tick finds
    no ACTIVE consent for this minor and so never re-evicts/re-notifies."""
    from apps.messaging.services import remove_user_from_conversations
    from apps.notifications.models import Notification
    from apps.notifications.services import notify
    from apps.social.services import remove_user_from_groups

    remove_user_from_groups(minor, reason="consent_lapsed")
    remove_user_from_conversations(minor, reason="consent_lapsed")
    for consent in lapsed_consents:
        consent.status = ParentalConsent.Status.EXPIRED
        consent.save(update_fields=["status", "updated_at"])
    notify(
        minor,
        Notification.Kind.SYSTEM,
        str(_("Your parental consent has expired")),
        body=str(_("Ask a parent or guardian to renew their consent so you can take part again.")),
        url="/guardianship/",
    )
    for guardian in _active_guardians(minor):
        notify(
            guardian,
            Notification.Kind.SYSTEM,
            str(_("Your ward's parental consent has expired")),
            body=str(_("Renew your consent so they can keep joining and chatting.")),
            url="/wards/",
        )


def run_consent_renewal_sweep(*, now=None) -> dict:
    """W3-F4: ACTIVE enforcement of parental-consent expiry (otherwise only checked lazily by
    has_valid_parental_consent at action time). For each minor with consent rows: if ALL their
    ACTIVE consents have lapsed, evict them (+ a one-time SYSTEM notice to the minor + ACTIVE
    guardians) and flip those consents EXPIRED; otherwise nudge the ACTIVE guardians once per
    expiring-soon consent. The per-consent SOON marker + the ACTIVE->EXPIRED flip make every notice
    at-most-once. Evictions are CAPPED per tick and the cap is AUDITED, so a clock-skew / mass-lapse
    event can never silently evict a whole cohort. Consents with no expiry (pre-W3-F4 grants) never
    lapse. Runs the SYSTEM (non-mutable) channel — no new mutable Kind."""
    from apps.safety.services import record_audit

    now = now or timezone.now()
    reminder = getattr(settings, "CONSENT_RENEWAL_REMINDER_DAYS", 14)
    cap = getattr(settings, "CONSENT_SWEEP_BATCH", 1000)
    soon_cutoff = now + timedelta(days=reminder)

    nudged = paused = newly_lapsed = 0
    # Gate to users who still REQUIRE parental consent (CHILD cohort), mirroring run_reverify_sweep.
    # An aged-up former minor who re-verified to TEEN/ADULT keeps their stale child-era
    # consent rows (apply_assurance evicts from rosters on a cohort change but doesn't purge the
    # consent rows) — yet no longer needs consent and can_participate is True for them, so they must
    # NEVER be swept or evicted by a lapsed leftover consent.
    minors = (
        User.objects.filter(cohort=Cohort.CHILD, parental_consents__isnull=False)
        .distinct()
        .order_by("id")
    )
    for minor in minors.iterator():
        try:
            # Defence-in-depth beneath the cohort filter: never act on a user who needs no consent.
            if not minor.requires_parental_consent:
                continue
            active = list(
                ParentalConsent.objects.filter(minor=minor, status=ParentalConsent.Status.ACTIVE)
            )
            if not active:
                continue  # no live consent term to watch (revoked/already-expired/none)
            valid = [c for c in active if c.is_valid()]  # ACTIVE + (no expiry OR future expiry)
            if not valid:
                # Every ACTIVE consent has passed its expiry -> the minor can no longer participate.
                newly_lapsed += 1
                if paused < cap:
                    _pause_lapsed_consent(minor, active)  # evict + notify + flip EXPIRED (once)
                    paused += 1
                # If capped, leave them ACTIVE so the next tick re-detects and evicts them.
                continue
            for consent in valid:
                if (
                    consent.expires_at is not None
                    and consent.expires_at <= soon_cutoff
                    and consent.renewal_notice == ParentalConsent.RenewalNotice.NONE
                ):
                    _nudge_consent_renewal(minor, consent)
                    nudged += 1
        except Exception:  # noqa: BLE001 — one bad minor must not starve the rest of the sweep
            logger.exception("consent_renewal_sweep: skipping minor %s after an error", minor.pk)

    if newly_lapsed > cap:
        record_audit("accounts.consent_mass_lapse_guard", newly_lapsed=newly_lapsed, cap=cap)
    record_audit("accounts.consent_swept", nudged=nudged, paused=paused, newly_lapsed=newly_lapsed)
    return {"nudged": nudged, "paused": paused, "newly_lapsed": newly_lapsed}


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


def _clean_weekdays(value) -> str:
    """Normalise allowed ISO weekdays (Mon=1..Sun=7) to a canonical sorted digit string.
    Empty / None -> "" (NO weekday restriction — the common default). Junk (anything not 1-7)
    RAISES, so a malformed value can never silently parse to "all days" and WIDEN access
    (fail-closed at the input boundary, mirroring _clean_hour). Accepts a list (form checkboxes)
    or a string of digits."""
    if value is None or value == "":
        return ""
    if not isinstance(value, (str, list, tuple, set, frozenset)):
        # A bare non-iterable (e.g. an int) is junk — raise ValueError (not the TypeError that
        # list() would), so the input-boundary contract "junk RAISES" holds for every caller.
        raise ValueError("Pick valid days of the week.")
    items = list(value)
    days = set()
    for item in items:
        s = str(item).strip()
        if not s:
            continue
        try:
            d = int(s)
        except (TypeError, ValueError):
            raise ValueError("Pick valid days of the week.") from None
        if not 1 <= d <= 7:
            raise ValueError("Pick valid days of the week.")
        days.add(d)
    return "".join(str(d) for d in sorted(days))


def _clean_categories(value) -> list[str]:
    """Normalise an allowlist of activity-CATEGORY slugs (W3-F2) to a canonical sorted-unique
    list. Empty / None -> [] (NO category restriction — the common default). Junk (a non-iterable,
    or any slug that is not a real ActivityCategory) RAISES, so a malformed value can never
    silently parse to [] and WIDEN access (fail-closed at the input boundary, mirroring
    _clean_weekdays). Accepts a list (form checkboxes) or a single slug string."""
    if value is None or value == "":
        return []
    if isinstance(value, str):
        value = [value]
    if not isinstance(value, (list, tuple, set, frozenset)):
        raise ValueError("Pick valid activity categories.")
    slugs = {str(s).strip() for s in value if str(s).strip()}
    if not slugs:
        return []
    from apps.taxonomy.models import ActivityCategory

    known = set(ActivityCategory.objects.filter(slug__in=slugs).values_list("slug", flat=True))
    if known != slugs:  # an unknown slug is junk -> raise, never silently drop (fail-closed)
        raise ValueError("Pick valid activity categories.")
    return sorted(slugs)


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
    allowed_weekdays=None,
    earliest_start_hour=None,
    allowed_categories=None,
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
    weekdays = _clean_weekdays(allowed_weekdays)
    earliest = _clean_hour(earliest_start_hour)
    categories = _clean_categories(allowed_categories)
    rail, _created = GuardianGuardrail.objects.update_or_create(
        relationship=rel,
        defaults={
            "supervised_only": bool(supervised_only),
            "latest_start_hour": hour,
            "max_open_joins": cap,
            "allowed_weekdays": weekdays,
            "earliest_start_hour": earliest,
            "allowed_categories": categories,
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
        allowed_weekdays=weekdays,
        earliest_start_hour=earliest,
        allowed_categories=categories,
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
    fail-closed: supervised_only if ANY guardian requires it, the EARLIEST latest_start_hour, the
    SMALLEST max_open_joins, the INTERSECTION of allowed weekdays (W3-F1), and the LATEST (MAX)
    earliest_start_hour. A guardian with no guardrail row never loosens another's limit. Returns
    None when no active guardrail applies (the common case → no enforcement).

    ``allowed_weekdays`` is None when NO guardian set a weekday restriction, else a frozenset of
    ISO day ints (Mon=1..Sun=7); a conflicting intersection yields the EMPTY frozenset, which
    correctly means "no weekday passes" (the strictest, fail-closed direction).
    ``allowed_categories`` (W3-F2) behaves identically: None when no guardian set an envelope, else
    a frozenset of category slugs intersected across guardians (empty = nothing passes)."""
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
    earliest_hours = [r.earliest_start_hour for r in rails if r.earliest_start_hour is not None]
    # A guardian with an empty allowed_weekdays imposes NO weekday restriction (never widens).
    weekday_sets = [{int(c) for c in r.allowed_weekdays} for r in rails if r.allowed_weekdays]
    allowed_weekdays = None
    if weekday_sets:
        allowed_weekdays = set(weekday_sets[0])
        for s in weekday_sets[1:]:
            allowed_weekdays &= s
        allowed_weekdays = frozenset(allowed_weekdays)  # may be empty -> nothing passes
    # W3-F2: a guardian with an empty allowed_categories imposes NO category restriction. The
    # combine is the INTERSECTION across guardians who DID set one (fail-closed, like weekdays).
    category_sets = [set(r.allowed_categories) for r in rails if r.allowed_categories]
    allowed_categories = None
    if category_sets:
        allowed_categories = set(category_sets[0])
        for s in category_sets[1:]:
            allowed_categories &= s
        allowed_categories = frozenset(allowed_categories)  # may be empty -> nothing passes
    return {
        "supervised_only": any(r.supervised_only for r in rails),
        "latest_start_hour": min(hours) if hours else None,
        "max_open_joins": min(caps) if caps else None,
        "allowed_weekdays": allowed_weekdays,
        "earliest_start_hour": max(earliest_hours) if earliest_hours else None,
        "allowed_categories": allowed_categories,
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


def erasure_preview(actor: User, target: User) -> dict:
    """W2-F33: an honest, COUNTS-ONLY inventory of what ``erase_user`` will destroy and the one
    audit pseudonym that lawfully survives — so the irreversible right-to-erasure (GDPR Art.17)
    stops being a black box. Strictly self-scoped: the SAME authorization guard as erase_user
    (the user themselves, or an active guardian of the target). Returns ``.count()`` values only —
    no titles, no content, nothing about any other member — mirroring the relations erase_user
    actually cascades over, so the preview can neither over- nor under-promise (a divergence test
    pins these counts to what erasure truly removes). Read-only; no audit side effect."""
    if not (actor.id == target.id or is_guardian_of(actor, target)):
        raise ValueError("You are not authorized to preview this account's erasure.")

    from apps.messaging.models import Message
    from apps.safety.models import AuditLog
    from apps.social.models import Post

    # Everything that erase_user destroys (CASCADE from the account, plus the explicit
    # message/ciphertext wipe). Guardian links count BOTH directions (as guardian + as ward).
    destroyed = {
        "memberships": target.memberships.count(),
        "owned_activities": target.owned_activities.count(),
        "owned_groups": target.owned_groups.count(),
        "group_memberships": target.group_memberships.count(),
        "thread_posts": Post.objects.filter(author=target).count(),
        "messages_sent": Message.objects.filter(sender=target).count(),
        "photos": target.photos.count(),
        "attachments": target.attachments.count(),  # files shared inside threads (CASCADE)
        "age_assurance_records": target.age_assurances.count(),
        "parental_consents": target.parental_consents.count(),
        "guardian_links": target.wards.count() + target.guardians.count(),
    }
    # What lawfully stays — stated plainly so "you deleted everything" can never be a surprise.
    retained = {
        # Donations are financial records (Donation.donor is SET_NULL): the amount/status survive
        # for accounting integrity, but the donor link is severed, so they no longer point to you.
        "donations_anonymised": target.donations.count(),
        # The hash-chained audit log is PERMANENT (a row can never be edited or deleted without
        # breaking the tamper-evidence chain). Erasure SET_NULLs the actor FK on every row that
        # references the user, keeping only a non-identifying internal reference (actor_ref) + event
        # metadata — never a username/name or activity/message content. This is the user's CURRENT
        # footprint; deletion ITSELF appends a few rows (account.erased + one per owned group), so
        # the number that ultimately survives is NOT a fixed 1.
        "audit_entries_retained": AuditLog.objects.filter(actor_ref=target.id).count(),
    }
    return {"destroyed": destroyed, "retained": retained}


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
    # W3-F4: a consent now has a finite term so it can be re-affirmed (GDPR storage-limitation +
    # genuine, current consent). Caller-supplied expires_at wins; otherwise default to
    # CONSENT_VALIDITY_DAYS from now. renewal_notice resets to NONE so the fresh term gets its own
    # expiring-soon nudge.
    validity_days = getattr(settings, "CONSENT_VALIDITY_DAYS", 365)
    effective_expires_at = expires_at or (timezone.now() + timedelta(days=validity_days))
    consent, _ = ParentalConsent.objects.update_or_create(
        minor=ward,
        guardian_identifier=str(guardian.public_id),
        defaults={
            "status": ParentalConsent.Status.ACTIVE,
            "scope": scope,
            "granted_at": timezone.now(),
            "expires_at": effective_expires_at,
            "revoked_at": None,
            "renewal_notice": ParentalConsent.RenewalNotice.NONE,
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
    # W3-F1: the COMBINED window across ALL active guardians can shut entirely (two guardians with
    # disjoint allowed-weekday sets -> empty intersection; or a combined earliest > latest). That
    # block-everything state must be LEGIBLE here, not silent breakage — so a guardian understands
    # why their child currently matches no meetups even though their own limits look reasonable.
    eff = effective_guardrail(ward) if is_child else None
    combined_blocks_all = bool(
        eff
        and (
            eff["allowed_weekdays"] == frozenset()
            # W3-F2: disjoint category allowlists across guardians -> empty intersection ->
            # NO activity type passes. Legible here for the same reason as the weekday case.
            or eff["allowed_categories"] == frozenset()
            or (
                eff["earliest_start_hour"] is not None
                and eff["latest_start_hour"] is not None
                and eff["earliest_start_hour"] > eff["latest_start_hour"]
            )
        )
    )
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
        # W3-F1: the family-calendar window, surfaced so the legibility panel + the edit form
        # render exactly what _passes_guardrails enforces. allowed_weekdays is the stored ISO-day
        # string ("" = no restriction); weekdays as a list of ints for convenient rendering.
        "guardrail_allowed_weekdays": (rail.allowed_weekdays if rail else "") or "",
        "guardrail_allowed_weekday_ints": (
            [int(c) for c in rail.allowed_weekdays] if (rail and rail.allowed_weekdays) else []
        ),
        "guardrail_earliest_start_hour": rail.earliest_start_hour if rail else None,
        # W3-F2: this guardian's own category envelope (slugs; [] = no restriction), surfaced so
        # the edit form pre-ticks the chosen categories and the panel renders what the gate does.
        "guardrail_allowed_categories": list(rail.allowed_categories) if rail else [],
        # True when the COMBINED limits across all this ward's guardians currently match NO meetup.
        "guardrail_combined_blocks_all": combined_blocks_all,
    }


def _humanize_seconds(secs: int) -> str:
    """Plain-language duration for a TTL in seconds (e.g. 3600 -> '1 hour', 86400 -> '1 day')."""
    if secs % 86400 == 0:
        n = secs // 86400
        return f"{n} day" if n == 1 else f"{n} days"
    if secs % 3600 == 0:
        n = secs // 3600
        return f"{n} hour" if n == 1 else f"{n} hours"
    if secs % 60 == 0:
        n = secs // 60
        return f"{n} minute" if n == 1 else f"{n} minutes"
    return f"{secs} seconds"


def retention_disclosure(user: User) -> list[dict]:
    """W3-F16: a self-scoped, DURATIONS-ONLY statement of how long each category of the user's data
    lives before it self-deletes — turning the platform's aggressive data-minimisation into a felt,
    GDPR Art.5(e) (storage-limitation) legible surface. Pure read of constants/fields that already
    drive live DUE_JOBS jobs + the user's OWN latest age proof; no PII, no location, no cohort.

    Each ``ttl_description`` is DERIVED honestly from the live value, INCLUDING the disabled/null
    cases (MESSAGING_RETENTION_DAYS=0 -> "no automatic deletion"; a null age-proof expiry -> "no
    expiry set"), so this can never publish a FALSE storage-limitation claim — the load-bearing
    correctness requirement. Returns an ordered list of {category, ttl_description}."""
    is_minor = user.cohort in (Cohort.CHILD, Cohort.TEEN)
    invite_days = getattr(settings, "GUARDIAN_INVITE_TTL_DAYS", 7)
    token_days = getattr(settings, "API_TOKEN_MAX_AGE_DAYS", 90)
    arrival_hours = getattr(settings, "ARRIVAL_RETENTION_HOURS", 6)
    photo_floor = (
        getattr(settings, "MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS", 86400)
        if is_minor
        else getattr(settings, "MEDIA_EPHEMERAL_MIN_TTL_SECONDS", 3600)
    )
    msg_days = getattr(settings, "MESSAGING_RETENTION_DAYS", 0)
    if msg_days and msg_days > 0:
        msg_text = (
            f"Automatically deleted {msg_days} days after they're sent. You can also set a shorter "
            "per-conversation disappearing timer."
        )
    else:
        msg_text = (
            "Kept until you delete them — there is no automatic deletion. You can set a "
            "per-conversation disappearing timer to remove them sooner."
        )
    # Mirror is_assurance_current's exact ordering (the -id tiebreaker matters when two assurances
    # share a verified_at) so the disclosed expiry is the OPERATIVE one, never a stale row's.
    assurance = user.age_assurances.order_by("-verified_at", "-id").first()
    if assurance and assurance.expires_at:
        age_text = (
            f"Your current age check expires on {assurance.expires_at:%d %b %Y}; after that you "
            "re-verify (we still never store your date of birth)."
        )
    else:
        age_text = (
            "Your current age check has no set expiry. We store an age band only, never a "
            "date of birth."
        )
    return [
        {
            "category": "Guardian invitations",
            "ttl_description": (
                f"Deleted {invite_days} days after they're sent (an accepted one's guardian link "
                "lives on as a separate record)."
            ),
        },
        {
            "category": "Device app access",
            "ttl_description": (
                f"A device's sign-in is automatically revoked {token_days} days after it's granted."
            ),
        },
        {
            "category": "Arrival / on-my-way status",
            "ttl_description": (
                f"Cleared about {arrival_hours} hours after a meetup starts — it never becomes a "
                "lasting record of where you were."
            ),
        },
        {
            "category": "Disappearing photos",
            "ttl_description": (
                f"A photo you mark as disappearing is removed after your chosen timer (at least "
                f"{_humanize_seconds(photo_floor)})."
            ),
        },
        {"category": "Private (encrypted) messages", "ttl_description": msg_text},
        {"category": "Age verification", "ttl_description": age_text},
    ]
