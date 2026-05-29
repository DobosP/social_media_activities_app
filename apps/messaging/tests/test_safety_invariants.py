"""Negative child-safety regression tests for the D10 messaging layer (Wave 0 audit
fixes). Each asserts an invariant CANNOT be violated — the threat model
(docs/THREAT_MODEL.md) explicitly asks for tests proving "no cross-cohort / no-consent
messaging path exists". See docs/AUDIT_2026-05.md (SAFE-1, SAFE-4, CONC-2, CONC-3, PRIV-4).
"""

import pytest

from apps.accounts.models import AgeBand, GuardianRelationship, ParentalConsent
from apps.accounts.services import link_guardian, revoke_guardian
from apps.messaging import services

from .conftest import PUBLIC_JWK, keys_for, make_user

pytestmark = pytest.mark.django_db


# --- SAFE-1: the parental-consent gate covers messaging ---
def test_consentless_minor_cannot_register_key():
    child = make_user("nc_child", age_band=AgeBand.UNDER_16, consented=False)
    with pytest.raises(services.MessagingError):
        services.register_public_key(child, PUBLIC_JWK)


def test_consentless_minor_cannot_message_peer():
    a = make_user("nc_a", age_band=AgeBand.UNDER_16, consented=False)
    b = make_user("nc_b", age_band=AgeBand.UNDER_16, consented=True)
    assert services.can_message(a, b) is False
    assert services.can_message(b, a) is False
    with pytest.raises(services.MessagingError):
        services.start_direct(a, b)


# --- SAFE-4: revoking consent cuts existing messaging access (send AND read) ---
def test_revoked_consent_blocks_send_and_read():
    a = make_user("rv_a", age_band=AgeBand.UNDER_16, consented=True)
    b = make_user("rv_b", age_band=AgeBand.UNDER_16, consented=True)
    conv = services.start_direct(a, b)
    services.accept_invite(b, conv)
    services.post_message(a, conv, ciphertext="Yw==", iv="aXY=", recipient_keys=keys_for(conv))
    # a's parental consent is revoked.
    ParentalConsent.objects.filter(minor=a).update(status=ParentalConsent.Status.REVOKED)
    with pytest.raises(services.MessagingError):
        services.post_message(a, conv, ciphertext="Yw==", iv="aXY=", recipient_keys=keys_for(conv))
    with pytest.raises(services.MessagingError):
        services.messages_for(a, conv)


# --- CONC-2: cohort is re-validated on accept and on send (a stale snapshot must not
# create a cross-cohort adult<->minor channel) ---
def test_accept_invite_rejected_after_cohort_change():
    a = make_user("cc_a", age_band=AgeBand.ADULT)
    x = make_user("cc_x", age_band=AgeBand.ADULT)
    conv = services.start_direct(a, x)  # ADULT cohort; x is INVITED
    # x's age is corrected to under-16 (now CHILD), with consent on file.
    x.age_band = AgeBand.UNDER_16
    x.recompute_cohort()
    x.save()
    ParentalConsent.objects.create(
        minor=x, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    with pytest.raises(services.MessagingError):
        services.accept_invite(x, conv)


def test_post_message_rejected_after_sender_cohort_change():
    a = make_user("sc_a", age_band=AgeBand.ADULT)
    b = make_user("sc_b", age_band=AgeBand.ADULT)
    conv = services.start_direct(a, b)
    services.accept_invite(b, conv)
    a.age_band = AgeBand.UNDER_16
    a.recompute_cohort()
    a.save()
    ParentalConsent.objects.create(
        minor=a, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    with pytest.raises(services.MessagingError):
        services.post_message(a, conv, ciphertext="x", iv="y", recipient_keys=keys_for(conv))


# --- CONC-3 / PRIV-4: a guardian observer does not outlive its consent basis ---
def _observed_group():
    c1 = make_user("go_c1", age_band=AgeBand.UNDER_16, consented=True)
    c2 = make_user("go_c2", age_band=AgeBand.UNDER_16, consented=True)
    g = make_user("go_g", age_band=AgeBand.ADULT)
    services.register_public_key(g, PUBLIC_JWK)
    conv = services.start_group(c1, [c2])
    services.accept_invite(c2, conv)
    return c1, c2, g, conv


def test_guardian_observer_removed_when_ward_leaves():
    c1, c2, g, conv = _observed_group()
    GuardianRelationship.objects.create(guardian=g, ward=c1)
    services.add_guardian_observer(g, conv)
    assert services.is_active_participant(g, conv) is True
    services.leave(c1, conv)  # the only ward leaves
    assert services.is_active_participant(g, conv) is False


def test_revoking_guardianship_ends_observer():
    c1, c2, g, conv = _observed_group()
    link_guardian(g, c1)
    services.add_guardian_observer(g, conv)
    assert services.is_active_participant(g, conv) is True
    revoke_guardian(g, c1)
    assert services.is_active_participant(g, conv) is False
