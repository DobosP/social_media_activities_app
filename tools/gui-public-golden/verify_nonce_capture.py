#!/usr/bin/env python3
"""Read-only DATA verifier for the two explicit nonce-delta capture outputs.

This does not run tests, inspect containers, write goldens or qualify an app.
Actual Go command/count/source/image proofs remain the owning runner's concern.
"""
import argparse
import hashlib
import json
from pathlib import Path
import stat
import sys

PATHS = ['/static/js/hovercard.js', '/static/js/site.js']
TAGS = [b'<script src="/static/js/hovercard.js" defer>',
        b'<script id="site-js" src="/static/js/site.js" defer>']
MARKER = b'<script nonce="synthetic-expected-two-script-delta"'


def need(value, reason):
    if not value:
        raise ValueError(reason)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def sha(value):
    need(isinstance(value, str) and len(value) == 64 and set(value) <= set('0123456789abcdef'),
         'Exact lowercase SHA256 required')
    return value


def read(file, private=False):
    need(file.is_absolute() and file.resolve() == file, 'Canonical unaliased input required')
    info = file.lstat()
    need(stat.S_ISREG(info.st_mode) and 0 < info.st_size <= 512 << 10, 'Bounded regular input required')
    if private:
        need(stat.S_IMODE(info.st_mode) == 0o600, 'Private capture member required')
    raw = file.read_bytes()
    need(len(raw) == info.st_size, 'Input size changed')
    return raw


def members(directory, wanted):
    info = directory.lstat()
    need(directory.is_absolute() and directory.resolve() == directory and stat.S_ISDIR(info.st_mode)
         and stat.S_IMODE(info.st_mode) == 0o700, 'Private canonical capture directory required')
    need({p.name for p in directory.iterdir()} == wanted, 'Exact fourteen bodies plus manifest required')


def empty_hard(value):
    return value is None or (type(value) is list and len(value) == 0)


def expected_copy(raw):
    expected = raw
    for tag in TAGS:
        need(raw.count(tag) == 1, 'Unsupported original external script shape')
        expected = expected.replace(tag, MARKER + tag[len(b'<script'):], 1)
    return expected


def verify(checkpoint_path, original_dir, disk_dir, filesystem_dir, binary_sha):
    sha(binary_sha)
    checkpoint = json.loads(read(checkpoint_path))
    references = checkpoint['captured_cases']
    need(type(references) is list and len(references) == 14, 'Original fourteen-case checkpoint required')
    wanted = {'manifest.json'} | {r['file'] for r in references}
    need(len(wanted) == 15, 'Unique original case files required')
    members(original_dir, wanted)
    need(digest(read(original_dir / 'manifest.json', True)) == checkpoint['capture_manifest_sha256'],
         'Original manifest hash differs')
    originals = {}
    for ref in references:
        case_id = ref['case']['ID']
        need(ref['file'] == case_id + '.html' and '/' not in case_id and '\\' not in case_id
             and case_id not in originals and ref['full_mode'] == 0o100600, 'Original case identity differs')
        raw = read(original_dir / ref['file'], True)
        need(len(raw) == ref['bytes'] and digest(raw) == sha(ref['sha256']), 'Original body hash differs')
        originals[case_id] = raw
    joined = {}
    for mode, directory, transport in [('disk', disk_dir, 'original-disk-root'),
                                       ('filesystem', filesystem_dir, 'hash-bound-test-fs-snapshot')]:
        members(directory, wanted)
        manifest = json.loads(read(directory / 'manifest.json', True))
        need(manifest['status'] == 'captured' and manifest['engine'] == 'original-native-pongo2'
             and manifest['template_transport'] == transport and manifest['original_reference_verified'] is True
             and manifest['source_before'] == manifest['source_after'], 'Capture status/source/reference differs')
        need(manifest['released_normalizer']['binary_sha256'] == binary_sha
             and manifest['released_normalizer']['policy'] == 'core-v1.6/golden.Normalize/strict',
             'Actual reused binary/strict SDK policy differs')
        cases = manifest['cases']
        need(type(cases) is list and len(cases) == 14, 'Complete ordered current cases required')
        hashes, nonces = {}, set()
        for ref, case in zip(references, cases):
            case_id = ref['case']['ID']
            need(json.dumps(case['case'], sort_keys=True) == json.dumps(ref['case'], sort_keys=True)
                 and case['body_file'] == ref['file'] and case['assertions_passed'] is True,
                 'Current case identity/assertions differ')
            raw = read(directory / case['body_file'], True)
            need(len(raw) == case['body_bytes'] and digest(raw) == sha(case['body_sha256']), 'Current body hash differs')
            diagnostic = case['released_normalization']
            need('normalized_bytes_equal' not in diagnostic, 'Old equality schema cannot attest the nonce delta')
            need(diagnostic['original_normalized_bytes_equal'] is False
                 and diagnostic['expected_delta_normalized_bytes_equal'] is True
                 and diagnostic['original_success'] is True and diagnostic['expected_delta_success'] is True
                 and diagnostic['current_success'] is True and diagnostic['diagnostic_pass'] is True,
                 'Original/expected/current exact diagnostic flags differ')
            need(all(empty_hard(diagnostic[name]) for name in ['original_hard', 'expected_delta_hard', 'current_hard']),
                 'Hard findings cannot be hidden by equality')
            original_hash = sha(diagnostic['original_normalized_sha256'])
            expected_hash = sha(diagnostic['expected_delta_normalized_sha256'])
            current_hash = sha(diagnostic['current_normalized_sha256'])
            need(original_hash != current_hash == expected_hash, 'Required structural delta/hash relation differs')
            delta = diagnostic['declared_delta']
            need(delta['kind'] == 'only-two-existing-external-script-nonce-attributes'
                 and delta['paths'] == PATHS and type(delta['attributes_added']) is int and delta['attributes_added'] == 2
                 and delta['original_raw_sha256'] == digest(originals[case_id])
                 and delta['expected_copy_sha256'] == digest(expected_copy(originals[case_id])),
                 'Declared exact two-attribute expected copy differs')
            binding = case['external_script_nonce_binding']
            need(type(binding['bound_external_scripts']) is int and binding['bound_external_scripts'] == 2
                 and binding['paths'] == PATHS and binding['owner_attribute_present'] is False,
                 'Anonymous native header/tag/path/conditional binding differs')
            nonce = sha(binding['header_nonce_sha256'])
            need(nonce not in nonces, 'Actual response nonce was reused')
            nonces.add(nonce)
            hashes[case_id] = current_hash
        joined[mode] = hashes
    need(joined['disk'] == joined['filesystem'], 'Disk/FS expected-delta canonical outputs differ')
    return {'scope': 'Capture DATA readback only; no test/container/fullM1/group/Live qualification',
            'cases_per_mode': 14, 'diagnostics': 28, 'original_structural_difference_required': True,
            'expected_delta_equal_required': True, 'hard_findings': 0, 'baseline_writes': False,
            'binary_sha256': binary_sha}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['checkpoint', 'original', 'disk', 'filesystem']:
        parser.add_argument('--' + name, required=True, type=Path)
    parser.add_argument('--binary-sha256', required=True)
    args = parser.parse_args()
    try:
        result = verify(args.checkpoint, args.original, args.disk, args.filesystem, args.binary_sha256)
    except (OSError, ValueError, KeyError, TypeError) as error:
        print('Capture DATA verification refused: ' + str(error), file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == '__main__':
    sys.exit(main())
