#!/usr/bin/env python3
"""Write only a new scratch overlay config for committed native UI fixtures."""
import argparse
import json
import os
from pathlib import Path
import stat
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--repo', type=Path, default=Path(__file__).absolute().parents[2])
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
repo, output = args.repo, args.output
if not (__debug__ and repo.is_absolute() and repo.resolve() == repo and output.is_absolute()
        and output.parent.resolve() == output.parent and output.parent.is_dir()
        and output.parent.stat().st_uid == os.getuid() and '_temp' in output.parts
        and not output.is_relative_to(repo)
        and Path(__file__).absolute() == repo / 'tools/gui-native-family/prepare-ui-overlay.py'):
    raise SystemExit('Canonical checkout and existing owned task scratch output required')
methods = ['tools/gui-native-family/prepare-ui-overlay.py',
           *['tools/gui-native-family/ui/' + name for name in
             ('desktop-qa.mjs', 'color-stability.mjs', 'replay-colors.mjs', 'fixture-export_test.go.txt')]]
for name in methods:
    file = repo / name
    if file.resolve() != file or not stat.S_ISREG(file.lstat().st_mode):
        raise SystemExit('Regular committed method source required')
subprocess.run(['git', '-C', str(repo), 'ls-files', '--error-unmatch', '--', *methods],
               check=True, stdout=subprocess.DEVNULL)
subprocess.run(['git', '-C', str(repo), 'diff', '--quiet', 'HEAD', '--', *methods], check=True)
config = {'Replace': {str(repo / 'services/server/internal/web/family_ui_qa_overlay_test.go'):
                      str(repo / 'tools/gui-native-family/ui/fixture-export_test.go.txt')}}
fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
with os.fdopen(fd, 'w') as stream:
    stream.write(json.dumps(config, indent=2) + '\n')
print(str(output))
