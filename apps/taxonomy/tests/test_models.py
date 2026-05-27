import pytest

from apps.taxonomy.models import ActivityCategory, ActivityRelation, ActivityType


@pytest.mark.django_db
def test_seed_taxonomy_loaded():
    # Populated by the 0002_seed_taxonomy data migration at DB setup.
    assert ActivityType.objects.count() >= 8
    for slug in ("basketball", "table_tennis", "reading", "video_games"):
        assert ActivityType.objects.filter(slug=slug).exists()
    assert ActivityCategory.objects.filter(slug="sport").exists()


@pytest.mark.django_db
def test_category_hierarchy():
    team = ActivityCategory.objects.get(slug="team_sport")
    assert team.parent is not None and team.parent.slug == "sport"


@pytest.mark.django_db
def test_seeded_relation_exists():
    assert ActivityRelation.objects.filter(
        source__slug="chess", target__slug="board_games"
    ).exists()


@pytest.mark.django_db
def test_reading_archive_types_seeded():
    # Added by 0003_seed_reading_archives.
    for slug in ("archive", "used_bookshop"):
        obj = ActivityType.objects.get(slug=slug)
        assert obj.category.slug == "reading"
    assert ActivityRelation.objects.filter(source__slug="archive", target__slug="reading").exists()
