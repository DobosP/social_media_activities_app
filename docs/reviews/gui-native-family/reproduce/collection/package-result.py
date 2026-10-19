#!/usr/bin/env python3
"""Propose the compact Social family keeper; file/Git reads only, no deletion."""
import argparse
import collections
from decimal import Decimal
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tarfile
import uuid

ROOT = Path('/home/dobo/work/_temp/feat__gui-app-migration-a/social-family')
REPO = Path('/home/dobo/work/_worktrees/social_media_activities_app/feat__gui-app-migration-a')
DRAFT = Path(__file__).absolute().parent
SOURCE = '118fd7f46c6cfc9504d752ba382f890cdbbc8a44'
TREE = '2e151e16a26aa1d29ef23d71e6c8e940910645d1'
INPUT_SHA = '14c6f1fc908b627053d9125547b24fe58bb5184c3c46839522f4154a4d20d0a8'
METHOD_SHA = 'dccfe6bbd27289785e690361c6ce277f2f82b406acd6c5734d37254f79ae4ce2'
LANES = ('web', 'app', 'accounts', 'recommendations', 'social', 'messaging')
PREFIX = 'github.com/DobosP/social_media_activities_app/services/server/internal/'
UI_METHODS = ('tools/gui-native-family/prepare-ui-overlay.py',) + tuple(
    'tools/gui-native-family/ui/' + name for name in
    ('desktop-qa.mjs', 'color-stability.mjs', 'replay-colors.mjs', 'fixture-export_test.go.txt'))


def need(ok, reason):
    if not ok:
        raise RuntimeError(reason)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def literal(name):
    need(isinstance(name, str) and not name.startswith('/') and '\\' not in name
         and all(p not in ('', '.', '..') for p in name.split('/')), 'Literal relative path required')
    return name


def read(path, maximum=64 << 20):
    need(path.is_absolute() and path.resolve() == path, 'Canonical unaliased input required')
    before = path.lstat()
    need(stat.S_ISREG(before.st_mode) and 0 <= before.st_size <= maximum, 'Bounded regular input required')
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        opened = os.fstat(fd)
        with os.fdopen(fd, 'rb', closefd=False) as stream:
            raw = stream.read(maximum + 1)
        after = os.fstat(fd)
        need((before.st_dev, before.st_ino) == (opened.st_dev, opened.st_ino)
             and len(raw) == after.st_size <= maximum and all(getattr(opened, k) == getattr(after, k)
             for k in ('st_dev', 'st_ino', 'st_mode', 'st_uid', 'st_gid', 'st_size', 'st_mtime_ns', 'st_ctime_ns')),
             'Input changed during read')
        return raw, after
    finally:
        os.close(fd)


def pinned(path, row):
    raw, info = read(path)
    need(digest(raw) == row['sha256'] and len(raw) == row['bytes'], 'Pinned evidence differs: ' + str(path))
    return raw, info


def test_receipt(lane, listed, raw, stored):
    # Match the canonical qualifier's required inventory exactly. Raw -list=.
    # also lists benchmarks, which a normal test run does not execute.
    roots = [n for n in listed.decode().splitlines() if re.fullmatch(r'(?:Test|Example|Fuzz)[A-Za-z0-9_]+', n)]
    need(roots and len(roots) == len(set(roots)), 'Complete test inventory required')
    events = [json.loads(line) for line in raw.splitlines()]
    need(events and all(e.get('Package') == PREFIX + lane for e in events), 'Single exact package required')
    need(not any(e.get('Action') in ('fail', 'skip') for e in events), 'Actual fail/skip receipt')
    runs = [e['Test'] for e in events if e.get('Action') == 'run']
    passes = [e['Test'] for e in events if e.get('Action') == 'pass' and e.get('Test')]
    need(collections.Counter(runs) == collections.Counter(passes) and all(n == 1 for n in collections.Counter(runs).values())
         and {n for n in runs if '/' not in n} == set(roots)
         and len([e for e in events if e.get('Action') == 'pass' and not e.get('Test')]) == 1,
         'Every listed root and named case must run/pass once')
    need(stored['top_level'] == len(roots) and stored['named_passes'] == len(passes)
         and stored['fail'] == 0 and stored['skip'] == 0
         and collections.Counter(stored['identities']) == collections.Counter(runs), 'Stored receipt differs from actual JSON')
    return {'top_level': len(roots), 'named_passes': len(passes), 'skip': 0, 'fail': 0}


