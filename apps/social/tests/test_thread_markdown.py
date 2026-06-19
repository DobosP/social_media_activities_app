"""W7 chat modernization — render + transport gates.

`highlight_mentions` now also renders a small, escape-first markdown subset (bold/italic/inline
code) and, for ADULT threads only, autolinks bare http(s) URLs. `typing_identity` gates the
transient typing signal exactly like the write gate (non-guardian member, not a minor-cohort
announcement-only group, not frozen). Both stay XSS-safe and child-safe.
"""

import pytest
from django.utils import timezone

from apps.social import services as social
from apps.social.models import Activity, Membership

from .conftest import make_user


def _activity(owner, place, activity_type):
    return social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )


def _join(activity, user, role=Membership.Role.MEMBER):
    Membership.objects.create(
        activity=activity, user=user, role=role, state=Membership.State.MEMBER
    )


# --- markdown rendering (escape-first; mentions unaffected) ----------------------------------


def test_markdown_bold_italic_code_render():
    html = str(social.highlight_mentions("a **bold** and *italic* and `code` here", {}))
    assert "<strong>bold</strong>" in html
    assert "<em>italic</em>" in html
    assert "<code>code</code>" in html


def test_markdown_underscore_italic_but_not_intra_word():
    # _emphasis_ at a word boundary renders; snake_case stays literal (no false emphasis).
    assert "<em>emph</em>" in str(social.highlight_mentions("an _emph_ word", {}))
    out = str(social.highlight_mentions("a snake_case_name and 2*3*4", {}))
    assert "<em>" not in out  # neither underscores-in-a-word nor digits-around-* emphasise


def test_markdown_is_escape_first_no_injection():
    # Markup inside a token is escaped; a raw tag never survives.
    html = str(social.highlight_mentions("**<script>x</script>** plain <b>y</b>", {}))
    assert "<script>" not in html and "<b>y</b>" not in html
    assert "&lt;script&gt;" in html and "&lt;b&gt;y&lt;/b&gt;" in html
    assert "<strong>" in html  # the bold wrapper itself is still applied


def test_adult_thread_autolinks_https_safely():
    html = str(social.highlight_mentions("see http://example.com/a?b=1 now", {}, allow_links=True))
    assert '<a href="http://example.com/a?b=1"' in html
    assert 'rel="noopener noreferrer nofollow"' in html
    assert 'target="_blank"' in html


def test_minor_thread_never_autolinks():
    html = str(social.highlight_mentions("see http://example.com now", {}, allow_links=False))
    assert "<a " not in html
    assert "http://example.com" in html  # present, but as plain (non-clickable) escaped text


def test_autolink_ignores_script_schemes_even_for_adults():
    # The URL token only matches http(s); a script-bearing scheme is never linked, and
    # safe_external_url is a second guard.
    html = str(social.highlight_mentions("x javascript:alert(1) y", {}, allow_links=True))
    assert "<a " not in html
    assert "javascript:alert(1)" in html  # left as escaped plain text, not a link


def test_trailing_punctuation_stays_outside_the_link():
    html = str(social.highlight_mentions("go to https://example.com. done", {}, allow_links=True))
    assert '<a href="https://example.com"' in html
    assert "</a>." in html  # the sentence period is not swallowed into the href


@pytest.mark.django_db
def test_mention_and_markdown_compose(place, activity_type):
    owner = make_user("alice")
    bob = make_user("bob")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    roster = social.mention_roster(activity)
    html = str(social.highlight_mentions("hi @bob **welcome**", roster))
    assert '<span class="mention">@bob</span>' in html
    assert "<strong>welcome</strong>" in html


@pytest.mark.django_db
def test_thread_allows_links_only_for_adults(place, activity_type):
    owner = make_user("alice")
    activity = _activity(owner, place, activity_type)
    assert social.thread_allows_links(activity) is True  # adult cohort


# --- typing transport gate ------------------------------------------------------------------


@pytest.mark.django_db
def test_typing_identity_member_ok(place, activity_type):
    owner = make_user("alice")
    bob = make_user("bob")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    info = social.typing_identity(bob, activity)
    assert info is not None and info["author_id"] == bob.id


@pytest.mark.django_db
def test_typing_identity_excludes_guardian_and_nonmember(place, activity_type):
    owner = make_user("alice")
    grd = make_user("granny")
    stranger = make_user("mallory")
    activity = _activity(owner, place, activity_type)
    _join(activity, grd, role=Membership.Role.GUARDIAN)
    assert social.typing_identity(grd, activity) is None  # supervisory guardian never emits
    assert social.typing_identity(stranger, activity) is None  # non-member never emits


@pytest.mark.django_db
def test_typing_identity_none_on_frozen_thread(place, activity_type):
    owner = make_user("alice")
    bob = make_user("bob")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    activity.status = Activity.Status.CANCELLED  # a cancelled meetup freezes its thread
    activity.save(update_fields=["status"])
    assert social.typing_identity(bob, activity) is None
