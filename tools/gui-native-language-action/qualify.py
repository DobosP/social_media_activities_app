#!/usr/bin/env python3
"""Disabled-until-reviewed standalone language interaction companion; no PG/identity input."""
import sys
sys.dont_write_bytecode = True
import argparse
import base64
from decimal import Decimal
import hashlib
import io
import json
import os
from pathlib import Path
import re
import selectors
import shutil
import signal
import stat
import subprocess
import tarfile
import tempfile
import time

HERE = Path(__file__).resolve().parent
REPO = Path('/home/dobo/work/_worktrees/social_media_activities_app/feat__gui-app-migration-a')
SCRATCH = Path('/home/dobo/work/_temp/la-ui')
ARCHIVE_SHA = 'e31b656ba310cb09639a6ab7e1a2a2dc8d305e17ae7a1f30ce3c8bc5ca3bee26'
MANIFEST_SHA = '6a0c5c50e3c8e77f46a36becc9617f2120476716350354b447716ee29089af8f'
STATES = ['overview-basic-en', 'overview-guardian-ro', 'overview-ward-connections-en', 'overview-escaped-ro', 'settings-no-token-en', 'settings-token-ro', 'settings-expired-record-en', 'settings-revoke-error-ro']
CODE = ['qualify.py', 'desktop.mjs', 'fixture_test.go.txt']
STAT = ['st_dev', 'st_ino', 'st_mode', 'st_uid', 'st_gid', 'st_size', 'st_mtime_ns', 'st_ctime_ns', 'st_nlink']
SEEN = {}

def need(value, message='Companion admission or evidence refused'):
    if not value: raise RuntimeError(message)

def digest(raw):
    return hashlib.sha256(raw).hexdigest()

def identity(value):
    return tuple(getattr(value, key) for key in STAT)

