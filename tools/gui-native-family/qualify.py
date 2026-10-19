#!/usr/bin/env python3
"""Qualify six complete Social family PG lanes from an explicit clean Git SHA.

Uses committed sibling DB method/binding files and explicit existing scratch/cache
paths; never discovers credentials, builds/pulls images, or filters test cases.
"""
import argparse
import collections
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import tarfile
import time
import uuid

DEFAULT_REPO = Path(__file__).absolute().parents[2]
DB_IMAGE = 'sha256:f66de888683449864f820c365551125bb2627782be15a264f9746d53573b39d5'
CODEC_IMAGE = 'sha256:34c1c2281d9fc96cf486dda8e88497ef5061e21a412c712d8ef61ca14b07031b'
DB_BINDING_SHA = '2d354cf44466cc7cb377d5aa169caa75ad5558ffd707434330926ee99a47eeef'
DB_ENTRY_SHA = 'b4ed745834cdb9b91ba0291b49dc049e316806b01494b67ff48a0eb8ff9432b4'
PKG_ROOT = 'github.com/DobosP/social_media_activities_app/services/server/internal/'
LANES = [('web', 'web-domain-test-dsn'), ('app', 'app-test-dsn'),
         ('accounts', 'accounts-test-dsn'), ('recommendations', 'recommendations-test-dsn'),
         ('social', 'social-test-dsn'), ('messaging', 'messaging-test-dsn')]
PG = '/usr/lib/postgresql/16/bin/'
REQUIRED_FAMILY = {'web': ['TestNativeFamilyCopyUsesExistingCatalogAndEscapesAfterTranslation', 'TestNativeFamilyDispatchCustodyIsExactAndRefusesOtherBytes', 'TestNativeFamilyHardAndSoftLimitsPreserveTheirDifferentContracts', 'TestNativeFamilyInvitationAvailabilityAndPublicErrorsAreFinite', 'TestNativeFamilyLimitCopyKeepsParentAndWardPerspective', 'TestNativeFamilyMandatoryAuditFailureRollsBackAllThreeMutations', 'TestNativeFamilyProjectionKeepsTeenFactsAndWardExplanationReadOnly', 'TestNativeFamilyProjectionPreservesValidCustomRelationshipAndStoredCategory', 'TestNativeFamilyProjectionPreservesZeroNilAndIndependentSelections', 'TestNativeFamilyProjectionRejectsMalformedSnapshotBeforeWriting', 'TestNativeFamilyRendererUsesEmbeddedBranchAndExistingHeaderPolicy', 'TestNativeFamilyTemplatesEmptyAndTranslatedCopyAreFinite', 'TestNativeFamilyTemplatesEscapeEveryProjectedTextSurface', 'TestNativeFamilyTemplatesHonorCapabilitiesWithoutInventingAuthority', 'TestNativeFamilyTemplatesKeepFiniteMutationContracts', 'TestNativeFamilyTemplatesKeepWardViewReadOnly'], 'app': ['TestNativeFamilyAssembledHTTPScopesCSRFAndCurrentAuthority']}
INSPECT = ('{"id":{{json .Id}},"name":{{json .Name}},"image":{{json .Image}},'
           '"running":{{json .State.Running}},"exit":{{json .State.ExitCode}},'
           '"oom":{{json .State.OOMKilled}},"user":{{json .Config.User}},'
           '"labels":{{json .Config.Labels}},"entrypoint":{{json .Config.Entrypoint}},'
           '"cmd":{{json .Config.Cmd}},"network":{{json .HostConfig.NetworkMode}},'
           '"ports":{{json .HostConfig.PortBindings}},"mounts":{{json .Mounts}},'
           '"tmpfs":{{json .HostConfig.Tmpfs}},"groups":{{json .HostConfig.GroupAdd}},'
           '"cpus":{{json .HostConfig.NanoCpus}},"memory":{{json .HostConfig.Memory}},'
           '"pids":{{json .HostConfig.PidsLimit}},"readonly":{{json .HostConfig.ReadonlyRootfs}},'
           '"privileged":{{json .HostConfig.Privileged}},"caps":{{json .HostConfig.CapDrop}},'
           '"security":{{json .HostConfig.SecurityOpt}}}')
