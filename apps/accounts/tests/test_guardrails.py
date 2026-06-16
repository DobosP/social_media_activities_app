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
        "allowed_weekdays": None,
        "earliest_start_hour": None,
        "allowed_categories": None,
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
    assert rail == {
        "supervised_only": True,
        "latest_start_hour": 18,
        "max_open_joins": 3,
        "allowed_weekdays": None,
        "earliest_start_hour": None,
        "allowed_categories": None,
    }


# --- W3-F1: family-calendar window (allowed weekdays + earliest start hour) ---


# "12" is VALID (days 1+2, Mon+Tue); junk = an out-of-range digit (0/8/9) or a non-digit anywhere.
@pytest.mark.parametrize("bad", ["8", "0", "x", "1x", "90", ["1", "9"]])
def test_rejects_bad_weekdays(bad):
    guardian, child = _adult("g_wd"), _child("c_wd")
    link_guardian(guardian, child)
    with pytest.raises(ValueError):
        set_guardian_guardrail(guardian, child, allowed_weekdays=bad)


def test_weekdays_normalise_to_canonical_sorted_unique():
    guardian, child = _adult("g_wd2"), _child("c_wd2")
    link_guardian(guardian, child)
    rail = set_guardian_guardrail(guardian, child, allowed_weekdays=["3", "1", "3", "5"])
    assert rail.allowed_weekdays == "135"  # sorted, de-duped
    assert effective_guardrail(child)["allowed_weekdays"] == frozenset({1, 3, 5})


def test_empty_weekdays_mean_no_restriction():
    guardian, child = _adult("g_wd3"), _child("c_wd3")
    link_guardian(guardian, child)
    set_guardian_guardrail(guardian, child, allowed_weekdays=[])
    assert effective_guardrail(child)["allowed_weekdays"] is None  # not "block all"