def admit_final(result, supervisor):
    need(result.get('source') == SOURCE and result.get('tree') == TREE and result.get('method_sha256') == METHOD_SHA
         and result.get('method_pass') is True and result.get('errors') == [] and set(result.get('lanes', {})) == set(LANES), 'Exact six-lane successful result required')
    need(supervisor.get('status') == 'PASS' and type(supervisor.get('gate_child_exit')) is int
         and supervisor['gate_child_exit'] == 0 and supervisor.get('source') == SOURCE
         and supervisor.get('method_sha256') == METHOD_SHA, 'Actual waited child0/source/method required')
    roles = {'db', *LANES, *('reference-' + n for n in LANES)}
    need(set(result['cleanup']) == roles and all(v.get('stopped') is True and v.get('id_absent') is True
         and v.get('name_absent') is True for v in result['cleanup'].values()), 'All thirteen owned objects must have recorded cleanup')
    cleanup = result['pg_cleanup']
    need(cleanup['stop_exit'] == 0 and cleanup['status_exit'] == 3 and cleanup['owned_pid'] > 0
         and cleanup['pid_probe_exit'] == 1 and cleanup['pid_and_socket_files_absent'] == 1, 'Recorded PG shutdown/PID/socket proof required')
    need(set(result['retired_databases']) == set(LANES) and result['pg_storage_initial']['tmpfs']['bytes'] == 1 << 30, 'Original fixed data tmpfs/six retirements required')
    for lane in LANES:
        item = result['lanes'][lane]
        retired = result['retired_databases'][lane]
        need(item['state'] == 'PASS' and item['pg_before'] == item['pg_after'] and item['pg_after']['fixtures'] == 0
             and retired['checkpoint_completed'] is True and retired['name_and_oid_absent'] is True
             and retired['database'] == item['pg_after']['database'] and retired['oid'] == item['pg_after']['oid'], 'Lane source/PG/lifecycle identity differs')


def git(*args):
    return subprocess.check_output(['git', '-C', str(REPO), *args])


def source_fence(rows):
    need(git('rev-parse', 'HEAD').decode().strip() == SOURCE and not git('status', '--porcelain'), 'Exact clean source required')
    tree = {}
    for entry in git('ls-tree', '-r', '-z', SOURCE).split(b'\0'):
        if entry:
            header, name = entry.split(b'\t', 1)
            mode, kind, oid = header.decode().split()
            tree[name.decode()] = (mode, kind, oid)
    need(len(rows) == len(tree) and len({r['path'] for r in rows}) == len(rows), 'Complete Git source manifest required')
    for row in rows:
        path = literal(row['path'])
        raw, info = read(REPO / path)
        blob = hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()
        need(digest(raw) == row['sha256'] and len(raw) == row['bytes']
             and tree.get(path) == ('100755' if row['mode'] & 0o111 else '100644', 'blob', blob)
             and bool(info.st_mode & 0o111) == bool(row['mode'] & 0o111), 'Actual current source/Git binding differs')


