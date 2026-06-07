"""F3 must never open a private-contact path: a saved search is a discovery filter, not a shared
activity, so matching the same activity must never make two savers connectable."""

from datetime import timedelta

import pytest
from django.utils import timezone

from apps.connections.services import can_connect, shares_activity
from apps.saved_searches import services as ss
from apps.social.services import create_activity

pytestmark = pytest.mark.django_db


def test_saved_search_match_does_not_enable_connection(adult, adult2, place, activity_type):
    ss.create_saved_search(adult2, activity_type=activity_type)
    create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="x",
        starts_at=timezone.now() + timedelta(days=3),
    )
    ss.match_saved_searches()  # adult2 is alerted but never joined
    assert not shares_activity(adult, adult2)
    assert not can_connect(adult, adult2)
