"""Summarise exported browser CSP report payloads for operator review."""

from __future__ import annotations

import json
import sys
from pathlib import Path

from django.core.management.base import BaseCommand, CommandError

from apps.ops.csp import digest_csp_reports


class Command(BaseCommand):
    help = "Group CSP report-only payloads from a JSON/JSONL file or stdin."

    def add_arguments(self, parser):
        parser.add_argument("path", nargs="?", help="JSON or JSONL file. Reads stdin when omitted.")
        parser.add_argument(
            "--format",
            choices=("text", "json"),
            default="text",
            help="Output format (default: text).",
        )

    def handle(self, *args, **options):
        payloads = self._read_payloads(options.get("path"))
        summary = digest_csp_reports(payloads)
        if options["format"] == "json":
            self.stdout.write(json.dumps(summary, sort_keys=True))
            return
        self.stdout.write(
            f"CSP reports: total={summary['total']} malformed={summary['malformed']} "
            f"groups={len(summary['groups'])}"
        )
        for group in summary["groups"]:
            self.stdout.write(
                f"{group['count']:>5} {group['directive']} blocked={group['blocked']} "
                f"doc={group['document']}"
            )

    def _read_payloads(self, path: str | None) -> list[str]:
        try:
            text = Path(path).read_text(encoding="utf-8") if path else sys.stdin.read()
        except OSError as exc:
            raise CommandError(str(exc)) from exc
        if not text.strip():
            return []
        try:
            data = json.loads(text)
        except ValueError:
            return [line for line in text.splitlines() if line.strip()]
        if isinstance(data, list):
            return [json.dumps(item) for item in data]
        return [json.dumps(data)]
