#!/usr/bin/env python3
"""Pure standard-library transport/cap controls; never invoke qualification."""
import argparse
import ast
import gzip
import io
import os
from pathlib import Path
import stat
import tarfile
import tempfile
import types
import unittest

SCRATCH = None
SOURCE = None


def source_namespace():
    tree = ast.parse(SOURCE.read_bytes(), filename=str(SOURCE))
    functions = [node for node in tree.body if isinstance(node, ast.FunctionDef)
                 and node.name in {'need', 'digest', 'regular', 'source_rows'}]
    if len(functions) != 4:
        raise ValueError('exact four existing pure transport helpers required')
    import hashlib
    namespace = {'Path': Path, 'os': os, 'stat': stat, 'hashlib': hashlib}
    exec(compile(ast.Module(body=functions, type_ignores=[]), str(SOURCE), 'exec'), namespace)
    execute = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'execute')
    blocks = []
    for node in ast.walk(execute):
        if not isinstance(node, ast.Try):
            continue
        for index, statement in enumerate(node.body):
            if (isinstance(statement, ast.Assign) and len(statement.targets) == 1
                    and isinstance(statement.targets[0], ast.Name) and statement.targets[0].id == 'archive'):
                blocks.append(node.body[index:index + 3])
    if len(blocks) != 1 or len(blocks[0]) != 3:
        raise ValueError('exact existing three-statement archive transport required')
    transport = compile(ast.Module(body=blocks[0], type_ignores=[]), str(SOURCE), 'exec')
    return namespace, transport


def synthetic_tar():
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w:') as archive:
        for name, payload, mode in [('services/server/main.go', b'fixed source one\n', 0o644),
                                    ('tools/check.sh', b'fixed source two\n', 0o755)]:
            entry = tarfile.TarInfo(name); entry.size = len(payload); entry.mode = mode; entry.mtime = 0
            archive.addfile(entry, io.BytesIO(payload))
    return output.getvalue()


class ArchiveTransportControls(unittest.TestCase):
    def setUp(self):
        if SCRATCH is None or SOURCE is None:
            raise RuntimeError('invoke with explicit owned scratch and reviewed source arguments')

    def invoke_fragment(self, root, payload):
        namespace, transport = source_namespace()
        calls = []
        def fake_git(*args):
            calls.append(args)
            return payload
        namespace.update(git=fake_git, args=types.SimpleNamespace(sha='1' * 40), result={},
                         tarfile=tarfile, io=io, out=root)
        exec(transport, namespace)
        self.assertEqual(calls, [('archive', '--format=tar.gz', '-9', '1' * 40)])
        return namespace

    def test_entire_fixed_source_roundtrip_and_format_receipt(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as raw_root:
            root = Path(raw_root)
            compressed = gzip.compress(synthetic_tar(), mtime=0)
            namespace = self.invoke_fragment(root, compressed)
            receipt = namespace['result']['git_archive']
            self.assertEqual(receipt['format'], 'tar.gz')
            self.assertEqual(receipt['bytes'], len(compressed))
            self.assertEqual(receipt['sha256'], namespace['digest'](compressed))
            rows = namespace['source_rows'](root / 'source')
            self.assertEqual([(row['path'], row['mode']) for row in rows],
                             [('services/server/main.go', 0o644), ('tools/check.sh', 0o755)])
            self.assertEqual((root / 'source/services/server/main.go').read_bytes(), b'fixed source one\n')
            self.assertEqual((root / 'source/tools/check.sh').read_bytes(), b'fixed source two\n')

    def test_explicit_gzip_mode_refuses_uncompressed_and_truncated_transport(self):
        tar = synthetic_tar()
        for payload in (tar, gzip.compress(tar, mtime=0)[:16]):
            with self.subTest(kind='fixed invalid archive'), tempfile.TemporaryDirectory(dir=SCRATCH) as root:
                with self.assertRaises((tarfile.TarError, EOFError, OSError)):
                    self.invoke_fragment(Path(root), payload)

    def test_existing_data_filter_refuses_escape_member(self):
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode='w:') as archive:
            entry = tarfile.TarInfo('../outside.txt'); entry.size = 5
            archive.addfile(entry, io.BytesIO(b'fixed'))
        with tempfile.TemporaryDirectory(dir=SCRATCH) as root:
            with self.assertRaises(tarfile.TarError):
                self.invoke_fragment(Path(root), gzip.compress(output.getvalue(), mtime=0))
            self.assertFalse((Path(root) / 'outside.txt').exists())

    def test_unchanged_regular_cap_and_small_exact_boundary(self):
        namespace, _ = source_namespace(); regular = namespace['regular']
        self.assertEqual(regular.__defaults__, (64 << 20,))
        with tempfile.TemporaryDirectory(dir=SCRATCH) as root:
            path = Path(root) / 'fixed.stdout'; path.write_bytes(b'x' * 16)
            self.assertEqual(regular(path, maximum=16), b'x' * 16)
            path.write_bytes(b'x' * 17)
            with self.assertRaises(RuntimeError):
                regular(path, maximum=16)
            # Sparse metadata exercises the real unchanged default refusal
            # before bulk read; it does not allocate a64MiB payload fixture.
            with path.open('wb') as stream:
                stream.truncate((64 << 20) + 1)
            with self.assertRaises(RuntimeError):
                regular(path)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--scratch', type=Path, required=True)
    parser.add_argument('--source', type=Path, default=Path(__file__).with_name('qualify-pg.py'))
    arguments = parser.parse_args()
    SCRATCH, SOURCE = arguments.scratch, arguments.source
    if (not SCRATCH.is_absolute() or SCRATCH.resolve() != SCRATCH or not SCRATCH.is_dir()
            or SCRATCH.stat().st_uid != os.getuid() or '/_temp/' not in str(SCRATCH) + '/'):
        raise ValueError('explicit existing canonical owned scratch required')
    if not SOURCE.is_absolute() or SOURCE.resolve() != SOURCE or not SOURCE.is_file():
        raise ValueError('explicit canonical source required')
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(ArchiveTransportControls)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    raise SystemExit(0 if result.wasSuccessful() else 1)
