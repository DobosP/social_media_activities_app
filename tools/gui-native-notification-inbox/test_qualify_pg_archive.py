#!/usr/bin/env python3
"""Four source-selected standard-library XZ transport controls; never qualify PG."""
import argparse
import ast
import gzip
import hashlib
import io
import json
import lzma
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
    names = {'need', 'digest', 'regular', 'source_rows', 'strict_xz_receipt'}
    functions = [node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name in names]
    if {node.name for node in functions} != names or len(functions) != 5:
        raise ValueError('exact five reviewed pure transport helpers required')
    constant_names = {'SOURCE_ARCHIVE_ENV', 'XZ_FILTER'}
    constants = [node for node in tree.body if isinstance(node, ast.Assign) and len(node.targets) == 1
                 and isinstance(node.targets[0], ast.Name) and node.targets[0].id in constant_names]
    if len(constants) != 2:
        raise ValueError('exact two transport constants required')
    namespace = {'Path': Path, 'os': os, 'stat': stat, 'hashlib': hashlib, 'lzma': lzma}
    exec(compile(ast.Module(body=constants + functions, type_ignores=[]), str(SOURCE), 'exec'), namespace)
    execute = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'execute')
    blocks = []
    for node in ast.walk(execute):
        if isinstance(node, ast.Try):
            for index, statement in enumerate(node.body):
                if (isinstance(statement, ast.Assign) and len(statement.targets) == 1
                        and isinstance(statement.targets[0], ast.Name) and statement.targets[0].id == 'archive'):
                    blocks.append(node.body[index:index + 3])
    if len(blocks) != 1 or len(blocks[0]) != 3:
        raise ValueError('exact current three-statement XZ transport required')
    transport = compile(ast.Module(body=blocks[0], type_ignores=[]), str(SOURCE), 'exec')
    return namespace, transport


def synthetic_tar(escape=False):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w:', format=tarfile.PAX_FORMAT) as archive:
        entries = [('services/server/main.go', b'fixed source one\n', 0o644),
                   ('tools/check.sh', b'fixed source two\n', 0o755)]
        if escape:
            entries = [('../outside.txt', b'fixed', 0o644)]
        for name, payload, mode in entries:
            entry = tarfile.TarInfo(name)
            entry.size = len(payload); entry.mode = mode; entry.mtime = 0
            entry.pax_headers = {'mtime': '0.123456789', 'comment': 'fixed provenance'}
            archive.addfile(entry, io.BytesIO(payload))
    return output.getvalue()


def xz(raw, check=lzma.CHECK_CRC64):
    return lzma.compress(raw, format=lzma.FORMAT_XZ, check=check, preset=6)


