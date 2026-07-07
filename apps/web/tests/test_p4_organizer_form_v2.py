from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.social.models import Activity, Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType
from apps.web.forms import ActivityForm

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    user = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(user, AssuranceResult(age_band=band, provider="dev"))
    return user


def _place(name="Court"):
    return Place.objects.create(
        name=name, location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _type(slug="p4-basketball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="p4-sport", defaults={"name": "Sport"})
    return ActivityType.objects.create(slug=slug, name="Basketball", category=cat)


def _form_data(place, activity_type, starts_at, **overrides):
    data = {
        "place": str(place.pk),
        "activity_type": str(activity_type.pk),
        "title": "Pickup game",
        "starts_at": starts_at.strftime("%Y-%m-%dT%H:%M"),
        "cost_band": Activity.CostBand.UNSPECIFIED,
        "difficulty": Activity.Difficulty.UNSPECIFIED,
    }
    data.update(overrides)
    return data


def test_activity_form_cost_amount_coerces_unspecified_band_to_paid():
    user = _user("p4-form-owner")
    place = _place()
    activity_type = _type("p4-form-cost")
    form = ActivityForm(
        data=_form_data(place, activity_type, timezone.now() + timedelta(days=1), cost_amount="25"),
        user=user,
    )

    assert form.is_valid(), form.errors
    assert form.cleaned_data["cost_band"] == Activity.CostBand.PAID


def test_activity_form_cost_amount_with_free_band_is_field_error():
    user = _user("p4-form-free")
    place = _place()
    activity_type = _type("p4-form-free-type")
    form = ActivityForm(
        data=_form_data(
            place,
            activity_type,
            timezone.now() + timedelta(days=1),
            cost_band=Activity.CostBand.FREE,
            cost_amount="25",
        ),
        user=user,
    )

    assert not form.is_valid()
    assert "cost_amount" in form.errors


def test_activity_form_sections_cover_each_visible_field_once():
    user = _user("p4-form-sections")
    form = ActivityForm(user=user)
    rendered = [field.name for field in form.essential_fields()]
    for _key, _title, fields in form.sections():
        rendered.extend(field.name for field in fields)
    visible = [name for name, field in form.fields.items() if not field.widget.is_hidden]

    assert sorted(rendered) == sorted(visible)
    assert len(rendered) == len(set(rendered))


def test_place_propose_return_to_organize_redirects_to_create_with_place():
    user = _user("p4-place-proposer")
    activity_type = _type("p4-place-propose")
    client = Client()
    client.force_login(user)

    resp = client.post(
        "/places/propose/",
        {
            "name": "Missing court",
            "lat": "46.77",
            "lon": "23.6",
            "activity_type": str(activity_type.pk),
            "return_to": "organize",
        },
    )

    place = Place.objects.get(name="Missing court")
    assert resp.status_code == 302
    assert resp["Location"] == f"/activities/new/?place={place.pk}"


def test_activity_edit_initial_includes_place_and_moving_notifies_member():
    owner = _user("p4-edit-owner")
    member = _user("p4-edit-member")
    activity_type = _type("p4-edit-type")
    old_place = _place("Old hall")
    new_place = _place("New hall")
    activity = create_activity(
        owner,
        place=old_place,
        activity_type=activity_type,
        title="Move me",
        starts_at=(timezone.now() + timedelta(days=2)).replace(second=0, microsecond=0),
    )
    activity.memberships.create(
        user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    client = Client()
    client.force_login(owner)

    get_resp = client.get(f"/activities/{activity.pk}/edit/")
    assert get_resp.context["form"].initial["place"] == old_place.pk

    resp = client.post(
        f"/activities/{activity.pk}/edit/",
        {
            "place": str(new_place.pk),
            "title": activity.title,
            "description": activity.description,
            "starts_at": activity.starts_at.strftime("%Y-%m-%dT%H:%M"),
            "ends_at": "",
            "capacity": "",
            "min_to_go": "",
            "meeting_point": "",
            "what_to_bring": "",
            "organizer_note": "",
            "cost_band": activity.cost_band,
            "cost_amount": "",
            "cost_note": "",
            "difficulty": activity.difficulty,
            "accessibility_notes": "",
            "first_time_note": "",
            "beginners_welcome": "",
        },
    )
    activity.refresh_from_db()

    assert resp.status_code == 302
    assert activity.place == new_place
    notice = Notification.objects.filter(
        recipient=member,
        kind=Notification.Kind.ACTIVITY_UPDATED,
        body__contains="moved venue",
    ).get()
    assert "Old hall" in notice.body
    assert "New hall" in notice.body
