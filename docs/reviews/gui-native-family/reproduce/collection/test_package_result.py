#!/usr/bin/env python3
"""Pure refusal/archive controls; never invokes native tests, Docker or PostgreSQL."""
import copy
import json
import os
from pathlib import Path
import runpy
import tempfile
import unittest

HERE = Path(__file__).absolute().parent
M = runpy.run_path(str(HERE / 'package-result.py'))


class Controls(unittest.TestCase):
    def test_final_frame_refuses_mutation(self):
        inputs = json.loads((HERE / 'inputs.json').read_bytes())
        spec = inputs['runs'][-1]
        result = json.loads((M['ROOT'] / spec['run'] / 'result.json').read_bytes())
        status = json.loads((M['ROOT'] / spec['supervisor'] / 'supervisor-status.json').read_bytes())
        M['admit_final'](result, status)
        for name, mutation in [
            ('old-source', lambda r: r.update(source='cdbd21a5a54ba2705cbe5be84e746ed4705fa5d1')),
            ('failed-method', lambda r: r.update(method_pass=False)),
            ('error', lambda r: r['errors'].append('actual failure')),
            ('missing-lane', lambda r: r['lanes'].pop('messaging')),
            ('missing-cleanup', lambda r: r['cleanup'].pop('db')),
            ('remaining-object', lambda r: r['cleanup']['db'].update(id_absent=False)),
            ('missing-checkpoint', lambda r: r['retired_databases']['social'].update(checkpoint_completed=False)),
            ('increased-tmpfs', lambda r: r['pg_storage_initial']['tmpfs'].update(bytes=2 << 30)),
        ]:
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                changed = copy.deepcopy(result)
                mutation(changed)
                M['admit_final'](changed, status)
        changed = dict(status, gate_child_exit=1)
        with self.assertRaises(RuntimeError):
            M['admit_final'](result, changed)

    def test_actual_json_inventory_refusals(self):
        package = M['PREFIX'] + 'web'
        events = [{'Action': 'start', 'Package': package}, {'Action': 'run', 'Package': package, 'Test': 'TestSynthetic'},
                  {'Action': 'run', 'Package': package, 'Test': 'TestSynthetic/one'},
                  {'Action': 'pass', 'Package': package, 'Test': 'TestSynthetic/one'},
                  {'Action': 'pass', 'Package': package, 'Test': 'TestSynthetic'}, {'Action': 'pass', 'Package': package}]
        # All canonical qualifying prefixes remain mandatory; Benchmark remains
        # in the unchanged raw list but is not an expected normal test run.
        for name in ('ExampleSynthetic', 'FuzzSynthetic'):
            events[-1:-1] = [{'Action': 'run', 'Package': package, 'Test': name},
                             {'Action': 'pass', 'Package': package, 'Test': name}]
        listed = b'BenchmarkNativeFamilyBodyRender\nTestSynthetic\nExampleSynthetic\nFuzzSynthetic\n'
        stored = {'top_level': 3, 'named_passes': 4, 'fail': 0, 'skip': 0,
                  'identities': ['TestSynthetic', 'TestSynthetic/one', 'ExampleSynthetic', 'FuzzSynthetic']}
        encode = lambda rows: b'\n'.join(json.dumps(row).encode() for row in rows)
        M['test_receipt']('web', listed, encode(events), stored)
        for changed in [events[:-1], events + [events[1]], events + [{'Action': 'skip', 'Package': package, 'Test': 'TestSynthetic'}]]:
            with self.assertRaises(RuntimeError):
                M['test_receipt']('web', listed, encode(changed), stored)
        with self.assertRaises(RuntimeError):
            M['test_receipt']('web', b'TestOther\n', encode(events), stored)
        for missing in ('ExampleSynthetic', 'FuzzSynthetic'):
            with self.assertRaises(RuntimeError):
                M['test_receipt']('web', listed, encode([e for e in events if e.get('Test') != missing]), stored)
        with self.assertRaises(RuntimeError):
            benchmark = events[:-1] + [{'Action': 'run', 'Package': package, 'Test': 'BenchmarkNativeFamilyBodyRender'},
                                       {'Action': 'pass', 'Package': package, 'Test': 'BenchmarkNativeFamilyBodyRender'}, events[-1]]
            M['test_receipt']('web', listed, encode(benchmark), stored)

    def test_archive_roundtrip_and_path_refusals(self):
        with tempfile.TemporaryDirectory(prefix='pure-package-', dir=HERE) as tmp:
            root = Path(tmp)
            source = root / 'synthetic.log'
            source.write_bytes(b'synthetic receipt only\n')
            source.chmod(0o640)
            os.utime(source, ns=(1_700_000_000_123456789, 1_700_000_000_123456789))
            raw, info = M['read'](source)
            rows = M['archive'](root / 'test.tar.gz', {'proof/synthetic.log': (raw, info, str(source))})
            self.assertEqual(rows[0]['mtime_ns'], info.st_mtime_ns)
            self.assertEqual(rows[0]['mode'], 0o640)
            alias = root / 'alias.log'
            alias.symlink_to(source)
            with self.assertRaises(RuntimeError):
                M['read'](alias)
            with self.assertRaises(RuntimeError):
                M['read'](source, 1)
        for name in ('/absolute', '../escape', 'a//b', 'a/./b', 'a\\b'):
            with self.assertRaises(RuntimeError):
                M['literal'](name)


if __name__ == '__main__':
    unittest.main()
