#!/usr/bin/env python3
"""Full native21 from a committed Git export and existing codec/PG images only."""
import sys
sys.dont_write_bytecode = True
import argparse, hashlib, io, ipaddress, json, lzma, os, re, shutil, signal, stat, subprocess, tarfile, time, uuid
from pathlib import Path, PurePosixPath
DB_IMAGE='sha256:f66de888683449864f820c365551125bb2627782be15a264f9746d53573b39d5'
CODEC_IMAGE='sha256:34c1c2281d9fc96cf486dda8e88497ef5061e21a412c712d8ef61ca14b07031b'
NATIVE_SHA='19265fec060df11262e799956e275aa031366ae0ca404a4919a4d8390aefae76'
XZ_SHA='b5b163eb273291934556377ab883b4b2a5d4da50bd0dc0a91774ecc234ccd8d0'
PACKAGES='configuration accounts admin app backup booking budgets catalog commands contracts discovery donations export jobs media messaging notifications recommendations safety social web'.split()
OWNER_LABEL='com.dobolabs.native21'
INTERRUPTED=None

def need(ok,message):
    if not ok: raise RuntimeError(message)

def qualification_mode(affected):
    need(type(affected) is bool,'finite affected-mode boolean required')
    return (['catalog','web'],'PASS_AFFECTED_CATALOG_WEB_ONLY') if affected else (list(PACKAGES),'PASS_FULL_NATIVE21')

def affected_driver(native,source):
    need(digest(native)==NATIVE_SHA and source.is_absolute() and source.resolve()==source,'pinned canonical script and exported source required')
    splices=[{'before':'for native_package in '+' '.join(PACKAGES)+'; do\n','after':'for native_package in catalog web; do\n'},
             {'before':'native_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)\n','after':'native_root='+shlex_quote(str(source))+'\n'}]
    derived=native.decode()
    for splice in splices:
        need(derived.count(splice['before'])==1,'one exact affected-driver anchor required');derived=derived.replace(splice['before'],splice['after'],1)
    restored=derived
    for splice in reversed(splices):
        need(restored.count(splice['after'])==1,'one exact inverse driver anchor required');restored=restored.replace(splice['after'],splice['before'],1)
    need(restored.encode()==native,'affected driver inverse differs from canonical script')
    return derived.encode(),{'canonical_sha256':digest(native),'derived_sha256':digest(derived.encode()),'inverse_sha256':digest(restored.encode()),'splices':splices,'exact_inverse':True,'scope':'Complete catalog+web only;19 packages excluded; never full21 acceptance'}

def fixture_host_rule(network,owner):
    need(network.get('Internal') is True and (network.get('Labels') or {}).get(OWNER_LABEL)==owner and not network.get('Containers'),'fresh owned internal network required')
    configurations=(network.get('IPAM') or {}).get('Config') or []
    need(len(configurations)==1,'one private IPv4 fixture subnet required')
    subnet=ipaddress.ip_network(configurations[0]['Subnet'],strict=True)
    need(subnet.version==4 and any(subnet.subnet_of(ipaddress.ip_network(block)) for block in ('10.0.0.0/8','172.16.0.0/12','192.168.0.0/16')),'RFC1918 fixture subnet required')
    return 'host all app '+str(subnet)+' trust'

def fixture_wal_entry(entry):
    # Normal durable checkpoints recycle WAL well before the fixed1GiB volume.
    # max_wal_size is a soft checkpoint trigger, never a larger storage ceiling.
    anchor = 'port = 5432\nPG_CONF\n'
    need(entry.count(anchor) == 1, 'single derived fixture configuration anchor required')
    return entry.replace(anchor, 'port = 5432\nmax_wal_size = 128MB\nmin_wal_size = 32MB\ncheckpoint_timeout = 30s\nPG_CONF\n', 1)

def digest(raw): return hashlib.sha256(raw).hexdigest()

def file_hash(path):
    h=hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda:stream.read(1<<20),b''): h.update(chunk)
    return h.hexdigest()

def write(path,value):
    temporary=path.with_suffix(path.suffix+'.pending')
    with temporary.open('w') as stream:
        json.dump(value,stream,sort_keys=True,separators=(',',':'));stream.write('\n');stream.flush();os.fsync(stream.fileno())
    os.replace(temporary,path)

def signal_received(number,frame):
    global INTERRUPTED
    INTERRUPTED=number

