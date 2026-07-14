"""ADR-0029 exposure regression: the plural-sentiment system must never widen the surfaces this
repo already pins closed — the public API, the agent snapshot / Go sidecar, the self-export, and
the notification-mutability registry. Each test below locks ONE such surface so a future change
can't accidentally leak a reaction/dissent/concern identity, count, or model name through it.

CHILD-cohort e2e lives here too (rather than in test_reactions.py) since it's the same "what does
NOT show up" posture: a CHILD thread renders no footer and no dissent/concern affordances at all,
while the (countless) appreciation picker stays available."""

import inspect
import pathlib

import pytest
from django.test import Client
from django.utils import timezone

from apps.accounts.export import build_user_export
from apps.accounts.models import AgeBand
from apps.notifications.models import NON_MUTABLE_KINDS, Notification
from apps.social import services as social
from apps.social.models import Membership

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _setup(place, activity_type):
    owner = make_user("sx_owner")
    member = make_user("sx_member")
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return owner, member, activity


# --- PostSerializer field allowlist ------------------------------------------------------------


def test_post_serializer_has_no_reaction_sentiment_field():
    from apps.social.serializers import PostSerializer

    fields = set(PostSerializer.Meta.fields)
    # Explicit allowlist equality (not a subset check) so a stray new field is caught immediately.
    assert fields == {
        "id",
        "author",
        "body",
        "is_announcement",
        "reply_to",
        "ping",
        "share_activity",
        "share_place",
        "share_event",
        "share",
        "created_at",
    }
    leak_terms = ("reaction", "sentiment", "dissent", "concern")
    for f in fields:
        assert not any(term in f.lower() for term in leak_terms)


# --- activity_detail context: no per-post identity/count data ----------------------------------


def test_activity_detail_context_exposes_only_mine_and_sentiment_lines(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    social.toggle_reaction(member, post, e)

    c = Client()
    c.force_login(member)
    resp = c.get(f"/activities/{activity.id}/")
    rendered = next(p for p in resp.context["posts"] if p.id == post.id)

    assert rendered.reaction_mine == {e}
    assert rendered.sentiment_lines == []  # below any latch threshold: silence, not "0"
    # No identity/count data was ever attached to the rendered post object.
    for forbidden in ("present", "reaction_count", "reactors", "who_reacted", "reaction_users"):
        assert not hasattr(rendered, forbidden)


# --- agent snapshot + Go sidecar: the sentiment models never appear ----------------------------

_SENTIMENT_MODEL_NAMES = ("PostReaction", "PostDissent", "PostConcern", "PostSentimentFooter")


def test_agent_snapshot_source_never_mentions_sentiment_models():
    from apps.web import agent_snapshot

    src = inspect.getsource(agent_snapshot)
    for name in _SENTIMENT_MODEL_NAMES:
        assert name not in src


def test_agentapi_go_sources_never_mention_sentiment_models():
    # apps/social/tests/ -> apps/social/ -> apps/ -> repo root -> services/agentapi/
    root = pathlib.Path(__file__).resolve().parents[3] / "services" / "agentapi"
    go_files = sorted(root.glob("*.go"))
    assert go_files, f"expected Go sidecar sources under {root}"
    for path in go_files:
        text = path.read_text(encoding="utf-8")
        for name in _SENTIMENT_MODEL_NAMES:
            assert name not in text, f"{name} leaked into {path.name}"


# --- accounts self-export: own rows only, never another member's, never a count ----------------


def test_self_export_includes_only_own_sentiment_rows_never_a_count_or_others(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e0, e1 = social.allowed_reactions()[0], social.allowed_reactions()[1]
    social.toggle_reaction(owner, post, e0)  # owner's own facet
    social.toggle_reaction(member, post, e1)  # member's own facet
    social.toggle_dissent(member, post)
    social.record_concern(member, post)

    export = build_user_export(member)
    own = export["own_sentiment_actions"]
    assert own["reactions"] == [
        {"post_id": post.id, "facet": e1, "created_at": own["reactions"][0]["created_at"]}
    ]
    assert [d["post_id"] for d in own["dissents"]] == [post.id]
    assert [cn["post_id"] for cn in own["concerns"]] == [post.id]
    # Never the OWNER's facet (another member's row) and never an aggregate count field anywhere.
    assert e0 not in [r["facet"] for r in own["reactions"]]
    assert "count" not in own and not any("count" in k for k in own)


def test_self_export_empty_when_user_never_reacted(place, activity_type):
    owner, _member, activity = _setup(place, activity_type)
    social.post_to_thread(owner, activity, "hi")
    export = build_user_export(owner)
    assert export["own_sentiment_actions"] == {"reactions": [], "dissents": [], "concerns": []}


# --- notification kind mutability ---------------------------------------------------------------


def test_formative_note_and_mod_alert_are_mutable_moderation_system_unchanged():
    assert Notification.Kind.FORMATIVE_NOTE not in NON_MUTABLE_KINDS
    assert Notification.Kind.MOD_ALERT not in NON_MUTABLE_KINDS
    # DSA-mandated notices remain non-mutable, untouched by ADR-0029.
    assert Notification.Kind.MODERATION in NON_MUTABLE_KINDS
    assert Notification.Kind.SYSTEM in NON_MUTABLE_KINDS


# --- CHILD cohort e2e: no footer, no dissent/concern, react picker stays available --------------


def _child_setup(place, activity_type):
    owner = make_user("sx_child_owner", AgeBand.UNDER_16, consented=True)
    member = make_user("sx_child_member", AgeBand.UNDER_16, consented=True)
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    activity.memberships.create(
        user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return owner, member, activity


def test_child_thread_renders_no_footer_no_dissent_concern_but_keeps_react_picker(
    place, activity_type
):
    owner, member, activity = _child_setup(place, activity_type)
    social.post_to_thread(owner, activity, "hi")

    c = Client()
    c.force_login(member)
    html = c.get(f"/activities/{activity.id}/").content.decode()

    assert 'class="sentiment-line"' not in html  # no footer, ever, for a CHILD thread
    assert "I see this differently" not in html  # dissent row omitted (flattened Respond menu)
    assert "doesn't seem to fit here" not in html  # concern row omitted
    assert "concern-intro" not in html  # the first-use education never renders on a CHILD thread
    assert 'class="rx-pick"' in html  # the (countless) appreciation picker is still there
    assert social.allowed_reactions()[0] in html


def test_child_dissent_toggle_post_is_rejected(place, activity_type):
    owner, member, activity = _child_setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    c = Client()
    c.force_login(member)
    r = c.post(f"/activities/{activity.id}/post/{post.id}/dissent/", HTTP_X_REQUESTED_WITH="fetch")
    assert r.status_code == 400
    assert r.json()["ok"] is False
    from apps.social.models import PostDissent

    assert not PostDissent.objects.filter(post=post, user=member).exists()


def test_child_concern_post_is_rejected(place, activity_type):
    owner, member, activity = _child_setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    c = Client()
    c.force_login(member)
    r = c.post(f"/activities/{activity.id}/post/{post.id}/concern/", HTTP_X_REQUESTED_WITH="fetch")
    assert r.status_code == 400
    assert r.json()["ok"] is False
    from apps.social.models import PostConcern

    assert not PostConcern.objects.filter(post=post, user=member).exists()
