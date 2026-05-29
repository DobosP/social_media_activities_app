"""Regression tests for the 2026-05-29 audit fixes (docs/AUDIT_STRESS_2026-05-29.md):

* F1 / read-time isolation — can_view fails closed when the member is deactivated/banned
  or their cohort changed (the per-delivery WS recheck calls can_view, so this also stops
  a revoked user from receiving live messages).
* F-cohort — a cohort change evicts the user from old-cohort conversations.
* L-ERASE — GDPR erasure deletes the user's authored E2EE ciphertext rows (not merely
  nulls the sender FK, which would leave them decryptable in recipients' histories).
"""

import pytest

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Cohort
from apps.accounts.services import apply_assurance, erase_user
from apps.messaging import services
from apps.messaging.models import Conversation, Message, Participant

pytestmark = pytest.mark.django_db


def _group_with_active(initiator, member):
    conv = Conversation.objects.create(
        kind=Conversation.Kind.GROUP, cohort=initiator.cohort, creator=initiator
    )
    for u in (initiator, member):
        Participant.objects.create(
            conversation=conv, user=u, state=Participant.State.ACTIVE, role=Participant.Role.MEMBER
        )
    return conv


def test_can_view_fails_closed_on_deactivation(adult_a, adult_b):
    conv = _group_with_active(adult_a, adult_b)
    assert services.can_view(adult_b, conv) is True
    adult_b.is_active = False
    adult_b.save(update_fields=["is_active"])
    assert services.can_view(adult_b, conv) is False  # a banned/deactivated user can't read


def test_can_view_fails_closed_on_cohort_change(adult_a, adult_b):
    conv = _group_with_active(adult_a, adult_b)  # pinned to ADULT cohort
    assert services.can_view(adult_b, conv) is True
    adult_b.cohort = Cohort.TEEN
    adult_b.save(update_fields=["cohort"])
    assert services.can_view(adult_b, conv) is False  # read-time cohort isolation


def test_cohort_change_evicts_from_conversations(adult_a, adult_b):
    conv = _group_with_active(adult_a, adult_b)
    apply_assurance(adult_b, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    p = Participant.objects.get(conversation=conv, user=adult_b)
    assert p.state != Participant.State.ACTIVE


def test_erasure_deletes_authored_ciphertext(adult_a, adult_b):
    conv = _group_with_active(adult_a, adult_b)
    msg = Message.objects.create(
        conversation=conv, sender=adult_b, ciphertext="Y2lwaGVydGV4dA==", iv="aXYtbm9uY2U="
    )
    mid = msg.id
    erase_user(adult_b, adult_b)
    # The ciphertext ROW is gone — not merely sender=NULL (which SET_NULL would leave
    # decryptable in recipients' histories).
    assert not Message.objects.filter(id=mid).exists()
