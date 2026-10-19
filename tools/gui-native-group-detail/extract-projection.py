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

ROOT_TEST = 'TestNativeGroupDetailRegisteredProjectionCaptureUsesCurrentAuthority'
PACKAGE = 'github.com/DobosP/social_media_activities_app/services/server/internal/app'
MARKER = 'NATIVE_GROUP_DETAIL_V1 '
SOURCE_NAMES = ['services/server/internal/web/routes.json', 'services/server/internal/web/forms.json', 'services/server/internal/web/social_pages.go', 'services/server/internal/web/social_threads.go', 'services/server/internal/web/social_actions.go', 'services/server/internal/web/actions.go', 'services/server/internal/web/server.go', 'services/server/internal/web/router.go', 'services/server/internal/web/renderer.go', 'services/server/internal/web/forms.go', 'services/server/internal/social/groups.go', 'services/server/internal/social/threads.go', 'services/server/internal/social/authorization.go', 'services/server/internal/social/service.go', 'services/server/internal/social/logistics.go', 'services/server/internal/social/http.go', 'services/server/internal/platform/platform.go', 'apps/web/templates/web/group_detail.html', 'apps/web/templates/web/_post.html', 'apps/web/templates/web/_activity_card.html', 'templates/base.html', 'static/js/site.js', 'static/js/hovercard.js', 'static/js/presend-nudge.js', 'static/js/thread-chat.js', 'services/server/internal/web/social_pages_test.go', 'services/server/internal/web/thread_case_port4_test.go', 'services/server/internal/web/policy_thread_test.go', 'services/server/internal/social/social_test.go', 'services/server/internal/social/rate_policy_test.go', 'services/server/internal/social/rest_thread_access_case_test.go', 'services/server/internal/app/group_native_http_test.go', 'apps/social/tests/test_groups.py', 'apps/social/tests/test_public_listing_api.py', 'apps/social/tests/test_group_ask_organiser.py', 'apps/web/tests/test_group_thread_sentiment.py', 'services/server/internal/web/gui_public_get_walk_test.go', 'services/server/internal/web/gui_preferences_get_transport_test.go', 'services/server/internal/web/group_create_custody_test.go', 'services/server/internal/web/group_create_source_guard_test.go', 'services/server/internal/web/family_custody_test.go', 'services/server/internal/web/testdata/gui-public-get-walk/source.json', 'services/server/internal/web/markup.go', 'apps/web/templates/web/_post_sentiment.html', 'services/server/internal/web/spa_models.go', 'services/server/internal/web/family.go', 'services/server/internal/web/group_create.go', 'services/server/internal/web/post_notice.go', 'services/server/internal/web/media_busy.go', 'services/server/internal/accounts/avatar.go', 'services/server/internal/avatars/render.go', 'services/server/internal/social/sentiment.go', 'services/server/internal/media/attachments.go', 'services/server/internal/media/service.go', 'services/server/internal/schema/baseline.sql', 'services/server/internal/app/app.go', 'services/server/internal/app/group_detail_native_http_test.go', 'services/server/internal/web/group_detail.go', 'services/server/internal/web/group_detail_copy.go', 'services/server/internal/web/group_detail_custody_test.go', 'services/server/internal/web/group_detail_post.go', 'services/server/internal/web/group_detail_projection.go', 'services/server/internal/web/group_detail_test.go', 'services/server/internal/web/pages/group-detail-card.gohtml', 'services/server/internal/web/pages/group-detail-post.gohtml', 'services/server/internal/web/pages/group-detail.gohtml', 'static/css/group-detail-native.css', 'services/server/internal/web/group_detail_ask_compat_test.go', 'services/server/internal/web/group_detail_config_schema_test.go', 'services/server/internal/app/group_detail_ask_native_http_test.go', 'services/server/internal/app/group_detail_capture_test.go', 'services/server/go.mod', 'services/server/go.sum', 'tools/gui-native-family/qualify.py', 'tools/gui-native-family/db-entry.sh', 'tools/gui-native-family/db-binding.json', 'tools/gui-native-family/test_qualify.py', 'services/server/tools/gui-native-family-bootstrap/main.go', 'tools/gui-native-group-detail/extract-projection.py', 'tools/gui-native-family/ui/fixture-export_test.go.txt', 'tools/gui-native-group/fixture-export_test.go.txt', 'tools/gui-native-group/family-html-baseline.json', 'tools/gui-native-family/ui/color-stability.mjs', 'services/server/internal/web/family_projection_test.go', 'services/server/internal/web/family_benchmark_test.go']


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
         and Path(__file__).absolute() == repo / 'tools/gui-native-group-detail/extract-projection.py', 'Exact clean committed method and owned task scratch required')

    def bridge():
        raw = subprocess.check_output(['python3', '-I', str(repo / 'tools/gui-native-group-detail/prepare-ui-overlay.py'), '--repo', str(repo), '--producer-source', args.source, '--check-bridge'])
        proof = json.loads(raw)
        need(proof['producer_source'] == args.source and proof['product_paths'] == SOURCE_NAMES,
             'Exact current committed product/capture and UI-only descendant bridge required')
        return proof

    bridge_before = bridge()
    def fence():
        need(bridge() == bridge_before, 'Actual capture/producer/UI source bridge changed')
        return [{'path': name, 'bytes': len(raw), 'sha256': digest(raw)} for name in SOURCE_NAMES for raw in [read(repo / name)]]

    before = fence()
    subprocess.run(['git', '-C', str(repo), 'ls-files', '--error-unmatch', '--', *SOURCE_NAMES], check=True, stdout=subprocess.DEVNULL)
    result_raw = read(run / 'result.json')
    result = json.loads(result_raw)
    need(result.get('source') == args.source and result.get('method_pass') is True and result.get('errors') == []
         and set(result.get('lanes', {})) == {'web', 'app', 'accounts', 'recommendations', 'social', 'messaging'}
         and all(v.get('state') == 'PASS' and v['tests']['fail'] == 0 and v['tests']['skip'] == 0 for v in result['lanes'].values()), 'Complete successful six-lane result required; no filtered or skipped substitute')
    method_rows = result.get('method_inputs', [])
    need(len(method_rows) == 5 and len({row['path'] for row in method_rows}) == 5
         and all(row['path'] in SOURCE_NAMES and len(read(repo / row['path'])) == row['bytes']
                 and digest(read(repo / row['path'])) == row['sha256'] for row in method_rows)
         and any(row['path'] == 'tools/gui-native-family/qualify.py' and row['sha256'] == result['method_sha256'] for row in method_rows),
         'Actual current qualified method/source frame required; no historical hardcoded method hash')
    need(result['cleanup']['db']['id_absent'] is True and result['cleanup']['db']['name_absent'] is True
         and result['pg_cleanup']['stop_exit'] == 0 and result['pg_cleanup']['status_exit'] == 3, 'Recorded successful fixture shutdown required before extraction/benchmark')
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
    # Concatenate chunked output first: test2json may split a long t.Log line.
    text = ''.join(e.get('Output', '') for e in events if e.get('Action') == 'output' and e.get('Test') == ROOT_TEST)
    captures = re.findall(re.escape(MARKER) + r'([^\r\n]+)', text)
    need(len(captures) == 1, 'Exactly one bounded actual semantic marker required')
    projection = captures[0].encode()
    need(len(projection) <= 512 << 10, 'Bounded actual synthetic projection required')
    value = json.loads(projection)
    need(set(value) == {'schema', 'actor', 'variants', 'render_environment'} and value['schema'] == 1
         and value['render_environment'] == 'EXCLUDED; later QA supplies identical fixed synthetic CSRF/nonce to both body renderers'
         and isinstance(value['variants'], list) and [v.get('language') for v in value['variants']] == ['en', 'ro'],
         'Exact synthetic source projection schema/languages required')
    for variant in value['variants']:
        need(set(variant) == {'language', 'path', 'projection'} and re.fullmatch(r'/groups/[1-9][0-9]*/', variant['path']), 'Finite actual group path required')
        data = variant['projection']
        need(isinstance(data, dict) and all(data.get(key) is True for key in ('is_owner', 'is_member', 'show_thread', 'can_post', 'can_announce'))
             and data.get('can_ask') is False and data['group']['cohort'] == 'adult' and data['group']['title'] == 'Generated capture standing group'
             and len(data['roster']) == 2 and len(data['posts']) == 1 and len(data['posts'][0]['replies']['all']) == 1
             and len(data['announcements']) == 1 and data['feed'] == [] and isinstance(data['post_form']['body']['widget'], dict),
             'Only the actual adult owner/thread/roster synthetic snapshot is admitted')
        def privacy(node):
            if isinstance(node, dict):
                for key, child in node.items():
                    lower = key.lower()
                    need(lower not in ('request', 'authorization', 'token') and not any(word in lower for word in ('password', 'cookie', 'session', 'csrf', 'nonce')), 'Credential/render-environment field forbidden')
                    privacy(child)
            elif isinstance(node, list):
                for child in node:privacy(child)
            elif isinstance(node, str):
                need('/api/media/' not in node, 'Signed media URL forbidden in capture')
            else:need(node is None or type(node) in (bool, int, float), 'Finite JSON semantic values required')
        privacy(data)
    actor = value['actor']
    need(set(actor) == {'id', 'public_id', 'username', 'display_name', 'age_band', 'cohort', 'role', 'identity_verified', 'is_active', 'is_staff', 'is_superuser'}
         and type(actor['id']) is int and actor['id'] > 0 and actor['username'] == 'generated-native-group-owner'
         and actor['age_band'] == actor['cohort'] == 'adult' and actor['role'] == 'user'
         and actor['is_active'] is True and actor['identity_verified'] is True and actor['is_staff'] is False and actor['is_superuser'] is False,
         'Only the finite synthetic adult/nonstaff fixture may be captured')
    need(fence() == before and read(run / 'result.json') == result_raw and read(run / 'source-manifest.json') == manifest_raw
         and read(run / 'proof/app-test2json.stdout', 64 << 20) == stream, 'Capture/source fence changed')
    artifact = {'schema': 1, 'source_sha': args.source, 'ui_method_source': bridge_before['ui_head'], 'producer_root': ROOT_TEST, 'result_sha256': digest(result_raw),
                'qualifier_method_sha256': result['method_sha256'], 'source_manifest_sha256': digest(manifest_raw), 'raw_event_sha256': digest(stream), 'source_rows': before,
                'projection_base64': base64.b64encode(projection).decode(), 'projection_sha256': digest(projection),
                'scope': 'Synthetic real-cookie/current-Actor registered-view semantics and original widgets only. Render environment excluded; later fixed synthetic environment shared by both body renderers. No production HTML trust, staff/worst-case/full-HTTP timing or speedup claim.'}
    raw = json.dumps(artifact, indent=2).encode() + b'\n'
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as file:
        file.write(raw)
    print(json.dumps({'output': str(output), 'sha256': digest(raw), 'source': args.source, 'producer_root': ROOT_TEST}), flush=True)


if __name__ == '__main__':
    main()
