"""One real person = one account (Phase 1).

bind_identity records an HMAC of the wallet holder subject so the same EU Digital Identity
credential can never assure two accounts. Enforcement is OFF by default (the dev/sandbox flow
proves no holder key), so these tests turn it on explicitly.
"""

import jwt
import pytest
from cryptography.hazmat.primitives.asymmetric import ec
from django.urls import reverse
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.identity.eudi import issuer
from apps.accounts.models import AgeBand, IdentityBinding, User
from apps.accounts.services import (
    IdentityAlreadyBound,
    IdentityBanned,
    apply_assurance,
    ban_identity,
    bind_identity,
    has_unique_identity,
    identity_uniqueness_active,
)


def _verified_result(sub="holder-abc"):
    """An AssuranceResult that proves holder-key possession (drives a real binding)."""
    return AssuranceResult(
        age_band=AgeBand.ADULT,
        verified=True,
        provider="eudi",
        method="openid4vp",
        holder_sub=sub,
        raw={
            "age_over_16": True,
            "age_over_18": True,
            "format": "jwt_vc",
            "holder_proof": "verified",
        },
    )


# --- service-level binding semantics ---


@pytest.mark.django_db
def test_binding_is_noop_when_enforcement_off(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = False
    user = User.objects.create_user(username="a", password="pw")
    assert bind_identity(user, _verified_result()) is None
    assert IdentityBinding.objects.count() == 0
    # Uniqueness is simply not asserted on this deployment.
    assert has_unique_identity(user) is True


@pytest.mark.django_db
def test_binding_is_noop_without_holder_proof(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    user = User.objects.create_user(username="a", password="pw")
    result = _verified_result()
    result.raw["holder_proof"] = "unverified"  # the dev/sandbox flow proves no key
    assert identity_uniqueness_active(result) is False
    assert bind_identity(user, result) is None
    assert IdentityBinding.objects.count() == 0


@pytest.mark.django_db
def test_binding_created_and_is_idempotent(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    user = User.objects.create_user(username="a", password="pw")
    binding = bind_identity(user, _verified_result())
    assert binding is not None
    assert has_unique_identity(user) is True
    # Re-verifying the same wallet for the same user makes no second row.
    again = bind_identity(user, _verified_result())
    assert again.pk == binding.pk
    assert IdentityBinding.objects.count() == 1


@pytest.mark.django_db
def test_same_wallet_cannot_bind_second_account(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    first = User.objects.create_user(username="first", password="pw")
    second = User.objects.create_user(username="second", password="pw")
    bind_identity(first, _verified_result())
    with pytest.raises(IdentityAlreadyBound):
        bind_identity(second, _verified_result())
    assert IdentityBinding.objects.count() == 1


@pytest.mark.django_db
def test_binding_survives_erasure_and_blocks_until_recovery(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    first = User.objects.create_user(username="first", password="pw")
    bind_identity(first, _verified_result())
    # GDPR erasure deletes the account; SET_NULL keeps the binding row so the wallet can't
    # silently re-register elsewhere.
    first.delete()
    binding = IdentityBinding.objects.get()
    assert binding.user_id is None
    # An orphaned binding is recoverable — the same person may take a fresh account (lifetime
    # bans are enforced separately, in Phase 2's BannedIdentity ledger).
    fresh = User.objects.create_user(username="fresh", password="pw")
    rebound = bind_identity(fresh, _verified_result())
    assert rebound.user_id == fresh.pk
    assert IdentityBinding.objects.count() == 1


@pytest.mark.django_db
def test_holder_subject_never_persisted_as_pii(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    user = User.objects.create_user(username="a", password="pw")
    result = _verified_result(sub="super-secret-holder")
    bind_identity(user, result)
    assurance = apply_assurance(user, result)
    # The raw subject lives only in the (keyed) HMAC, never in the assurance record.
    assert "super-secret-holder" not in str(assurance.raw)
    assert set(assurance.raw) == {"age_over_16", "age_over_18", "format", "holder_proof"}
    binding = IdentityBinding.objects.get()
    assert "super-secret-holder" not in binding.holder_hash


# --- lifetime ban: the holder-hash ledger rejects re-binding (direct branch coverage) ---


@pytest.mark.django_db
def test_bind_identity_rejects_a_banned_holder(settings):
    # Direct coverage of the BannedIdentity branch in bind_identity (the ban-evasion ledger).
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    owner = User.objects.create_user(username="banowner", password="pw")
    bind_identity(owner, _verified_result(sub="holder-ban"))
    ban_identity(owner)

    # Any account presenting the banned wallet is refused outright. The ban check fires BEFORE the
    # already-bound check (so the message is "banned", not "already linked"), and no new row is
    # written for the newcomer.
    newcomer = User.objects.create_user(username="bannewcomer", password="pw")
    with pytest.raises(IdentityBanned):
        bind_identity(newcomer, _verified_result(sub="holder-ban"))
    assert IdentityBinding.objects.count() == 1  # only the owner's binding


@pytest.mark.django_db
def test_verify_age_web_refuses_a_banned_wallet(settings, client, monkeypatch):
    # Surface symmetry: the web verify_age flow rejects a banned wallet too (assurance NOT applied).
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    from apps.accounts.identity.providers import eudi as eudi_provider

    owner = User.objects.create_user(username="vaowner", password="pw")
    bind_identity(owner, _verified_result(sub="holder-va-ban"))
    ban_identity(owner)

    # The sandbox issuer proves no holder key, so simulate the REAL EUDI flow this guards: have the
    # provider return a verified-holder result for the (banned) wallet.
    monkeypatch.setattr(
        eudi_provider.EUDIWalletProvider,
        "verify",
        lambda self, user, presentation: _verified_result(sub="holder-va-ban"),
    )
    user = User.objects.create_user(username="vauser", password="pw")
    client.force_login(user)
    resp = client.post("/verify-age/", {"age": "adult"})
    # Refused to profile (NOT a re-prompt to verify_age) and the age is never applied.
    assert resp.status_code == 302 and resp.url == reverse("profile")
    user.refresh_from_db()
    assert user.is_identity_verified is False  # a banned identity never gets the age applied


@pytest.mark.django_db
def test_verify_age_web_refuses_an_already_bound_wallet(settings, client, monkeypatch):
    # One person = one account on the web surface too: a wallet already bound to a DIFFERENT
    # account is refused, and a second person's verified age is NEVER applied to this account.
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    from apps.accounts.identity.providers import eudi as eudi_provider

    first = User.objects.create_user(username="vab-first", password="pw")
    bind_identity(first, _verified_result(sub="holder-va-dup"))
    monkeypatch.setattr(
        eudi_provider.EUDIWalletProvider,
        "verify",
        lambda self, user, presentation: _verified_result(sub="holder-va-dup"),
    )
    second = User.objects.create_user(username="vab-second", password="pw")
    client.force_login(second)
    resp = client.post("/verify-age/", {"age": "adult"})
    assert resp.status_code == 302 and resp.url == reverse("profile")
    second.refresh_from_db()
    assert second.is_identity_verified is False  # the shared wallet never verifies a 2nd account
    assert IdentityBinding.objects.count() == 1  # still only first's binding


# --- end-to-end OpenID4VP API flow: duplicate wallet is rejected with 409 ---


def _holder_keypair():
    private_key = ec.generate_private_key(ec.SECP256R1())
    public_jwk = jwt.algorithms.ECAlgorithm.to_jwk(private_key.public_key(), as_dict=True)
    return private_key, public_jwk


def _present(client, *, subject, audience):
    """Run the full /start -> /verify-age flow with a holder-bound credential."""
    started = client.post("/api/accounts/verify-age/start/")
    nonce = started.data["nonce"]
    state = started.data["state"]
    private_key, public_jwk = _holder_keypair()
    token = issuer.issue_age_credential(
        audience=audience,
        nonce=nonce,
        age_over_16=True,
        age_over_18=True,
        subject=subject,
        extra_claims={"cnf": {"jwk": public_jwk}},
    )
    proof = jwt.encode(
        {"aud": audience, "nonce": nonce},
        private_key,
        algorithm="ES256",
        headers={"typ": "kb+jwt"},
    )
    return client.post(
        "/api/accounts/verify-age/",
        {"vp_token": token, "state": state, "holder_binding_proof": proof},
    )


@pytest.mark.django_db
def test_duplicate_wallet_blocked_at_api(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    aud = settings.EUDI_CLIENT_ID
    first = User.objects.create_user(username="first", password="pw")
    second = User.objects.create_user(username="second", password="pw")
    c1, c2 = APIClient(), APIClient()
    c1.force_authenticate(first)
    c2.force_authenticate(second)

    r1 = _present(c1, subject="holder-xyz", audience=aud)
    assert r1.status_code == 200
    first.refresh_from_db()
    assert first.is_identity_verified is True

    # Same wallet (same holder subject) on a second account -> 409, no assurance applied.
    r2 = _present(c2, subject="holder-xyz", audience=aud)
    assert r2.status_code == 409
    second.refresh_from_db()
    assert second.is_identity_verified is False
    assert IdentityBinding.objects.count() == 1


@pytest.mark.django_db
def test_banned_wallet_blocked_at_api_with_403(settings):
    # The ban-evasion ledger rejects a banned wallet through the real OpenID4VP endpoint with 403.
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    aud = settings.EUDI_CLIENT_ID
    owner = User.objects.create_user(username="apibanowner", password="pw")
    bind_identity(owner, _verified_result(sub="holder-api-ban"))
    ban_identity(owner)

    newcomer = User.objects.create_user(username="apibannew", password="pw")
    c = APIClient()
    c.force_authenticate(newcomer)
    resp = _present(c, subject="holder-api-ban", audience=aud)
    assert resp.status_code == 403
    newcomer.refresh_from_db()
    assert newcomer.is_identity_verified is False
    assert IdentityBinding.objects.count() == 1  # no new binding for the banned newcomer


# --- release paths: lifting a ban + voluntary fresh start ---


@pytest.mark.django_db
def test_release_identity_ban_lifts_ledger(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    from apps.accounts.models import BannedIdentity
    from apps.accounts.services import ban_identity, identity_is_banned, release_identity_ban

    user = User.objects.create_user(username="rb", password="pw")
    bind_identity(user, _verified_result(sub="holder-rb"))
    ban_identity(user)
    assert identity_is_banned("holder-rb") is True

    assert release_identity_ban(user) is True
    assert identity_is_banned("holder-rb") is False
    assert BannedIdentity.objects.count() == 0
    assert release_identity_ban(user) is False  # idempotent


@pytest.mark.django_db
def test_release_identity_ban_noop_without_binding(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    from apps.accounts.services import release_identity_ban

    assert release_identity_ban(User.objects.create_user(username="rb2", password="pw")) is False


@pytest.mark.django_db
def test_release_binding_frees_wallet_for_a_new_account(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    from apps.accounts.services import release_binding

    a = User.objects.create_user(username="ra", password="pw")
    bind_identity(a, _verified_result(sub="holder-shared"))
    assert release_binding(a) is True
    assert IdentityBinding.objects.first().released_at is not None

    # The same wallet may now (re)bind a different account via bind_identity's recovery branch.
    b = User.objects.create_user(username="rbn", password="pw")
    bound = bind_identity(b, _verified_result(sub="holder-shared"))
    assert bound is not None and bound.user_id == b.id
    assert release_binding(a) is False  # a's binding was already released (idempotent)


# --- admin tooling. transaction=True reproduces autocommit admin requests: the audit write's
#     select_for_update raises outside a transaction, so these guard that the actions wrap one. ---


def _staff():
    u = User.objects.create_user(username="adm", password="pw")
    u.is_staff = u.is_superuser = True
    u.save(update_fields=["is_staff", "is_superuser"])
    return u


@pytest.mark.django_db(transaction=True)
def test_admin_release_binding_action(client, settings):
    from django.urls import reverse

    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    bound_user = User.objects.create_user(username="bound", password="pw")
    bind_identity(bound_user, _verified_result(sub="holder-adm"))
    binding = IdentityBinding.objects.get()
    client.force_login(_staff())
    resp = client.post(
        reverse("admin:accounts_identitybinding_changelist"),
        {"action": "release_bindings", "_selected_action": [str(binding.pk)]},
        follow=True,
    )
    assert resp.status_code == 200  # not a 500 from select_for_update-outside-transaction
    binding.refresh_from_db()
    assert binding.released_at is not None


@pytest.mark.django_db(transaction=True)
def test_admin_lift_ban_action(client, settings):
    from django.urls import reverse

    from apps.accounts.models import BannedIdentity
    from apps.accounts.services import ban_identity, identity_is_banned

    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    banned_user = User.objects.create_user(username="banned", password="pw")
    bind_identity(banned_user, _verified_result(sub="holder-lift"))
    ban_identity(banned_user)
    assert identity_is_banned("holder-lift") is True
    banned = BannedIdentity.objects.get()
    client.force_login(_staff())
    resp = client.post(
        reverse("admin:accounts_bannedidentity_changelist"),
        {"action": "lift_bans", "_selected_action": [str(banned.pk)]},
        follow=True,
    )
    assert resp.status_code == 200
    assert identity_is_banned("holder-lift") is False