def stop_group(child):
    def alive():
        child.poll()
        try:os.killpg(child.pid,0);return True
        except ProcessLookupError:return False
    for number in (signal.SIGTERM,signal.SIGKILL):
        if not alive():break
        try:os.killpg(child.pid,number)
        except ProcessLookupError:pass
        deadline=time.monotonic()+5
        while alive() and time.monotonic()<deadline:time.sleep(.05)
    return child.poll() is not None and not alive()

# Exact legacy CRC64/full-EOF/single-stream reader; copied without changing caps.
def strict_xz_receipt(raw, maximum_compressed=64 << 20,
                      maximum_expanded=128 << 20, memory_limit=64 << 20):
    """Validate one complete CRC64 XZ stream without retaining expanded tar."""
    need(isinstance(raw, bytes) and 0 < len(raw) <= maximum_compressed <= 64 << 20,
         'Compressed source archive exceeds unchanged64MiB bound')
    need(0 < maximum_expanded <= 128 << 20 and 0 < memory_limit <= 64 << 20,
         'Bounded source archive expansion and decoder memory required')
    decoder = lzma.LZMADecompressor(format=lzma.FORMAT_XZ, memlimit=memory_limit)
    offset, expanded = 0, 0
    hashed = hashlib.sha256()
    while not decoder.eof:
        if decoder.needs_input:
            need(offset < len(raw), 'Complete XZ footer/EOF required')
            chunk = raw[offset:offset + (1 << 20)]
            offset += len(chunk)
        else:
            chunk = b''
        unpacked = decoder.decompress(chunk, max_length=min(1 << 20, maximum_expanded - expanded + 1))
        expanded += len(unpacked)
        need(expanded <= maximum_expanded, 'Expanded source tar exceeds128MiB bound')
        hashed.update(unpacked)
    need(decoder.check == lzma.CHECK_CRC64 and not decoder.unused_data and offset == len(raw),
         'Exactly one CRC64 XZ stream without trailing data or extra streams required')
    return {'expanded_tar_bytes': expanded, 'expanded_tar_sha256': hashed.hexdigest(),
            'compressed_limit_bytes': 64 << 20, 'expanded_limit_bytes': 128 << 20,
            'decoder_memory_limit_bytes': 64 << 20, 'CRC64_full_EOF_single_stream': True}

def tree_rows(root,expected):
    rows=[];seen=set()
    for directory,dirs,files in os.walk(root,followlinks=False):
        for name in sorted(dirs+files):
            path=Path(directory)/name;relative=path.relative_to(root).as_posix();mode=path.lstat().st_mode
            if stat.S_ISDIR(mode): continue
            need(relative in expected,'unexpected exported source '+relative);gitmode,oid=expected[relative]
            if gitmode=='120000':
                need(stat.S_ISLNK(mode) and path.resolve().is_relative_to(root),'invalid/escaping source link '+relative)
                raw=os.readlink(path).encode();sha=hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest();row={'path':relative,'mode':gitmode,'git_blob':sha,'sha256':digest(raw),'link':raw.decode()}
            else:
                need(stat.S_ISREG(mode) and stat.S_IMODE(mode)==(0o755 if gitmode=='100755' else 0o644),'exported Git mode mismatch '+relative)
                size=path.stat().st_size;h=hashlib.sha1(b'blob '+str(size).encode()+b'\0');h256=hashlib.sha256()
                with path.open('rb') as stream:
                    for chunk in iter(lambda:stream.read(1<<20),b''):h.update(chunk);h256.update(chunk)
                sha=h.hexdigest();row={'path':relative,'mode':gitmode,'git_blob':sha,'sha256':h256.hexdigest(),'bytes':size}
            need(sha==oid,'exported Git blob mismatch '+relative);seen.add(relative);rows.append(row)
    need(seen==set(expected),'full Git source path closure differs')
    return sorted(rows,key=lambda row:row['path'])

def extract_source(raw,destination,expected,sha):
    receipt=strict_xz_receipt(raw)
    with tarfile.open(fileobj=io.BytesIO(raw),mode='r:xz') as archive:
        need(archive.pax_headers.get('comment')==sha,'Git archive commit comment differs');seen=set()
        for member in archive.getmembers():
            name=member.name.rstrip('/');parts=PurePosixPath(name)
            need(name and not parts.is_absolute() and '..' not in parts.parts and str(parts)==name,'unsafe source archive path')
            if member.isdir(): continue
            need(name not in seen and name in expected,'duplicate/unknown source archive member');seen.add(name)
            gitmode=expected[name][0];need(member.issym() if gitmode=='120000' else member.isfile(),'unexpected source member kind')
        need(seen==set(expected),'archive omitted Git blobs')
        archive.extractall(destination,filter='data')
    for relative,(mode,_) in expected.items():
        if mode!='120000':(destination/relative).chmod(0o755 if mode=='100755' else 0o644)
    return receipt,tree_rows(destination,expected)

