#!/usr/bin/env python3
"""Fresh scratch overlay plus the exact six-path UI-only Git bridge."""
import argparse
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
allowed.add('docs/execplans/gui-native-group-create.md')
changed = [name.decode() for name in subprocess.check_output(
    ['git', '-C', str(repo), 'diff', '--no-renames', '--name-only', '-z', args.producer_source, 'HEAD']).split(b'\0') if name]
if not set(changed) <= allowed:
    raise SystemExit('Change outside the exact six UI method/doc paths: ' + repr(changed))
head = subprocess.check_output(['git', '-C', str(repo), 'rev-parse', 'HEAD']).decode().strip()
if args.check_bridge:
    if output is not None:
        raise SystemExit('Bridge-only check writes no overlay')
    print(json.dumps({'producer_source': args.producer_source, 'ui_head': head, 'UI_only_changed_paths': changed}))
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
