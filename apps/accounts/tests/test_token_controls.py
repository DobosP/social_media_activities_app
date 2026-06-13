"""Regression tests for the W10 token-control review fixes (W1-1): the API token is
disclosed to the user, revocable from a session, and surfaced in the data export as
METADATA only (never the key)."""

import pytest
from rest_framework.authtoken.models import Token
from rest_framework.test import APIClient

from apps.accounts.export import build_user_export
from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance

pytestmark = pytest.mark.django_db


def _user(name="tok-user", password="pw12345"):
    u = User.objects.create_user(username=name, password=password, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_settings_page_discloses_and_revokes_token(client):
    user = _user("tok-web")
    Token.objects.create(user=user)
    client.force_login(user)
    page = client.get("/settings/").content.decode()
    assert "API access" in page and "Revoke API access" in page
    # session-authenticated revoke kills the token (no token needed)
    resp = client.post("/settings/api-token/revoke/")
    assert resp.status_code == 302
    assert not Token.objects.filter(user=user).exists()


def test_export_discloses_token_metadata_only():
    user = _user("tok-export")
    token = Token.objects.create(user=user)
    export = build_user_export(user)
    assert export["api_access"]["api_token_issued"] is True
    assert export["api_access"]["issued_at"] is not None
    # the actual key must NEVER appear anywhere in the export
    assert token.key not in str(export)


def test_expire_api_tokens_command_sweeps_old(settings):
    from datetime import timedelta

    from django.core.management import call_command
    from django.utils import timezone

    settings.API_TOKEN_MAX_AGE_DAYS = 30
    user = _user("tok-stale")
    token = Token.objects.create(user=user)
    Token.objects.filter(pk=token.pk).update(created=timezone.now() - timedelta(days=40))
    call_command("expire_api_tokens")
    assert not Token.objects.filter(user=user).exists()


def test_fresh_token_survives_sweep(settings):
    from django.core.management import call_command

    settings.API_TOKEN_MAX_AGE_DAYS = 30
    user = _user("tok-fresh")
    Token.objects.create(user=user)
    call_command("expire_api_tokens")
    assert Token.objects.filter(user=user).exists()