def inspected_object(code,raw,error,name):
    if code:
        text=error.decode('utf8','replace').lower()
        need(code==1 and name.lower() in text and ('no such' in text or 'not found' in text) and 'cannot connect' not in text,'object absence not proven: '+name)
        need(json.loads(raw or b'[]')==[],'failed inspect returned an object')
        return None
    values=json.loads(raw)
    need(isinstance(values,list) and len(values)==1 and values[0].get('Name','').lstrip('/')==name,'single exact inspected object required')
    return values[0]

def docker_wrapper(plan_path,argv):
    need(plan_path.is_absolute() and plan_path.resolve()==plan_path and plan_path.stat().st_uid==os.getuid(),'owned canonical plan required')
    plan=json.loads(plan_path.read_text());root=Path(plan['source']);scratch=Path(plan['scratch']);out=Path(plan['out'])
    need(plan_path==out/'plan.json' and root==out/'source' and scratch==out/'runtime' and plan['codec']==CODEC_IMAGE and str(uuid.UUID(plan['owner']))==plan['owner'],'exact generated plan boundary required')
    need(argv[:1]==['run'],'only committed native docker run admitted')
    image=argv.index(plan['codec']);prefix=argv[:image];tail=argv[image+1:]
    need(tail and re.fullmatch(r'/scratch/tests/('+ '|'.join(PACKAGES)+r')\.test',tail[0]),'finite native package command')
    package=Path(tail[0]).name[:-5];need(tail[1:3]==['-test.v','-test.timeout=10m'],'unchanged complete native test flags')
    workdir='/src/services/server/cmd/social-server' if package=='configuration' else '/src/services/server/internal/'+package
    normal=['run','--rm','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--user',str(os.getuid())+':'+str(os.getgid()),'--network',plan['network'],'-e','TMPDIR=/scratch/test-tmp','-e','GOMAXPROCS=2','-v',str(root)+':/src:ro','-v',str(scratch)+':/scratch','-w',workdir]
    need(prefix==normal,'exact committed native Docker prefix required')
    volumes=[prefix[i+1] for i,value in enumerate(prefix[:-1]) if value=='-v']
    need(volumes==[str(root)+':/src:ro',str(scratch)+':/scratch'],'exact exported native source/scratch binds required')
    for volume in volumes:
        i=prefix.index(volume);del prefix[i-1:i+1]
    prefix+=['--name',plan['package_name'],'--label',OWNER_LABEL+'='+plan['owner'],'--pull=never','--tmpfs','/tmp:rw,nosuid,nodev,size=64m,mode=1777','-v',str(root)+':/src:ro','-v',str(scratch/'tests')+':/scratch/tests:ro','-v',str(scratch/'test-tmp')+':/scratch/test-tmp:rw']
    need(not any(flag.startswith(('-test.run','-test.skip','-test.count')) for flag in tail),'no native test filtering/retry')
    command=[plan['docker'],'--host=unix:///var/run/docker.sock',*prefix,plan['codec'],*tail]
    with (Path(plan['out'])/'package-starts.jsonl').open('a') as progress:progress.write(json.dumps({'package':package,'phase':'starting'})+'\n')
    code=124
    try:
        child=subprocess.Popen(command)
        try:code=child.wait(timeout=660)
        except BaseException:child.kill();child.wait(timeout=10);raise
    finally:
        inspected=subprocess.run([plan['docker'],'--host=unix:///var/run/docker.sock','inspect',plan['package_name'],'--format','{{json .Config.Labels}}'],capture_output=True,timeout=20)
        if inspected.returncode==0:
            need(json.loads(inspected.stdout).get(OWNER_LABEL)==plan['owner'],'package ownership differs')
            subprocess.run([plan['docker'],'--host=unix:///var/run/docker.sock','rm','-f',plan['package_name']],check=True,capture_output=True,timeout=30)
        write(Path(plan['out'])/('package-'+package+'.json'),{'package':package,'argv':command,'exit':code})
    return code

