"""F7 — guardian-set participation guardrails: service-layer behaviour.

Covers the model gate (ACTIVE guardianship + CHILD ward), fail-closed input validation,
the strictest-across-all-guardians combination, the revoked-link exclusion, the F13
capability surfacing, and the in-transaction audit.
"""

import pytest
from django.db import IntegrityError, transaction

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import (
    AgeBand,
    Cohort,
    GuardianGuardrail,
    GuardianRelationship,
    ParentalConsent,
    User,
)
from apps.accounts.services import (
    apply_assurance,
    effective_guardrail,
    guardianship_capabilities,
    guardrail_for,
    link_guardian,
    revoke_guardian,
    set_guardian_guardrail,
)
from apps.safety.models import AuditLog

pytestmark = pytest.mark.django_db


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _teen(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    return u


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_requires_active_guardianship():
    stranger = _adult("s1")
    child = _child("c1")
    with pytest.raises(ValueError):
        set_guardian_guardrail(stranger, child, supervised_only=True)


def test_rejects_non_child_ward():
    guardian = _adult("g_teen")
    teen = _teen("t1")
    link_guardian(guardian, teen)  # an adult may guard a teen, but guardrails are CHILD-only
    assert teen.cohort == Cohort.TEEN
    with pytest.raises(ValueError):
        set_guardian_guardrail(guardian, teen, supervised_only=True)


@pytest.mark.parametrize("bad_hour", ["24", "-1", "noon", "1.5"])
def test_rejects_bad_hour(bad_hour):
    guardian = _adult("gh")
    child = _child("ch_hour")
    link_guardian(guardian, child)
    with pytest.raises(ValueError):
        set_guardian_guardrail(guardian, child, latest_start_hour=bad_hour)


@pytest.mark.parametrize("bad_cap", ["0", "51", "lots"])
def test_rejects_bad_cap(bad_cap):
    guardian = _adult("gc")
    child = _child("ch_cap")
    link_guardian(guardian, child)
    with pytest.raises(ValueError):
        set_guardian_guardrail(guardian, child, max_open_joins=bad_cap)


def test_empty_strings_mean_no_limit():
    guardian = _adult("g_empty")
    child = _child("c_empty")
    link_guardian(guardian, child)
    rail = set_guardian_guardrail(
        guardian, child, supervised_only=False, latest_start_hour="", max_open_joins=""
    )
    assert rail.latest_start_hour is None
    assert rail.max_open_joins is None
    # No limits set at all -> no effective guardrail to enforce.
    assert effective_guardrail(child) == {
        "supervised_only": False,
        "latest_start_hour": None,
        "max_open_joins": None,
    }


def test_hour_zero_is_a_real_limit_not_unset():
    guardian = _adult("g_zero")
    child = _child("c_zero")
    link_guardian(guardian, child)
    rail = set_guardian_guardrail(guardian, child, latest_start_hour="0")
    assert rail.latest_start_hour == 0
    assert effective_guardrail(child)["latest_start_hour"] == 0


def test_effective_none_when_no_guardrail():
    child = _child("c_none")
    guardian = _adult("g_none")
    link_guardian(guardian, child)  # linked, but no guardrail row
    assert effective_guardrail(child) is None


def test_strictest_across_two_guardians():
    child = _child("c_two")
    g1 = _adult("g_two_1")
    g2 = _adult("g_two_2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    set_guardian_guardrail(
        g1, child, supervised_only=False, latest_start_hour="20", max_open_joins="5"
    )
    set_guardian_guardrail(
        g2, child, supervised_only=True, latest_start_hour="18", max_open_joins="3"
    )
    rail = effective_guardrail(child)
    assert rail == {"supervised_only": True, "latest_start_hour": 18, "max_open_joins": 3}


def test_guardian_without_guardrail_does_not_loosen():
    child = _child("c_loose")
    g1 = _adult("g_loose_1")
    g2 = _adult("g_loose_2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    set_guardian_guardrail(g1, child, latest_start_hour="17")
    # g2 set nothing -> g1's 17:00 still applies (an absent guardrail never widens access).
    assert effective_guardrail(child)["latest_start_hour"] == 17


def test_revoked_guardian_guardrail_ignored():
    child = _child("c_rev")
    guardian = _adult("g_rev")
    link_guardian(guardian, child)
    set_guardian_guardrail(guardian, child, supervised_only=True, latest_start_hour="12")
    assert effective_guardrail(child) is not None
    revoke_guardian(guardian, child)
    assert effective_guardrail(child) is None  # a revoked link's guardrail no longer enforces


def test_capabilities_surface_guardrail():
    child = _child("c_cap")
    guardian = _adult("g_cap")
    link_guardian(guardian, child)
    set_guardian_guardrail(
        guardian, child, supervised_only=True, latest_start_hour="19", max_open_joins="2"
    )
    caps = guardianship_capabilities(guardian, child)
    assert caps["can_set_guardrails"] is True
    assert caps["guardrail_supervised_only"] is True
    assert caps["guardrail_latest_start_hour"] == 19
    assert caps["guardrail_max_open_joins"] == 2


def test_capabilities_no_guardrails_for_teen():
    guardian = _adult("g_teen_cap")
    teen = _teen("t_cap")
    link_guardian(guardian, teen)
    caps = guardianship_capabilities(guardian, teen)
    assert caps["can_set_guardrails"] is False
    assert caps["guardrail_supervised_only"] is False
    assert caps["guardrail_latest_start_hour"] is None


def test_set_is_audited():
    child = _child("c_audit")
    guardian = _adult("g_audit")
    link_guardian(guardian, child)
    set_guardian_guardrail(guardian, child, supervised_only=True)
    row = AuditLog.objects.filter(event="guardian.guardrail_set").latest("id")
    assert row.actor_ref == guardian.id
    assert row.target_ref == f"accounts.user:{child.pk}"


def test_update_overwrites_same_guardrail_row():
    child = _child("c_upd")
    guardian = _adult("g_upd")
    link_guardian(guardian, child)
    set_guardian_guardrail(guardian, child, latest_start_hour="20")
    set_guardian_guardrail(guardian, child, latest_start_hour="16")
    # One row per (guardian, ward) link — an edit updates, never stacks.
    rel = GuardianRelationship.objects.get(guardian=guardian, ward=child)
    assert hasattr(rel, "guardrail")
    assert guardrail_for(guardian, child).latest_start_hour == 16


# --- DB-level CheckConstraints (defence in depth beneath the service validation) -------


def _rel(slug):
    guardian = _adult(f"g_db_{slug}")
    child = _child(f"c_db_{slug}")
    link_guardian(guardian, child)
    return GuardianRelationship.objects.get(guardian=guardian, ward=child)


@pytest.mark.parametrize("bad_hour", [24, 99])
def test_db_constraint_rejects_out_of_range_hour(bad_hour):
    rel = _rel(f"hour{bad_hour}")
    with pytest.raises(IntegrityError):
        with transaction.atomic():
            GuardianGuardrail.objects.create(relationship=rel, latest_start_hour=bad_hour)


@pytest.mark.parametrize("bad_cap", [0, 51])
def test_db_constraint_rejects_out_of_range_cap(bad_cap):
    rel = _rel(f"cap{bad_cap}")
    with pytest.raises(IntegrityError):
        with transaction.atomic():
            GuardianGuardrail.objects.create(relationship=rel, max_open_joins=bad_cap)


def test_db_constraint_allows_nulls_and_boundaries():
    rel = _rel("ok")
    rail = GuardianGuardrail.objects.create(
        relationship=rel, latest_start_hour=0, max_open_joins=50
    )
    assert rail.pk is not None  # 0 and 50 are the inclusive boundaries; NULLs allowed too
