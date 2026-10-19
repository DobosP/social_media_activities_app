#!/usr/bin/env python3
"""Fresh overlay; exact UI-tool delta and pinned opaque-header presentation repair."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--repo', type=Path, default=Path(__file__).absolute().parents[2])
parser.add_argument('--output', type=Path)
parser.add_argument('--producer-source', required=True)
parser.add_argument('--check-bridge', action='store_true')
args = parser.parse_args()
repo, output = args.repo, args.output
if not (__debug__ and repo.is_absolute() and repo.resolve() == repo
        and Path(__file__).absolute() == repo / 'tools/gui-native-group/prepare-ui-overlay.py'
        and re.fullmatch('[0-9a-f]{40}', args.producer_source)):
    raise SystemExit('Canonical committed checkout and actual PG producer source required')
methods = ['tools/gui-native-group/' + name for name in
           ('prepare-ui-overlay.py', 'desktop-qa.mjs', 'fixture-export_test.go.txt', 'family-html-baseline.json', 'extract-projection.py')]
methods += ['tools/gui-native-family/ui/' + name for name in ('color-stability.mjs', 'fixture-export_test.go.txt')]
for name in methods:
    file = repo / name
    if file.resolve() != file or not stat.S_ISREG(file.lstat().st_mode):
        raise SystemExit('Regular committed method source required')
subprocess.run(['git', '-C', str(repo), 'ls-files', '--error-unmatch', '--', *methods], check=True, stdout=subprocess.DEVNULL)
if subprocess.check_output(['git', '-C', str(repo), 'status', '--porcelain']):
    raise SystemExit('Clean committed product/method checkpoint required')
subprocess.run(['git', '-C', str(repo), 'merge-base', '--is-ancestor', args.producer_source, 'HEAD'], check=True)
allowed = {'tools/gui-native-group/' + name for name in
           ('prepare-ui-overlay.py', 'desktop-qa.mjs', 'fixture-export_test.go.txt', 'family-html-baseline.json', 'README.md')}
allowed.update(('docs/execplans/gui-native-group-create.md', 'STATUS.md'))
changed = [name.decode() for name in subprocess.check_output(
    ['git', '-C', str(repo), 'diff', '--no-renames', '--name-only', '-z', args.producer_source, 'HEAD']).split(b'\0') if name]
# This is an exact presentation repair, not a general product-source allowlist.
# Original complete PG/capture source stays immutable. Every other path outside
# the seven reviewed UI methods/docs remains forbidden, including production Go.
repairs = {'static/css/group-create-native.css': ('4e8b3c6a33c257b4f9503f96ceddaa23153db8d1590a025b903be5e234beebb9', '5cf7f8d6e02186091fe98279d38ceeff7ccdd31eb7c223807a33641795df4228'), 'services/server/internal/app/group_native_http_test.go': ('30038fefb3354475a10865c0b87c19dd6e7b0063785bad109af0559ad7cfd011', 'a22605f35544b53e792a643e3f8a1aea42c358baed3e1a1a1ceebaa67e574616')}
if not set(changed) <= allowed | repairs.keys():
    raise SystemExit('Change outside exact UI methods/pinned presentation repair: ' + repr(changed))
presentation = []
for name, (before_sha, after_sha) in repairs.items():
    if name not in changed:
        continue
    before = subprocess.check_output(['git', '-C', str(repo), 'show', args.producer_source + ':' + name])
    current = repo / name
    if current.resolve() != current or not stat.S_ISREG(current.lstat().st_mode):
        raise SystemExit('Regular unaliased presentation repair input required')
    after = current.read_bytes()
    if hashlib.sha256(before).hexdigest() != before_sha or hashlib.sha256(after).hexdigest() != after_sha:
        raise SystemExit('Exact before/after presentation repair required: ' + name)
    presentation.append({'path': name, 'PG_source_sha256': before_sha, 'UI_source_sha256': after_sha})
if presentation and len(presentation) != 2:
    raise SystemExit('Opaque header CSS and its exact transport assertion must move together')
head = subprocess.check_output(['git', '-C', str(repo), 'rev-parse', 'HEAD']).decode().strip()
if args.check_bridge:
    if output is not None:
        raise SystemExit('Bridge-only check writes no overlay')
    print(json.dumps({'producer_source': args.producer_source, 'ui_head': head, 'UI_only_changed_paths': [name for name in changed if name in allowed], 'presentation_changed_paths': presentation, 'scope': 'PG producer epoch plus exact opaque-header presentation/test repair; no complete PG claim for later presentation epoch'}))
else:
    if not (output is not None and output.is_absolute() and output.parent.resolve() == output.parent
            and output.parent.is_dir() and output.parent.stat().st_uid == os.getuid()
            and '_temp' in output.parts and not output.is_relative_to(repo)):
        raise SystemExit('Existing owned task scratch and fresh output required')
    config = {'Replace': {
        str(repo / 'services/server/internal/web/group_ui_qa_overlay_test.go'): str(repo / 'tools/gui-native-group/fixture-export_test.go.txt'),
        str(repo / 'services/server/internal/web/family_ui_qa_overlay_test.go'): str(repo / 'tools/gui-native-family/ui/fixture-export_test.go.txt'),
    }}
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(json.dumps(config, indent=2) + '\n')
    print(str(output))