def execute(args):
    global INTERRUPTED
    repo,out,go=args.repo,args.out,args.go
    packages,pass_status=qualification_mode(args.affected_catalog_web)
    need(all(path.is_absolute() and path.resolve()==path for path in (repo,go,out.parent)),'canonical repo/go/output parent required')
    need(repo.is_dir() and out.parent.is_dir() and not out.exists() and not out.is_relative_to(repo),'fresh owned output outside repo required')
    need(out.parent.stat().st_uid==os.getuid() and go.is_file() and os.access(go,os.X_OK),'owned output parent and Go executable required')
    need(args.codec_image==CODEC_IMAGE and args.db_image==DB_IMAGE and re.fullmatch('[0-9a-f]{40}',args.sha),'literal current source SHA and existing canonical images only')
    method=Path(__file__).resolve();relative=method.relative_to(repo).as_posix();docker=shutil.which('docker',path='/usr/bin:/bin');need(docker,'installed docker CLI required')
    base_env={key:os.environ[key] for key in ('HOME','GOCACHE','GOMODCACHE','GOPATH') if key in os.environ}
    base_env.update(PATH='/usr/local/bin:/usr/bin:/bin',LANG='C.UTF-8',LC_ALL='C.UTF-8',GOWORK='off',GOTOOLCHAIN='local',GOPROXY='off',GOSUMDB='off',GOFLAGS='-mod=readonly -buildvcs=false',GOMAXPROCS='2')
    os.umask(0o077)
    out.mkdir(mode=0o700);(out/'commands').mkdir();(out/'runtime/tests').mkdir(parents=True);(out/'runtime/test-tmp').mkdir();(out/'host-tmp').mkdir();(out/'socket').mkdir(mode=0o770);(out/'socket').chmod(0o770)
    base_env['TMPDIR']=str(out/'host-tmp');owner=str(uuid.uuid4());prefix='native21-'+owner.replace('-','')[:16];names={'db':prefix+'-db','package':prefix+'-package','network':prefix+'-network','volume':prefix+'-data'}
    report={'status':'RUNNING','source':args.sha,'scope':'Full21 current synthetic native packages; canonical images supply PG/codecs, not a current application image qualification. No provider/ingestion/minor/production activity.','disk_before':dict(zip(('total','used','free'),shutil.disk_usage(out.parent))),'owner':owner,'resources':names,'commands':[],'packages':[],'cleanup':{},'errors':[]};started=time.monotonic()
    write(out/'ownership.json',{'owner':owner,'label':OWNER_LABEL,'resources':names,'docker_host':'unix:///var/run/docker.sock'})
    report['required_packages']=packages;report['qualification_mode']='affected-catalog-web' if args.affected_catalog_web else 'full21'
    if args.affected_catalog_web:report['scope']='AFFECTED CATALOG+WEB ONLY: two complete current synthetic packages;19 omitted, never full21 or milestone acceptance. Same exact committed export/images/isolation/WAL/cleanup checks.'
    def call(argv,label,timeout=60,env=None,okay=False,maximum=None,progress=False,cleanup=False):
        if INTERRUPTED and not cleanup:raise InterruptedError('signal '+str(INTERRUPTED))
        index=len(report['commands']);stdout=out/'commands'/('%03d-'%index+label+'.stdout');stderr=stdout.with_suffix('.stderr');row={'argv':[str(x)for x in argv],'label':label,'stdout':str(stdout.relative_to(out)),'stderr':str(stderr.relative_to(out))};report['commands'].append(row)
        print('native21 '+label,flush=True);child=None
        try:
            with stdout.open('xb') as dst,stderr.open('xb') as err:
                child=subprocess.Popen(row['argv'],cwd=repo,env=env or base_env,stdout=dst,stderr=err,start_new_session=True);deadline=time.monotonic()+timeout;heartbeat=time.monotonic();offset=0
                while child.poll() is None:
                    if (INTERRUPTED and not cleanup) or time.monotonic()>=deadline:raise TimeoutError(label+' interrupted/timed out')
                    if maximum is not None:need(stdout.stat().st_size<=maximum,label+' output cap exceeded')
                    if progress:
                        with stdout.open('rb') as stream:stream.seek(offset);fresh=stream.read();offset=stream.tell()
                        if fresh:print(fresh.decode('utf8','replace'),end='',flush=True)
                    if time.monotonic()-heartbeat>=30:print('native21 '+label+' still running',flush=True);heartbeat=time.monotonic()
                    time.sleep(.1)
                row['exit']=child.returncode
            need(okay or row['exit']==0,label+' exited '+str(row['exit']))
            if maximum is not None:need(stdout.stat().st_size<=maximum,label+' output cap exceeded')
            return stdout.read_bytes()
        finally:
            if child is not None:
                row['process_group_absent']=stop_group(child);row['exit']=child.returncode
                need(row['process_group_absent'],'owned child process group remains')
            for role,path in [('stdout',stdout),('stderr',stderr)]:
                if path.exists():row[role+'_sha256']=file_hash(path)
            write(out/'commands.json',report['commands'])
    def dk(*argv,**kw):return call([docker,'--host=unix:///var/run/docker.sock',*argv],**kw)
    def object_state(kind,name):
        raw=dk(kind,'inspect',name,label='inspect-'+kind+'-'+name[-8:],okay=True,cleanup=True)
        command=report['commands'][-1];return inspected_object(command['exit'],raw,(out/command['stderr']).read_bytes(),name)
    def owned(kind,name):
        value=object_state(kind,name)
        if value is None:return False
        labels=value.get('Config',{}).get('Labels') if kind=='container' else value.get('Labels')
        need((labels or {}).get(OWNER_LABEL)==owner,'refuse cleanup of nonowned '+kind);return True
    def fixture_storage(label,cleanup=False):
        need(owned('container',names['db']),'storage receipt requires owned DB container')
        raw=dk('exec',names['db'],'df','-B1','--output=size,used,avail,target','/var/lib/postgresql/data',label=label+'-tmpfs',cleanup=cleanup,maximum=4096)
        lines=raw.decode().splitlines();need(len(lines)==2,'one fixture tmpfs row required');parts=lines[1].split()
        need(len(parts)==4 and parts[3]=='/var/lib/postgresql/data' and all(v.isdigit() for v in parts[:3]),'fixed fixture tmpfs fields required')
        total,used,available=map(int,parts[:3]);need(total==1<<30 and 0<=used<=total and 0<=available<=total,'unchanged1GiB fixture ceiling required')
        result={'tmpfs_bytes':total,'used_bytes':used,'available_bytes':available}
        for key,path in [('data','/var/lib/postgresql/data'),('wal','/var/lib/postgresql/data/pg_wal'),('base','/var/lib/postgresql/data/base')]:
            fields=dk('exec',names['db'],'du','-s','-B1',path,label=label+'-'+key,cleanup=cleanup,maximum=4096).decode().split()
            need(len(fields)==2 and fields[0].isdigit() and fields[1]==path,'fixed owned fixture usage row required');result[key+'_allocated_bytes']=int(fields[0])
        report.setdefault('fixture_storage',{})[label]=result
    try:
        resolved=call(['git','-C',repo,'rev-parse',args.sha+'^{commit}'],'source-commit').decode().strip();need(resolved==args.sha,'exact commit required')
        own=call(['git','-C',repo,'show',args.sha+':'+relative],'committed-method');need(own==method.read_bytes(),'commit this exact qualifier before invocation')
        native=call(['git','-C',repo,'show',args.sha+':scripts/qualify-native.sh'],'native-method');need(digest(native)==NATIVE_SHA,'reviewed native21 method changed')
        listing=call(['git','-C',repo,'ls-tree','-rz','--full-tree',args.sha],'git-tree');expected={}
        for row in listing.split(b'\0'):
            if not row:continue
            head,name=row.split(b'\t',1);mode,kind,oid=head.decode().split();need(kind=='blob' and mode in ('100644','100755','120000'),'full source rejects submodules/special modes');expected[name.decode()]=(mode,oid)
        report['tree']=call(['git','-C',repo,'rev-parse',args.sha+'^{tree}'],'source-tree').decode().strip()
        for image in (CODEC_IMAGE,DB_IMAGE):need(dk('image','inspect',image,'--format','{{.Id}}',label='image-'+image[7:15]).decode().strip()==image,'existing canonical image missing; no pull/build')
        report['go']={'path':str(go),'sha256':file_hash(go),'version':call([go,'version'],'go-version').decode().strip()}
        caches=call([go,'env','GOCACHE','GOMODCACHE'],'rolling-caches').decode().splitlines();need(len(caches)==2 and all(Path(p).is_absolute() and Path(p).is_dir() for p in caches),'existing rolling Go caches required');base_env.update(GOCACHE=caches[0],GOMODCACHE=caches[1]);report['caches']=caches
        need(file_hash(Path('/usr/bin/xz'))==XZ_SHA,'existing pinned XZ differs')
        archive=call(['git','-C',repo,'-c','tar.tar.xz.command=/usr/bin/xz --threads=1 --check=crc64 -c','archive','--format=tar.xz','-6',args.sha],'source-archive',timeout=60,maximum=64<<20)
        def archive_timeout(number,frame):raise TimeoutError('source archive decode/extract exceeds60s')
        signal.signal(signal.SIGALRM,archive_timeout);signal.setitimer(signal.ITIMER_REAL,60)
        try:receipt,before=extract_source(archive,out/'source',expected,args.sha)
        finally:signal.setitimer(signal.ITIMER_REAL,0)
        report['archive']={'sha256':digest(archive),'bytes':len(archive),**receipt};write(out/'source-manifest.json',before);source=out/'source';need((source/relative).read_bytes()==own,'exported qualifier differs')
        need((source/'scripts/qualify-native.sh').read_bytes()==native,'exported native method differs')
        entry=(source/'tools/gui-native-family/db-entry.sh').read_text();need(digest(entry.encode())=='b4ed745834cdb9b91ba0291b49dc049e316806b01494b67ff48a0eb8ff9432b4','canonical DB init/cleanup source differs')
        for a,b in [('--auth-host=reject','--auth-host=trust'),("listen_addresses = ''","listen_addresses = '*'"),('55434','5432'),('--no-locale','--locale=C.utf8')]:need(a in entry,'DB fixture anchor missing');entry=entry.replace(a,b)
        entry=fixture_wal_entry(entry)
        call([go,'-C',source/'services/server','build','-trimpath','-o',out/'runtime/tests/social-server','./cmd/social-server'],'current-bootstrap-build',timeout=600,env=dict(base_env,CGO_ENABLED='0'))
        dk('network','create','--internal','--label',OWNER_LABEL+'='+owner,names['network'],label='network-create')
        network=json.loads(dk('network','inspect',names['network'],label='network-before-db'))[0]
        rule=fixture_host_rule(network,owner)
        # initdb only creates loopback HBA rows. Admit the exact fresh private
        # subnet before starting the DB; no host ports or other networks exist.
        need(entry.count('started=1\n')==1,'single DB start anchor required')
        entry=entry.replace('started=1\n',"printf '%s\\n' '"+rule+"' >>\"$data/pg_hba.conf\"\nstarted=1\n")
        entry_path=out/'db-entry.sh';entry_path.write_text(entry);entry_path.chmod(0o644)
        report['fixture_host_admission']=rule
        dk('volume','create','--label',OWNER_LABEL+'='+owner,'--opt','type=tmpfs','--opt','device=tmpfs','--opt','o=size=1g,uid=999,gid=999,mode=0700',names['volume'],label='volume-create')
        common=['--rm','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--network',names['network'],'--label',OWNER_LABEL+'='+owner,'--pull=never']
        dk('run','-d',*common,'--name',names['db'],'--network-alias','native21-db','--user','999:999','--group-add',str((out/'socket').stat().st_gid),'--tmpfs','/tmp:rw,nosuid,nodev,size=64m,uid=999,gid=999,mode=1777','--mount','type=volume,source='+names['volume']+',target=/var/lib/postgresql/data','-v',str(out/'socket')+':/socket','-v',str(entry_path)+':/entry.sh:ro',DB_IMAGE,'bash','-u','-o','pipefail','/entry.sh',label='db-start')
        network=json.loads(dk('network','inspect',names['network'],label='network-state'))[0]
        need(network['Internal'] is True and len(network.get('Containers') or {})<=1,'isolated internal network required')
        db=json.loads(dk('container','inspect',names['db'],label='db-state'))[0]
        need(db['Image']==DB_IMAGE and db['Config']['User']=='999:999' and not db['HostConfig'].get('PortBindings') and db['HostConfig']['NetworkMode']==names['network'],'private canonical DB admission differs')
        deadline=time.monotonic()+45
        while not (out/'socket/ready.json').exists():
            need(not INTERRUPTED and time.monotonic()<deadline,'DB readiness deadline/interruption');time.sleep(.2)
        report['db_ready']=json.loads((out/'socket/ready.json').read_text());need(report['db_ready']['uid']==report['db_ready']['gid']==999,'DB UID/GID differs')
        dk('exec',names['db'],'/usr/lib/postgresql/16/bin/createdb','-h','/socket','-p','5432','-U','app','native21',label='database-create')
        locale=dk('exec',names['db'],'/usr/lib/postgresql/16/bin/psql','-h','/socket','-p','5432','-U','app','-d','native21','-At','-c',"SELECT datctype||'|'||datcollate||'|'||lower('ŞȘŢȚĂÂÎ') FROM pg_catalog.pg_database WHERE datname=current_database()",label='fixture-unicode-locale').decode().strip()
        need(locale=='C.utf8|C.utf8|'+'ŞȘŢȚĂÂÎ'.lower(),'Unicode-aware fixture locale required')
        report['fixture_unicode_locale']=locale
        wal_sql="SELECT json_build_object('max_wal_size',current_setting('max_wal_size'),'min_wal_size',current_setting('min_wal_size'),'checkpoint_timeout',current_setting('checkpoint_timeout'),'fsync',current_setting('fsync'),'full_page_writes',current_setting('full_page_writes'),'synchronous_commit',current_setting('synchronous_commit'))"
        wal=json.loads(dk('exec',names['db'],'/usr/lib/postgresql/16/bin/psql','--no-psqlrc','-h','/socket','-p','5432','-U','app','-d','native21','-v','ON_ERROR_STOP=1','-Atc',wal_sql,label='fixture-durable-checkpoint-settings'))
        need(wal=={'max_wal_size':'128MB','min_wal_size':'32MB','checkpoint_timeout':'30s','fsync':'on','full_page_writes':'on','synchronous_commit':'on'},'normal durable fixture checkpoint settings required')
        report['fixture_durable_checkpoint_settings']=wal
        # The CLI requires a URL and explicit password; this marker is not a
        # credential because the disposable, isolated DB uses trust admission.
        dsn='postgres://app:fixture-only-not-a-credential@native21-db:5432/native21?sslmode=disable'
        dk('run',*common,'--name',names['package'],'--user',str(os.getuid())+':'+str(os.getgid()),'-e','DATABASE_URL='+dsn,'-v',str(out/'runtime/tests')+':/tests:ro','--tmpfs','/tmp:rw,nosuid,nodev,size=64m,mode=1777',CODEC_IMAGE,'/tests/social-server','--migrate-only',label='current-migrate-only',timeout=90)
        fixture_storage('before-tests')
        (out/'shim').mkdir();plan={'source':str(source),'scratch':str(out/'runtime'),'out':str(out),'docker':docker,'codec':CODEC_IMAGE,'owner':owner,'network':names['network'],'package_name':names['package']};write(out/'plan.json',plan)
        shim=out/'shim/docker';shim.write_text('#!/bin/sh\nexec '+shlex_quote(sys.executable)+' -I '+shlex_quote(str(source/relative))+' --docker-wrapper '+shlex_quote(str(out/'plan.json'))+' "$@"\n');shim.chmod(0o755)
        runtime_env=dict(base_env,PATH=str(out/'shim')+':'+base_env['PATH'])
        driver=source/'scripts/qualify-native.sh'
        if args.affected_catalog_web:
            derived,derivation=affected_driver(native,source);driver=out/'affected-driver.sh';driver.write_bytes(derived);driver.chmod(0o700)
            write(out/'affected-driver-derivation.json',derivation);report['affected_driver_sha256']=digest(derived)
        call(['/bin/bash',driver,go,CODEC_IMAGE,names['network'],dsn,out/'runtime'],'affected-catalog-web-complete' if args.affected_catalog_web else 'native21-complete',timeout=len(packages)*(600+660)+600,env=runtime_env,progress=True)
        need(not args.affected_catalog_web or file_hash(driver)==report['affected_driver_sha256'],'affected driver changed during execution')
        for package in packages:
            logfile=out/'runtime/tests'/(package+'.log');text=logfile.read_text();roots=re.findall(r'^--- PASS: (\S+)',text,re.M);named=re.findall(r'^\s*--- PASS: (\S+)',text,re.M)
            need(roots and len(roots)==len(set(roots)) and not re.search(r'--- (SKIP|FAIL):|DATA RACE|^FAIL\b',text,re.M),'package not completely passing: '+package)
            receipt=json.loads((out/('package-'+package+'.json')).read_text());need(receipt['exit']==0,'package container failed');report['packages'].append({'name':package,'roots':len(roots),'named_passes':len(named),'skip':0,'log_sha256':file_hash(logfile),'binary_sha256':file_hash(out/'runtime/tests'/(package+'.test'))})
        need(len(report['packages'])==len(packages) and tree_rows(source,expected)==before,'complete selected packages/read-only source verification failed')
        fixture_storage('after-tests')
        report['status']=pass_status
    except BaseException as error:
        report['errors'].append(type(error).__name__+': '+str(error));report['status']='FAILED'
        try:
            if owned('container',names['db']):
                try:fixture_storage('failed-before-cleanup',cleanup=True)
                except BaseException as storage_error:report['errors'].append('fixture storage diagnostic: '+str(storage_error))
                dk('exec',names['db'],'sh','-c',"grep -E ' (ERROR|FATAL|PANIC|DETAIL|HINT):' /tmp/postgres.log | tail -c 65536",label='failed-owned-fixture-pg-log',okay=True,cleanup=True,maximum=65536)
        except BaseException as diagnostic_error:report['errors'].append('fixture diagnostic: '+str(diagnostic_error))
    finally:
        for kind,key,operation in [('container','package','rm'),('container','db','rm'),('network','network','rm'),('volume','volume','rm')]:
            name=names[key]
            try:
                if kind=='container' and key=='db' and (out/'socket/ready.json').exists():
                    try:
                        (out/'socket/stop-request').touch();dk('wait',name,label='db-graceful-wait',timeout=30,okay=True,cleanup=True)
                        report['pg_cleanup']=json.loads((out/'socket/pg-cleanup.json').read_text())
                        need(all(report['pg_cleanup'].get(k)==v for k,v in {'stop_exit':0,'status_exit':3,'pid_probe_exit':1,'pid_and_socket_files_absent':1}.items()) and report['pg_cleanup']['owned_pid']==report['db_ready']['owned_pid'],'graceful PG cleanup differs')
                    except BaseException as error:report['errors'].append('PG graceful cleanup: '+str(error))
                if owned(kind,name):dk(kind,operation,*(['-f'] if kind=='container' else []),name,label='cleanup-'+key,cleanup=True)
                need(object_state(kind,name) is None,'owned '+key+' remains');report['cleanup'][key]=True
            except BaseException as error:report['cleanup'][key]=False;report['errors'].append('cleanup '+key+': '+str(error))
        if not all(report['cleanup'].values()) or report['errors']:report['status']='FAILED'
        report['seconds']=round(time.monotonic()-started,3);write(out/'packages.json',report.pop('packages'));write(out/'commands.json',report.pop('commands'))
        artifacts=[{'path':str(p.relative_to(out)),'bytes':p.stat().st_size,'sha256':file_hash(p)} for p in sorted(out.rglob('*')) if p.is_file() and not p.is_relative_to(out/'source') and not p.is_relative_to(out/'host-tmp') and not p.is_relative_to(out/'runtime/test-tmp')]
        write(out/'artifact-manifest.json',artifacts);report['manifests']={n:{'path':n,'sha256':file_hash(out/n)} for n in ['source-manifest.json','artifact-manifest.json','commands.json','packages.json'] if (out/n).exists()};report['disk_after']=dict(zip(('total','used','free'),shutil.disk_usage(out.parent)));write(out/'RESULT.json',report);print(json.dumps({'status':report['status'],'result':str(out/'RESULT.json'),'sha256':file_hash(out/'RESULT.json')}),flush=True)
    return 0 if report['status']==pass_status else 1

def shlex_quote(value):
    import shlex
    return shlex.quote(value)

if __name__=='__main__':
    if sys.argv[1:2]==['--docker-wrapper']:raise SystemExit(docker_wrapper(Path(sys.argv[2]),sys.argv[3:]))
    parser=argparse.ArgumentParser();parser.add_argument('--repo',required=True,type=Path);parser.add_argument('--sha',required=True);parser.add_argument('--go',required=True,type=Path);parser.add_argument('--codec-image',required=True);parser.add_argument('--db-image',required=True);parser.add_argument('--out',required=True,type=Path);parser.add_argument('--affected-catalog-web',action='store_true',help='Complete catalog+web only; never full21 acceptance');args=parser.parse_args()
    for number in (signal.SIGINT,signal.SIGTERM):signal.signal(number,signal_received)
    raise SystemExit(execute(args))
