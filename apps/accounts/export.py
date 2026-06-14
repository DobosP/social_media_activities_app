"""GDPR Art. 20 (data portability) export for a single user.

Produces a structured, machine-readable (JSON) snapshot of the personal data we hold for
one account: profile, the proven age band / cohort (never a birthdate — see data
minimisation in docs/COMPLIANCE.md), consent metadata, the activities they own/joined, and
a donations summary. Cross-app data is gathered through ORM relations only; no payment-card
data is ever stored, so the donations section is an aggregate-plus-references summary.

This is the portability counterpart to account erasure: it discloses ONLY the requesting
user's (or, via the guardian variant, their ward's) own data, never other members' PII.
"""

from django.utils import timezone

# Export schema version, so consumers can detect format changes over time.
EXPORT_SCHEMA_VERSION = 2  # W2-F32: added "thread_posts" (the user's own authored words)


def build_user_export(user) -> dict:
    """Return a JSON-serialisable dict of all personal data held for ``user``.

    Self-contained and side-effect free: callers (the export views) decide how to deliver
    it. The shape is intentionally explicit (not a blind model dump) so we never leak a
    field we did not mean to disclose."""
    return {
        "schema_version": EXPORT_SCHEMA_VERSION,
        "generated_at": timezone.now().isoformat(),
        "profile": _profile(user),
        "age_assurance": _age_assurance(user),
        "consents": _consents(user),
        "guardianships": _guardianships(user),
        "memberships": _memberships(user),
        "owned_activities": _owned_activities(user),
        "owned_groups": _owned_groups(user),
        "group_memberships": _group_memberships(user),
        "thread_posts": _thread_posts(user),
        "donations": _donations_summary(user),
        "api_access": _api_access(user),
    }


def _api_access(user) -> dict:
    """W10 disclosure: whether an API token exists for this account and when it was
    issued — METADATA only, never the key itself (Art. 15 transparency without turning
    the export into a credential leak)."""
    from rest_framework.authtoken.models import Token

    token = Token.objects.filter(user=user).first()
    return {
        "api_token_issued": token is not None,
        "issued_at": token.created.isoformat() if token else None,
    }


def _profile(user) -> dict:
    return {
        "public_id": str(user.public_id),
        "username": user.username,
        "display_name": user.display_name,
        "age_band": user.age_band,
        "cohort": user.cohort,
        "role": user.role,
        "is_identity_verified": user.is_identity_verified,
        "identity_verified_at": _iso(user.identity_verified_at),
        "is_active": user.is_active,
        "date_joined": _iso(user.date_joined),
    }


def _age_assurance(user) -> list[dict]:
    """Age-assurance events — the proven band and provenance, never identity data."""
    return [
        {
            "provider": a.provider,
            "method": a.method,
            "age_band": a.age_band,
            "verified_at": _iso(a.verified_at),
            "expires_at": _iso(a.expires_at),
            # `raw` holds only the over-threshold booleans / format markers (no PII).
            "evidence": a.raw,
        }
        for a in user.age_assurances.all().order_by("verified_at")
    ]


def _consents(user) -> dict:
    """Parental-consent metadata: consents held *as the minor*, plus references to
    consents this user granted *as a guardian* (identified by guardian public id, not
    free-form personal data)."""
    as_minor = [
        {
            "status": c.status,
            "scope": c.scope,
            "guardian_identifier": c.guardian_identifier,
            "granted_at": _iso(c.granted_at),
            "expires_at": _iso(c.expires_at),
            "revoked_at": _iso(c.revoked_at),
            "created_at": _iso(c.created_at),
        }
        for c in user.parental_consents.all().order_by("created_at")
    ]
    return {"as_minor": as_minor}


def _guardianships(user) -> dict:
    """Guardianship links in both directions (account-level), by public id only."""
    wards = [
        {
            "ward_public_id": str(link.ward.public_id),
            "relationship": link.relationship,
            "status": link.status,
            "created_at": _iso(link.created_at),
        }
        for link in user.wards.select_related("ward").order_by("created_at")
    ]
    guardians = [
        {
            "guardian_public_id": str(link.guardian.public_id),
            "relationship": link.relationship,
            "status": link.status,
            "created_at": _iso(link.created_at),
        }
        for link in user.guardians.select_related("guardian").order_by("created_at")
    ]
    return {"as_guardian_of": wards, "guarded_by": guardians}