MIGRATION_SQL = ("CREATE TABLE IF NOT EXISTS go_backend_migrations(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now()); "
                 "INSERT INTO go_backend_migrations(version) VALUES('django-477d43a-contract-v1') ON CONFLICT DO NOTHING; "
                 "INSERT INTO go_backend_migrations(version) VALUES('go-shared-rate-budgets-v1'),('go-shared-rate-budgets-v2') ON CONFLICT DO NOTHING;")
BIND_SQL = """SELECT json_build_object('database',current_database(),'oid',(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),'version',current_setting('server_version_num')::integer,'listen',current_setting('listen_addresses'),'fixtures',(SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'native_fixture_%'),'extensions',(SELECT json_object_agg(extname,extversion ORDER BY extname) FROM pg_extension WHERE extname IN ('postgis','fuzzystrmatch','postgis_tiger_geocoder','postgis_topology','pg_trgm','vector')),'budgets',to_regclass('public.go_rate_budget') IS NOT NULL,'markers',(SELECT json_agg(version ORDER BY version) FROM go_backend_migrations))"""


REFERENCE_SQL = """SELECT jsonb_build_object(
'categories',(SELECT jsonb_agg(to_jsonb(t)-'created_at'-'updated_at' ORDER BY t.id) FROM public.taxonomy_activitycategory t),
'types',(SELECT jsonb_agg(to_jsonb(t)-'created_at'-'updated_at' ORDER BY t.id) FROM public.taxonomy_activitytype t),
'relations',(SELECT jsonb_agg(to_jsonb(t) ORDER BY t.id) FROM public.taxonomy_activityrelation t),
'child_venues',(SELECT jsonb_agg(to_jsonb(t)-'created_at' ORDER BY t.id) FROM public.places_childvenueclass t),
'content_types',(SELECT jsonb_agg(to_jsonb(t) ORDER BY t.id) FROM public.django_content_type t))"""
REFERENCE_SEED = 'services/server/internal/catalog/testdata/reference_seed.json'
REFERENCE_HELPER = 'services/server/tools/gui-native-family-bootstrap/main.go'


def need(ok, message):
    if not ok:
        raise RuntimeError(message)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def regular(path, maximum=64 << 20):
    need(path.is_absolute() and path.resolve() == path, 'Canonical unaliased input required: ' + str(path))
    before = path.lstat()
    need(stat.S_ISREG(before.st_mode) and before.st_size <= maximum, 'Bounded regular input required')
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        opened = os.fstat(fd)
        with os.fdopen(fd, 'rb', closefd=False) as stream:
            raw = stream.read(maximum + 1)
        after = os.fstat(fd)
        need((before.st_dev, before.st_ino) == (opened.st_dev, opened.st_ino)
             and len(raw) == after.st_size <= maximum
             and all(getattr(opened, name) == getattr(after, name) for name in
                     ('st_dev', 'st_ino', 'st_size', 'st_mode', 'st_mtime_ns', 'st_ctime_ns')),
             'Input changed while reading')
        return raw
    finally:
        os.close(fd)


def source_rows(root):
    rows = []
    for directory, dirs, files in os.walk(root, followlinks=False):
        for name in sorted(dirs + files):
            path = Path(directory) / name
            info = path.lstat()
            relative = path.relative_to(root).as_posix()
            if stat.S_ISLNK(info.st_mode):
                target = os.readlink(path)
                need(path.resolve().is_relative_to(root), 'Export symlink escapes source')
                rows.append({'path': relative, 'link': target, 'mode': stat.S_IMODE(info.st_mode)})
            elif stat.S_ISREG(info.st_mode):
                raw = regular(path)
                rows.append({'path': relative, 'bytes': len(raw), 'sha256': digest(raw), 'mode': stat.S_IMODE(info.st_mode)})
            else:
                need(stat.S_ISDIR(info.st_mode), 'Special source object refused')
    return sorted(rows, key=lambda row: row['path'])


