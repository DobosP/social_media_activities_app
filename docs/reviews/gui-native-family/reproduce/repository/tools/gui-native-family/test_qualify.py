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


class OwnedLaneRetirementTests(unittest.TestCase):
    invocation = 'abcdef01-2222-4333-8444-555555555555'

    def snapshot(self):
        rows = [{'name': name, 'oid': n, 'owner': 'app', 'template': name != 'postgres',
                 'client_connections': 0, 'bytes': 1024} for n, name in enumerate(['postgres', 'template0', 'template1'], 1)]
        rows.append({'name': METHOD['lane_database'](self.invocation, 'web'), 'oid': 42, 'owner': 'app',
                     'template': False, 'client_connections': 0, 'bytes': 2048})
        return {'maintenance_database': 'postgres', 'listen': '', 'databases': rows}

    def test_exact_generated_qualified_database_is_admitted(self):
        row = METHOD['retirement_identity'](self.invocation, 'web', 42, self.snapshot())
        self.assertEqual(row['name'], 'social_family_abcdef012222_web')
        self.assertEqual(row['oid'], 42)

    def test_invalid_namespace_or_lane_is_refused(self):
        for invocation, lane in [(self.invocation, 'postgres'), (self.invocation, '../web'),
                                  (self.invocation.upper(), 'web'), ('11111111-2222-1333-8444-555555555555', 'web')]:
            with self.subTest(invocation=invocation, lane=lane), self.assertRaises((RuntimeError, ValueError)):
                METHOD['lane_database'](invocation, lane)

    def test_foreign_oid_owner_template_or_live_clients_refuse_drop(self):
        for key, value in [('oid', 43), ('owner', 'foreign'), ('template', True), ('client_connections', 1)]:
            changed = self.snapshot()
            changed['databases'][-1][key] = value
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                METHOD['retirement_identity'](self.invocation, 'web', 42, changed)
        with self.assertRaises(RuntimeError):
            METHOD['retirement_identity'](self.invocation, 'web', True, self.snapshot())

    def test_missing_or_extra_database_and_wrong_maintenance_refuse_drop(self):
        for change in ('missing', 'extra', 'maintenance', 'listen'):
            changed = self.snapshot()
            if change == 'missing':
                changed['databases'].pop()
            elif change == 'extra':
                changed['databases'].append({'name': 'unowned_database', 'oid': 90})
            elif change == 'maintenance':
                changed['maintenance_database'] = changed['databases'][-1]['name']
            else:
                changed['listen'] = '*'
            with self.subTest(change=change), self.assertRaises(RuntimeError):
                METHOD['retirement_identity'](self.invocation, 'web', 42, changed)


if __name__ == '__main__':
    if not __debug__:
        raise SystemExit('Optimization is refused')
    unittest.main()