def test_weekday_intersection_can_be_empty_fail_closed():
    # Two guardians with disjoint allowlists -> empty intersection -> NOTHING passes (strictest).
    child = _child("c_wd_x")
    g1, g2 = _adult("g_wd_x1"), _adult("g_wd_x2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    set_guardian_guardrail(g1, child, allowed_weekdays="12")
    set_guardian_guardrail(g2, child, allowed_weekdays="34")
    assert effective_guardrail(child)["allowed_weekdays"] == frozenset()


def test_earliest_start_hour_takes_the_latest_max_across_guardians():
    child = _child("c_eh")
    g1, g2 = _adult("g_eh1"), _adult("g_eh2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    set_guardian_guardrail(g1, child, earliest_start_hour="8")
    set_guardian_guardrail(g2, child, earliest_start_hour="10")
    assert effective_guardrail(child)["earliest_start_hour"] == 10  # strictest = latest earliest


def test_capabilities_surface_family_calendar():
    guardian, child = _adult("g_cap"), _child("c_cap")
    link_guardian(guardian, child)
    set_guardian_guardrail(guardian, child, allowed_weekdays="16", earliest_start_hour="9")
    caps = guardianship_capabilities(guardian, child)
    assert caps["guardrail_allowed_weekday_ints"] == [1, 6]
    assert caps["guardrail_earliest_start_hour"] == 9
    assert caps["guardrail_combined_blocks_all"] is False


@pytest.mark.parametrize("junk", [123, True, 5.0])
def test_clean_weekdays_rejects_non_iterable(junk):
    # A bare non-iterable is junk -> ValueError (not the TypeError list() would raise), so the
    # "junk RAISES, never widens" contract holds for any future caller.
    guardian, child = _adult("g_ni"), _child("c_ni")
    link_guardian(guardian, child)
    with pytest.raises(ValueError):
        set_guardian_guardrail(guardian, child, allowed_weekdays=junk)


def test_combined_block_all_flag_on_disjoint_weekdays():
    # Two guardians with disjoint allowlists -> empty intersection -> the child matches no meetup;
    # that combined state must be LEGIBLE on each guardian's panel (not silent breakage).
    child = _child("c_block")
    g1, g2 = _adult("g_block1"), _adult("g_block2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    set_guardian_guardrail(g1, child, allowed_weekdays="12")
    set_guardian_guardrail(g2, child, allowed_weekdays="34")
    assert guardianship_capabilities(g1, child)["guardrail_combined_blocks_all"] is True
    assert guardianship_capabilities(g2, child)["guardrail_combined_blocks_all"] is True


def test_combined_block_all_flag_on_inverted_hour_window():
    child = _child("c_inv")
    g1, g2 = _adult("g_inv1"), _adult("g_inv2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    set_guardian_guardrail(g1, child, earliest_start_hour="20")  # no meetup starts >= 20 AND
    set_guardian_guardrail(g2, child, latest_start_hour="10")  # <= 10 -> empty window
    assert guardianship_capabilities(g1, child)["guardrail_combined_blocks_all"] is True


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


# --- W3-F2: guardian-curated activity-category allowlist ------------------------------


def _category(slug, parent=None):
    from apps.taxonomy.models import ActivityCategory

    return ActivityCategory.objects.create(slug=slug, name=slug.title(), parent=parent)


def test_rejects_unknown_category_slug():
    guardian, child = _adult("g_cat_bad"), _child("c_cat_bad")
    link_guardian(guardian, child)
    _category("f2g-sport")
    # "knitting" is not a real category -> fail-closed (raise), never silently dropped to "".
    with pytest.raises(ValueError):
        set_guardian_guardrail(guardian, child, allowed_categories=["f2g-sport", "f2g-nope"])


def test_categories_normalise_sorted_unique():
    guardian, child = _adult("g_cat_n"), _child("c_cat_n")
    link_guardian(guardian, child)
    _category("f2g-sport")
    _category("f2g-reading")
    rail = set_guardian_guardrail(
        guardian, child, allowed_categories=["f2g-reading", "f2g-sport", "f2g-reading"]
    )
    assert rail.allowed_categories == ["f2g-reading", "f2g-sport"]  # sorted, de-duped
    assert effective_guardrail(child)["allowed_categories"] == frozenset(
        {"f2g-sport", "f2g-reading"}
    )


def test_empty_categories_mean_no_restriction():
    guardian, child = _adult("g_cat_e"), _child("c_cat_e")
    link_guardian(guardian, child)
    set_guardian_guardrail(guardian, child, allowed_categories=[])
    assert effective_guardrail(child)["allowed_categories"] is None  # not "block all"


def test_category_intersection_can_be_empty_fail_closed():
    # Two guardians with disjoint allowlists -> empty intersection -> NOTHING passes (strictest).
    child = _child("c_cat_x")
    g1, g2 = _adult("g_cat_x1"), _adult("g_cat_x2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    _category("f2g-sport")
    _category("f2g-reading")
    set_guardian_guardrail(g1, child, allowed_categories=["f2g-sport"])
    set_guardian_guardrail(g2, child, allowed_categories=["f2g-reading"])
    assert effective_guardrail(child)["allowed_categories"] == frozenset()


def test_guardian_without_categories_does_not_loosen():
    child = _child("c_cat_loose")
    g1, g2 = _adult("g_cat_l1"), _adult("g_cat_l2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    _category("f2g-sport")
    set_guardian_guardrail(g1, child, allowed_categories=["f2g-sport"])
    # g2 set no category restriction -> g1's {sport} still applies (absent never widens).
    assert effective_guardrail(child)["allowed_categories"] == frozenset({"f2g-sport"})


def test_combined_block_all_flag_on_disjoint_categories():
    child = _child("c_cat_block")
    g1, g2 = _adult("g_cat_b1"), _adult("g_cat_b2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    _category("f2g-sport")
    _category("f2g-reading")
    set_guardian_guardrail(g1, child, allowed_categories=["f2g-sport"])
    set_guardian_guardrail(g2, child, allowed_categories=["f2g-reading"])
    # Disjoint category allowlists also mean the child matches no meetup -> legible, like weekdays.
    assert guardianship_capabilities(g1, child)["guardrail_combined_blocks_all"] is True


def test_capabilities_surface_categories():
    guardian, child = _adult("g_cat_cap"), _child("c_cat_cap")
    link_guardian(guardian, child)
    _category("f2g-sport")
    _category("f2g-reading")
    set_guardian_guardrail(guardian, child, allowed_categories=["f2g-sport", "f2g-reading"])
    caps = guardianship_capabilities(guardian, child)
    assert caps["guardrail_allowed_categories"] == ["f2g-reading", "f2g-sport"]
    assert caps["guardrail_combined_blocks_all"] is False


@pytest.mark.parametrize("junk", [123, True, 5.0])
def test_clean_categories_rejects_non_iterable(junk):
    guardian, child = _adult("g_cat_ni"), _child("c_cat_ni")
    link_guardian(guardian, child)
    with pytest.raises(ValueError):
        set_guardian_guardrail(guardian, child, allowed_categories=junk)