def archive(path, payload):
    manifest = []
    with path.open('xb') as file:
        os.fchmod(file.fileno(), 0o600)
        with gzip.GzipFile(fileobj=file, mode='wb', compresslevel=9, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode='w|', format=tarfile.PAX_FORMAT) as output:
                for name, (raw, info, original) in sorted(payload.items()):
                    entry = tarfile.TarInfo(name)
                    entry.size, entry.mode, entry.uid, entry.gid = len(raw), stat.S_IMODE(info.st_mode), info.st_uid, info.st_gid
                    entry.mtime = info.st_mtime_ns // 1_000_000_000
                    entry.pax_headers = {'mtime': str(entry.mtime) + '.' + str(info.st_mtime_ns % 1_000_000_000).zfill(9)}
                    output.addfile(entry, io.BytesIO(raw))
                    manifest.append({'member': name, 'original': original, 'bytes': len(raw), 'sha256': digest(raw), 'mode': entry.mode,
                                     'uid': entry.uid, 'gid': entry.gid, 'mtime_ns': info.st_mtime_ns})
    with tarfile.open(path, 'r:gz') as restored:
        members = restored.getmembers()
        need([e.name for e in members] == [r['member'] for r in manifest], 'Exact archive inventory required')
        for entry, row in zip(members, manifest):
            need(entry.isfile() and entry.size == row['bytes'] and entry.mode == row['mode'] and entry.uid == row['uid']
                 and entry.gid == row['gid'] and int(Decimal(entry.pax_headers['mtime']) * 1_000_000_000) == row['mtime_ns']
                 and digest(restored.extractfile(entry).read()) == row['sha256'], 'Archive bytes/metadata roundtrip differs')
    return manifest


