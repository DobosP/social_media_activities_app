import pytest
from django.utils import timezone

from apps.accounts.models import AgeBand, User
from apps.messaging.models import Participant

# A syntactically valid public EC JWK (no private `d` component).
PUBLIC_JWK = {"kty": "EC", "crv": "P-256", "x": "QUJD", "y": "REVG"}


def make_user(username, *, age_band=AgeBand.ADULT, verified=True):
    user = User.objects.create_user(username=username, password="pw", age_band=age_band)
    user.recompute_cohort()
    user.is_identity_verified = verified
    user.identity_verified_at = timezone.now() if verified else None
    user.save()
    return user


def keys_for(conversation, *, users=None):
    """Build a valid `recipient_keys` payload for a conversation's active members.

    Key material is opaque to the server, so dummy (but well-formed) values suffice
    to exercise the zero-knowledge storage and recipient-set validation."""
    if users is None:
        parts = conversation.participants.filter(state=Participant.State.ACTIVE).select_related(
            "user"
        )
        users = [p.user for p in parts]
    return [
        {
            "recipient_public_id": str(u.public_id),
            "ephemeral_public_jwk": {"kty": "EC", "crv": "P-256", "x": "ZQ", "y": "cA"},
            "wrapped_key": "d3JhcHBlZC1jZWs=",
            "wrap_iv": "aXYtbm9uY2U=",
        }
        for u in users
    ]


@pytest.fixture
def adult_a(db):
    return make_user("adult_a")


@pytest.fixture
def adult_b(db):
    return make_user("adult_b")


@pytest.fixture
def adult_c(db):
    return make_user("adult_c")


@pytest.fixture
def child(db):
    return make_user("child_u", age_band=AgeBand.UNDER_16)


@pytest.fixture
def teen(db):
    return make_user("teen_u", age_band=AgeBand.AGE_16_17)


@pytest.fixture
def unverified(db):
    return make_user("unverified_u", age_band=AgeBand.UNKNOWN, verified=False)
