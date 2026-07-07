"""Load the RO-EDU local/demo data snapshot after migrations.

This command intentionally runs after ``manage.py migrate``. The SQL file is
data-only; schema, constraints, extensions, and django_migrations are owned by
Django migrations.
"""

from pathlib import Path

from django.conf import settings
from django.core.management.base import BaseCommand, CommandError
from django.db import connection, transaction

from apps.events.models import Event
from apps.places.models import Place, PlaceActivity

DEFAULT_SEED_PATH = Path(settings.BASE_DIR) / "db" / "seed-data.sql"


class Command(BaseCommand):
    help = "Load the RO-EDU local/demo data snapshot once, after migrations."

    def add_arguments(self, parser):
        parser.add_argument(
            "--path",
            default=str(DEFAULT_SEED_PATH),
            help="Path to the data-only SQL seed file.",
        )

    def handle(self, *args, **options):
        if not settings.DEBUG:
            raise CommandError("load_roedu_seed is local/demo only and refuses DEBUG=False.")
        if connection.vendor != "postgresql":
            raise CommandError("load_roedu_seed requires PostgreSQL COPY support.")

        seed_path = Path(options["path"])
        if not seed_path.exists():
            raise CommandError(f"Seed file does not exist: {seed_path}")

        if self._roedu_rows_exist():
            self.stdout.write("RO-EDU seed already present; skipping.")
            return

        if self._target_tables_have_non_seed_rows():
            self.stdout.write(
                self.style.WARNING(
                    "RO-EDU seed skipped: places/events tables already contain non-seed data."
                )
            )
            return

        with transaction.atomic():
            self._load_sql(seed_path)

        self.stdout.write(
            self.style.SUCCESS(
                "Loaded RO-EDU seed data "
                f"({Place.objects.filter(source='roedu').count()} places, "
                f"{PlaceActivity.objects.filter(source='roedu').count()} edges, "
                f"{Event.objects.filter(source='roedu').count()} events)."
            )
        )

    def _roedu_rows_exist(self):
        return (
            Place.objects.filter(source="roedu").exists()
            or PlaceActivity.objects.filter(source="roedu").exists()
            or Event.objects.filter(source="roedu").exists()
        )

    def _target_tables_have_non_seed_rows(self):
        return Place.objects.exists() or PlaceActivity.objects.exists() or Event.objects.exists()

    def _load_sql(self, seed_path):
        with connection.cursor() as django_cursor, seed_path.open(encoding="utf-8") as seed:
            raw_cursor = django_cursor.cursor
            for line in seed:
                stripped = line.strip()
                if not stripped or stripped.startswith("--"):
                    continue
                if line.startswith("COPY "):
                    self._copy_from_stdin(raw_cursor, line, seed)
                    continue
                if line.startswith("SET ") or line.startswith("SELECT pg_catalog.setval("):
                    django_cursor.execute(line)
                    continue
                raise CommandError(f"Unsupported statement in seed SQL: {stripped[:80]}")

    def _copy_from_stdin(self, raw_cursor, copy_sql, seed):
        with raw_cursor.copy(copy_sql) as copy:
            for line in seed:
                if line == "\\.\n":
                    return
                copy.write(line)
        raise CommandError("Unterminated COPY block in seed SQL.")
