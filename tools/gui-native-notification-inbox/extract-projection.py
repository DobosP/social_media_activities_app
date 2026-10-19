#!/usr/bin/env python3
"""Admit one synthetic authorized-view marker after the complete native PG run."""
import argparse
import base64
import collections
import datetime
from urllib.parse import urlsplit
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess

ROOT_TEST = 'TestNativeNotificationInboxRegisteredHTTPAndCurrentAuthority'
PACKAGE = 'github.com/DobosP/social_media_activities_app/services/server/internal/app'
MARKER = 'NATIVE_NOTIFICATION_INBOX_V1 '
LANES = ('web', 'app', 'accounts', 'recommendations', 'social', 'messaging', 'notifications')
SUBTESTS = ['registered-self-newest-first-100-and-read-only-get', 'csrf-origin-head-and-existing-method-admission', 'mark-all-beyond-100-self-only-idempotent-and-fetch', 'current-active-account-and-existing-cohort-admission', 'upstream-failure-preserves-state-and-response', 'registered-display-whitelist-and-existing-spa-source']


def need(ok, message):
    if not ok:
        raise SystemExit(message)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def read(path, maximum=16 << 20):
    need(path.is_absolute() and path.resolve() == path, 'Canonical unaliased input required')
    before = path.lstat()
    need(stat.S_ISREG(before.st_mode) and 0 <= before.st_size <= maximum, 'Bounded regular input required')
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        with os.fdopen(fd, 'rb', closefd=False) as stream:
            raw = stream.read(maximum + 1)
        after = os.fstat(fd)
        need(len(raw) == after.st_size <= maximum and all(getattr(before, k) == getattr(after, k)
             for k in ('st_dev', 'st_ino', 'st_mode', 'st_size', 'st_mtime_ns', 'st_ctime_ns')), 'Input changed during read')
        return raw
    finally:
        os.close(fd)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', type=Path, required=True)
    parser.add_argument('--run', type=Path, required=True)
    parser.add_argument('--source', required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    repo, run, output = args.repo, args.run, args.output
    need(re.fullmatch('[0-9a-f]{40}', args.source) and repo.is_absolute() and repo.resolve() == repo
         and run.is_absolute() and run.resolve() == run and '_temp' in run.parts and run.stat().st_uid == os.getuid()
         and output.is_absolute() and output.parent.resolve() == output.parent and output.parent.is_dir()
         and output.parent.stat().st_uid == os.getuid() and '_temp' in output.parts and not output.is_relative_to(repo)
         and Path(__file__).absolute() == repo / 'tools/gui-native-notification-inbox/extract-projection.py', 'Exact clean committed method and owned task scratch required')

    def bridge():
        raw = subprocess.check_output(['python3', '-I', str(repo / 'tools/gui-native-notification-inbox/qualify.py'), '--repo', str(repo), '--producer-source', args.source, '--check-bridge'])
        proof = json.loads(raw)
        need(proof['producer_source'] == args.source and len(proof['product_paths']) > 2000,
             'Exact current committed product/capture and UI-only descendant bridge required')
        return proof

    bridge_before = bridge()
    def fence():
        need(bridge() == bridge_before, 'Actual capture/producer/UI source bridge changed')
        return [{'path': name, 'bytes': len(raw), 'sha256': digest(raw)} for name in bridge_before['product_paths'] for raw in [read(repo / name)]]

    before = fence()
    subprocess.run(['git', '-C', str(repo), 'ls-files', '--error-unmatch', '--', *bridge_before['product_paths']], check=True, stdout=subprocess.DEVNULL)
    result_raw = read(run / 'result.json')
    result = json.loads(result_raw)
    need(result.get('source') == args.source and result.get('method_pass') is True and result.get('errors') == []
         and set(result.get('lanes', {})) == set(LANES)
         and all(v.get('state') == 'PASS' and v['tests']['fail'] == 0 and v['tests']['skip'] == 0 for v in result['lanes'].values()), 'Complete successful seven-lane result required; no filtered or skipped substitute')
    need(set(result.get('retired_databases', {})) == set(LANES), 'All seven controlled database retirements required')
    method_rows = result.get('method_inputs', [])
    expected_methods = {'tools/gui-native-family/' + name for name in ('qualify.py', 'db-entry.sh', 'db-binding.json', 'test_qualify.py')} | {'services/server/tools/gui-native-family-bootstrap/main.go', 'tools/gui-native-notification-inbox/qualify-pg.py', 'services/server/tools/gui-native-notification-inbox-bootstrap/main.go', 'services/server/tools/gui-native-notification-inbox-bootstrap/main_test.go'}
    need(len(method_rows) == 8 and {row['path'] for row in method_rows} == expected_methods
         and all(row['path'] in bridge_before['product_paths'] and len(read(repo / row['path'])) == row['bytes']
                 and digest(read(repo / row['path'])) == row['sha256'] for row in method_rows)
         and any(row['path'] == 'tools/gui-native-notification-inbox/qualify-pg.py' and row['sha256'] == result['method_sha256'] for row in method_rows),
         'Actual current qualified method/source frame required; no historical hardcoded method hash')
    need(result['cleanup']['db']['id_absent'] is True and result['cleanup']['db']['name_absent'] is True
         and result['pg_cleanup']['stop_exit'] == 0 and result['pg_cleanup']['status_exit'] == 3, 'Recorded successful fixture shutdown required before extraction/benchmark')
    expected_roles = {'db', *LANES, *('reference-' + name for name in LANES)}
    need(set(result['cleanup']) == expected_roles and all(value.get('stopped') is True and value.get('id_absent') is True
         and value.get('name_absent') is True for value in result['cleanup'].values())
         and result['pg_cleanup']['pid_probe_exit'] == 1 and result['pg_cleanup']['pid_and_socket_files_absent'] == 1
         and result['pg_storage_initial']['tmpfs']['bytes'] == 1 << 30,
         'All fifteen owned objects and original-cap clean private PG shutdown required')
    for name, lane in result['lanes'].items():
        retired = result['retired_databases'][name]
        need(lane['pg_before'] == lane['pg_after'] and lane['pg_after']['fixtures'] == 0
             and retired['checkpoint_completed'] is True and retired['name_and_oid_absent'] is True
             and retired['database'] == lane['pg_after']['database'] and retired['oid'] == lane['pg_after']['oid'],
             'Complete seven-lane catalogue/lifetime and controlled database retirement receipts required')
    manifest_raw = read(run / 'source-manifest.json')
    source_list = json.loads(manifest_raw)
    need(isinstance(source_list, list) and len({row['path'] for row in source_list}) == len(source_list), 'Unique complete actual export source inventory required')
    source = {row['path']: row for row in source_list}
    need(all(source.get(row['path']) == row or source.get(row['path'], {}).get('sha256') == row['sha256']
             and source[row['path']]['bytes'] == row['bytes'] for row in before), 'Every actual method/product source must match the qualified export')
    artifacts = result['artifact_rows']
    need(len({row['path'] for row in artifacts}) == len(artifacts), 'Unique actual artifact inventory required')
    receipt_bytes = {}
    def receipt(name, maximum=64 << 20):
        matches = [row for row in artifacts if row['path'] == name]
        need(len(matches) == 1, 'One exact actual artifact required: ' + name)
        raw = read(run / name, maximum)
        need(len(raw) == matches[0]['bytes'] and digest(raw) == matches[0]['sha256'], 'Actual artifact digest differs: ' + name)
        receipt_bytes[name] = raw
        return raw
    for lane in LANES:
        listed = receipt('proof/' + lane + '-list.stdout').decode().splitlines()
        expected = [name for name in listed if re.fullmatch(r'(?:Test|Example|Fuzz)[A-Za-z0-9_]+', name)]
        need(expected and len(set(expected)) == len(expected), 'Complete unique declared root list required: ' + lane)
        raw = receipt('proof/' + lane + '-test2json.stdout')
        events = [json.loads(line) for line in raw.splitlines()]
        package = PACKAGE.rsplit('/', 1)[0] + '/' + lane
        need(events and all(event.get('Package') == package for event in events)
             and not any(event.get('Action') in ('skip', 'fail') or '(cached)' in event.get('Output', '') or 'DATA RACE' in event.get('Output', '') for event in events), 'Unskipped current complete package stream required: ' + lane)
        runs = [event['Test'] for event in events if event.get('Action') == 'run' and event.get('Test')]
        passes = [event['Test'] for event in events if event.get('Action') == 'pass' and event.get('Test')]
        counts = result['lanes'][lane]['tests']
        need(runs and len(runs) == len(set(runs)) and collections.Counter(runs) == collections.Counter(passes)
             and {name for name in runs if '/' not in name} == set(expected)
             and runs == counts['identities'] and len(expected) == counts['top_level'] and len(passes) == counts['named_passes']
             and counts['json_events'] == len(events) and sum(e.get('Action') == 'pass' and not e.get('Test') for e in events) == 1,
             'Every old/new listed root and named case must pass exactly once: ' + lane)
        native = receipt('proof/' + lane + '-native.stdout')
        stderr = receipt('proof/' + lane + '-native.stderr')
        need(b'DATA RACE' not in stderr and b'FAIL' not in stderr, 'Actual native stderr failure/race refused')
        conversions = [row for row in result['commands'] if row.get('stdout', {}).get('path') == 'proof/' + lane + '-test2json.stdout']
        need(len(conversions) == 1 and conversions[0]['exit'] == 0 and conversions[0]['stdin'] == {'bytes': len(native), 'sha256': digest(native)}
             and conversions[0]['argv'][1:] == ['tool', 'test2json', '-p', package], 'Actual raw native stream must join its one package-qualified conversion')
    rows = [r for r in result['artifact_rows'] if r['path'] == 'proof/app-test2json.stdout']
    need(len(rows) == 1, 'Exactly one native App JSON receipt required')
    stream = read(run / 'proof/app-test2json.stdout', 64 << 20)
    need(len(stream) == rows[0]['bytes'] and digest(stream) == rows[0]['sha256'], 'Actual raw JSON receipt differs')
    events = [json.loads(line) for line in stream.splitlines()]
    need(events and all(e.get('Package') == PACKAGE for e in events) and not any(e.get('Action') in ('fail', 'skip') for e in events), 'Single passed native App package stream required')
    runs = [e.get('Test') for e in events if e.get('Action') == 'run']
    passes = [e.get('Test') for e in events if e.get('Action') == 'pass' and e.get('Test')]
    need(collections.Counter(runs) == collections.Counter(passes) and runs.count(ROOT_TEST) == passes.count(ROOT_TEST) == 1
         and collections.Counter(result['lanes']['app']['tests']['identities']) == collections.Counter(runs)
         and len([e for e in events if e.get('Action') == 'pass' and not e.get('Test')]) == 1, 'Actual producer root and complete App pass must join once')
    need(all(runs.count(ROOT_TEST + '/' + name) == passes.count(ROOT_TEST + '/' + name) == 1 for name in SUBTESTS)
         and {name for name in runs if name.startswith(ROOT_TEST + '/')} == {ROOT_TEST + '/' + name for name in SUBTESTS},
         'All six exact registered HTTP/current-source/capture leaves must pass once')
    # Concatenate chunked output first: test2json may split a long t.Log line.
    text = ''.join(e.get('Output', '') for e in events if e.get('Action') == 'output' and e.get('Test') == ROOT_TEST)
    captures = re.findall(re.escape(MARKER) + r'([^\r\n]+)', text)
    need(len(captures) == 1, 'Exactly one bounded actual semantic marker required')
    projection = captures[0].encode()
    need(len(projection) <= 256 << 10, 'Bounded actual synthetic display projection required')
    value = json.loads(projection)
    need(isinstance(value, dict) and set(value) == {'schema', 'variants', 'capture'} and type(value['schema']) is int and value['schema'] == 1
         and value['capture'] == 'same registered-cookie GET callback; display whitelist only; no actor/IDs/credentials/render environment'
         and isinstance(value['variants'], list) and [v.get('language') for v in value['variants']] == ['en', 'ro'],
         'Exact minimal same-callback source schema/languages required; no actor or private environment')
    for variant in value['variants']:
        need(isinstance(variant, dict) and set(variant) == {'language', 'projection'}
             and isinstance(variant['projection'], dict) and set(variant['projection']) == {'items'}, 'Display whitelist only')
        items = variant['projection']['items']
        need(isinstance(items, list) and len(items) == 100, 'Exact actual current bounded100 rows required; do not sort, paginate or infer unread totals')
        stamps = []
        for row in items:
            need(isinstance(row, dict) and set(row) == {'title', 'body', 'why', 'url', 'created_at', 'read_at'}
                 and all(isinstance(row[name], str) and len(row[name].encode()) <= 16384 for name in ('title', 'body', 'why', 'url', 'created_at'))
                 and (row['read_at'] is None or row['read_at'] is True), 'Exact plain display/timestamp/read-presence whitelist required')
            stamp = datetime.datetime.fromisoformat(row['created_at'].replace('Z', '+00:00'))
            need(stamp.tzinfo is not None, 'Actual source timestamp with timezone required')
            stamps.append(stamp)
            href = row['url']
            if href:
                parsed = urlsplit(href)
                need(not any(c in href for c in ('\r', '\n', '\\')) and parsed.username is None
                     and not re.search(r'%(?![0-9A-Fa-f]{2})', href)
                     and (href.startswith('/') and not href.startswith('//') or parsed.scheme in ('http', 'https')),
                     'Only already-admitted original strict safe_href spelling may enter capture')
        need(all(a >= b for a, b in zip(stamps, stamps[1:])), 'Actual newest-first source order must be retained, never re-sorted')
    need(value['variants'][0]['projection'] == value['variants'][1]['projection'], 'Identical actual source item order/read presence across display languages required')
    need(fence() == before and read(run / 'result.json') == result_raw and read(run / 'source-manifest.json') == manifest_raw
         and read(run / 'proof/app-test2json.stdout', 64 << 20) == stream
         and all(read(run / name, 64 << 20) == raw for name, raw in receipt_bytes.items()), 'Capture/source/all-seven-raw-receipt fence changed')
    artifact = {'schema': 1, 'source_sha': args.source, 'ui_method_source': bridge_before['ui_head'], 'producer_root': ROOT_TEST, 'result_sha256': digest(result_raw),
                'qualifier_method_sha256': result['method_sha256'], 'source_manifest_sha256': digest(manifest_raw), 'raw_event_sha256': digest(stream), 'qualified_lane_receipts': {name: digest(raw) for name, raw in receipt_bytes.items()}, 'source_rows': before,
                'projection_base64': base64.b64encode(projection).decode(), 'projection_sha256': digest(projection),
                'scope': 'Actual successful registered cookie GET, exact same forwarded API callback once; only ordered100 admitted title/body/why/strict URL/timestamp/read presence. No second private fetch, Actor/IDs/cookie/session/CSRF/nonce/raw extras. Both engines receive identical fixed current default environment; native VM included. Body-only, no HTTP/DB/equivalent-markup/general speedup/CPU-profile claim.'}
    raw = json.dumps(artifact, indent=2).encode() + b'\n'
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as file:
        file.write(raw)
    print(json.dumps({'output': str(output), 'sha256': digest(raw), 'source': args.source, 'producer_root': ROOT_TEST}), flush=True)


if __name__ == '__main__':
    main()