def regular(file, cap=4 << 20):
    file = Path(file)
    need(file.is_absolute() and file.resolve(strict=True) == file, 'Canonical regular input required')
    before = file.lstat()
    need(stat.S_ISREG(before.st_mode) and before.st_nlink == 1 and before.st_size <= cap, 'Bounded unaliased regular input required')
    with os.fdopen(os.open(file, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as stream:
        need(identity(os.fstat(stream.fileno())) == identity(before))
        raw = stream.read(cap + 1)
        need(len(raw) == before.st_size and identity(os.fstat(stream.fileno())) == identity(before))
    need(identity(file.lstat()) == identity(before))
    if file in SEEN: need(SEEN[file] == identity(before), 'Input changed since earliest read')
    SEEN[file] = identity(before)
    return raw

def file_hash(file):
    file = Path(file)
    need(file.is_absolute() and file.resolve(strict=True) == file)
    before = file.lstat(); need(stat.S_ISREG(before.st_mode) and before.st_nlink == 1)
    hashed = hashlib.sha256(); count = 0
    with os.fdopen(os.open(file, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as stream:
        need(identity(os.fstat(stream.fileno())) == identity(before))
        while True:
            raw = stream.read(1 << 20)
            if not raw: break
            count += len(raw); need(count <= before.st_size); hashed.update(raw)
        need(identity(os.fstat(stream.fileno())) == identity(before))
    need(count == before.st_size and identity(file.lstat()) == identity(before))
    if file in SEEN: need(SEEN[file] == identity(before))
    SEEN[file] = identity(before)
    return hashed.hexdigest()

def review(spec):
    path = Path(spec['path'])
    roots = [HERE, SCRATCH / 'admission', Path('/home/dobo/work/_temp/feat__gui-app-migration-a/social-family'), Path('/home/dobo/work/_temp/gui-functional-hub/language-companion-proposal')]
    need(any(path.is_relative_to(root) for root in roots) and path.suffix == '.json', 'Finite review scope required')
    raw = regular(path); need(digest(raw) == spec['sha256'])
    value = json.loads(raw)
    need(spec['expected'].startswith('ACCEPT') and value.get(spec['field']) == spec['expected'] and value.get('findings') == [])
    return value

def bridge(binding):
    driver = REPO / 'tools/gui-native-account-center/qualify.py'
    need(digest(regular(driver)) == '41eff00431eeae4212eb83f11434ac080c6fe5e3d2e45c2de0dac901d3b1f97f')
    raw = subprocess.check_output([sys.executable, '-I', str(driver), '--repo', str(REPO), '--producer-source', binding['language_product_source'], '--check-bridge'], timeout=120)
    return json.loads(raw)

def compare_bodies(binding):
    need(binding['account_keeper']['archive_sha256'] == ARCHIVE_SHA and binding['account_keeper']['manifest_sha256'] == MANIFEST_SHA)
    for key, expected in [('keeper_review', '279909705896aa2ca5acbf51cc056df5b47288fa7b95ec0be4c2513134b7549a'), ('feature_review', '2c3806753662f6a483117446b0d0d3fc9ec2f69f4bc11c0f27341ae50f01806a')]:
        spec = binding['account_keeper'][key]; need(spec['sha256'] == expected)
        accepted_review = review(spec); need(accepted_review.get('archive_sha256', accepted_review.get('keeper_archive_sha256')) == ARCHIVE_SHA)
    run = Path(binding['account_run']['path'])
    need(run.is_absolute() and run.resolve(strict=True) == run and run.is_relative_to(Path('/home/dobo/work/_temp/ac-ui')))
    result_raw = regular(run / 'result.json')
    need(digest(result_raw) == binding['account_run']['result_sha256'])
    result = json.loads(result_raw)
    need(result['status'] == 'PASS' and result['PG_producer_source'] == binding['language_product_source'])
    current_raw = regular(run / 'fixtures/fixtures.json')
    need(digest(current_raw) == result['fixture_manifest_sha256'] == binding['account_run']['fixtures_sha256'])
    current = json.loads(current_raw)
    need(current['PG_producer_source'] == binding['language_product_source'] and current['source_unchanged'] is True and current['sources_before'] == current['sources_after'])
    need([r['name'] for r in current['states']] == STATES)
    norm = current['normalization']
    need(norm['schema'] == 1 and norm['kind'] == 'render-nonce-only' and norm['replacement'] == 'synthetic-account-center-ui-nonce')
    need([r['name'] for r in norm['states']] == STATES)
    need(all(r['attribute_replacements'] == 2 and r['csp_replacements'] == 1 for r in norm['states']))
    prior_raw = regular(run / 'prior-body-equality.json')
    need(digest(prior_raw) == binding['account_run']['prior37_sha256'])
    prior = json.loads(prior_raw)
    need(prior == result['previous_body_equality'] and prior['count'] == len(prior['rows']) == 37 and prior['literal_byte_equal'] is True)
    need(prior['archive_sha256'] == 'bc10a80ae1a9b0f404e9434f2a0065d941a0cb4a336d5ea06eb39032da09c7ea' and prior['archive_manifest_sha256'] == '2787ff123c6b333d5aeed9650c22cdab92b8e2a76f189085a4eb52c5f3bc21d4')
    need(len({(r['kind'], r['name']) for r in prior['rows']}) == 37)
    for row in prior['rows']:
        path = Path(row['path']); need(not path.is_absolute() and '..' not in path.parts and row['equal'] is True)
        raw = regular(run / 'fixtures' / path)
        need(len(raw) == row['bytes'] and digest(raw) == row['sha256'])
    archive_path = REPO / binding['account_keeper']['archive']
    manifest_path = REPO / binding['account_keeper']['manifest']
    need(archive_path.is_relative_to(REPO / 'docs/reviews') and manifest_path.is_relative_to(archive_path.parent))
    archive_raw = regular(archive_path, 64 << 20); manifest_raw = regular(manifest_path)
    need(digest(archive_raw) == ARCHIVE_SHA and digest(manifest_raw) == MANIFEST_SHA)
    manifest = json.loads(manifest_raw)
    need(manifest['archive_sha256'] == ARCHIVE_SHA and manifest['complete_bytes_mode_UID_GID_nsmtime_roundtrip'] is True and len(manifest['members']) == 2587)
    wanted = {'evidence/UI-final/fixtures/' + name + '.html' for name in STATES} | {'evidence/UI-final/fixtures/fixtures.json'}
    found = {}; total = 0; seen = set()
    with tarfile.open(fileobj=io.BytesIO(archive_raw), mode='r:gz') as archive:
        members = archive.getmembers(); need([m.name for m in members] == [r['member'] for r in manifest['members']])
        for member, row in zip(members, manifest['members']):
            name = Path(member.name)
            need(member.isfile() and not member.issym() and not member.islnk() and not name.is_absolute() and '..' not in name.parts and member.name not in seen)
            seen.add(member.name); total += member.size
            need(member.size <= 64 << 20 and total <= 128 << 20)
            need(member.size == row['bytes'] and member.mode == row['mode'] and member.uid == row['uid'] and member.gid == row['gid'] and int(Decimal(member.pax_headers['mtime']) * 10**9) == row['mtime_ns'])
            raw = archive.extractfile(member).read(member.size + 1)
            need(len(raw) == member.size and digest(raw) == row['sha256'])
            if member.name in wanted: found[member.name] = raw
    need(set(found) == wanted)
    accepted = json.loads(found['evidence/UI-final/fixtures/fixtures.json'])
    need([r['name'] for r in accepted['states']] == STATES and accepted['normalization']['replacement'] == norm['replacement'])
    compared = []
    for row, old in zip(current['states'], accepted['states']):
        raw = regular(run / 'fixtures' / (row['name'] + '.html'))
        member = 'evidence/UI-final/fixtures/' + row['name'] + '.html'
        need(raw == found[member] and len(raw) == row['bytes'] == old['bytes'] and digest(raw) == row['sha256'] == old['sha256'])
        compared.append({'name': row['name'], 'bytes': len(raw), 'sha256': digest(raw), 'baseline_member': member, 'literal_equal': True})
    return {'old37_proof_sha256': digest(prior_raw), 'old37_physical_hashes_rechecked': True, 'new8_literal_account_bodies': compared, 'total': 45, 'account_archive_sha256': ARCHIVE_SHA, 'account_archive_manifest_sha256': MANIFEST_SHA, 'all2587_original_PAX_and_bytes_rechecked': True, 'normalization': 'Existing nonce-only export; comparison adds no normalization or rendering change.'}

def command(argv, cwd, env, output, label, timeout):
    with (output / (label + '.stdout')).open('xb') as out, (output / (label + '.stderr')).open('xb') as err:
        child = subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL, stdout=out, stderr=err, start_new_session=True)
        timed_out = False
        try: code = child.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True; os.killpg(child.pid, signal.SIGTERM)
            try: code = child.wait(timeout=5)
            except subprocess.TimeoutExpired: os.killpg(child.pid, signal.SIGKILL); code = child.wait(timeout=5)
    return {'argv': argv, 'exit': code, 'timed_out': timed_out}

def frame_server_stdout(chunk, state, log):
    # No BufferedReader operation touches this pipe. Keep every consumed byte
    # in the raw log, with bounded cumulative bytes and per-line framing.
    state['bytes'] += len(chunk)
    log.write(chunk); log.flush()
    need(state['bytes'] <= 1 << 20, 'Server stdout exceeded bounded framing')
    state['pending'].extend(chunk)
    while b'\n' in state['pending']:
        line, _, rest = state['pending'].partition(b'\n')
        state['pending'] = bytearray(rest)
        need(len(line) <= 8192, 'Server stdout line exceeded bound')
        if line.startswith(b'{'):
            value = json.loads(line)
            need(value.get('kind') in ['LANGUAGE_COMPANION_READY', 'LANGUAGE_COMPANION_STOPPED'], 'Unexpected server record')
            state['records'].append(value)
    need(len(state['pending']) <= 8192, 'Server stdout incomplete line exceeded bound')

def main():
    parser = argparse.ArgumentParser(description=__doc__); parser.add_argument('--binding', required=True, type=Path)
    parser.add_argument('--binding-sha256', required=True)
    args = parser.parse_args()
    need(args.binding == SCRATCH / 'admission/binding.json')
    need(args.binding.parent.resolve(strict=True) == args.binding.parent and args.binding.parent.is_dir() and args.binding.parent.stat().st_uid == os.getuid(), 'Canonical owned admission parent required')
    need(re.fullmatch('[0-9a-f]{64}', args.binding_sha256))
    binding_raw = regular(args.binding); need(digest(binding_raw) == args.binding_sha256, 'Explicit CLI binding digest differs'); binding = json.loads(binding_raw)
    # This is intentionally the first gate after the single bounded binding read.
    need(binding.get('status') == 'BOUND_REVIEWED_LANGUAGE_COMPANION', 'Pending language companion refuses before traversal or effects')
    need(HERE == REPO / 'tools/gui-native-language-action')
    need(all(re.fullmatch('[0-9a-f]{40}', binding.get(k) or '') for k in ['account_closing_source', 'language_product_source']))
    need(set(binding['method_sha256']) == set(CODE))
    for name in CODE: need(digest(regular(HERE / name)) == binding['method_sha256'][name])
    admitted = review(binding['source_review']); payload = dict(binding); payload['source_review'] = None
    need(admitted['binding_payload_sha256'] == digest((json.dumps(payload, sort_keys=True, separators=(',', ':'), ensure_ascii=False) + '\n').encode()) and admitted['method_sha256'] == binding['method_sha256'])
    for spec in [binding['complete7_review'], binding['account_operational_review']]:
        value = review(spec); need(value[spec['source_field']] == binding['language_product_source'])
    dependencies = binding['dependencies']; need(set(dependencies) == {'go', 'node', 'chrome', 'axe_file', 'playwright_package'})
    for name, row in dependencies.items():
        file = Path(row['path']) / 'package.json' if name == 'playwright_package' else Path(row['path'])
        need(file_hash(file) == row['sha256'])
        if name in ['go', 'node', 'chrome']: need(os.access(file, os.X_OK))
    need(SCRATCH.is_dir() and SCRATCH.resolve() == SCRATCH and SCRATCH.stat().st_uid == os.getuid())
    for name in ['cache', 'modcache']:
        folder = Path(binding[name]); need(folder.is_dir() and folder.is_absolute() and folder.resolve() == folder and '_temp' in folder.parts)
    need(shutil.disk_usage(SCRATCH).free > 1 << 30, 'One GiB free scratch required')
    before = bridge(binding)
    subprocess.run(['git', '-C', str(REPO), 'merge-base', '--is-ancestor', binding['account_closing_source'], binding['language_product_source']], check=True, timeout=30)
    need(subprocess.check_output(['git', '-C', str(REPO), 'status', '--porcelain']) == b'')
    bodies = compare_bodies(binding)
    site = regular(REPO / 'static/js/site.js'); axe = regular(Path(dependencies['axe_file']['path']))
    need(digest(site) == binding['site_js_sha256'] and digest(axe) == dependencies['axe_file']['sha256'])
    # Only after all bindings/reviews/dependencies/45body admission are proved.
    output = Path(tempfile.mkdtemp(prefix='run-', dir=SCRATCH)); home = Path(tempfile.mkdtemp(prefix='lh-', dir=SCRATCH))
    env = {'PATH': os.defpath, 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8', 'HOME': str(home), 'TMPDIR': str(home), 'GOCACHE': binding['cache'], 'GOMODCACHE': binding['modcache'], 'GOTOOLCHAIN': 'local', 'GOENV': 'off', 'GOWORK': 'off', 'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOFLAGS': '-p=2', 'GOMAXPROCS': '2'}
    report = {'schema': 1, 'status': 'RUNNING', 'product': binding['language_product_source'], 'scope': 'Standalone real registered language POST with fixed synthetic GET pages, separate45body preservation; no assembled authentication, benchmark, legal or production acceptance.', 'commands': [], 'bodies': bodies, 'owned': {'server_stopped': False, 'browser_closed': False, 'HOME_removed': False}, 'failures': []}
    server = None; log = None; selector = None; browser_ok = False
    framing = {'bytes': 0, 'pending': bytearray(), 'records': [], 'EOF': False}
    try:
        overlay = output / 'overlay.json'; target = REPO / 'services/server/internal/web/language_browser_companion_test.go'
        need(not target.exists())
        overlay.write_text(json.dumps({'Replace': {str(target): str(HERE / 'fixture_test.go.txt')}}) + '\n')
        binary = output / 'language-browser.test'
        result = command([dependencies['go']['path'], 'test', '-mod=readonly', '-overlay', str(overlay), '-c', '-o', str(binary), './internal/web'], REPO / 'services/server', env, output, 'compile', 300)
        report['commands'].append(result); need(result['exit'] == 0 and not result['timed_out'], 'Companion overlay compilation failed')
        server_err = (output / 'server.stderr').open('xb'); log = (output / 'server.stdout').open('xb')
        argv = [str(binary), '-test.run', '^TestNativeLanguageBrowserCompanion$', '-test.v', '-language-companion-product', binding['language_product_source'], '-language-companion-site-sha', digest(site), '-language-companion-axe-sha', digest(axe)]
        server = subprocess.Popen(argv, cwd=REPO / 'services/server', env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=server_err, start_new_session=True)
        report['server_process'] = {'argv': argv, 'cwd': str(REPO / 'services/server'), 'pid': server.pid, 'owned_process_group': server.pid, 'signals': [], 'waited_exit': None, 'wait_timeout': False, 'readiness_deadline_seconds': 30, 'stdout_cap_bytes': 1 << 20, 'stdout_line_cap_bytes': 8192, 'stdout_framing': 'bounded os.read on pipe descriptor; BufferedReader never read', 'stdin': 'memory-only fixed synthetic script buffers; existing blocking buffered write unchanged'}
        selector = selectors.DefaultSelector(); selector.register(server.stdout, selectors.EVENT_READ)
        data = {'product': binding['language_product_source'], 'site_js': base64.b64encode(site).decode(), 'axe': base64.b64encode(axe).decode()}
        server.stdin.write((json.dumps(data, separators=(',', ':')) + '\n').encode()); server.stdin.close()
        ready = None; deadline = time.monotonic() + 30
        while time.monotonic() < deadline and ready is None:
            if not selector.select(timeout=1): continue
            chunk = os.read(server.stdout.fileno(), min(65536, (1 << 20) - framing['bytes'] + 1))
            if not chunk: framing['EOF'] = True; break
            frame_server_stdout(chunk, framing, log)
            rows = [r for r in framing['records'] if r['kind'] == 'LANGUAGE_COMPANION_READY']
            need(len(rows) <= 1, 'Duplicate readiness record')
            if rows: ready = rows[0]
        need(server.poll() is None, 'Companion server exited before browser admission')
        need(ready and ready['product'] == binding['language_product_source'] and re.fullmatch(r'http://127\.0\.0\.1:[1-9][0-9]{0,4}', ready['origin']), 'Bounded loopback readiness required')
        report['server_process']['ready'] = ready
        runtime = {'binding_sha256': digest(binding_raw), 'product': binding['language_product_source'], 'origin': ready['origin'], 'output': str(output)}
        runtime_path = output / 'runtime-input.json'; runtime_path.write_text(json.dumps(runtime) + '\n')
        result = command([dependencies['node']['path'], str(HERE / 'desktop.mjs'), '--binding', str(args.binding), '--binding-sha256', args.binding_sha256, '--runtime-input', str(runtime_path)], REPO, env, output, 'browser', 150)
        report['commands'].append(result)
        desktop = json.loads(regular(output / 'desktop-result.json'))
        browser_ok = desktop['ownership']['contextsClosed'] and desktop['ownership']['profilesRemoved']
        report['owned']['browser_closed'] = browser_ok
        need(result['exit'] == 0 and not result['timed_out'] and browser_ok and desktop['status'] == 'PASS_STANDALONE_LANGUAGE_INTERACTION_ONLY' and desktop['failures'] == [], 'Standalone browser evidence failed')
        report['status'] = 'PASS_STANDALONE_LANGUAGE_ACTION_AND_45BODY_PRESERVATION'
    except Exception:
        report['status'] = 'FAIL'; report['failures'].append('Companion admission, execution or evidence failed')
    finally:
        if server is not None:
            if server.poll() is None:
                os.killpg(server.pid, signal.SIGTERM); report['server_process']['signals'].append('SIGTERM')
            try: code = server.wait(timeout=7)
            except subprocess.TimeoutExpired:
                report['server_process']['wait_timeout'] = True
                os.killpg(server.pid, signal.SIGKILL); report['server_process']['signals'].append('SIGKILL'); code = server.wait(timeout=5)
            report['server_process']['waited_exit'] = code
            # The child is waited. Drain the same descriptor to EOF under a
            # deadline, never mixing selector readiness with prefetched bytes.
            drain_deadline = time.monotonic() + 5
            while not framing['EOF'] and framing['bytes'] <= 1 << 20 and time.monotonic() < drain_deadline:
                if not selector.select(timeout=1): continue
                chunk = os.read(server.stdout.fileno(), min(65536, (1 << 20) - framing['bytes'] + 1))
                if not chunk: framing['EOF'] = True; break
                frame_server_stdout(chunk, framing, log)
            log.close(); server_err.close()
            if selector is not None: selector.close()
            server.stdout.close()
            stopped = [r for r in framing['records'] if r['kind'] == 'LANGUAGE_COMPANION_STOPPED']
            report['owned']['server_stopped'] = code == 0 and framing['EOF'] and not framing['pending'] and len(stopped) == 1 and stopped[0]['server_stopped'] is True and stopped[0]['unexpected'] == stopped[0]['internal'] == 0 and stopped[0]['posts'] == 10 and stopped[0]['pages'] == 11
            report['server_process'].update({'stdout_bytes': framing['bytes'], 'stdout_EOF': framing['EOF'], 'stdout_partial_line_bytes': len(framing['pending']), 'stdout_raw_path': 'server.stdout', 'stderr_raw_path': 'server.stderr', 'all_consumed_stdout_retained': (output / 'server.stdout').stat().st_size == framing['bytes'], 'stdout_sha256': digest(regular(output / 'server.stdout')), 'stderr_sha256': digest(regular(output / 'server.stderr'))})
            report['server_exit'] = code; report['server_summary'] = stopped
        try:
            need(bridge(binding) == before and regular(args.binding) == binding_raw, 'Source bridge changed')
            for file, original in SEEN.items(): need(identity(file.lstat()) == original, 'Earliest input identity changed')
            if report['owned']['server_stopped'] and browser_ok: shutil.rmtree(home); report['owned']['HOME_removed'] = not home.exists()
            need(all(report['owned'].values()), 'Owned process/profile/HOME cleanup unproven')
        except Exception:
            report['status'] = 'FAIL'; report['failures'].append('Final source or lifecycle preservation failed')
        (output / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({'status': report['status'], 'output': str(output)}))
    return 0 if report['status'].startswith('PASS_') else 1

if __name__ == '__main__':
    try: sys.exit(main())
    except Exception: print('REFUSED: language companion inputs are not admitted', file=sys.stderr); sys.exit(1)
