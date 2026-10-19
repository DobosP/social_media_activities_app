#!/usr/bin/env python3
"""Admit one synthetic authorized-view marker after the complete native PG run."""
import argparse
import base64
import collections
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess

ROOT_TEST = 'TestNativeNotificationPreferencesRegisteredHTTPAndDelivery'
PACKAGE = 'github.com/DobosP/social_media_activities_app/services/server/internal/app'
MARKER = 'NATIVE_NOTIFICATION_PREFERENCES_V1 '
ORDER = ['join_requested', 'join_approved', 'event_reminder', 'activity_cancelled', 'activity_updated', 'announcement', 'group_announcement', 'meetup_confirmed', 'arrival', 'connection_request', 'connection_accepted', 'mention', 'organizer_role', 'activity_match', 'gauge_match', 'group_question', 'interest_converted', 'rsvp_nudge', 'organizer_prep', 'supervisor_needed', 'formative_note', 'mod_alert']
SUBTESTS = ['registered-get-is-self-scoped-and-side-effect-free', 'csrf-origin-and-current-unverified-account-policy', 'repeated-form-filter-sort-and-own-vs-other-boundary', 'mandatory-audit-refusal-rolls-back-and-renders-current-checks', 'actual-delivery-mandatory-bypass-and-absent-selection-clears', 'existing-spa-data-branch-and-mutation-return-path', 'registered-display-whitelist-and-current-inactive-account']


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
         and Path(__file__).absolute() == repo / 'tools/gui-native-notification-preferences/extract-projection.py', 'Exact clean committed method and owned task scratch required')

    def bridge():
        raw = subprocess.check_output(['python3', '-I', str(repo / 'tools/gui-native-notification-preferences/qualify.py'), '--repo', str(repo), '--producer-source', args.source, '--check-bridge'])
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
         and set(result.get('lanes', {})) == {'web', 'app', 'accounts', 'recommendations', 'social', 'messaging'}
         and all(v.get('state') == 'PASS' and v['tests']['fail'] == 0 and v['tests']['skip'] == 0 for v in result['lanes'].values()), 'Complete successful six-lane result required; no filtered or skipped substitute')
    method_rows = result.get('method_inputs', [])
    need(len(method_rows) == 5 and len({row['path'] for row in method_rows}) == 5
         and all(row['path'] in bridge_before['product_paths'] and len(read(repo / row['path'])) == row['bytes']
                 and digest(read(repo / row['path'])) == row['sha256'] for row in method_rows)
         and any(row['path'] == 'tools/gui-native-family/qualify.py' and row['sha256'] == result['method_sha256'] for row in method_rows),
         'Actual current qualified method/source frame required; no historical hardcoded method hash')
    need(result['cleanup']['db']['id_absent'] is True and result['cleanup']['db']['name_absent'] is True
         and result['pg_cleanup']['stop_exit'] == 0 and result['pg_cleanup']['status_exit'] == 3, 'Recorded successful fixture shutdown required before extraction/benchmark')
    expected_roles = {'db', 'web', 'app', 'accounts', 'recommendations', 'social', 'messaging',
                      *('reference-' + name for name in ('web', 'app', 'accounts', 'recommendations', 'social', 'messaging'))}
    need(set(result['cleanup']) == expected_roles and all(value.get('stopped') is True and value.get('id_absent') is True
         and value.get('name_absent') is True for value in result['cleanup'].values())
         and result['pg_cleanup']['pid_probe_exit'] == 1 and result['pg_cleanup']['pid_and_socket_files_absent'] == 1
         and result['pg_storage_initial']['tmpfs']['bytes'] == 1 << 30,
         'All thirteen owned objects and original-cap clean private PG shutdown required')
    for name, lane in result['lanes'].items():
        retired = result['retired_databases'][name]
        need(lane['pg_before'] == lane['pg_after'] and lane['pg_after']['fixtures'] == 0
             and retired['checkpoint_completed'] is True and retired['name_and_oid_absent'] is True
             and retired['database'] == lane['pg_after']['database'] and retired['oid'] == lane['pg_after']['oid'],
             'Complete six-lane catalogue/lifetime and controlled database retirement receipts required')
    manifest_raw = read(run / 'source-manifest.json')
    source = json.loads(manifest_raw)
    source = {row['path']: row for row in source}
    need(all(source.get(row['path']) == row or source.get(row['path'], {}).get('sha256') == row['sha256']
             and source[row['path']]['bytes'] == row['bytes'] for row in before), 'Every actual method/product source must match the qualified export')
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
         'All seven exact registered HTTP/security/delivery/capture leaves must pass once')
    # Concatenate chunked output first: test2json may split a long t.Log line.
    text = ''.join(e.get('Output', '') for e in events if e.get('Action') == 'output' and e.get('Test') == ROOT_TEST)
    captures = re.findall(re.escape(MARKER) + r'([^\r\n]+)', text)
    need(len(captures) == 1, 'Exactly one bounded actual semantic marker required')
    projection = captures[0].encode()
    need(len(projection) <= 64 << 10, 'Bounded actual synthetic display projection required')
    value = json.loads(projection)
    need(isinstance(value, dict) and set(value) == {'schema', 'variants'} and value['schema'] == 1
         and isinstance(value['variants'], list) and [v.get('language') for v in value['variants']] == ['en', 'ro'],
         'Exact minimal source schema/languages required; no Actor or private environment')
    for variant in value['variants']:
        need(isinstance(variant, dict) and set(variant) == {'language', 'projection'}
             and isinstance(variant['projection'], dict) and set(variant['projection']) == {'rows'}, 'Display whitelist only')
        rows = variant['projection']['rows']
        need(isinstance(rows, list) and len(rows) == 22, 'Complete current optional row set required')
        for index, row in enumerate(rows):
            need(isinstance(row, dict) and set(row) == {'value', 'label', 'reason', 'muted'}
                 and row['value'] == ORDER[index] and type(row['muted']) is bool
                 and row['muted'] is (row['value'] == 'arrival')
                 and all(isinstance(row[name], str) and 0 < len(row[name]) <= 8192 for name in ('label', 'reason')),
                 'Exact source kind order/plain copy/boolean preferences required')
    need(value['variants'][0]['projection'] == value['variants'][1]['projection'], 'Same current source rows for both languages required')
    need(fence() == before and read(run / 'result.json') == result_raw and read(run / 'source-manifest.json') == manifest_raw
         and read(run / 'proof/app-test2json.stdout', 64 << 20) == stream, 'Capture/source fence changed')
    artifact = {'schema': 1, 'source_sha': args.source, 'ui_method_source': bridge_before['ui_head'], 'producer_root': ROOT_TEST, 'result_sha256': digest(result_raw),
                'qualifier_method_sha256': result['method_sha256'], 'source_manifest_sha256': digest(manifest_raw), 'raw_event_sha256': digest(stream), 'source_rows': before,
                'projection_base64': base64.b64encode(projection).decode(), 'projection_sha256': digest(projection),
                'scope': 'After real registered cookie GETs, same current authority AccountView display rows only; this is a second authorized side-effect-free projection, not a captured Render invocation. No Actor/cookie/session/CSRF/nonce/ward/access payload. Both engines receive identical fixed render environment; body-only, no HTTP/DB/throughput/general speedup claim.'}
    raw = json.dumps(artifact, indent=2).encode() + b'\n'
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as file:
        file.write(raw)
    print(json.dumps({'output': str(output), 'sha256': digest(raw), 'source': args.source, 'producer_root': ROOT_TEST}), flush=True)


if __name__ == '__main__':
    main()