def parse_tests(raw, package, expected):
    events = [json.loads(line) for line in raw.splitlines()]
    need(events and all(isinstance(event, dict) and event.get('Package') == package for event in events),
         'Actual pinned test2json package frames required')
    runs = [event['Test'] for event in events if event.get('Action') == 'run' and 'Test' in event]
    passes = [event['Test'] for event in events if event.get('Action') == 'pass' and 'Test' in event]
    need(runs and len(runs) == len(set(runs)) and collections.Counter(runs) == collections.Counter(passes)
         and {name for name in runs if '/' not in name} == set(expected), 'Complete listed identities must run and pass exactly once')
    need(not any(event.get('Action') in ('skip', 'fail') or '(cached)' in event.get('Output', '')
                 or 'DATA RACE' in event.get('Output', '') for event in events), 'Skip/failure/cache/race refused')
    need(sum(event.get('Action') == 'pass' and 'Test' not in event for event in events) == 1,
         'Exactly one actual package PASS required')
    return {'top_level': len(expected), 'named_passes': len(passes), 'skip': 0, 'fail': 0,
            'identities': runs, 'json_events': len(events)}


def validate_reference(raw, expected):
    actual = json.loads(raw)
    need(actual == expected, 'Full canonical catalog reference metadata differs')
    return {'canonical_json_sha256': digest(json.dumps(actual, sort_keys=True, separators=(',', ':')).encode()),
            'rows': {name: len(rows) for name, rows in actual.items()}}


