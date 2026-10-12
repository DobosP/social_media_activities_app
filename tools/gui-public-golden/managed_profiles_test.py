"""Offline adversarial profile framing and exported-source custody checks."""
import importlib.util
import json
from pathlib import Path
import unittest

_spec = importlib.util.spec_from_file_location('profiles', Path(__file__).with_name('managed_profiles.py'))
profiles = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(profiles)


class ProfileContractTests(unittest.TestCase):
    def setUp(self):
        self.inventory = json.loads(Path(__file__).with_name('profiles-inventory.json').read_bytes())

    def stream(self, profile):
        rows = []
        for name in profile['ordered_ids']:
            rows.extend([{'Action': action, 'Package': profiles.PACKAGE, 'Test': name} for action in ['run', 'pass']])
        rows.append({'Action': 'pass', 'Package': profiles.PACKAGE})
        return rows

    def encoded(self, rows):
        return b'\n'.join(json.dumps(row).encode() for row in rows)

    def test_complete_profiles_have_exact_identity_counts(self):
        for profile in self.inventory['profiles'].values():
            result = profiles.parse_tests(self.encoded(self.stream(profile)), profile['ordered_ids'], profile)
            self.assertEqual(result['pass'], profile['named_actions'])

    def test_missing_duplicate_or_unknown_action_refuses(self):
        profile = self.inventory['profiles']['preferences']
        rows = self.stream(profile)
        for bad in [rows[1:], rows + [rows[0]], rows + [{'Action': 'pass', 'Package': profiles.PACKAGE, 'Test': 'TestUnknown'}]]:
            with self.assertRaises(RuntimeError):
                profiles.parse_tests(self.encoded(bad), profile['ordered_ids'], profile)

    def test_skip_failure_cache_and_race_refuse(self):
        profile = self.inventory['profiles']['current_g1']
        for extra in [{'Action': 'skip'}, {'Action': 'fail'}, {'Action': 'output', 'Output': '(cached)'}, {'Action': 'output', 'Output': 'DATA RACE'}]:
            with self.assertRaises(RuntimeError):
                profiles.parse_tests(self.encoded(self.stream(profile) + [{'Package': profiles.PACKAGE, **extra}]), profile['ordered_ids'], profile)

    def test_missing_package_completion_refuses(self):
        profile = self.inventory['profiles']['default']
        with self.assertRaises(RuntimeError):
            profiles.parse_tests(self.encoded(self.stream(profile)[:-1]), profile['ordered_ids'], profile)

    def test_unknown_creation_never_grants_cleanup(self):
        result = profiles.creation_observation(True, 1, b'', b'Error: No such object: owned', 'owned', profiles.IMAGE)
        self.assertEqual(result['action'], 'unknown')
        self.assertFalse(result['cleanup_pass'])
        with self.assertRaises(RuntimeError):
            profiles.creation_observation(False, 1, b'', b'', 'owned', profiles.IMAGE)

    def test_pg_fixture_rejects_tcp_and_dirty_schemas(self):
        good = {'database': 'synthetic', 'database_oid': 1, 'server_version_num': 160015, 'listen_addresses': '', 'fixture_schemas': 0, 'extensions': dict.fromkeys(['postgis', 'fuzzystrmatch', 'postgis_tiger_geocoder', 'postgis_topology', 'pg_trgm', 'vector'], '1')}
        self.assertEqual(profiles.validate_pg_profile(json.dumps(good), 'synthetic'), good)
        for bad in [{**good, 'listen_addresses': '*'}, {**good, 'fixture_schemas': 1}, {**good, 'database': 'other'}]:
            with self.assertRaises(RuntimeError):
                profiles.validate_pg_profile(json.dumps(bad), 'synthetic')


if __name__ == '__main__':
    unittest.main()