def package():
    inputs_raw, _ = read(DRAFT / 'inputs.json')
    need(digest(inputs_raw) == INPUT_SHA, 'Reviewed input frame required')
    inputs = json.loads(inputs_raw)
    payload, omitted, results = {}, [], {}

    def add(path, name, expected=None):
        literal(name)
        need(name not in payload, 'Duplicate member')
        raw, info = pinned(path, expected) if expected else read(path)
        payload[name] = (raw, info, str(path))
        return raw

    for spec in inputs['runs']:
        label, run, supervisor = spec['label'], ROOT / literal(spec['run']), ROOT / literal(spec['supervisor'])
        for row in spec['files']:
            add(ROOT / literal(row['path']), 'evidence/' + label + '/records/' + Path(row['path']).name, row)
        result = json.loads(read(run / 'result.json')[0])
        status = json.loads(read(supervisor / 'supervisor-status.json')[0])
        need(result['source'] == spec['source'] and result['method_pass'] is spec['pass'] and result['method_sha256'] == spec['method_sha256'], 'Run disposition differs')
        need(status['source'] == spec['source'] and status['status'] == ('PASS' if spec['pass'] else 'FAIL')
             and status['gate_child_exit'] == (0 if spec['pass'] else 1)
             and status['stdout_sha256'] == digest(read(supervisor / 'supervised-gate.log')[0]), 'Supervisor/raw gate join differs')
        excluded = {c['stdout']['path'] for c in result['commands'] if c['argv'][0] == 'git' and 'archive' in c['argv']}
        for row in result['artifact_rows']:
            name = literal(row['path'])
            need(name.startswith('proof/') and name.endswith(('.json', '.stdout', '.stderr')), 'Only raw proof receipts selected')
            if name in excluded:
                omitted.append({'run': label, **row, 'reason': 'Full Git archive stdout excluded; exact Git SHA/tree and recorded archive hash remain source keepers. Not a deletion claim.'})
            else:
                add(run / name, 'evidence/' + label + '/' + name, row)
        results[label] = result
    final = results['final-six-lane-pass']
    final_spec = inputs['runs'][-1]
    admit_final(final, json.loads(read(ROOT / final_spec['supervisor'] / 'supervisor-status.json')[0]))
    final_run = ROOT / final_spec['run']
    source_rows = json.loads(read(final_run / 'source-manifest.json')[0])
    source_fence(source_rows)
    counts = {lane: test_receipt(lane, read(final_run / ('proof/' + lane + '-list.stdout'))[0],
              read(final_run / ('proof/' + lane + '-test2json.stdout'))[0], final['lanes'][lane]['tests']) for lane in LANES}
    for row in inputs['records']:
        add(ROOT / literal(row['path']), 'evidence/selected/' + row['path'], row)
    ui_root = ROOT / 'ui-qa'
    ui = json.loads(read(ui_root / 'desktop-v3/desktop-results.json')[0])
    fixtures = json.loads(read(ui_root / 'fixtures-final/fixtures.json')[0])
    binding = json.loads(read(ui_root / 'final-byte-binding.json')[0])
    need(ui['status'] == 'pass' and ui['failures'] == [] and ui['sourceUnchanged'] is True and ui['sourcesBefore'] == ui['sourcesAfter']
         and len(ui['states']) == 8 and len(ui['noJavaScript']) == 2 and all(ui['ownership'][k] is True for k in ('browserClosed', 'noJSContextClosed', 'serverStopped', 'profileRemoved')),
         'Actual scoped UI/source/cleanup required')
    need(not any(ui[k] for k in ('requestFailures', 'pageErrors', 'cspConsole')) and len(ui['assetResponses']) == 48
         and all(r['status'] == 200 for r in ui['assetResponses']), 'Actual UI assets/errors differ')
    need(fixtures['source_unchanged'] is True and fixtures['sources_before'] == fixtures['sources_after']
         and len(fixtures['sources_before']) == 19 and len(fixtures['states']) == 8
         and binding['accepted_report_sha256'] == digest(read(ui_root / 'desktop-v3/desktop-results.json')[0])
         and binding['final_manifest_sha256'] == digest(read(ui_root / 'fixtures-final/fixtures.json')[0])
         and binding['actual_export_log_sha256'] == digest(read(ROOT / 'logs/ui-export-final.log')[0]), 'Final native export/body bridge differs')
    final_sources = {r['path']: r for r in fixtures['sources_before']}
    old_sources = {r['path']: r for r in ui['sourcesBefore']}
    need(set(final_sources) == set(old_sources) and {p for p in final_sources if final_sources[p]['sha256'] != old_sources[p]['sha256']}
         == {'services/server/internal/web/family.go', 'services/server/internal/web/family_copy.go', 'services/server/internal/web/family_render.go'}, 'Only reviewed three-file perf source bridge allowed')
    for row in final_sources.values():
        pinned(REPO / literal(row['path']), row)
    html = {r['name']: r for r in fixtures['states']}
    equal = {r['path']: r for r in binding['html']}
    need(len(equal) == 8 and len(html) == 8, 'Eight distinct HTML bodies required')
    for state in ui['states']:
        name = literal(state['name'])
        need('/' not in name and state['axe']['violations'] == [] and state['axe']['incomplete'] == []
             and state['steadyState']['initialColorsEqualSettled'] is True and equal[name + '.html']['equal'] is True
             and state['fixtureSHA256'] == html[name]['sha256'] == equal[name + '.html']['sha256'], 'UI fixture/PNG/steady sample differs')
        add(ui_root / ('fixtures-final/' + name + '.html'), 'evidence/ui/html/' + name + '.html', html[name])
        png = ui_root / ('desktop-v3/' + name + '-desktop.png')
        need(digest(read(png)[0]) == state['screenshotSHA256'], 'Latest PNG differs')
        add(png, 'evidence/ui/png/' + name + '.png')
    methods = [r['path'] for r in final['method_inputs']] + list(UI_METHODS)
    for row in final['method_inputs']:
        pinned(REPO / literal(row['path']), row)
    need(digest(read(REPO / UI_METHODS[1])[0]) == ui['setup']['scriptSHA256']
         and digest(read(REPO / UI_METHODS[2])[0]) == ui['setup']['colorPredicateSHA256'], 'Committed observed UI method bytes differ')
    need(shutil.disk_usage(DRAFT).free > sum(len(v[0]) for v in payload.values()) * 2 + (64 << 20), 'Free disk insufficient')
    out = DRAFT / ('proposed-keeper-' + str(uuid.uuid4()))
    out.mkdir(mode=0o700)

    def emit(name, raw):
        literal(name)
        path = out / name
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('xb') as file:
            os.fchmod(file.fileno(), 0o600)
            file.write(raw)
        return path

    for name in methods:
        add(emit('reproduce/repository/' + name, read(REPO / name)[0]), 'reproduce/repository/' + name)
    for name in ('package-result.py', 'test_package_result.py', 'inputs.json', 'plan.json', 'README.template.md'):
        add(emit('reproduce/collection/' + name, read(DRAFT / name)[0]), 'reproduce/collection/' + name)
    add(emit('omitted-artifacts.json', json.dumps(omitted, indent=2).encode() + b'\n'), 'omitted-artifacts.json')
    archive_path = out / 'evidence.tar.gz'
    manifest = archive(archive_path, payload)
    for raw, info, original in payload.values():
        reread, after = read(Path(original))
        need(reread == raw and all(getattr(info, k) == getattr(after, k) for k in ('st_mode', 'st_uid', 'st_gid', 'st_mtime_ns')), 'Original capture fence changed')
    source_fence(source_rows)
    archive_record = {'sha256': digest(read(archive_path)[0]), 'bytes': archive_path.stat().st_size, 'members': len(manifest), 'roundtrip': 'PASS'}
    record = {'schema': 1, 'status': 'PROPOSED_BOUNDED_FEATURE_KEEPER', 'operator': 'agent:codex (PROGRAM E8)', 'source': SOURCE, 'tree': TREE,
              'six_lane_pass': True, 'counts': counts, 'totals': {'packages': 6, 'top_level': sum(r['top_level'] for r in counts.values()), 'named_passes': sum(r['named_passes'] for r in counts.values()), 'skip': 0, 'fail': 0},
              'method_sha256': METHOD_SHA, 'cleanup': 'Recorded thirteen-container and PG/PID/socket shutdown proofs; no new runtime probe by collector.',
              'history': {r['label']: {'source': r['source'], 'pass': r['pass'], 'disposition': r['disposition']} for r in inputs['runs']},
              'ui': {'states': 8, 'no_js': 2, 'axe_predicates': sum(s['axe']['passes'] for s in ui['states']), 'button_samples': sum(len(s['steadyState']['before']) for s in ui['states']), 'new_browser_run_after_perf': False, 'final_html_equal': 8, 'all_frame_certification': False},
              'benchmark': 'Actual final body-only raw retained; smaller payload and ~47% lower allocations than initial native, higher CPU than Pongo; no full HTTP speedup.',
              'retention': 'No deletion by collector. Prior16 superseded PNG paths already pruned per retained executed manifest; latest8 remain. Full Git archive stdout excluded from keeper, not claimed already deleted.',
              'archive': archive_record, 'manifest': 'archive-manifest.json', 'authority': 'Not full21, new/current image, main, milestone, deployment/provider or real-child activation.'}
    result = '{\n' + ',\n'.join('  ' + json.dumps(k) + ': ' + json.dumps(v) for k, v in record.items()) + '\n}\n'
    need(len(result.splitlines()) <= 150, 'Result150 budget exceeded')
    emit('result.json', result.encode())
    emit('archive-manifest.json', json.dumps(manifest, indent=2).encode() + b'\n')
    readme = read(DRAFT / 'README.template.md')[0].decode().replace('@ARCHIVE_SHA@', archive_record['sha256'])
    readme += '\nCollector template: [source](reproduce/collection/README.template.md).\n'
    need(len(readme.splitlines()) <= 40, 'README40 budget exceeded')
    emit('README.md', readme.encode())
    print(json.dumps({'out': str(out), 'archive': archive_record, 'scope': 'proposed bounded feature keeper; independent actual review pending'}), flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--after-success', action='store_true')
    need(parser.parse_args().after_success, 'Explicit Root invocation after actual success required')
    package()