def _memberships(user) -> list[dict]:
    return [
        {
            "activity_id": m.activity_id,
            "activity_title": m.activity.title,
            "role": m.role,
            "state": m.state,
            "created_at": _iso(m.created_at),
            "decided_at": _iso(m.decided_at),
        }
        for m in user.memberships.select_related("activity").order_by("created_at")
    ]


def _owned_activities(user) -> list[dict]:
    return [
        {
            "id": a.id,
            "title": a.title,
            "status": a.status,
            "cohort": a.cohort,
            "starts_at": _iso(a.starts_at),
            "created_at": _iso(a.created_at),
        }
        for a in user.owned_activities.all().order_by("created_at")
    ]


def _owned_groups(user) -> list[dict]:
    """Standing groups this user owns (a group is content like an activity)."""
    return [
        {
            "id": g.id,
            "title": g.title,
            "status": g.status,
            "cohort": g.cohort,
            "area": g.area.name,
            "is_staff_curated": g.is_staff_curated,
            "created_at": _iso(g.created_at),
        }
        for g in user.owned_groups.select_related("area").order_by("created_at")
    ]


def _group_memberships(user) -> list[dict]:
    """Standing-group memberships (role/state only — a group keeps no per-user history)."""
    return [
        {
            "group_id": m.group_id,
            "group_title": m.group.title,
            "role": m.role,
            "state": m.state,
            "joined_at": _iso(m.joined_at),
        }
        for m in user.group_memberships.select_related("group").order_by("joined_at")
    ]


def _thread_posts(user) -> list[dict]:
    """W2-F32: the user's OWN authored thread posts + announcements, so their actual words travel
    with them (GDPR Art.20), not just metadata. STRICT allowlist — only fields the user authored
    or that describe their own post:

    * body — their own text (an own post a moderator hid is exported as a neutral '[removed]'
      marker, never the moderator's identity or reason);
    * created_at, edited (derived live from updated_at > created_at — there is no stored flag),
      is_announcement, had_attachment (boolean only — never attachment bytes);
    * the parent thread's title + id (via the activity-XOR-group bridge).

    HARD EXCLUSIONS (another member's data / not the user's words): never the reply_to parent's
    body or the derived reply snippet, never a shared activity/place/event target's content.
    Bounded; attachments are prefetched so the had_attachment flag costs no extra query."""
    from apps.social.models import Post

    rows = []
    posts = (
        Post.objects.filter(author=user)
        .select_related("thread__activity", "thread__group")
        .prefetch_related("attachments")
        .order_by("created_at")[:5000]
    )
    for p in posts:
        owner = p.thread.owner_object  # an Activity XOR a Group
        rows.append(
            {
                "thread_kind": "group" if p.thread.group_id else "activity",
                "thread_id": getattr(owner, "id", None),
                "thread_title": getattr(owner, "title", None) or getattr(owner, "name", None),
                "body": "[removed]" if p.is_hidden else p.body,
                "is_announcement": p.is_announcement,
                "edited": p.updated_at > p.created_at,
                "had_attachment": bool(p.attachments.all()),
                "created_at": _iso(p.created_at),
            }
        )
    return rows


def _donations_summary(user) -> dict:
    """Donations the user made. No card/payment data is stored (the provider handles it);
    we keep only amount, status, provider and an opaque reference (see donations model)."""
    from django.db.models import Sum

    from apps.donations.models import Donation

    donations = user.donations.all().order_by("created_at")
    completed = donations.filter(status=Donation.Status.COMPLETED)
    return {
        "count": donations.count(),
        "completed_count": completed.count(),
        "completed_total_cents": completed.aggregate(s=Sum("amount_cents"))["s"] or 0,
        "items": [
            {
                "amount_cents": d.amount_cents,
                "currency": d.currency,
                "recurring": d.recurring,
                "campaign": d.campaign.title if d.campaign else None,
                "provider": d.provider,
                "status": d.status,
                "external_ref": d.external_ref,
                "created_at": _iso(d.created_at),
                "completed_at": _iso(d.completed_at),
            }
            for d in donations
        ],
    }


def _iso(value):
    return value.isoformat() if value else None
