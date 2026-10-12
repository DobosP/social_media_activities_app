#!/usr/bin/env python3
"""Recheck current loader inventory against the sealed unsigned GUI draft; source only."""
import hashlib
import io
import json
from pathlib import Path
import stat
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[2]
KEEPER = ROOT / 'docs/reviews/gui-capture-preparation'


def need(value, message):
    if not value:
        raise RuntimeError(message)


def read(path):
    info = path.lstat()
    need(stat.S_ISREG(info.st_mode) and info.st_size <= 4 << 20, 'Bounded regular source required')
    return path.read_bytes()


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def inspect():
    manifest = json.loads(read(KEEPER / 'manifest.json'))
    raw = read(KEEPER / manifest['archive']['path'])
    need(len(raw) == manifest['archive']['bytes'] and sha(raw) == manifest['archive']['sha256'], 'Sealed keeper archive changed')
    expected = {r['name']: r for r in manifest['members']}
    members = {}
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as archive:
        for item in archive:
            need(item.isfile() and item.name in expected and item.name not in members and item.size == expected[item.name]['preserved_bytes'], 'Exact bounded regular keeper members required')
            data = archive.extractfile(item).read()
            row = expected[item.name]
            need(len(data) == row['preserved_bytes'] and sha(data) == row['preserved_sha256'], 'Sealed keeper member changed')
            members[item.name] = data
    need(set(members) == set(expected) and len(members) == 13, 'Complete sealed thirteen-member draft required')
    draft = json.loads(members['capture-ownership.json'])
    groups = draft['groups']
    names = [name for rows in groups.values() for name in rows]
    need(len(names) == len(set(names)) == 91 and len(groups['FROZEN_LEGACY']) == 7, 'Disjoint unsigned91/84/7 ownership required')
    current = {}
    for directory in ['templates', 'apps/web/templates']:
        for path in sorted((ROOT / directory).rglob('*.html')):
            name = path.relative_to(ROOT / directory).as_posix()
            current.setdefault(name, {'path': path.relative_to(ROOT).as_posix(), 'sha256': sha(read(path))})
    need(set(current) == set(names), 'Current loader names differ from unsigned draft; owner reconciliation required')
    old = {row['template']: row['source_sha256'] for row in draft['files']}
    routes_raw = read(ROOT / 'services/server/internal/web/routes.json')
    routes = json.loads(routes_raw)
    need(len(routes) == 155, 'Current route inventory changed; inspect before planning')
    for row in current.values():
        tracked = subprocess.check_output(['git', 'show', 'HEAD:' + row['path']], cwd=ROOT)
        need(sha(tracked) == row['sha256'], 'Current template differs from committed Git')
    need(subprocess.check_output(['git', 'show', 'HEAD:services/server/internal/web/routes.json'], cwd=ROOT) == routes_raw, 'Route table differs from committed Git')
    return {
        'schema': 1,
        'scope': 'Current committed source inventory only; no runtime, capture, native reachability, coverage, owner sign-off or formal S3A claim',
        'source': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
        'keeper_source': manifest['source_snapshot']['commit'],
        'keeper_archive_sha256': sha(raw),
        'sealed_members_verified': len(members),
        'loader_templates': len(current),
        'port_candidates': 84,
        'frozen_legacy': 7,
        'route_names': len(routes),
        'unsigned_groups': {group: len(rows) for group, rows in sorted(groups.items())},
        'changed_templates_since_keeper': [name for name in sorted(current) if current[name]['sha256'] != old[name]],
        'loader_source_sha256': sha(json.dumps(current, sort_keys=True, separators=(',', ':')).encode()),
        'owner_decisions': ['OWN-GROUP-COUNT: exact G0=6/G5=12 provisional paths', 'OWN-LOGIN-GROUP: web/login.html remains HOLD_BOLD_OWNER'],
        'remaining_entry_dependencies': ['S0b templ-kit tag', 'Owner kit sync', 'Reviewed native-versus-historical capture applicability and matrix'],
        'historical_safety_questions': 'Remain historical unsigned drafts; this source-only inventory does not revalidate or answer them',
    }


if __name__ == '__main__':
    print(json.dumps(inspect(), indent=2))
