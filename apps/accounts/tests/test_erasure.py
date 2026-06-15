"""Tests for GDPR Art.17 right-to-erasure (W1-5): a user can erase their own account and
a guardian can erase a ward's; strangers cannot; the deletion is recorded in the
tamper-evident audit log BEFORE the row disappears (using the target's public_id), and the
chain stays valid afterwards. See docs/COMPLIANCE.md."""

import pytest
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import (
    apply_assurance,
    can_participate,
    erase_user,
    erasure_preview,
    grant_parental_consent,
    link_guardian,
)
from apps.safety.models import AuditLog
from apps.safety.services import verify_audit_chain

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def test_self_erasure_deletes_account_and_audits():
    user = _user("erase_me")
    public_id = str(user.public_id)
    uid = user.id

    erase_user(user, user)

    assert not User.objects.filter(id=uid).exists()
    entry = AuditLog.objects.get(event="account.erased")
    assert entry.data["erased_public_id"] == public_id
    # The username is NOT retained in the permanent log after erasure (only the UUID).
    assert "erased_username" not in entry.data
    assert verify_audit_chain() is True


def test_erasing_guardian_revokes_wards_consent():
    """A guardian self-erasing must not leave the ward able to participate off a consent
    whose guardian no longer exists (the consent is string-referenced, not an FK)."""
    guardian = _user("g_self_erase")
    child = _user("w_left_behind", AgeBand.UNDER_16)
    link_guardian(guardian, child)
    grant_parental_consent(guardian, child)
    assert can_participate(child) is True

    erase_user(guardian, guardian)

    child.refresh_from_db()
    assert can_participate(child) is False
    assert not ParentalConsent.objects.filter(
        minor=child, status=ParentalConsent.Status.ACTIVE
    ).exists()


def test_guardian_can_erase_ward():
    guardian = _user("g_erase")
    child = _user("w_erase", AgeBand.UNDER_16)
    link_guardian(guardian, child)
    child_id = child.id

    erase_user(guardian, child)

    assert not User.objects.filter(id=child_id).exists()
    # The guardian's own account survives.
    guardian.refresh_from_db()
    entry = AuditLog.objects.get(event="account.erased")
    assert entry.actor_id == guardian.id
    assert entry.data["erased_public_id"]


def test_stranger_cannot_erase():
    stranger = _user("stranger_erase")
    victim = _user("victim_erase")
    with pytest.raises(ValueError):
        erase_user(stranger, victim)
    assert User.objects.filter(id=victim.id).exists()
    assert not AuditLog.objects.filter(event="account.erased").exists()


def test_non_guardian_adult_cannot_erase_minor():
    other = _user("other_adult")
    child = _user("child_protected", AgeBand.UNDER_16)
    with pytest.raises(ValueError):
        erase_user(other, child)
    assert User.objects.filter(id=child.id).exists()


# --- W2-F33: erasure preview (counts-only, self-scoped, honest about what stays) ---


def test_erasure_preview_is_self_or_guardian_scoped():
    user = _user("preview_self")
    preview = erasure_preview(user, user)
    assert set(preview) == {"destroyed", "retained"}
    assert all(isinstance(v, int) for v in preview["destroyed"].values())

    stranger = _user("preview_stranger")
    with pytest.raises(ValueError):
        erasure_preview(stranger, user)

    guardian = _user("preview_guardian")
    ward = _user("preview_ward", AgeBand.UNDER_16)
    link_guardian(guardian, ward)
    assert "destroyed" in erasure_preview(guardian, ward)  # a guardian may preview their ward


