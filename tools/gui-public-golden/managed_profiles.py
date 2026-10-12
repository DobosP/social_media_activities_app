#!/usr/bin/env python3
"""Committed default17/preference28/currentG1-45 managed profiles; reserve a runtime slot first."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import time
import tarfile
import uuid
from urllib.parse import quote

OWNING_ROOT = Path(__file__).resolve().parents[2]
ROOT = None
SCRATCH = None
CACHE_ROOT = Path('/home/dobo/work/_temp/feat__gui-app-migration-a/social_media_activities_app')
SOURCE = None
DB_BUILD_SOURCE = 'bac5d718360504330d5f5cdbf2f75cf5a0ad0799'
DB_BINDING_APPLICATION_SOURCE = '813f197bf0f4d190e3e2ea0a394ddd73121c6bc6'
IMAGE = 'sha256:2e040a5165d79be63755424f707fdb8cf609ace831ada7a9baca398ac25e12b9'
LEDGER = None
LEDGER_SHA256 = None
INVENTORY = None
INVENTORY_SHA256 = None
CASES = None
CASES_SHA256 = None
HISTORICAL_INPUTS_SHA256 = 'e92e1d44feee05d052bc733f68db102d45b76d9fbcd927453bdace7392757ac3'
DB_DOCKERFILE_SHA256 = 'a22bc01a8602fe8a2f54b647ced3162ed0a4b9ace4702be659b2b7f380136d75'
PROFILE_ORDER = ('default', 'preferences', 'current_g1')
G1_CASES = None
G1_CASES_SHA256 = '35b30b7a2520750525fd2ee380d2f5597aa6fe6b374c92d5cd9a6db030abbce3'
BINARY = Path('/home/dobo/work/_temp/feat__gui-app-migration-a/social_media_activities_app/evidence/normalizer-r7-764beb1/social-gui-normalize')
ARCHIVE = Path('/home/dobo/gates/kits/core-v1.6/web-kit-go-core-v1.6.tgz')
ORIGINAL = Path('/home/dobo/work/_temp/feat__gui-app-migration-a/social_media_activities_app/capture-75dc717')
PG_SOURCE_BINDINGS = [{'path': 'Dockerfile.db', 'bytes': 606, 'sha256': 'a22bc01a8602fe8a2f54b647ced3162ed0a4b9ace4702be659b2b7f380136d75', 'full_mode': 33204}, {'path': '.github/workflows/native.yml', 'bytes': 4860, 'sha256': '3a51de6db5b7885bbd6237e2426164979db31511c46ff4cdd58ff85db4fdf267', 'full_mode': 33204}, {'path': 'scripts/qualify-native.sh', 'bytes': 4307, 'sha256': 'c5ea3b35b48c50d1caf9ab57dbae5260e8e23acbe6c89d20079687568cde13b1', 'full_mode': 33277}, {'path': 'services/server/internal/schema/baseline.sql', 'bytes': 233549, 'sha256': '6a5f1ca89c5117e6e5c331b4097a06456ce25da2d3d26a3b3a950cb89df1ef7a', 'full_mode': 33204}, {'path': 'services/server/internal/schema/budgets.sql', 'bytes': 13134, 'sha256': '5d2b6007a647d1aeede91bcf49508a95e9952cff13d8803877636c8b395ca785', 'full_mode': 33204}, {'path': 'services/server/internal/schema/migrate.go', 'bytes': 2661, 'sha256': 'f5f109580096f6cb9c61883b77fd0fa74fd0f08c2adf1babfdb5b378158e90cc', 'full_mode': 33204}]
# The exact existing helper is invoked for strict current OS/FS comparison; never rebuilt.
RAW_LEDGER = None
RAW_LEDGER_SHA256 = None
RAW_CORPUS = Path('/home/dobo/work/_temp/feat__gui-app-migration-a/social_media_activities_app/evidence/g1-get-walk-a115deb-cbd00d1a-927c-4a5c-a27c-00838941dfdd/proof/captured')
RAW_SOURCE = 'a115deb5d706745a22d5e1049b0b938cae10cfad'
RAW_MANIFEST_SHA256 = '591c6b70edc83620ead9ca5ba18ecf4c39d3171e5365276961b96b184b729fb7'
DB_IMAGE = 'sha256:f66de888683449864f820c365551125bb2627782be15a264f9746d53573b39d5'
DB_BASE_IMAGE = 'sha256:e8538db784b854a9baac5c1be6d059dcf0db257af2d94c8c5807b5fc15f11222'
NORMALIZER_SHA256 = '598515e75c709803c7b23673e07b14baa93861608346a81c8fc708521c1b2f05'
ACTUAL_PARENT_BINDING_SHA256 = '3a46d3534afcac71f24e92e7342c9bf50dceaab37176997a7fd7c692ca9ae7ea'
PACKAGE = 'github.com/DobosP/social_media_activities_app/services/server/internal/web'

INSPECT = ('{"id":{{json .Id}},"name":{{json .Name}},"image":{{json .Image}},'
           '"running":{{json .State.Running}},"exit_code":{{json .State.ExitCode}},'
           '"oom_killed":{{json .State.OOMKilled}},"user":{{json .Config.User}},'
           '"cpus":{{json .HostConfig.NanoCpus}},"memory":{{json .HostConfig.Memory}},'
           '"network":{{json .HostConfig.NetworkMode}},"mounts":{{json .Mounts}},'
           '"tmpfs":{{json (index .HostConfig "Tmpfs")}},"groups":{{json .HostConfig.GroupAdd}},'
           '"ports":{{json .HostConfig.PortBindings}},"cap_drop":{{json .HostConfig.CapDrop}},'
           '"entrypoint":{{json .Config.Entrypoint}},"cmd":{{json .Config.Cmd}},"env":{{json .Config.Env}},'
           '"pids":{{json .HostConfig.PidsLimit}},"privileged":{{json .HostConfig.Privileged}},'
           '"readonly":{{json .HostConfig.ReadonlyRootfs}},"security":{{json .HostConfig.SecurityOpt}}}')


def need(condition, reason):
    if not condition:
        raise RuntimeError(reason)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def regular(path, limit=64 << 20):
    need(path.is_absolute() and path.resolve() == path, 'Canonical unaliased file required')
    before = path.lstat()
    need(stat.S_ISREG(before.st_mode) and before.st_size <= limit, 'Bounded regular file required')
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        opened = os.fstat(fd)
        with os.fdopen(fd, 'rb', closefd=False) as stream:
            raw = stream.read(limit + 1)
        after = os.fstat(fd)
        need((before.st_dev, before.st_ino) == (opened.st_dev, opened.st_ino)
             and len(raw) == after.st_size <= limit
             and all(getattr(opened, k) == getattr(after, k) for k in
                     ['st_dev', 'st_ino', 'st_size', 'st_mode', 'st_mtime_ns', 'st_ctime_ns']),
             'Input changed while reading')
    finally:
        os.close(fd)
    return raw, {'bytes': len(raw), 'sha256': digest(raw), 'full_mode': after.st_mode}


def owned_directory(path):
    info = path.lstat()
    need(path.is_absolute() and path.resolve() == path and stat.S_ISDIR(info.st_mode)
         and info.st_uid == os.getuid() and stat.S_IMODE(info.st_mode) == 0o700,
         'Existing private task directory required')


def source_snapshot(expected):
    head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=OWNING_ROOT, text=True).strip()
    need(head == SOURCE and not subprocess.check_output(['git', 'status', '--porcelain'], cwd=OWNING_ROOT),
         'Exact clean source required')
    rows = []
    for item in expected:
        name = item['path']
        need(not name.startswith('/') and all(p not in ['', '.', '..'] for p in name.split('/')),
             'Literal repository source path required')
        _, actual = regular(ROOT / name)
        need(actual['bytes'] == item['bytes'] and actual['sha256'] == item['sha256']
             and actual['full_mode'] & 0o170111 == item['full_mode'] & 0o170111, 'Source binding differs: ' + name)
        rows.append({'path': name, **actual})
    return rows


def binding_snapshot(expected):
    # Finite existing normalizer/archive/build-info/original15 inputs, all bound
    # by the closed source ledger. Nothing is installed, copied or rebuilt.
    owned_directory(ORIGINAL)
    rows = []
    for item in expected:
        path = Path(item['path'])
        _, actual = regular(path)
        need(actual == {k: item[k] for k in ['bytes', 'sha256', 'full_mode']},
             'Existing normalizer/original binding differs: ' + str(path))
        rows.append({'path': str(path), **actual})
    return rows


def metadata_snapshot(expected):
    rows = []
    for item in expected:
        path = Path(item['path'])
        if not path.is_absolute():
            path = ROOT / path
        _, actual = regular(path)
        need(actual['bytes'] == item['bytes'] and actual['sha256'] == item['sha256']
             and actual['full_mode'] & 0o170111 == item['full_mode'] & 0o170111, 'Readonly policy binding differs')
        rows.append({'path': str(path), **actual})
    return rows


DB_ENTRY = r'''umask 077
pg=/usr/lib/postgresql/16/bin
data=/var/lib/postgresql/data
started=0
pid=0
cleanup() {
  status=$?
  trap - EXIT INT TERM
  set +e
  stop_rc=99; state_rc=99; pid_rc=99; files_absent=0
  if (( started )); then
    if [[ -f "$data/postmaster.pid" ]]; then read -r pid <"$data/postmaster.pid"; fi
    "$pg/pg_ctl" -D "$data" -m fast -w -t 20 stop >>/tmp/postgres.log 2>&1
    stop_rc=$?
    "$pg/pg_ctl" -D "$data" status >>/tmp/postgres.log 2>&1
    state_rc=$?
    if [[ $pid =~ ^[1-9][0-9]*$ ]]; then kill -0 "$pid" 2>/dev/null; pid_rc=$?; fi
    if [[ ! -e "$data/postmaster.pid" && ! -e /socket/.s.PGSQL.55434 && ! -e /socket/.s.PGSQL.55434.lock ]]; then files_absent=1; fi
  fi
  printf '{"stop_exit":%s,"status_exit":%s,"owned_pid":%s,"pid_probe_exit":%s,"pid_and_socket_files_absent":%s}\n' "$stop_rc" "$state_rc" "$pid" "$pid_rc" "$files_absent" >/socket/pg-cleanup.json
  chmod 0644 /socket/pg-cleanup.json
  tail -c 1048576 /tmp/postgres.log 2>/dev/null
  if (( stop_rc != 0 || state_rc != 3 || pid_rc != 1 || files_absent != 1 )); then status=1; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
"$pg/initdb" -D "$data" --username=app --encoding=UTF8 --no-locale --auth-local=trust --auth-host=reject
init_rc=$?
if (( init_rc != 0 )); then exit "$init_rc"; fi
cat >>"$data/postgresql.conf" <<'PG_CONF'
listen_addresses = ''
unix_socket_directories = '/socket'
unix_socket_permissions = 0777
port = 55434
PG_CONF
started=1
"$pg/pg_ctl" -D "$data" -l /tmp/postgres.log -w -t 30 start
start_rc=$?
if (( start_rc != 0 )); then exit "$start_rc"; fi
read -r pid <"$data/postmaster.pid"
[[ $pid =~ ^[1-9][0-9]*$ ]] || exit 1
[[ ! -e /socket/ready.pending.json && ! -L /socket/ready.pending.json && ! -e /socket/ready.json && ! -L /socket/ready.json ]] || exit 1
printf '{"owned_pid":%s,"uid":%s,"gid":%s}\n' "$pid" "$(id -u)" "$(id -g)" >/socket/ready.pending.json || exit 1
chmod 0644 /socket/ready.pending.json || exit 1
mv -T -- /socket/ready.pending.json /socket/ready.json || exit 1
while [[ ! -f /socket/stop-request ]]; do sleep 0.1; done
'''

ENTRY = r'''umask 077
/usr/local/go/bin/gofmt -d internal/web/renderer.go internal/web/server.go internal/web/gui_public_get_walk_test.go internal/web/gui_preferences_get_transport_test.go >/proof/format.stdout 2>/proof/format.stderr
format_rc=$?
printf '%s\n' "$format_rc" >/proof/format.exit
if [ "$format_rc" -ne 0 ] || [ -s /proof/format.stdout ] || [ -s /proof/format.stderr ]; then exit 1; fi
/usr/local/go/bin/go test -mod=readonly -race -count=1 -json ./internal/web -run '^TestGUIPublicGetWalk(SourceRefusals)?$' >/proof/default-tests.stdout 2>/proof/default-tests.stderr
test_rc=$?
printf '%s\n' "$test_rc" >/proof/default-tests.exit
if [ "$test_rc" -ne 0 ]; then exit "$test_rc"; fi
pg=/usr/lib/postgresql/16/bin
"$pg/createdb" -h /pgsocket -p 55434 -U app social_gui_preferences >/proof/preferences-database-create.stdout 2>/proof/preferences-database-create.stderr
create_rc=$?
printf '%s\n' "$create_rc" >/proof/preferences-database-create.exit
if [ "$create_rc" -ne 0 ]; then exit "$create_rc"; fi
"$pg/psql" --no-psqlrc -h /pgsocket -p 55434 -U app --dbname=social_gui_preferences -v ON_ERROR_STOP=1 -f /work/services/server/internal/schema/baseline.sql >/proof/preferences-schema.stdout 2>/proof/preferences-schema.stderr
schema_rc=$?
printf '%s\n' "$schema_rc" >/proof/preferences-schema.exit
if [ "$schema_rc" -ne 0 ]; then exit "$schema_rc"; fi
"$pg/psql" --no-psqlrc -h /pgsocket -p 55434 -U app --dbname=social_gui_preferences -Atc "SELECT json_build_object('database',current_database(),'database_oid',(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),'server_version_num',current_setting('server_version_num')::integer,'listen_addresses',current_setting('listen_addresses'),'fixture_schemas',(SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'native_fixture_%'),'extensions',(SELECT json_object_agg(extname,extversion ORDER BY extname) FROM pg_extension WHERE extname IN ('postgis','fuzzystrmatch','postgis_tiger_geocoder','postgis_topology','pg_trgm','vector')))" >/proof/preferences-pg-binding.stdout 2>/proof/preferences-pg-binding.stderr
binding_rc=$?
printf '%s\n' "$binding_rc" >/proof/preferences-pg-binding.exit
if [ "$binding_rc" -ne 0 ]; then exit "$binding_rc"; fi
/usr/local/go/bin/go test -mod=readonly -race -count=1 -json ./internal/web -run '^TestGUIPreferencesRegisteredGETTransport$' -web-domain-test-dsn 'host=/pgsocket port=55434 user=app dbname=social_gui_preferences sslmode=disable' -gui-public-golden-reference /original -gui-preferences-get-transport=true -gui-public-normalizer-binary /bindings/social-gui-normalize -gui-public-normalizer-sha256 598515e75c709803c7b23673e07b14baa93861608346a81c8fc708521c1b2f05 -gui-public-normalizer-archive /bindings/web-kit-go-core-v1.6.tgz >/proof/preferences-tests.stdout 2>/proof/preferences-tests.stderr
test_rc=$?
printf '%s\n' "$test_rc" >/proof/preferences-tests.exit
"$pg/psql" --no-psqlrc -h /pgsocket -p 55434 -U app --dbname=social_gui_preferences -Atc "SELECT json_build_object('database',current_database(),'database_oid',(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),'server_version_num',current_setting('server_version_num')::integer,'listen_addresses',current_setting('listen_addresses'),'fixture_schemas',(SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'native_fixture_%'),'extensions',(SELECT json_object_agg(extname,extversion ORDER BY extname) FROM pg_extension WHERE extname IN ('postgis','fuzzystrmatch','postgis_tiger_geocoder','postgis_topology','pg_trgm','vector')))" >/proof/preferences-pg-after.stdout 2>/proof/preferences-pg-after.stderr
binding_rc=$?
printf '%s\n' "$binding_rc" >/proof/preferences-pg-after.exit
if [ "$test_rc" -ne 0 ]; then exit "$test_rc"; fi
if [ "$binding_rc" -ne 0 ]; then exit "$binding_rc"; fi
"$pg/createdb" -h /pgsocket -p 55434 -U app social_gui_current_g1 >/proof/current_g1-database-create.stdout 2>/proof/current_g1-database-create.stderr
create_rc=$?
printf '%s\n' "$create_rc" >/proof/current_g1-database-create.exit
if [ "$create_rc" -ne 0 ]; then exit "$create_rc"; fi
"$pg/psql" --no-psqlrc -h /pgsocket -p 55434 -U app --dbname=social_gui_current_g1 -v ON_ERROR_STOP=1 -f /work/services/server/internal/schema/baseline.sql >/proof/current_g1-schema.stdout 2>/proof/current_g1-schema.stderr
schema_rc=$?
printf '%s\n' "$schema_rc" >/proof/current_g1-schema.exit
if [ "$schema_rc" -ne 0 ]; then exit "$schema_rc"; fi
"$pg/psql" --no-psqlrc -h /pgsocket -p 55434 -U app --dbname=social_gui_current_g1 -Atc "SELECT json_build_object('database',current_database(),'database_oid',(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),'server_version_num',current_setting('server_version_num')::integer,'listen_addresses',current_setting('listen_addresses'),'fixture_schemas',(SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'native_fixture_%'),'extensions',(SELECT json_object_agg(extname,extversion ORDER BY extname) FROM pg_extension WHERE extname IN ('postgis','fuzzystrmatch','postgis_tiger_geocoder','postgis_topology','pg_trgm','vector')))" >/proof/current_g1-pg-binding.stdout 2>/proof/current_g1-pg-binding.stderr
binding_rc=$?
printf '%s\n' "$binding_rc" >/proof/current_g1-pg-binding.exit
if [ "$binding_rc" -ne 0 ]; then exit "$binding_rc"; fi
/usr/local/go/bin/go test -mod=readonly -race -count=1 -json ./internal/web -run '^TestGUIPublicGetWalk$' -web-domain-test-dsn 'host=/pgsocket port=55434 user=app dbname=social_gui_current_g1 sslmode=disable' -gui-public-golden-reference /original -gui-public-current-get-transport=true -gui-public-normalizer-binary /bindings/social-gui-normalize -gui-public-normalizer-sha256 598515e75c709803c7b23673e07b14baa93861608346a81c8fc708521c1b2f05 -gui-public-normalizer-archive /bindings/web-kit-go-core-v1.6.tgz >/proof/current_g1-tests.stdout 2>/proof/current_g1-tests.stderr
test_rc=$?
printf '%s\n' "$test_rc" >/proof/current_g1-tests.exit
"$pg/psql" --no-psqlrc -h /pgsocket -p 55434 -U app --dbname=social_gui_current_g1 -Atc "SELECT json_build_object('database',current_database(),'database_oid',(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),'server_version_num',current_setting('server_version_num')::integer,'listen_addresses',current_setting('listen_addresses'),'fixture_schemas',(SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'native_fixture_%'),'extensions',(SELECT json_object_agg(extname,extversion ORDER BY extname) FROM pg_extension WHERE extname IN ('postgis','fuzzystrmatch','postgis_tiger_geocoder','postgis_topology','pg_trgm','vector')))" >/proof/current_g1-pg-after.stdout 2>/proof/current_g1-pg-after.stderr
binding_rc=$?
printf '%s\n' "$binding_rc" >/proof/current_g1-pg-after.exit
if [ "$test_rc" -ne 0 ]; then exit "$test_rc"; fi
if [ "$binding_rc" -ne 0 ]; then exit "$binding_rc"; fi
exit 0
'''


def test_observation(raw):
    events = [json.loads(line) for line in raw.splitlines()]
    need(events and all(type(e) is dict and e.get('Package') == PACKAGE and type(e.get('Action')) is str for e in events), 'Actual focused web-package Go JSON frames required')
    runs = [e['Test'] for e in events if e.get('Action') == 'run' and 'Test' in e]
    passes = [e['Test'] for e in events if e.get('Action') == 'pass' and 'Test' in e]
    return events, {'run':len(runs),'pass':len(passes),'fail':sum(e.get('Action') == 'fail' and 'Test' in e for e in events),'skip':sum(e.get('Action') == 'skip' for e in events),'package_pass':sum(e.get('Action') == 'pass' and 'Test' not in e for e in events),'package_fail':sum(e.get('Action') == 'fail' and 'Test' not in e for e in events),'identities_run':runs,'identities_pass':passes,'JSON_events':len(events),'observation_only_not_PASS':True}


def parse_tests(raw, expected, profile):
    events, observed = test_observation(raw)
    runs, passes = observed['identities_run'], observed['identities_pass']
    count = profile['named_actions']
    need(all(('Test' not in e or type(e['Test']) is str and e['Test'] in expected) and ('Output' not in e or type(e['Output']) is str) for e in events), 'Exact per-profile event identity/stream shapes required')
    need(len(runs) == len(set(runs)) == len(passes) == count and set(runs) == set(passes) == set(expected)
         and {n for n in runs if '/' not in n} == set(profile['roots']), 'Complete per-profile actual identities once required')
    need(observed['package_pass'] == 1 and not any(e.get('Action') in ['fail','skip','cancel','cancelled'] or '(cached)' in str(e.get('Output','')) or 'DATA RACE' in str(e.get('Output','')) for e in events), 'Failure, skip, cached result or race refused')
    return {'run':count,'pass':count,'top_level':len(profile['roots']),'subcases':count-len(profile['roots']),'package_pass':1,'fail':0,'skip':0,'cached':False,'identities':runs,'JSON_events':len(events)}


def default_observation(raw):
    events, _ = test_observation(raw)
    expected = 'HTTP NOT REQUESTED: historical preimage/custody and exact current reviewed repair-row bytes verified; no old/current producer equivalence, PG, capture, normalization or registered-GET qualification'
    logs = [e['Output'] for e in events if e.get('Test') == 'TestGUIPublicGetWalk' and expected in e.get('Output','')]
    need(len(logs) == 1, 'Actual default-only historical custody/current repair scope log required')
    return {'HTTP_GETs':0,'fixture_DB':None,'historical_capture_bound_control':'TestGUIPublicGetWalkSourceRefusals/repair-capture-boundary','claim':'Compiled strict old-capture binder refusal in no-DSN default profile; root routes explicit capture through that binder before PG/output. No old capture/corpus or current-original equivalence.'}


def validate_pg_profile(raw, database):
    pg = json.loads(raw)
    need(pg['database'] == database and type(pg['database_oid']) is int and pg['database_oid'] > 0
         and pg['server_version_num'] // 10000 == 16 and pg['listen_addresses'] == '' and pg['fixture_schemas'] == 0
         and set(pg['extensions']) == {'postgis', 'fuzzystrmatch', 'postgis_tiger_geocoder', 'postgis_topology', 'pg_trgm', 'vector'}
         and all(isinstance(v, str) and v for v in pg['extensions'].values()), 'Actual separate fresh PG16 database/extensions/Unix-only/fixture-cleanup proof differs')
    return pg


def validate_db_binding(raw):
    data = json.loads(raw)
    need(data.get('schema') == 1 and data.get('source') == DB_BUILD_SOURCE, 'Actual parent DB build source required')
    need(data.get('application_source') == DB_BINDING_APPLICATION_SOURCE, 'Actual old DB binding source required; current source separately joins unchanged PG6')
    need(data.get('application_pg_source_bindings') == PG_SOURCE_BINDINGS,
         'Exact unchanged six-PG-source application binding required')
    need(data.get('image_id') == DB_IMAGE,
         'Literal actual immutable DB IID required; no tag/default')
    need(data.get('dockerfile', {}).get('path') == 'Dockerfile.db'
         and data['dockerfile'].get('sha256') == DB_DOCKERFILE_SHA256,
         'Actual unchanged Dockerfile.db binding required')
    need(data.get('base', {}).get('declared') == 'postgres:16-bookworm'
         and data['base'].get('image_id') == DB_BASE_IMAGE,
         'Actual parent-inspected base IID required')
    need(all(type(data.get(k)) is int and 0 < data[k] < 65536 for k in ['postgres_uid', 'postgres_gid']),
         'Actual nonroot postgres UID/GID required; no guessed default')
    build = data.get('parent_build', {})
    need(build.get('exit_code') == 0 and build.get('source') == DB_BUILD_SOURCE and build.get('cwd') == '/home/dobo/work/_worktrees/social_media_activities_app/feat__gui-app-migration-a'
         and isinstance(build.get('argv'), list) and build['argv'][:2] == ['docker', 'build']
         and '-f' in build['argv'] and build['argv'].index('-f') + 1 < len(build['argv']) and build['argv'][build['argv'].index('-f') + 1] in ['Dockerfile.db', '/home/dobo/work/_worktrees/social_media_activities_app/feat__gui-app-migration-a/Dockerfile.db'],
         'Genuine parent build argv/result required')
    need(set(build.get('records', {})) == {'stdout', 'stderr', 'iidfile', 'image_inspect', 'base_inspect', 'postgres_account'},
         'Actual build streams/IID/image/base/account observation records required')
    return data


def parent_build_records(binding):
    rows = []
    values = {}
    for label, expected in sorted(binding['parent_build']['records'].items()):
        if label == 'postgres_account':
            continue
        path = Path(expected['path'])
        raw, actual = regular(path)
        need(actual == {k: expected[k] for k in ['bytes', 'sha256', 'full_mode']}, 'Parent DB build record changed')
        rows.append({'label': label, 'path': str(path), **actual})
        values[label] = raw
    need(values['iidfile'].decode().strip() == binding['image_id'], 'Parent actual IID output differs')
    for label, expected in [('image_inspect', binding['image_id']), ('base_inspect', binding['base']['image_id'])]:
        data = json.loads(values[label])
        need(isinstance(data, list) and len(data) == 1 and data[0]['Id'] == expected,
             'Parent actual image inspection differs')
    account_rows = account_observation(binding)
    rows.extend(account_rows)
    return rows


def account_observation(binding):
    observation = binding['parent_build']['records']['postgres_account']
    need(observation.get('image_id') == binding['image_id'] and observation.get('exit_code') == 0,
         'Actual same-DBIID account observation required')
    argv = observation.get('argv')
    need(isinstance(argv, list) and argv[:2] == ['docker', 'run'] and '--entrypoint' in argv,
         'Actual image account command argv required')
    position = argv.index('--entrypoint')
    need(position + 1 < len(argv), 'Actual account command entrypoint required')
    command = argv[position + 1]
    need((command == 'id' and argv[-2:] == [binding['image_id'], 'postgres'])
         or (command == 'getent' and argv[-3:] == [binding['image_id'], 'passwd', 'postgres']),
         'Only actual id postgres/getent passwd postgres observation admitted')
    values, rows = {}, []
    for label in ['stdout', 'stderr']:
        expected = observation[label]
        path = Path(expected['path'])
        raw, actual = regular(path, 64 << 10)
        need(actual == {k: expected[k] for k in ['bytes', 'sha256', 'full_mode']},
             'Actual image account output tuple changed')
        values[label] = raw
        rows.append({'label': 'postgres_account_' + label, 'path': str(path), **actual})
    need(not values['stderr'], 'Actual account lookup stderr must be empty')
    text = values['stdout'].decode('utf-8')
    if command == 'id':
        match = re.fullmatch(r'uid=([1-9][0-9]*)\(postgres\) gid=([1-9][0-9]*)\([^\n)]*\)(?: groups=[^\n]*)?\n?', text)
        need(match is not None, 'Actual id postgres output shape differs')
        uid, gid = int(match.group(1)), int(match.group(2))
    else:
        fields = text.rstrip('\n').split(':')
        need(len(fields) == 7 and fields[0] == 'postgres' and fields[2].isdigit() and fields[3].isdigit(),
             'Actual passwd postgres output shape differs')
        uid, gid = int(fields[2]), int(fields[3])
    need((uid, gid) == (binding['postgres_uid'], binding['postgres_gid']),
         'Actual image account UID/GID differs from required literal fields')
    return rows


def creation_observation(attempted, code, stdout, stderr, name, image):
    need(attempted is True, 'Name recovery requires an actual creation attempt')
    if code != 0:
        missing = code == 1 and not stdout and stderr.decode('utf-8', errors='replace').strip() in {
            'Error: No such object: ' + name,
            'Error: No such container: ' + name,
            'Error response from daemon: No such container: ' + name,
        }
        return {'action': 'unknown', 'verified_no_such_object': missing, 'cleanup_pass': False}
    observed = json.loads(stdout)
    need(isinstance(observed, dict) and re.fullmatch(r'[0-9a-f]{64}', observed.get('id', '')) is not None
         and observed.get('name') == '/' + name and observed.get('image') == image,
         'Actual name recovery ID/name/image differs; no removal authority')
    return {'action': 'candidate', 'id': observed['id'], 'observation': observed, 'cleanup_pass': False}


def validate_raw_ledger(raw):
    ledger = json.loads(raw)
    need(digest(raw) == RAW_LEDGER_SHA256 and ledger['source'] == RAW_SOURCE
         and ledger['directory'] == str(RAW_CORPUS) and ledger['directory_full_mode'] == 0o40700
         and len(ledger['rows']) == 38, 'Closed exact R6 raw38 ledger required')
    names = []
    for item in ledger['rows']:
        path = Path(item['path'])
        need(path.parent == RAW_CORPUS and path.name not in ['', '.', '..']
             and item['full_mode'] == 0o100600, 'Literal private R6 corpus reference required')
        names.append(path.name)
    need(len(set(names)) == 38 and 'manifest.json' in names
         and next(item['sha256'] for item in ledger['rows'] if Path(item['path']).name == 'manifest.json') == RAW_MANIFEST_SHA256,
         'Complete distinct R6 raw members and actual manifest required')
    return ledger


def raw_snapshot(expected):
    owned_directory(RAW_CORPUS)
    need({p.name for p in RAW_CORPUS.iterdir()} == {Path(item['path']).name for item in expected},
         'Complete raw38 member set required')
    rows = []
    for item in expected:
        path = Path(item['path'])
        _, actual = regular(path, 512 << 10)
        need(actual == {k: item[k] for k in ['bytes', 'sha256', 'full_mode']}, 'Accepted raw corpus bytes/modes differ')
        rows.append({'path': str(path), **actual})
    return rows


def private_input_envelope(expected):
    need(len(expected) == 55, 'Exact private raw38/original15/binary/buildinfo envelope required')
    paths, inodes, rows = set(), set(), []
    for item in expected:
        path = Path(item['path'])
        need(path.is_absolute() and path.resolve() == path and str(path) not in paths,
             'Distinct canonical private input paths required')
        before = path.lstat()
        need(stat.S_ISREG(before.st_mode) and before.st_uid == os.getuid() and before.st_nlink == 1,
             'Owned private input must have exactly one link')
        _, actual = regular(path)
        after = path.lstat()
        need((before.st_dev, before.st_ino, before.st_uid, before.st_nlink) ==
             (after.st_dev, after.st_ino, after.st_uid, after.st_nlink)
             and after.st_nlink == 1 and after.st_uid == os.getuid()
             and actual == {k: item[k] for k in ['bytes', 'sha256', 'full_mode']},
             'Private input identity/link/binding changed')
        identity = (after.st_dev, after.st_ino)
        need(identity not in inodes, 'Distinct private input inodes required')
        paths.add(str(path))
        inodes.add(identity)
        rows.append({'path': str(path), **actual, 'uid': after.st_uid,
                     'device': after.st_dev, 'inode': after.st_ino, 'nlink': after.st_nlink})
    return rows


def canonical_archive_observation():
    # The immutable canonical release is already checked against its exact
    # declared hash/mode by binding_snapshot. Nlink is an observation, not a
    # new restriction on canonical release storage or a reason to rewrite it.
    _, binding = regular(ARCHIVE)
    info = ARCHIVE.lstat()
    return {'path': str(ARCHIVE), **binding, 'nlink_observed': info.st_nlink,
            'link_count_constraint': None, 'constraint': 'Exact canonical archive hash/mode only'}


def safe_container(observed):
    # Actual raw configuration is verified in memory; no DSN/program/environment
    # values are retained. Identity/resource/mount/state fields remain literal.
    saved = dict(observed)
    command = saved.pop('cmd', None)
    environment = saved.pop('env', None)
    saved['command_sha256'] = digest(json.dumps(command, sort_keys=True, separators=(',', ':')).encode())
    saved['command_items'] = len(command) if isinstance(command, list) else None
    saved['environment_sha256'] = digest(json.dumps(environment, sort_keys=True, separators=(',', ':')).encode())
    saved['environment_names'] = sorted(value.split('=', 1)[0] for value in environment or [])
    saved['sensitive_values_retained'] = False
    return saved


def safe_argv(argv):
    saved = []
    hide_env = False
    for value in argv:
        if hide_env:
            name = value.split('=', 1)[0]
            saved.append(name + '=<value-withheld;sha256=' + digest(value.encode()) + '>')
            hide_env = False
        elif value == '--env':
            saved.append(value)
            hide_env = True
        elif value in (ENTRY, DB_ENTRY):
            saved.append('<program-withheld;sha256=' + digest(value.encode()) + '>')
        else:
            saved.append(value)
    return saved


def retained_control(args, stdout):
    if '--format' in args and args[args.index('--format') + 1] == INSPECT:
        try:
            value = json.loads(stdout)
            need(isinstance(value, dict), 'Actual inspection object required')
            return (json.dumps(safe_container(value), indent=2) + '\n').encode(), 'sanitized-effective-container-config'
        except Exception:
            return (json.dumps({'unparsed_inspection_bytes': len(stdout), 'unparsed_inspection_sha256': digest(stdout), 'raw_not_retained': True}) + '\n').encode(), 'inspection-hash-only-on-parse-failure'
    return stdout, 'literal-safe-control-output'


def transport_observation(raw, registry):
    events, _ = test_observation(raw)
    cases = {item['ID']:item for item in registry['Cases']}
    need(len(cases) == 13, 'Literal preference13 matrix required')
    rows, census = {}, []
    for event in events:
        output = event.get('Output','')
        if not isinstance(output,str): continue
        match = re.search(r'current preference case=([a-z0-9-]+) status=(200|302)(.*)',output)
        if match:
            identity, code, tail = match.groups()
            need(identity in cases and identity not in rows and event.get('Test') == 'TestGUIPreferencesRegisteredGETTransport/current-preference-'+identity and int(code) == cases[identity]['Status'], 'Actual preference metadata case/status/owner differs')
            if code == '200':
                frame = re.fullmatch(r' os_sha256=([a-f0-9]{64}) fs_sha256=([a-f0-9]{64}) os_canonical_sha256=([a-f0-9]{64}) fs_canonical_sha256=([a-f0-9]{64}) hard0=true; independent real typed callbacks, no shared-context or original semantic claim\n?',tail)
                need(frame is not None and frame.group(3) == frame.group(4), 'Actual strict hard0 whole canonical agreement required')
                rows[identity] = {'id':identity,'status':200,'os_body_sha256':frame.group(1),'fs_body_sha256':frame.group(2),'os_canonical_sha256':frame.group(3),'fs_canonical_sha256':frame.group(4),'hard0':True}
            else:
                frame = re.fullmatch(r' native_negative_raw_equal=true os_sha256=([a-f0-9]{64}) fs_sha256=([a-f0-9]{64}); no HTML normalization\n?',tail)
                need(frame is not None and frame.group(1) == frame.group(2), 'Actual exact native negative metadata required')
                rows[identity] = {'id':identity,'status':302,'os_body_sha256':frame.group(1),'fs_body_sha256':frame.group(2),'HTML_normalized':False}
        match = re.search(r'current preference observations requests=(\d+) paired_HTTP=(\d+) strict_HTML=(\d+) native_negative=(\d+) settings_API_reads=(\d+); expected26/13/10/3/12, no original oracle/authprivacy/adoption/browser/full qualification',output)
        if match:
            need(event.get('Test') == 'TestGUIPreferencesRegisteredGETTransport', 'Actual root owns preference census')
            census.append([int(v) for v in match.groups()])
    need(set(rows) == set(cases) and len(rows) == 13 and census == [[26,13,10,3,12]], 'Complete actual13/26/10HTML/3negative/12nativeSettingsAPI metadata required')
    need(sum(row['status'] == 200 for row in rows.values()) == 10 and sum(row['status'] == 302 for row in rows.values()) == 3, 'Exact10HTML/3native redirects required')
    return {'current_pairs':13,'requests':26,'HTML_pairs':10,'native302_pairs':3,'observed_settings_API_GETs':12,'method_source_inputs':141,'native_registry_source_inputs':138,'rows':[rows[item['ID']] for item in registry['Cases']],'body_retention':False,'normalizer_census':None,'accepted_normalized_baseline':False,'scope':'Synthetic adult/public independent typed current-current transport; no same-context/original/auth/privacy/adoption claim'}


def current_g1_transport_observation(raw, registry):
    events = [json.loads(line) for line in raw.splitlines()]
    cases = {item['id']: item for item in registry['cases']}
    need(len(cases) == 37, 'Actual unchanged registry37 required')
    rows = {}
    census = []
    for event in events:
        output = event.get('Output', '')
        if not isinstance(output, str):
            continue
        match = re.search(r'current pair=([a-z0-9-]+) status=(200|302|404) os_body_sha256=([a-f0-9]{64}) fs_body_sha256=([a-f0-9]{64})(.*)', output)
        if match:
            identity, code, disk_hash, files_hash, tail = match.groups()
            need(identity in cases and identity not in rows and event.get('Test') == 'TestGUIPublicGetWalk/current-transport-' + identity,
                 'Current transport observation must own one exact case')
            code = int(code)
            need(code == cases[identity]['status'], 'Current observed status differs')
            row = {'id': identity, 'status': code, 'os_body_sha256': disk_hash, 'fs_body_sha256': files_hash}
            if code == 200:
                canon = re.fullmatch(r' os_canonical_sha256=([a-f0-9]{64}) fs_canonical_sha256=([a-f0-9]{64}) os_hard_count=0 fs_hard_count=0 os_hard_codes=\[\] fs_hard_codes=\[\]; no renderer-oracle or baseline claim\n?', tail)
                need(canon is not None and canon.group(1) == canon.group(2), 'Current actual canonical agreement/hard0 metadata required')
                row.update({'os_canonical_sha256': canon.group(1), 'fs_canonical_sha256': canon.group(2), 'hard_count': 0})
            else:
                need(tail.rstrip('\n') == '; native negative NOT HTML NORMALIZED' and disk_hash == files_hash,
                     'Current exact native negative metadata required')
            rows[identity] = row
        match = re.search(r'current transport requests=(\d+) pairs=(\d+) HTML=(\d+) native_negative=(\d+) source_inputs=(\d+); current OS/FS only, no historical-current equality or baseline', output)
        if match:
            need(event.get('Test') == 'TestGUIPublicGetWalk', 'Current root must own actual request census')
            census.append([int(value) for value in match.groups()])
    need(set(rows) == set(cases) and len(rows) == 37 and census == [[74, 37, 31, 6, 124]], 'Complete current37-pair/74-request/124-input actual metadata required')
    need(sum(row['status'] == 200 for row in rows.values()) == 31 and sum(row['status'] == 302 for row in rows.values()) == 2 and sum(row['status'] == 404 for row in rows.values()) == 4,
         'Current actual31HTML/2anonymous302/4native404 required')
    return {'current_pairs': 37, 'requests': 74, 'HTML_pairs': 31, 'anonymous302_pairs': 2, 'native404_pairs': 4, 'source_inputs': 124,
            'rows': [rows[item['id']] for item in registry['cases']], 'scope': 'Synthetic anonymous current OS/FS registered GET template/catalog equivalence only',
            'raw_or_normalized_body_retention': False, 'normalizer_process_or_API_census': None, 'accepted_normalized_baseline': False}


def execute(db_binding_argument):
    need(__debug__, 'Optimized execution refused')
    for directory in [SCRATCH, SCRATCH / 'evidence', CACHE_ROOT / 'go-build', CACHE_ROOT / 'go-mod', CACHE_ROOT / 'tmp']:
        owned_directory(directory)
    ledger_raw, ledger_ref = regular(LEDGER)
    ledger = json.loads(ledger_raw)
    need(digest(ledger_raw) == LEDGER_SHA256 and ledger['source'] == SOURCE and len(ledger['source_inputs']) == 141
         and len(ledger['current7_inputs']) == 7 and len(ledger['current_g1_inputs']) == 124 and len(ledger['current20_inputs']) == 20
         and len(ledger['readonly_policy_refs']) == 7 and len(ledger['normalizer_and_original_inputs']) == 18,
         'Closed actual current source141/preference7/currentG1-124/template20/policy7/external18 ledger required')
    inventory_raw, _ = regular(INVENTORY)
    registry_raw, _ = regular(CASES)
    need(digest(inventory_raw) == INVENTORY_SHA256 and digest(registry_raw) == CASES_SHA256, 'Closed inventory/cases differ')
    inventory = json.loads(inventory_raw)
    profiles = inventory['profiles']
    need(inventory['source'] == SOURCE and set(profiles) == set(PROFILE_ORDER)
         and [profiles[n]['named_actions'] for n in PROFILE_ORDER] == [17,28,45]
         and all(len(v['ordered_ids']) == len(set(v['ordered_ids'])) == v['named_actions'] for v in profiles.values())
         and inventory['aggregate_actions'] == 90 and len({identity for v in profiles.values() for identity in v['ordered_ids']}) == 89,
         'Exact separate17/28/45 profiles:90 actions but89 cross-profile test labels required')
    raw_ledger_raw, _ = regular(RAW_LEDGER)
    raw_ledger = validate_raw_ledger(raw_ledger_raw)
    registry = json.loads(registry_raw)
    g1_registry_raw, g1_registry_ref = regular(G1_CASES)
    need(digest(g1_registry_raw) == G1_CASES_SHA256, 'Unchanged original37 case registry required')
    g1_registry = json.loads(g1_registry_raw)
    db_binding_path = Path(db_binding_argument)
    db_raw, db_ref = regular(db_binding_path)
    need(db_ref['full_mode'] == 0o100600 and db_ref['sha256'] == ACTUAL_PARENT_BINDING_SHA256, 'Exact actual prior parent813f/bac5/f66 binding required; no new DB build claimed')
    db_binding = validate_db_binding(db_raw)
    build_before = parent_build_records(db_binding)
    need(regular(ROOT / 'Dockerfile.db')[1]['sha256'] == DB_DOCKERFILE_SHA256,
         'Actual DB Dockerfile source changed')
    invocation = str(uuid.uuid4())
    out = SCRATCH / 'evidence' / ('profiles-' + SOURCE[:12] + '-' + invocation)
    out.mkdir(mode=0o700)
    owned_directory(out)
    proof = out / 'proof'
    proof.mkdir(mode=0o700)
    socket = out / 'pg-socket'
    socket.mkdir(mode=0o700)
    os.chmod(socket, 0o770)  # Shared only by two owned containers; parent remains private0700.
    need(socket.stat().st_uid == os.getuid() and socket.stat().st_gid == os.getgid(), 'Owned socket group differs')
    names = {'tc': 'social-preference-context-tc-' + invocation, 'db': 'social-preference-context-db-' + invocation}
    cids = {'tc': None, 'db': None}
    creation_attempted = {'tc': False, 'db': False}
    records = []
    mounts = [(ROOT, '/work', False), (CACHE_ROOT / 'go-build', '/scratch/go-build', True),
              (CACHE_ROOT / 'go-mod', '/scratch/go-mod', True), (CACHE_ROOT / 'tmp', '/scratch/tmp', True),
              (proof, '/proof', True), (BINARY, '/bindings/social-gui-normalize', False),
              (ARCHIVE, '/bindings/web-kit-go-core-v1.6.tgz', False), (ORIGINAL, '/original', False),
              (socket, '/pgsocket', False)]
    db_tmpfs = {'/var/lib/postgresql/data': f'rw,nosuid,nodev,size=512m,uid={db_binding["postgres_uid"]},gid={db_binding["postgres_gid"]},mode=0700',
                '/tmp': f'rw,nosuid,nodev,size=64m,uid={db_binding["postgres_uid"]},gid={db_binding["postgres_gid"]},mode=1777'}
    result = {'schema': 1, 'scope': 'Separate default-source17/preference28/currentG1-45 synthetic profiles; no old/current producer equivalence, historical capture/receiver, normalized baseline, Live/auth/privacy/browser/full/device/default/adoption qualification',
              'source': SOURCE, 'db_build_source': DB_BUILD_SOURCE, 'db_binding_application_source': DB_BINDING_APPLICATION_SOURCE, 'DB_reuse': 'Actual old DB build/IID/binding preserved; current application proves identical PG6 beforeafter; no new build/source relabel', 'invocation': invocation, 'image_argument': IMAGE, 'db_image_argument': db_binding['image_id'],
              'ledger': ledger_ref, 'method_source': regular(Path(__file__).absolute())[1], 'db_binding': db_ref,
              'resource_split': {'tc_cpus': 3, 'tc_bytes': 5 * 1024 ** 3, 'db_cpus': 1, 'db_bytes': 1024 ** 3,
                                 'aggregate_cpus': 4, 'aggregate_bytes': 6 * 1024 ** 3, 'performance_comparison': False},
              'method_pass': False, 'format': None, 'tests': None, 'profiles': {n:{'state':'NOT RUN','tests':None,'test_observation':None,'transport':None,'pg_before':None,'pg_after':None} for n in PROFILE_ORDER}, 'transport': None,
              'before': None, 'after': None, 'binding_before': None, 'binding_after': None,
              'containers': {}, 'cleanup': {}, 'pg_cleanup': None, 'creation_attempted': creation_attempted, 'name_recovery': {}, 'errors': []}

    def write(name, data):
        fd = os.open(out / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)

    def control(args, timeout=60, allow_failure=False):
        index = len(records)
        call = None
        try:
            call = subprocess.run(['docker', *args], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout, umask=0o077)
            stdout, stderr, code = call.stdout, call.stderr, call.returncode
        except subprocess.TimeoutExpired as error:
            stdout, stderr, code = error.stdout or b'', error.stderr or b'', None
        except OSError:
            stdout, stderr, code = b'', b'', None
        stored_stdout, output_scope = retained_control(args, stdout)
        write('control-%02d.stdout' % index, stored_stdout)
        write('control-%02d.stderr' % index, stderr)
        records.append({'argv': safe_argv(['docker', *args]), 'argv_sha256': digest(json.dumps(['docker', *args], separators=(',', ':')).encode()),
                        'stdout_record_scope': output_scope, 'actual_stdout_sha256': digest(stdout), 'actual_stdout_bytes': len(stdout), 'exit_code': code, 'timeout_seconds': timeout,
                        'stdout': 'control-%02d.stdout' % index, 'stderr': 'control-%02d.stderr' % index})
        if allow_failure:
            return code, stdout, stderr
        need(code == 0, 'Docker control refused: ' + args[0])
        return stdout

    def verify_owned(role, observed, cid):
        image = IMAGE if role == 'tc' else db_binding['image_id']
        user = f'{os.getuid()}:{os.getgid()}' if role == 'tc' else f'{db_binding["postgres_uid"]}:{db_binding["postgres_gid"]}'
        need(observed['id'] == cid and observed['name'] == '/' + names[role] and observed['image'] == image
             and observed['user'] == user and observed['network'] == 'none' and not observed['ports']
             and observed['privileged'] is False,
             'Owned identity/source image/user/no-network/no-ports differ')
        actual = {(m['Source'], m['Destination'], m['RW']) for m in observed['mounts'] if m['Type'] == 'bind'}
        wanted = {(str(s), d, rw) for s, d, rw in mounts} if role == 'tc' else {(str(socket), '/socket', True)}
        need(actual == wanted and not any(m['Type'] == 'volume' for m in observed['mounts']),
             'Owned exact mounts/no foreign or anonymous volumes required')
        if role == 'tc':
            need(observed['entrypoint'] == ['/usr/local/bin/roedu-runner'] and observed['readonly'] is False and not observed['security'] and not observed['cap_drop']
                 and observed['cpus'] == 3_000_000_000 and observed['memory'] == 5 * 1024 ** 3
                 and (observed['tmpfs'] is None or type(observed['tmpfs']) is dict and not observed['tmpfs'])
                 and observed['pids'] == 512 and observed['cmd'] == ['bash', '-u', '-o', 'pipefail', '-c', ENTRY], 'TC resource/command binding differs')
        else:
            need(observed['cpus'] == 1_000_000_000 and observed['memory'] == 1024 ** 3
                 and observed['tmpfs'] == db_tmpfs and observed['groups'] == [str(os.getgid())]
                 and observed['pids'] == 256 and observed['readonly'] is True
                 and observed['security'] == ['no-new-privileges'] and observed['cap_drop'] == ['ALL'] and observed['entrypoint'] == ['bash']
                 and observed['cmd'] == ['-u', '-o', 'pipefail', '-c', DB_ENTRY], 'DB resource/tmpfs/entrypoint binding differs')
        need(isinstance(observed.get('env'), list) and all(isinstance(value, str) and '=' in value for value in observed['env']), 'Actual effective environment shape required')
        environment = dict(value.split('=', 1) for value in observed['env'])
        need(len(environment) == len(observed['env']), 'Duplicate effective environment names refused')
        if role == 'tc':
            for name, value in [('GOWORK', 'off'), ('GOTOOLCHAIN', 'local'), ('GOCACHE', '/scratch/go-build'), ('GOMODCACHE', '/scratch/go-mod'), ('TMPDIR', '/scratch/tmp'), ('GOMAXPROCS', '2'), ('GOFLAGS', '-p=2'), ('CGO_ENABLED', '1')]:
                need(environment.get(name) == value, 'Actual effective TC configuration differs')
        return observed

    def inspect_owned(role):
        return verify_owned(role, json.loads(control(['container', 'inspect', cids[role], '--format', INSPECT])), cids[role])

    def recover_attempt(role):
        image = IMAGE if role == 'tc' else db_binding['image_id']
        code, stdout, stderr = control(['container', 'inspect', names[role], '--format', INSPECT], allow_failure=True)
        recovery = creation_observation(creation_attempted[role], code, stdout, stderr, names[role], image)
        result['name_recovery'][role] = {key: (safe_container(value) if key == 'observation' else value) for key, value in recovery.items()}
        if recovery['action'] != 'candidate':
            if recovery['verified_no_such_object']:
                recovery['exact_name_absent_now'] = not control(['container', 'ls', '-aq', '--filter', 'name=^/' + names[role] + '$']).strip()
            raise RuntimeError('Creation outcome unknown; no verified fullCID to clean; actual absence observation retained')
        # Full resources/command/mount/user/config verification precedes CID
        # assignment and every removal; a name match alone is never authority.
        observed = verify_owned(role, recovery['observation'], recovery['id'])
        cids[role] = observed['id']
        result['name_recovery'][role]['full_owned_configuration_verified'] = True
        return observed

    def create(role, args):
        creation_attempted[role] = True
        raw = control(args)
        _, witness = regular(out / (role + '.cid'), 128)
        cid = raw.decode().strip()
        need(re.fullmatch(r'[0-9a-f]{64}', cid) is not None
             and regular(out / (role + '.cid'), 128)[0].decode().strip() == cid
             and witness['full_mode'] == 0o100600, 'Actual full private CID stdout/file join required')
        cids[role] = cid
        result['containers'][role + '_created'] = safe_container(inspect_owned(role))
        need(not result['containers'][role + '_created']['running'] and not result['containers'][role + '_created']['oom_killed'], 'Created owned container already running/OOM')

    try:
        result['before'] = source_snapshot(ledger['source_inputs'])
        result['pg_source_before'] = source_snapshot(PG_SOURCE_BINDINGS)
        result['policy_before'] = metadata_snapshot(ledger['readonly_policy_refs'])
        result['binding_before'] = binding_snapshot(ledger['normalizer_and_original_inputs'])
        result['current7_before'] = source_snapshot(ledger['current7_inputs'])
        result['current_g1_before'] = source_snapshot(ledger['current_g1_inputs'])
        result['current20_before'] = source_snapshot(ledger['current20_inputs'])
        result['raw_before'] = raw_snapshot(raw_ledger['rows'])
        original_names = {Path(item['path']).name for item in ledger['normalizer_and_original_inputs'] if Path(item['path']).parent == ORIGINAL}
        need(len(original_names) == 15 and {path.name for path in ORIGINAL.iterdir()} == original_names, 'Complete unchanged original15 member set required')
        private_expected = raw_ledger['rows'] + [item for item in ledger['normalizer_and_original_inputs'] if Path(item['path']) != ARCHIVE]
        result['private_envelope_before'] = private_input_envelope(private_expected)
        result['canonical_archive_before'] = canonical_archive_observation()
        need(regular(BINARY)[1]['sha256'] == NORMALIZER_SHA256, 'Exact actual764 normalizer binary required')
        for role, image in [('tc', IMAGE), ('db', db_binding['image_id'])]:
            need(control(['image', 'inspect', image, '--format', '{{.Id}}']).decode().strip() == image, 'Actual immutable image differs')
            need(not control(['container', 'ls', '-aq', '--filter', 'name=^/' + names[role] + '$']).strip(),
                 'Generated owned name already exists; never reuse/remove it')
        db_create = ['create', '--pull=never', '--name', names['db'], '--cidfile', str(out / 'db.cid'),
                     '--network', 'none', '--cpus=1', '--memory=1g', '--pids-limit=256', '--read-only',
                     '--cap-drop=ALL', '--security-opt=no-new-privileges', '--user', f'{db_binding["postgres_uid"]}:{db_binding["postgres_gid"]}',
                     '--group-add', str(os.getgid()), '--mount', f'type=bind,src={socket},dst=/socket', '--entrypoint', 'bash']
        for destination, options in db_tmpfs.items():
            db_create += ['--tmpfs', destination + ':' + options]
        db_create += [db_binding['image_id'], '-u', '-o', 'pipefail', '-c', DB_ENTRY]
        create('db', db_create)
        control(['start', cids['db']])
        deadline = time.monotonic() + 45
        while not (socket / 'ready.json').exists():
            need(time.monotonic() < deadline, 'Owned PG readiness deadline exceeded')
            observed = inspect_owned('db')
            need(observed['running'] and not observed['oom_killed'], 'Owned DB exited before readiness')
            time.sleep(0.2)
        ready = json.loads(regular(socket / 'ready.json', 4096)[0])
        need(type(ready.get('owned_pid')) is int and ready['owned_pid'] > 0
             and ready.get('uid') == db_binding['postgres_uid'] and ready.get('gid') == db_binding['postgres_gid'],
             'Actual PG owned PID/user differs')
        result['pg_ready'] = ready
        result['containers']['db_ready'] = safe_container(inspect_owned('db'))
        tc_create = ['create', '--pull=never', '--name', names['tc'], '--cidfile', str(out / 'tc.cid'),
                     '--network', 'none', '--cpus=3', '--memory=5g', '--pids-limit=512', '--user', f'{os.getuid()}:{os.getgid()}',
                     '--workdir', '/work/services/server']
        for source, destination, writable in mounts:
            tc_create += ['--mount', f'type=bind,src={source},dst={destination}' + ('' if writable else ',readonly')]
        for value in ['GOWORK=off', 'GOTOOLCHAIN=local', 'GOCACHE=/scratch/go-build', 'GOMODCACHE=/scratch/go-mod',
                      'TMPDIR=/scratch/tmp', 'GOMAXPROCS=2', 'GOFLAGS=-p=2', 'CGO_ENABLED=1']:
            tc_create += ['--env', value]
        tc_create += [IMAGE, 'bash', '-u', '-o', 'pipefail', '-c', ENTRY]
        create('tc', tc_create)
        with (out / 'docker-start.stdout').open('xb') as stdout, (out / 'docker-start.stderr').open('xb') as stderr:
            os.fchmod(stdout.fileno(), 0o600)
            os.fchmod(stderr.fileno(), 0o600)
            started = time.monotonic()
            for profile in result['profiles'].values():
                profile['state'] = 'UNKNOWN/NOT_OBSERVED'
            call = subprocess.run(['docker', 'start', '-a', cids['tc']], stdout=stdout, stderr=stderr, timeout=900)
            result['docker_start_exit'] = call.returncode
            result['elapsed_seconds'] = time.monotonic() - started
        result['containers']['tc_exited'] = safe_container(inspect_owned('tc'))
        need(not result['containers']['tc_exited']['running'] and not result['containers']['tc_exited']['oom_killed'],
             'TC did not exit normally')
        fmt_out, _ = regular(proof / 'format.stdout')
        fmt_err, _ = regular(proof / 'format.stderr')
        fmt_exit = int(regular(proof / 'format.exit')[0])
        result['format'] = {'exit_code': fmt_exit, 'diff_bytes': len(fmt_out), 'stderr_bytes': len(fmt_err)}
        need(fmt_exit == 0 and not fmt_out and not fmt_err, 'Actual formatter refused; compiler/GET tests unrun')
        profile_errors = []
        for name in PROFILE_ORDER:
            observed = result['profiles'][name]
            if not (proof / (name + '-tests.exit')).exists():
                continue
            observed['state'] = 'EXECUTED; NOT QUALIFIED'
            try:
                raw, observed['stdout_binding'] = regular(proof / (name + '-tests.stdout'))
                stderr, observed['stderr_binding'] = regular(proof / (name + '-tests.stderr'))
                observed['exit_code'] = int(regular(proof / (name + '-tests.exit'))[0])
                _, observed['test_observation'] = test_observation(raw)
                need(observed['exit_code'] == 0 and not stderr, 'Actual ' + name + ' native command failed')
                observed['tests'] = parse_tests(raw, profiles[name]['ordered_ids'], profiles[name])
                if name == 'default':
                    observed['transport'] = default_observation(raw)
                else:
                    for stage in ['database-create', 'schema', 'pg-binding', 'pg-after']:
                        need(int(regular(proof / (name + '-' + stage + '.exit'))[0]) == 0, 'Actual separate database/schema/PG stage failed')
                    observed['pg_before'] = validate_pg_profile(regular(proof / (name + '-pg-binding.stdout'))[0], profiles[name]['fixture_DB'])
                    observed['pg_after'] = validate_pg_profile(regular(proof / (name + '-pg-after.stdout'))[0], profiles[name]['fixture_DB'])
                    need(observed['pg_before'] == observed['pg_after'], 'Actual per-profile database/extension/fixture cleanup changed')
                    observed['transport'] = transport_observation(raw, registry) if name == 'preferences' else current_g1_transport_observation(raw, g1_registry)
                observed['state'] = 'PASS'
            except Exception as error:
                observed['error'] = type(error).__name__ + ': ' + str(error)
                profile_errors.append(name + ': ' + observed['error'])
        need(not profile_errors and all(result['profiles'][n]['state'] == 'PASS' for n in PROFILE_ORDER), 'Separate profile failure or NOT RUN: ' + '; '.join(profile_errors))
        need(call.returncode == result['containers']['tc_exited']['exit_code'] == 0, 'Actual focused child exit failed')
        need(result['profiles']['preferences']['pg_before']['database_oid'] != result['profiles']['current_g1']['pg_before']['database_oid'], 'Two fresh named fixture databases must be distinct')
        result['tests'] = {'named_actions':90,'unique_test_labels_across_profiles':89,'profile_qualified_identities':90,'package_passes':3,'not_90_global_unique_IDs_or_processes':True}
        result['transport'] = {n:result['profiles'][n]['transport'] for n in PROFILE_ORDER}
    except Exception as error:
        result['errors'].append(type(error).__name__ + ': ' + str(error))
    finally:
        # Stop/remove the TC first, then stop only the actual owned PG. Every
        # cleanup role is attempted independently; one failure cannot hide it.
        for role in ['tc', 'db']:
            try:
                if cids[role] is None:
                    if creation_attempted[role]:
                        observed = recover_attempt(role)
                    else:
                        result['cleanup'][role] = {'creation_attempted': False, 'container_created': False, 'passed': True}
                        continue
                else:
                    observed = inspect_owned(role)
                if role == 'db':
                    try:
                        if observed['running']:
                            fd = os.open(socket / 'stop-request', os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                            os.close(fd)
                            need(control(['wait', cids[role]], timeout=40).decode().strip() == '0', 'Owned PG stop failed')
                        logs = control(['logs', cids[role]])
                        write('database.log', logs)
                        result['pg_cleanup'] = json.loads(regular(socket / 'pg-cleanup.json', 4096)[0])
                        pg_cleanup = result['pg_cleanup']
                        need(pg_cleanup['stop_exit'] == 0 and pg_cleanup['status_exit'] == 3
                             and pg_cleanup['owned_pid'] > 0 and pg_cleanup['pid_probe_exit'] == 1
                             and pg_cleanup['pid_and_socket_files_absent'] == 1,
                             'Actual PG stop/status/PID/socket absence required')
                    except Exception as error:
                        result['errors'].append('pg-cleanup: ' + type(error).__name__ + ': ' + str(error))
                    # PG evidence failure never skips exact owned-container removal.
                    observed = inspect_owned(role)
                if observed['running']:
                    control(['stop', '--time', '20', cids[role]], timeout=35)
                    observed = inspect_owned(role)
                need(not observed['running'], 'Owned container must stop before nonforce removal')
                control(['container', 'rm', cids[role]])
                id_absent = not control(['container', 'ls', '-aq', '--no-trunc', '--filter', 'id=' + cids[role]]).strip()
                name_absent = not control(['container', 'ls', '-aq', '--filter', 'name=^/' + names[role] + '$']).strip()
                need(id_absent and name_absent, 'Owned ID/name remains after removal')
                result['cleanup'][role] = {'id': cids[role], 'name': names[role], 'remove_exit': 0,
                                           'id_absent': id_absent, 'name_absent': name_absent, 'passed': True}
            except Exception as error:
                result['cleanup'][role] = {'id': cids[role], 'passed': False}
                result['errors'].append('cleanup-' + role + ': ' + type(error).__name__ + ': ' + str(error))
        try:
            result['after'] = source_snapshot(ledger['source_inputs'])
            result['pg_source_after'] = source_snapshot(PG_SOURCE_BINDINGS)
            result['policy_after'] = metadata_snapshot(ledger['readonly_policy_refs'])
            need(result['pg_source_before'] == result['pg_source_after'] and result['policy_before'] == result['policy_after'], 'PG fixture source/policy7 changed')
            result['binding_after'] = binding_snapshot(ledger['normalizer_and_original_inputs'])
            need(result['before'] == result['after'] and result['binding_before'] == result['binding_after'],
                 'CurrentSource141/external18 changed')
            need(regular(db_binding_path)[1] == db_ref and parent_build_records(db_binding) == build_before,
                 'Actual parent DB build controls changed')
            result['current7_after'] = source_snapshot(ledger['current7_inputs'])
            result['current_g1_after'] = source_snapshot(ledger['current_g1_inputs'])
            result['current20_after'] = source_snapshot(ledger['current20_inputs'])
            result['raw_after'] = raw_snapshot(raw_ledger['rows'])
            result['private_envelope_after'] = private_input_envelope(private_expected)
            result['canonical_archive_after'] = canonical_archive_observation()
            need(result['current7_before'] == result['current7_after'] and result['current_g1_before'] == result['current_g1_after']
                 and result['current20_before'] == result['current20_after'] and result['raw_before'] == result['raw_after']
                 and result['private_envelope_before'] == result['private_envelope_after']
                 and result['canonical_archive_before'] == result['canonical_archive_after'], 'Current7/raw38/private55/canonical custody changed')
            need(regular(LEDGER)[1] == ledger_ref and digest(regular(RAW_LEDGER)[0]) == RAW_LEDGER_SHA256
                 and digest(regular(INVENTORY)[0]) == INVENTORY_SHA256 and regular(G1_CASES)[1] == g1_registry_ref
                 and digest(regular(CASES)[0]) == CASES_SHA256, 'Closed method input metadata changed')
            for name in PROFILE_ORDER:
                observed = result['profiles'][name]
                if observed['transport'] is not None:
                    raw = regular(proof / (name + '-tests.stdout'))[0]
                    repeated = default_observation(raw) if name == 'default' else transport_observation(raw, registry) if name == 'preferences' else current_g1_transport_observation(raw, g1_registry)
                    need(repeated == observed['transport'], 'Actual per-profile transport metadata changed')
        except Exception as error:
            result['errors'].append('source-after: ' + type(error).__name__ + ': ' + str(error))
        result['controls'] = records
        result['method_pass'] = (not result['errors'] and result['tests'] is not None and result['transport'] is not None
                                 and result['pg_cleanup'] is not None and all(result['cleanup'].get(k, {}).get('passed') for k in ['tc', 'db']))
        result['artifact_rows'] = []
        try:
            for path in sorted(out.rglob('*')):
                info = path.lstat()
                if stat.S_ISREG(info.st_mode):
                    _, ref = regular(path)
                    result['artifact_rows'].append({'path': str(path.relative_to(out)), **ref})
                elif not stat.S_ISDIR(info.st_mode):
                    result['artifact_rows'].append({'path': str(path.relative_to(out)), 'bytes': info.st_size,
                                                   'full_mode': info.st_mode, 'sha256': None, 'kind': 'special-not-regular'})
        except Exception as error:
            result['method_pass'] = False
            result['errors'].append('artifact-after: ' + type(error).__name__ + ': ' + str(error))
        write('result.json', (json.dumps(result, indent=2) + '\n').encode())
        print('Current default/preference/G1 profiles result: ' + str(out / 'result.json') + ' pass=' + str(result['method_pass']), flush=True)
    return 0 if result['method_pass'] else 1






def write_private(path, raw):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(raw)


def prepare_workspace(workspace):
    """Export literal committed Git bytes, never mount the writer's worktree."""
    global ROOT, SCRATCH, SOURCE, LEDGER, LEDGER_SHA256, INVENTORY, INVENTORY_SHA256
    global RAW_LEDGER, RAW_LEDGER_SHA256, CASES, CASES_SHA256, G1_CASES
    need(workspace.is_absolute() and workspace.resolve() == workspace
         and '_temp' in workspace.parts and workspace.name != '_temp' and not workspace.exists(),
         'Fresh canonical _temp workspace required')
    need(not subprocess.check_output(['git', 'status', '--porcelain'], cwd=OWNING_ROOT), 'Clean committed source required')
    SOURCE = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=OWNING_ROOT, text=True).strip()
    workspace.mkdir(mode=0o700)
    ROOT, SCRATCH = workspace / 'source', workspace
    ROOT.mkdir(mode=0o700)
    for name in ['evidence']:
        (workspace / name).mkdir(mode=0o700)
    payload = subprocess.check_output(['git', 'archive', '--format=tar', SOURCE], cwd=OWNING_ROOT)
    exported = []
    with tarfile.open(fileobj=io.BytesIO(payload), mode='r:') as archive:
        for item in archive:
            name = item.name
            need(name and not name.startswith('/') and all(p not in ['', '.', '..'] for p in name.split('/')),
                 'Canonical literal Git archive path required')
            path = ROOT / name
            if item.isdir():
                path.mkdir(mode=0o700, parents=True, exist_ok=True)
                continue
            need(item.isfile() and item.size <= 8 << 20, 'Bounded regular tracked source required')
            path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            raw = archive.extractfile(item).read()
            write_private(path, raw)
            os.chmod(path, 0o755 if item.mode & 0o111 else 0o644)
            exported.append({'path': name, **regular(path)[1]})
    inputs = json.loads(regular(ROOT / 'tools/gui-public-golden/profiles-inputs.json')[0])
    source_groups = ['source_inputs', 'current7_inputs', 'current_g1_inputs', 'current20_inputs', 'readonly_policy_refs']
    ledger = {'schema': 1, 'source': SOURCE, 'normalizer_and_original_inputs': inputs['normalizer_and_original_inputs']}
    for group in source_groups:
        ledger[group] = [{'path': name, **regular(ROOT / name)[1]} for name in inputs[group]]
    inventory = json.loads(regular(ROOT / 'tools/gui-public-golden/profiles-inventory.json')[0])
    inventory['source'] = SOURCE
    raw_ledger = inputs['historical_private_raw']
    LEDGER, INVENTORY, RAW_LEDGER = (workspace / name for name in ['inputs.json', 'inventory.json', 'raw-inputs.json'])
    for path, value in [(LEDGER, ledger), (INVENTORY, inventory), (RAW_LEDGER, raw_ledger)]:
        write_private(path, (json.dumps(value, indent=2) + '\n').encode())
    LEDGER_SHA256, INVENTORY_SHA256, RAW_LEDGER_SHA256 = (regular(path)[1]['sha256'] for path in [LEDGER, INVENTORY, RAW_LEDGER])
    CASES = ROOT / 'services/server/internal/web/testdata/gui-preferences-get/registry.json'
    CASES_SHA256 = regular(CASES)[1]['sha256']
    G1_CASES = ROOT / 'services/server/internal/web/testdata/gui-public-get-walk/cases.json'
    write_private(workspace / 'acquisition.json', (json.dumps({'source': SOURCE, 'archive_sha256': digest(payload), 'tracked_files': exported}, indent=2) + '\n').encode())
    return exported


def terminate(signum, frame):
    raise RuntimeError("Managed invocation terminated by signal " + str(signum))


def main():
    signal.signal(signal.SIGTERM, terminate)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace', required=True, help='Fresh task-owned _temp directory; no live-worktree mount')
    parser.add_argument('--db-binding', required=True, help='Exact prior actual813f/bac5/f66 private DATA record, not a program')
    args = parser.parse_args()
    exported = prepare_workspace(Path(args.workspace))
    try:
        return execute(args.db_binding)
    finally:
        for item in exported:
            need(regular(ROOT / item['path'])[1] == {key: item[key] for key in ['bytes', 'sha256', 'full_mode']}, 'Exported committed source changed')


if __name__ == '__main__':
    sys.exit(main())
