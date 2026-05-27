import pytest
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, GuardianRelationship, Role, User
from apps.accounts.services import (
    apply_assurance,
    is_guardian_of,
    link_guardian,
    revoke_guardian,
)

pytestmark = pytest.mark.django_db


def _user(name, role=Role.USER, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name, role=role)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def test_role_helpers():
    admin = _user("adm", Role.ADMIN)
    mod = _user("mod", Role.MODERATOR)
    plain = _user("usr", Role.USER)
    assert admin.is_admin and admin.is_moderator
    assert mod.is_moderator and not mod.is_admin
    assert not plain.is_moderator and not plain.is_admin


def test_superuser_gets_admin_role():
    su = User.objects.create_superuser(username="root", password="pw")
    assert su.role == Role.ADMIN
    assert su.is_admin


def test_guardianship_link_and_revoke():
    guardian = _user("parent")
    child = _user("kid", band=AgeBand.UNDER_16)
    link_guardian(guardian, child, relationship="parent")
    assert is_guardian_of(guardian, child) is True
    assert guardian.is_guardian is True
    assert GuardianRelationship.objects.filter(guardian=guardian, ward=child).count() == 1

    revoke_guardian(guardian, child)
    assert is_guardian_of(guardian, child) is False
    guardian.refresh_from_db()
    assert guardian.is_guardian is False


def test_cannot_self_guardian():
    u = _user("solo")
    with pytest.raises(ValueError):
        link_guardian(u, u)


def test_me_endpoint_exposes_role_and_guardian_flag():
    guardian = _user("p2")
    child = _user("k2", band=AgeBand.UNDER_16)
    link_guardian(guardian, child)
    client = APIClient()
    client.force_authenticate(guardian)
    data = client.get("/api/accounts/me/").json()
    assert data["role"] == Role.USER
    assert data["is_guardian"] is True


def test_guardian_lists_and_manages_ward_account():
    guardian = _user("p3")
    child = _user("k3", band=AgeBand.UNDER_16)
    link_guardian(guardian, child)
    client = APIClient()
    client.force_authenticate(guardian)

    listing = client.get("/api/accounts/wards/").json()
    assert [w["username"] for w in listing] == ["k3"]

    resp = client.patch(
        f"/api/accounts/wards/{child.public_id}/",
        {"display_name": "Kiddo"},
        format="json",
    )
    assert resp.status_code == 200
    child.refresh_from_db()
    assert child.display_name == "Kiddo"


def test_non_guardian_cannot_access_ward():
    other = _user("stranger")
    child = _user("k4", band=AgeBand.UNDER_16)
    client = APIClient()
    client.force_authenticate(other)
    assert client.get(f"/api/accounts/wards/{child.public_id}/").status_code == 403