def test_erasure_preview_counts_match_what_erase_actually_does():
    """Divergence guard: seed one real row of EVERY 'destroyed' category, prove the preview count
    is faithful to the live ORM, then prove erase_user genuinely removes each one. Also pins the
    'retained' contract: the donation survives anonymised, and the permanent audit log keeps the
    user's footprint INCLUDING a group.owner_erased row per owned group — which is exactly why the
    surviving-audit count is not a hardcoded 1."""
    from django.contrib.gis.geos import Point

    from apps.accounts.models import AgeAssurance, GuardianRelationship
    from apps.communities.models import Area
    from apps.donations.models import Donation
    from apps.media.models import Attachment, Photo
    from apps.messaging.models import Conversation, Message, Participant
    from apps.places.models import Place
    from apps.social import services as social
    from apps.social.models import Membership, Post
    from apps.taxonomy.models import ActivityCategory, ActivityType

    user = _user("preview_full")  # +1 age-assurance record
    user.is_staff = True  # group creation is staff-only on this deployment
    user.save(update_fields=["is_staff"])
    other = _user("preview_full_other")
    ward = _user("preview_full_ward", AgeBand.UNDER_16)
    link_guardian(user, ward)  # +1 guardian link (as guardian)

    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug="cat-pf", name="Sport")
    atype = ActivityType.objects.create(slug="at-pf", name="Football", category=cat)
    owned = social.create_activity(
        user, place=place, activity_type=atype, title="Game", starts_at="2030-06-01T10:00Z"
    )  # +1 owned_activity, +1 owner membership
    post = social.post_to_thread(user, owned, "hello thread")  # +1 thread_post
    others_act = social.create_activity(
        other, place=place, activity_type=atype, title="Other", starts_at="2030-06-02T10:00Z"
    )
    Membership.objects.create(
        activity=others_act, user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )  # +1 membership (in someone else's activity)
    area = Area.objects.create(city="Cluj-Napoca", slug="cluj-pf", name="Cluj-Napoca")
    group = social.create_group(user, area=area, title="My Group", activity_type=atype)
    assert group.owner_id == user.id  # +1 owned_group (+ an owner group membership)
    Photo.objects.create(uploader=user, kind="profile")  # +1 photo
    Attachment.objects.create(
        post=post, uploader=user, kind="image", storage_key="k", content_type="image/png"
    )  # +1 attachment
    conv = Conversation.objects.create(
        kind=Conversation.Kind.DIRECT, cohort=user.cohort, creator=user
    )
    Participant.objects.create(conversation=conv, user=user, state=Participant.State.ACTIVE)
    Message.objects.create(conversation=conv, sender=user, ciphertext="x", iv="y")  # +1 message
    Donation.objects.create(
        donor=user, amount_cents=500, provider="dev", status=Donation.Status.COMPLETED
    )

    uid = user.id
    donation_id = user.donations.get().id

    # Every 'destroyed' count is faithful to the live ORM (the relations erase_user cascades over).
    d = erasure_preview(user, user)["destroyed"]
    assert d["memberships"] == user.memberships.count() >= 2
    assert d["owned_activities"] == user.owned_activities.count() == 1
    assert d["owned_groups"] == user.owned_groups.count() == 1
    assert d["group_memberships"] == user.group_memberships.count() >= 1
    assert d["thread_posts"] == Post.objects.filter(author=user).count() == 1
    assert d["messages_sent"] == Message.objects.filter(sender=user).count() == 1
    assert d["photos"] == user.photos.count() == 1
    assert d["attachments"] == user.attachments.count() == 1
    assert d["age_assurance_records"] == user.age_assurances.count() >= 1
    assert d["guardian_links"] == 1
    retained = erasure_preview(user, user)["retained"]
    assert retained["donations_anonymised"] == 1
    assert retained["audit_entries_retained"] == AuditLog.objects.filter(actor_ref=uid).count()

    erase_user(user, user)

    # ...and erasure truly destroys each seeded category.
    assert not User.objects.filter(id=uid).exists()
    assert AgeAssurance.objects.filter(user_id=uid).count() == 0
    assert GuardianRelationship.objects.filter(guardian_id=uid).count() == 0
    assert Membership.objects.filter(user_id=uid).count() == 0
    assert Post.objects.filter(author_id=uid).count() == 0
    assert Message.objects.filter(sender_id=uid).count() == 0
    assert Photo.objects.filter(uploader_id=uid).count() == 0
    assert Attachment.objects.filter(uploader_id=uid).count() == 0
    # The donation survives as an anonymous financial record (donor severed), exactly as promised.
    assert Donation.objects.get(id=donation_id).donor_id is None
    # The audit log is permanent + honest: account.erased PLUS one group.owner_erased per owned
    # group survive — so more than one row references the erased account (never a hardcoded 1).
    assert AuditLog.objects.filter(event="account.erased").count() == 1
    assert AuditLog.objects.filter(event="group.owner_erased").count() == 1
    assert AuditLog.objects.filter(actor_ref=uid).count() >= 2


def test_me_delete_self_erases():
    user = _user("api_self_erase")
    uid = user.id
    client = APIClient()
    client.force_authenticate(user)

    resp = client.delete("/api/accounts/me/")
    assert resp.status_code == 204
    assert not User.objects.filter(id=uid).exists()


def test_ward_delete_erases_ward():
    guardian = _user("api_g_erase")
    child = _user("api_w_erase", AgeBand.UNDER_16)
    link_guardian(guardian, child)
    child_id = child.id
    client = APIClient()
    client.force_authenticate(guardian)

    resp = client.delete(f"/api/accounts/wards/{child.public_id}/")
    assert resp.status_code == 204
    assert not User.objects.filter(id=child_id).exists()


def test_ward_delete_rejects_non_guardian():
    other = _user("api_stranger_erase")
    child = _user("api_protected", AgeBand.UNDER_16)
    client = APIClient()
    client.force_authenticate(other)

    resp = client.delete(f"/api/accounts/wards/{child.public_id}/")
    assert resp.status_code == 403
    assert User.objects.filter(id=child.id).exists()