def execute(args):
    REPO, BASE, GO, CACHE, MODCACHE = args.repo, args.scratch, args.go, args.cache, args.modcache
    method = REPO / 'tools/gui-native-family'
    need(all(path.is_absolute() and path.resolve() == path for path in (REPO, BASE, GO, CACHE, MODCACHE)),
         'Explicit canonical unaliased repo/scratch/Go/cache paths required')
    need(Path(__file__).absolute() == method / 'qualify.py', 'Invoke the method from the target repository')
    need(BASE.is_dir() and BASE.stat().st_uid == os.getuid() and not BASE.is_relative_to(REPO),
         'Existing owned scratch outside the repository required')
    DB_BINDING = method / 'db-binding.json'
    need(__debug__ and re.fullmatch('[0-9a-f]{40}', args.sha), 'Full literal source SHA required')
    need(os.getuid() != 0, 'Run as the owning nonroot user')
    need(BASE.resolve() == BASE and REPO.resolve() == REPO, 'Canonical task/repo paths required')
    lock = os.open(BASE / 'runner.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    invocation = str(uuid.uuid4())
    out = BASE / ('run-' + args.sha[:12] + '-' + invocation)
    out.mkdir(mode=0o700)
    for name in ('source', 'binaries', 'host-tmp', 'proof', 'socket', 'test-tmp'):
        (out / name).mkdir(mode=0o700)
    socket = out / 'socket'
    os.chmod(socket, 0o770)
    result = {'schema': 1, 'scope': 'Six affected complete synthetic PG packages only; not full21/current-image/main/milestone/deployment qualification',
              'source': args.sha, 'invocation': invocation, 'out': str(out), 'method_pass': False,
              'codec_image': {'iid': CODEC_IMAGE, 'supplies': 'Existing c27 runtime codecs only; current test binaries and source mounted read-only; no new image qualification'},
              'db_image': DB_IMAGE, 'paths': {'repo': str(REPO), 'scratch': str(BASE), 'go': str(GO), 'cache': str(CACHE), 'modcache': str(MODCACHE), 'method': str(method)}, 'commands': [], 'lanes': {}, 'cleanup': {}, 'errors': []}
    owners = {}
    started = time.monotonic()
    environment = {'PATH': '/usr/local/go/bin:/usr/bin:/bin', 'LANG': 'C.UTF-8',
                   'GOWORK': 'off', 'GOENV': 'off', 'GOTOOLCHAIN': 'local', 'GOPROXY': 'off',
                   'GOMAXPROCS': '2', 'GOFLAGS': '-p=2', 'CGO_ENABLED': '1',
                   'GOCACHE': str(CACHE), 'GOMODCACHE': str(MODCACHE), 'TMPDIR': str(out / 'host-tmp')}

    def write(name, raw):
        path = out / name
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_WRONLY, 0o600)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(raw)
        return {'path': name, 'bytes': len(raw), 'sha256': digest(raw)}

    def call(argv, *, timeout=60, data=None, host_go=False, failure=False, label=None, cwd=None):
        index = len(result['commands'])
        label = label or ('command-%03d' % index)
        start = time.monotonic()
        try:
            completed = subprocess.run(argv, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                       timeout=timeout, env=environment if host_go else None, umask=0o077, cwd=cwd)
            code, stdout, stderr = completed.returncode, completed.stdout, completed.stderr
        except subprocess.TimeoutExpired as error:
            code, stdout, stderr = None, error.stdout or b'', error.stderr or b''
        record = {'argv': [str(value) for value in argv], 'cwd': str(cwd) if cwd else None, 'exit': code, 'timeout': timeout,
                  'seconds': time.monotonic() - start,
                  'stdout': write('proof/' + label + '.stdout', stdout),
                  'stderr': write('proof/' + label + '.stderr', stderr)}
        if data is not None:
            record['stdin'] = {'bytes': len(data), 'sha256': digest(data)}
        result['commands'].append(record)
        if not failure:
            need(code == 0, 'Command failed: ' + label)
        return code, stdout, stderr

    def docker(*argv, **kwargs):
        return call(['docker', *argv], **kwargs)

    def git(*argv):
        return call(['git', '-C', str(REPO), *argv])[1]

    def unchanged_git():
        need(git('rev-parse', 'HEAD').decode().strip() == args.sha and not git('status', '--porcelain'),
             'Exact clean owning source must remain frozen')

    def observe(role):
        owner = owners[role]
        code, raw, _ = docker('container', 'inspect', owner['cid'], '--format', INSPECT)
        actual = json.loads(raw)
        need(code == 0 and actual['id'] == owner['cid'] and actual['name'] == '/' + owner['name']
             and actual['image'] == owner['image'] and (actual['labels'] or {}).get('io.dobolabs.gui.owner') == invocation
             and actual['network'] == 'none' and not actual['ports'] and not actual['privileged']
             and actual['readonly'] and actual['caps'] == ['ALL'] and actual['security'] == ['no-new-privileges'],
             'Owned full CID/name/image/safety configuration differs')
        for key in ('user', 'entrypoint', 'cmd', 'cpus', 'memory', 'pids', 'groups', 'tmpfs'):
            need(actual[key] == owner[key], 'Owned configuration differs: ' + key)
        binds = {(mount['Source'], mount['Destination'], mount['RW']) for mount in actual['mounts'] if mount['Type'] == 'bind'}
        need(binds == set(owner['binds']) and not any(mount['Type'] == 'volume' for mount in actual['mounts']),
             'Unexpected mount or anonymous volume refused')
        return actual

    def create(role, image, user, entrypoint, command, binds, tmpfs, groups, cpus, memory, pids):
        name = 'social-family-' + role + '-' + invocation
        need(not docker('container', 'ls', '-aq', '--filter', 'name=^/' + name + '$')[1].strip(), 'Owned generated name already exists')
        spec = {'name': name, 'image': image, 'cid': None, 'user': user, 'entrypoint': [entrypoint], 'cmd': command,
                'binds': binds, 'tmpfs': tmpfs or None, 'groups': groups or None,
                'cpus': cpus * 1_000_000_000, 'memory': memory, 'pids': pids, 'creation_attempted': True}
        owners[role] = spec
        argv = ['create', '--pull=never', '--name', name, '--cidfile', str(out / (role + '.cid')),
                '--label', 'io.dobolabs.gui.owner=' + invocation, '--network', 'none', '--read-only',
                '--cap-drop=ALL', '--security-opt=no-new-privileges', '--user', user,
                '--cpus=' + str(cpus), '--memory=' + str(memory), '--pids-limit=' + str(pids), '--entrypoint', entrypoint]
        for group in groups:
            argv += ['--group-add', group]
        for source, destination, writable in binds:
            argv += ['--mount', 'type=bind,src=' + source + ',dst=' + destination + ('' if writable else ',readonly')]
        for destination, options in tmpfs.items():
            argv += ['--tmpfs', destination + ':' + options]
        if role != 'db':
            workdir = '/src/services/server' if role.startswith('reference-') else '/src/services/server/internal/' + role
            argv += ['--workdir', workdir, '--env', 'TMPDIR=/scratch', '--env', 'GOMAXPROCS=2']
        argv += [image, *command]
        code, stdout, _ = docker(*argv, failure=True)
        if code == 0:
            cid = stdout.decode().strip()
        else:
            # A timed-out create can have succeeded. Recover only this fresh
            # named object and require its complete owned configuration below.
            recover_code, recovered, _ = docker('container', 'inspect', name, '--format', '{{.Id}}', failure=True)
            need(recover_code == 0, 'Creation outcome unknown; no removal authority without owned CID')
            cid = recovered.decode().strip()
        need(re.fullmatch('[0-9a-f]{64}', cid) and regular(out / (role + '.cid'), 128).decode().strip() == cid,
             'Actual full CID stdout/file join required')
        spec['cid'] = cid
        actual = observe(role)
        need(not actual['running'] and not actual['oom'], 'Created container already running/OOM')
        write('proof/' + role + '-created.json', json.dumps(actual, sort_keys=True).encode() + b'\n')
        need(code == 0, 'Creation command failed; recovered owned object is only eligible for cleanup')

    def remove(role):
        owner = owners[role]
        if owner['cid'] is None:
            # Creation may have completed before a CID-file admission error.
            code, raw, _ = docker('container', 'inspect', owner['name'], '--format', '{{.Id}}', failure=True)
            if code != 0:
                need(not docker('container', 'ls', '-aq', '--filter', 'name=^/' + owner['name'] + '$')[1].strip(), 'Unresolved created name remains')
                result['cleanup'][role] = {'created': False, 'name_absent': True}
                return
            owner['cid'] = raw.decode().strip()
        actual = observe(role)
        if role == 'db':
            try:
                if actual['running']:
                    request = socket / 'stop-request'
                    fd = os.open(request, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                    os.close(fd)
                    code, raw, _ = docker('wait', owner['cid'], timeout=45, failure=True)
                    need(code == 0 and raw.decode().strip() == '0', 'Graceful owned PG stop failed')
                cleanup = json.loads(regular(socket / 'pg-cleanup.json', 4096))
                need(cleanup['stop_exit'] == 0 and cleanup['status_exit'] == 3 and cleanup['owned_pid'] > 0
                     and cleanup['pid_probe_exit'] == 1 and cleanup['pid_and_socket_files_absent'] == 1,
                     'Owned PG process/status/socket absence proof failed')
                result['pg_cleanup'] = cleanup
            except Exception as error:
                result['errors'].append('PG graceful cleanup: ' + type(error).__name__ + ': ' + str(error))
            # A missing PG witness fails qualification but never skips removal
            # of the otherwise fully verified owned container.
            log_code, _, _ = docker('logs', owner['cid'], label='database-final-log', failure=True)
            if log_code != 0:
                result['errors'].append('Owned PG log retrieval failed; removal still required')
            actual = observe(role)
        if actual['running']:
            docker('stop', '--time', '20', owner['cid'], timeout=35)
            actual = observe(role)
        need(not actual['running'], 'Stop before nonforce removal required')
        docker('container', 'rm', owner['cid'])
        absent_id = not docker('container', 'ls', '-aq', '--no-trunc', '--filter', 'id=' + owner['cid'])[1].strip()
        absent_name = not docker('container', 'ls', '-aq', '--filter', 'name=^/' + owner['name'] + '$')[1].strip()
        need(absent_id and absent_name, 'Owned ID/name remains after removal')
        result['cleanup'][role] = {'cid': owner['cid'], 'stopped': True, 'id_absent': absent_id, 'name_absent': absent_name}

    def psql(database, *, sql=None, raw=None, label):
        argv = ['exec', '-i', owners['db']['cid'], PG + 'psql', '--no-psqlrc', '-h', '/socket', '-p', '55434', '-U', 'app', '-d', database, '-v', 'ON_ERROR_STOP=1']
        if sql is not None:
            argv += ['-Atc', sql]
        return docker(*argv, data=raw, timeout=180, label=label)[1]

    def pg_binding(database, label):
        value = json.loads(psql(database, sql=BIND_SQL, label=label))
        need(value['database'] == database and value['oid'] > 0 and value['version'] // 10000 == 16
             and value['listen'] == '' and value['fixtures'] == 0 and value['budgets']
             and set(value['extensions']) == {'postgis', 'fuzzystrmatch', 'postgis_tiger_geocoder', 'postgis_topology', 'pg_trgm', 'vector'}
             and value['markers'] == ['django-477d43a-contract-v1', 'go-shared-rate-budgets-v1', 'go-shared-rate-budgets-v2'],
             'Fresh public PG16/extensions/budgets/fixture-cleanup binding differs')
        value['catalog_reference'] = validate_reference(psql(database, sql=REFERENCE_SQL, label=label + '-reference'), reference_expected)
        return value

    try:
        result['disk_before'] = dict(zip(('total', 'used', 'free'), shutil.disk_usage(BASE)))
        need(result['disk_before']['free'] >= 4 << 30, 'At least 4GiB actual free scratch disk required')
        unchanged_git()
        result['tree'] = git('rev-parse', args.sha + '^{tree}').decode().strip()
        result['method_sha256'] = digest(regular(Path(__file__).absolute()))
        binding_raw = regular(DB_BINDING)
        need(digest(binding_raw) == DB_BINDING_SHA, 'Existing immutable DB binding differs')
        binding = json.loads(binding_raw)
        need(binding['image_id'] == DB_IMAGE and binding['postgres_uid'] == 999 and binding['postgres_gid'] == 999,
             'Actual historical DB image/account binding differs')
        db_entry = regular(method / 'db-entry.sh')
        need(digest(db_entry) == DB_ENTRY_SHA == binding['db_entry_sha256'], 'Exact reused DB_ENTRY differs')
        method_inputs = {str((method / name).relative_to(REPO)): regular(method / name)
                         for name in ('qualify.py', 'db-entry.sh', 'db-binding.json', 'test_qualify.py')}
        method_inputs[REFERENCE_HELPER] = regular(REPO / REFERENCE_HELPER)
        result['method_inputs'] = [{'path': path, 'bytes': len(raw), 'sha256': digest(raw)}
                                  for path, raw in method_inputs.items()]
        for image in (DB_IMAGE, CODEC_IMAGE):
            observed = docker('image', 'inspect', image, '--format', '{{.Id}}')[1].decode().strip()
            need(observed == image, 'Existing literal image not available; no build or pull permitted')
        version = call([str(GO), 'version'], host_go=True)[1].decode().strip()
        need(version == 'go version go1.27.1 linux/amd64', 'Exact host Go1.27.1 required')
        result['go'] = {'version': version, 'actual_executable': str(GO.resolve()), 'sha256': digest(regular(GO.resolve()))}
        for cache in (CACHE, MODCACHE):
            need(cache.resolve() == cache and cache.is_dir() and cache.stat().st_uid == os.getuid(), 'Existing owned rolling cache required')
        archive = git('archive', '--format=tar', args.sha)
        result['git_archive'] = {'bytes': len(archive), 'sha256': digest(archive), 'retention': 'Exported once to the rolling source tree; duplicate archive bytes not retained; exact clean Git SHA/tree is the source keeper'}
        with tarfile.open(fileobj=io.BytesIO(archive), mode='r:') as source_tar:
            source_tar.extractall(out / 'source', filter='data')
        before = source_rows(out / 'source')
        write('source-manifest.json', json.dumps(before, sort_keys=True, separators=(',', ':')).encode() + b'\n')
        for path, raw in method_inputs.items():
            need(regular(out / 'source' / path) == raw, 'Acceptance method inputs must belong to the exact clean Git SHA')
        for row in binding['application_pg_source_bindings']:
            actual = regular(out / 'source' / row['path'])
            need(len(actual) == row['bytes'] and digest(actual) == row['sha256'], 'Current PG6 source differs from reused DB compatibility binding')
        reference_raw = regular(out / 'source' / REFERENCE_SEED)
        reference_expected = {name: sorted(rows, key=lambda row: row['id'])
                              for name, rows in json.loads(reference_raw).items()}
        need(set(reference_expected) == {'categories', 'types', 'relations', 'child_venues', 'content_types'}
             and all(reference_expected.values()), 'Complete current canonical reference source required')
        result['catalog_reference_source'] = {'path': REFERENCE_SEED, 'bytes': len(reference_raw), 'sha256': digest(reference_raw)}
        call([str(GO), '-C', str(out / 'source/services/server'), 'build', '-mod=readonly', '-buildvcs=false', '-trimpath',
              '-o', str(out / 'binaries/reference-bootstrap'), './tools/gui-native-family-bootstrap'],
             host_go=True, timeout=600, label='reference-bootstrap-compile')
        result['reference_bootstrap_binary'] = {'sha256': digest(regular(out / 'binaries/reference-bootstrap'))}
        for lane, _ in LANES:
            call([str(GO), '-C', str(out / 'source/services/server'), 'test', '-mod=readonly', '-race', '-c', '-o', str(out / 'binaries' / (lane + '.test')), './internal/' + lane], host_go=True, timeout=600, label=lane + '-compile')
        call([str(GO), '-C', str(out / 'source/services/server'), 'vet', *['./internal/' + lane for lane, _ in LANES], './tools/gui-native-family-bootstrap'], host_go=True, timeout=600, label='affected-vet')
        db_tmpfs = {'/var/lib/postgresql/data': 'rw,nosuid,nodev,size=1g,uid=999,gid=999,mode=0700',
                    '/tmp': 'rw,nosuid,nodev,size=64m,uid=999,gid=999,mode=1777'}
        create('db', DB_IMAGE, '999:999', 'bash', ['-u', '-o', 'pipefail', '-c', db_entry.decode()],
               [(str(socket), '/socket', True)], db_tmpfs, [str(os.getgid())], 1, 2 << 30, 256)
        docker('start', owners['db']['cid'])
        deadline = time.monotonic() + 45
        while not (socket / 'ready.json').exists():
            need(time.monotonic() < deadline and observe('db')['running'], 'Owned DB failed bounded readiness')
            time.sleep(0.2)
        ready = json.loads(regular(socket / 'ready.json', 4096))
        need(ready['uid'] == ready['gid'] == 999 and ready['owned_pid'] > 0, 'Actual owned DB process identity differs')
        result['pg_ready'] = ready
        oids = []
        for lane, flag in LANES:
            database = 'social_family_' + invocation.replace('-', '')[:12] + '_' + lane
            docker('exec', owners['db']['cid'], PG + 'createdb', '-h', '/socket', '-p', '55434', '-U', 'app', database, label=lane + '-createdb')
            psql(database, raw=regular(out / 'source/services/server/internal/schema/baseline.sql'), label=lane + '-baseline')
            psql(database, raw=b'BEGIN;\n' + regular(out / 'source/services/server/internal/schema/budgets.sql') + b'\nCOMMIT;\n', label=lane + '-budgets')
            psql(database, sql=MIGRATION_SQL, label=lane + '-migration-markers')
            dsn = 'host=/pgsocket port=55434 user=app dbname=' + database + ' sslmode=disable'
            binds = [(str(out / 'source'), '/src', False), (str(out / 'binaries'), '/tests', False),
                     (str(out / 'test-tmp'), '/scratch', True), (str(socket), '/pgsocket', False)]
            reference_role = 'reference-' + lane
            create(reference_role, CODEC_IMAGE, str(os.getuid()) + ':' + str(os.getgid()),
                   '/tests/reference-bootstrap', ['-dsn', dsn], binds,
                   {'/tmp': 'rw,nosuid,nodev,size=64m,mode=1777'}, [], 2, 3 << 30, 512)
            reference_code, reference_stdout, reference_stderr = docker('start', '-a', owners[reference_role]['cid'],
                                                                         timeout=90, failure=True, label=lane + '-reference-bootstrap')
            reference_observed = observe(reference_role)
            need(reference_code == reference_observed['exit'] == 0 and not reference_observed['running']
                 and not reference_observed['oom'] and not reference_stderr
                 and json.loads(reference_stdout) == {'status': 'catalog-migrated'}, 'Canonical catalog bootstrap failed')
            remove(reference_role)
            pg_before = pg_binding(database, lane + '-pg-before')
            need(pg_before['oid'] not in oids, 'Each complete lane must have a distinct fresh public database')
            oids.append(pg_before['oid'])
            command = ['-test.v=test2json', '-test.timeout=10m', '-' + flag, dsn]
            create(lane, CODEC_IMAGE, str(os.getuid()) + ':' + str(os.getgid()), '/tests/' + lane + '.test',
                   command, binds, {'/tmp': 'rw,nosuid,nodev,size=64m,mode=1777'}, [], 2, 3 << 30, 512)
            # Inventory the SAME built binary without running any test. The
            # subsequent actual invocation has no -test.run/-skip/retry filter.
            listed = call([str(out / 'binaries' / (lane + '.test')), '-test.list=.'],
                          host_go=True, label=lane + '-list', cwd=out / 'source/services/server/internal' / lane)[1]
            expected = [name for name in listed.decode().splitlines() if re.fullmatch(r'(?:Test|Example|Fuzz)[A-Za-z0-9_]+', name)]
            need(expected and len(expected) == len(set(expected)), 'Nonempty complete binary test inventory required')
            need(set(REQUIRED_FAMILY.get(lane, ())).issubset(expected), 'Required new family declaration missing from actual binary inventory')
            print(json.dumps({'lane': lane, 'phase': 'complete native PG package starting', 'listed_top_level': len(expected)}), flush=True)
            code, stdout, stderr = docker('start', '-a', owners[lane]['cid'], timeout=660, failure=True, label=lane + '-native')
            observed = observe(lane)
            need(code == observed['exit'] == 0 and not observed['running'] and not observed['oom'], 'Actual native lane failed/OOM/timed out')
            # Preserve both actual raw streams. test2json sees stdout; stderr is
            # separately retained, and any race/failure text fails closed.
            need(b'DATA RACE' not in stderr and b'FAIL' not in stderr, 'Native stderr contains race/failure')
            converted = call([str(GO), 'tool', 'test2json', '-p', PKG_ROOT + lane], data=stdout, host_go=True, label=lane + '-test2json')[1]
            counts = parse_tests(converted, PKG_ROOT + lane, expected)
            pg_after = pg_binding(database, lane + '-pg-after')
            need(pg_before == pg_after, 'Public database/extension/fixture lifecycle changed after lane')
            result['lanes'][lane] = {'state': 'PASS', 'tests': counts, 'pg_before': pg_before, 'pg_after': pg_after,
                                     'binary': {'sha256': digest(regular(out / 'binaries' / (lane + '.test')))}}
            remove(lane)
            unchanged_git()
            print(json.dumps({'lane': lane, 'phase': 'complete package passed and owned container removed', 'named_passes': counts['named_passes']}), flush=True)
        need(source_rows(out / 'source') == before, 'Read-only exported source changed')
        unchanged_git()
    except Exception as error:
        result['errors'].append(type(error).__name__ + ': ' + str(error))
    finally:
        for role in [role for lane, _ in reversed(LANES) for role in (lane, 'reference-' + lane)] + ['db']:
            if role in owners and role not in result['cleanup']:
                try:
                    remove(role)
                except Exception as error:
                    result['errors'].append('cleanup ' + role + ': ' + type(error).__name__ + ': ' + str(error))
        result['seconds'] = time.monotonic() - started
        result['method_pass'] = (not result['errors'] and set(result['lanes']) == {lane for lane, _ in LANES}
                                 and all(value.get('id_absent') and value.get('name_absent') for value in result['cleanup'].values()))
        result['artifact_rows'] = [{'path': path.relative_to(out).as_posix(), 'bytes': path.stat().st_size, 'sha256': digest(regular(path))}
                                   for path in sorted((out / 'proof').glob('*')) if path.is_file()]
        write('result.json', json.dumps(result, indent=2, sort_keys=True).encode() + b'\n')
        os.close(lock)
        print(json.dumps({'out': str(out), 'method_pass': result['method_pass'], 'errors': result['errors']}), flush=True)
    return 0 if result['method_pass'] else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', type=Path, default=DEFAULT_REPO, help='Target checkout containing this committed method')
    parser.add_argument('--scratch', type=Path, required=True, help='Existing owned scratch directory outside the checkout')
    parser.add_argument('--go', type=Path, default=Path('/usr/local/go/bin/go'), help='Existing pinned Go1.27.1 executable')
    parser.add_argument('--cache', type=Path, required=True, help='Existing owned rolling GOCACHE')
    parser.add_argument('--modcache', type=Path, required=True, help='Existing owned offline GOMODCACHE')
    parser.add_argument('--sha', required=True)
    parser.add_argument('--execute', action='store_true', help='Explicit invocation after source review and other heavy fixture work has ended')
    arguments = parser.parse_args()
    need(arguments.execute, 'Explicit post-review execution invocation required')
    def interrupted(signum, frame):
        raise RuntimeError('Interrupted by signal ' + str(signum))
    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGTERM, interrupted)
    raise SystemExit(execute(arguments))