class ArchiveTransportControls(unittest.TestCase):
    def setUp(self):
        if SCRATCH is None or SOURCE is None:
            raise RuntimeError('explicit owned scratch and reviewed source arguments required')

    def invoke_fragment(self, root, payload):
        namespace, transport = source_namespace()
        calls = []
        def fake_call(argv, **kwargs):
            calls.append((argv, kwargs))
            return 0, payload, b''
        namespace.update(call=fake_call, args=types.SimpleNamespace(sha='1' * 40),
                         REPO=Path('/synthetic/repo'), result={}, tarfile=tarfile, io=io, out=root)
        exec(transport, namespace)
        self.assertEqual(calls, [(['git', '-C', '/synthetic/repo', '-c',
                                  'tar.tar.xz.command=/usr/bin/xz --threads=1 --check=crc64 -c',
                                  'archive', '--format=tar.xz', '-6', '1' * 40],
                                 {'command_env': {'PATH': '/usr/bin:/bin', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8'}})])
        return namespace

    def test_entire_fixed_source_roundtrip_format_hash_executable_and_PAX(self):
        raw_tar = synthetic_tar(); compressed = xz(raw_tar)
        with tempfile.TemporaryDirectory(dir=SCRATCH) as raw_root:
            root = Path(raw_root); namespace = self.invoke_fragment(root, compressed)
            receipt = namespace['result']['git_archive']
            self.assertEqual(receipt['format'], 'tar.xz')
            self.assertEqual(receipt['bytes'], len(compressed))
            self.assertEqual(receipt['sha256'], hashlib.sha256(compressed).hexdigest())
            self.assertEqual(receipt['expanded_tar_bytes'], len(raw_tar))
            self.assertEqual(receipt['expanded_tar_sha256'], hashlib.sha256(raw_tar).hexdigest())
            self.assertIs(receipt['CRC64_full_EOF_single_stream'], True)
            rows = namespace['source_rows'](root / 'source')
            self.assertEqual([(r['path'], r['mode']) for r in rows],
                             [('services/server/main.go', 0o644), ('tools/check.sh', 0o755)])
            self.assertEqual((root / 'source/services/server/main.go').read_bytes(), b'fixed source one\n')
            self.assertEqual((root / 'source/tools/check.sh').read_bytes(), b'fixed source two\n')
            with tarfile.open(fileobj=io.BytesIO(compressed), mode='r:xz') as archive:
                members = archive.getmembers()
                self.assertEqual([m.name for m in members], [r['path'] for r in rows])
                self.assertEqual([m.pax_headers for m in members],
                                 [{'mtime': '0.123456789', 'comment': 'fixed provenance'}] * 2)
                self.assertEqual([m.mode for m in members], [0o644, 0o755])

    def test_strict_XZ_CRC64_full_EOF_refuses_wrong_damaged_or_extra_transport(self):
        tar = synthetic_tar(); valid = xz(tar)
        damaged = valid[:-1] + bytes([valid[-1] ^ 1])
        invalid = [tar, gzip.compress(tar, mtime=0), lzma.compress(tar, format=lzma.FORMAT_ALONE),
                   valid[:16], valid[:-1], damaged, valid + b'x', valid + b'\0' * 4,
                   valid + valid, xz(tar, lzma.CHECK_CRC32), xz(tar, lzma.CHECK_NONE)]
        for index, payload in enumerate(invalid):
            with self.subTest(fixed_invalid_transport=index), tempfile.TemporaryDirectory(dir=SCRATCH) as raw_root:
                root = Path(raw_root)
                with self.assertRaises((RuntimeError, lzma.LZMAError, EOFError, tarfile.TarError)):
                    self.invoke_fragment(root, payload)
                self.assertFalse((root / 'source').exists())

    def test_existing_data_filter_refuses_escape_member(self):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as raw_root:
            root = Path(raw_root)
            with self.assertRaises(tarfile.TarError):
                self.invoke_fragment(root, xz(synthetic_tar(escape=True)))
            self.assertFalse((root / 'outside.txt').exists())

    def test_unchanged_regular_and_strict_decoder_exact_small_boundaries(self):
        namespace, _ = source_namespace(); regular = namespace['regular']; strict = namespace['strict_xz_receipt']
        self.assertEqual(regular.__defaults__, (64 << 20,))
        self.assertEqual(strict.__defaults__, (64 << 20, 128 << 20, 64 << 20))
        raw = b'x' * 16; compressed = xz(raw)
        self.assertEqual(strict(compressed, len(compressed), 16, 64 << 20)['expanded_tar_bytes'], 16)
        with self.assertRaises(lzma.LZMAError):
            strict(compressed, len(compressed), 16, 1)
        with self.assertRaises(RuntimeError): strict(compressed, len(compressed) - 1, 16)
        with self.assertRaises(RuntimeError): strict(compressed, len(compressed), 15)
        with self.assertRaises(RuntimeError): strict(compressed, (64 << 20) + 1, 16)
        with self.assertRaises(RuntimeError): strict(compressed, len(compressed), (128 << 20) + 1)
        with self.assertRaises(RuntimeError): strict(compressed, len(compressed), 16, (64 << 20) + 1)
        with tempfile.TemporaryDirectory(dir=SCRATCH) as raw_root:
            path = Path(raw_root) / 'fixed.stdout'; path.write_bytes(b'x' * 16)
            self.assertEqual(regular(path, maximum=16), b'x' * 16)
            path.write_bytes(b'x' * 17)
            with self.assertRaises(RuntimeError): regular(path, maximum=16)
            with path.open('wb') as stream: stream.truncate((64 << 20) + 1)
            with self.assertRaises(RuntimeError): regular(path)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--scratch', type=Path, required=True)
    parser.add_argument('--source', type=Path, default=Path(__file__).with_name('qualify-pg.py'))
    arguments = parser.parse_args(); SCRATCH, SOURCE = arguments.scratch, arguments.source
    if (not SCRATCH.is_absolute() or SCRATCH.resolve() != SCRATCH or not SCRATCH.is_dir()
            or SCRATCH.stat().st_uid != os.getuid() or '/_temp/' not in str(SCRATCH) + '/'):
        raise ValueError('explicit existing canonical owned scratch required')
    if not SOURCE.is_absolute() or SOURCE.resolve() != SOURCE or not SOURCE.is_file():
        raise ValueError('explicit canonical source required')
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(ArchiveTransportControls)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    raise SystemExit(0 if result.wasSuccessful() else 1)
