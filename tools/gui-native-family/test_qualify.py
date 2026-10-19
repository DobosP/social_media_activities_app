#!/usr/bin/env python3
"""Pure full-catalog admission controls; no Go, database, Docker or native tests."""
import copy
import json
from pathlib import Path
import runpy
import unittest

METHOD = runpy.run_path(str(Path(__file__).with_name('qualify.py')))
SEED = Path(__file__).resolve().parents[2] / METHOD['REFERENCE_SEED']


class CatalogBindingTests(unittest.TestCase):
    def setUp(self):
        self.expected = {name: sorted(rows, key=lambda row: row['id'])
                         for name, rows in json.loads(SEED.read_bytes()).items()}

    def admit(self, actual):
        return METHOD['validate_reference'](json.dumps(actual).encode(), self.expected)

    def test_complete_current_source_metadata_is_admitted(self):
        report = self.admit(self.expected)
        self.assertEqual(report['rows'], {name: len(rows) for name, rows in self.expected.items()})
        self.assertEqual(len(report['canonical_json_sha256']), 64)

    def test_missing_any_canonical_row_is_refused(self):
        for name in self.expected:
            with self.subTest(dataset=name):
                changed = copy.deepcopy(self.expected)
                changed[name].pop()
                with self.assertRaises(RuntimeError):
                    self.admit(changed)

    def test_any_changed_value_or_extra_row_is_refused(self):
        for name in self.expected:
            with self.subTest(dataset=name):
                changed = copy.deepcopy(self.expected)
                changed[name][0]['id'] = -1
                with self.assertRaises(RuntimeError):
                    self.admit(changed)
                changed = copy.deepcopy(self.expected)
                changed[name].append(copy.deepcopy(changed[name][0]))
                with self.assertRaises(RuntimeError):
                    self.admit(changed)

    def test_missing_or_unknown_dataset_is_refused(self):
        changed = copy.deepcopy(self.expected)
        del changed['content_types']
        with self.assertRaises(RuntimeError):
            self.admit(changed)
        changed = copy.deepcopy(self.expected)
        changed['test_only'] = []
        with self.assertRaises(RuntimeError):
            self.admit(changed)


if __name__ == '__main__':
    if not __debug__:
        raise SystemExit('Optimization is refused')
    unittest.main()
