#!/usr/bin/env python3
"""Disabled-until-reviewed fixed-synthetic display/access desktop qualification."""
import sys
sys.dont_write_bytecode = True
import argparse,hashlib,json,os,re,shutil,signal,stat,subprocess,tempfile,time,uuid
from pathlib import Path
METHOD=Path(__file__).absolute().parent
STATES=['display-public-en','display-public-ro','display-signed-dark-ro','display-signed-contrast-en','display-public-empty-csrf-en','access-none-en','access-mixed-ro','access-error-ro']
METHOD_NAMES=['tools/gui-native-display-access/'+n for n in ['qualify.py','desktop-qa.mjs','README.md','export_test.go.txt','source-bindings.json']]
CODE=['qualify.py','desktop-qa.mjs','export_test.go.txt']
SHARED={'tools/gui-native-account-center/qualify.py':'41eff00431eeae4212eb83f11434ac080c6fe5e3d2e45c2de0dac901d3b1f97f','tools/gui-native-language-action/qualify.py':'c69b9025add1fd7c4aeb1ca891894fd38e4c5bf6dd75abe7c8ade7897f3d781d'}
FROZEN_OVERLAYS=[{'bytes': 16962, 'path': 'tools/gui-native-account-center/export_test.go.txt', 'sha256': '1aea74e77a28a0cee9bd23d59d4d7e30d0d4d0259ab8f21e055b182306fdf368'}, {'bytes': 16509, 'path': 'tools/gui-native-notification-inbox/export_test.go.txt', 'sha256': 'fceb971adde713f410aacdcefd143e09ada7a8e1ff8c59cd679ace18ab119289'}, {'bytes': 15643, 'path': 'tools/gui-native-notification-preferences/export_test.go.txt', 'sha256': 'dae147e2f5ae09427a3a1049ef03cb5f6e4cfdb58412e662b7e093f1a7589b62'}, {'bytes': 24769, 'path': 'tools/gui-native-group-detail/fixture-export_test.go.txt', 'sha256': 'c1b742a9e88f316169ddeace5a8aa619870ee3895f0e6e0584953a830309be53'}, {'bytes': 19404, 'path': 'tools/gui-native-group/fixture-export_test.go.txt', 'sha256': 'ac0aac2dbcd999040456d7d9e04ff2fe4a799f0c370ea2e47e5d1e10341b402c'}, {'bytes': 7919, 'path': 'tools/gui-native-family/ui/fixture-export_test.go.txt', 'sha256': '101cc1a1a03cbec5ba27c316c74d711cb55d0e2d11bbfc6df00cca01799e2272'}]
BASELINE_SOURCE='0dbcc6ba753e94b3dee2203f32546692f97f7894'
BASELINE_METHOD='016c5a69e3199da643ad755301e5c78fb8d2e0c7'
ACCOUNT_STATES=['overview-basic-en','overview-guardian-ro','overview-ward-connections-en','overview-escaped-ro','settings-no-token-en','settings-token-ro','settings-expired-record-en','settings-revoke-error-ro']
NATIVE=['services/server/internal/web/display_access_native.go','services/server/internal/web/pages/display-access.gohtml','services/server/internal/web/display_access_native_test.go','static/css/display-access-native.css','services/server/internal/web/renderer.go']
_SHARED={}
def need(ok, message):
    if not ok:
        raise ValueError(message)

def digest(raw):
    return hashlib.sha256(raw).hexdigest()

def regular(path, maximum=64 << 20):
    need(path.is_absolute() and path.resolve() == path, "canonical unaliased input required")
    before = path.lstat()
    need(stat.S_ISREG(before.st_mode) and 0 <= before.st_size <= maximum, "bounded regular input required")
    with path.open("rb") as stream:
        raw = stream.read(maximum + 1)
        after = os.fstat(stream.fileno())
    need(len(raw) == after.st_size <= maximum and
         all(getattr(before, key) == getattr(after, key) for key in
             ("st_dev", "st_ino", "st_mode", "st_size", "st_mtime_ns", "st_ctime_ns")),
         "input changed during read")
    return raw

def executable_digest(path):
    """Hash an explicit installed executable without loading it as a payload."""
    need(path.is_absolute() and path.resolve() == path, "canonical executable required")
    before = path.lstat()
    need(stat.S_ISREG(before.st_mode) and before.st_size > 0 and os.access(path, os.X_OK),
         "existing regular executable required")
    hashed, count = hashlib.sha256(), 0
    with path.open("rb") as stream:
        opened = os.fstat(stream.fileno())
        need((before.st_dev, before.st_ino) == (opened.st_dev, opened.st_ino),
             "executable replaced before read")
        while chunk := stream.read(1 << 20):
            count += len(chunk)
            need(count <= before.st_size, "executable grew during read")
            hashed.update(chunk)
        after = os.fstat(stream.fileno())
    final = path.lstat()
    keys = ("st_dev", "st_ino", "st_mode", "st_size", "st_mtime_ns", "st_ctime_ns")
    need(count == before.st_size and
         all(getattr(before, key) == getattr(opened, key) == getattr(after, key) == getattr(final, key)
             for key in keys), "executable changed during streamed read")
    return hashed.hexdigest()

def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args],
                                   env={"PATH": os.defpath, "LANG": "C.UTF-8"})

def accepted_review(spec, required):
    need(isinstance(spec, dict) and set(spec) == {"path", "sha256", "field", "expected"}, "exact independently accepted source review binding")
    path = Path(spec["path"])
    need(path.is_absolute() and ("_temp" in path.parts or "docs" in path.parts), "owned or canonical review path")
    raw = regular(path); value = json.loads(raw)
    need(digest(raw) == spec["sha256"] and spec["field"] in ("status", "verdict")
         and spec["expected"].startswith("ACCEPT") and "NOT_ACCEPTED" not in spec["expected"]
         and value.get(spec["field"]) == spec["expected"] and value.get("findings") == [], "actual independent accepted review required")
    def scalars(item):
        if isinstance(item, str): return {item}
        if isinstance(item, dict): return set().union(*(scalars(x) for x in item.values()), set())
        if isinstance(item, list): return set().union(*(scalars(x) for x in item), set())
        return set()
    need(set(required) <= scalars(value), "source review must directly contain each exact code digest")

def dependency_admission(paths, binding):
    # Retain the exact bounded readers. Hash every explicit input once and
    # compare the entire receipt BEFORE creating output or executing Go/Node.
    need(all(paths[name].is_dir() for name in ("cache", "modcache", "playwright_package")),
         "existing caches and installed Playwright directory required")
    rows = [{"name": name, "path": str(paths[name]),
             "sha256": digest(regular(paths[name])) if name == "axe_file" else executable_digest(paths[name])}
            for name in ("go", "node", "axe_file", "chrome")]
    package = paths["playwright_package"] / "package.json"
    rows.append({"name": "playwright_package", "path": str(package), "sha256": digest(regular(package))})
    need({row["name"]: row["sha256"] for row in rows} == binding["dependency_sha256"],
         "all exact reviewed Go/Node/Chrome/Axe/Playwright input identities required before process/output")
    return rows

_INTERRUPTED = None
class MethodInterrupted(BaseException):
    pass

def interrupted(signum,frame):
    # Record rather than asynchronously raise across Popen ownership handoff.
    # The bounded wait loop observes this within100ms;all cleanup still runs.
    global _INTERRUPTED
    _INTERRUPTED = signum

def check_interrupted():
    if _INTERRUPTED is not None:raise MethodInterrupted('scoped signal '+str(_INTERRUPTED))

def durable_status(output,report,name='status.json'):
    need(name in ('status.json','result.json'),'literal durable record role')
    temporary=output/('status-'+str(uuid.uuid4())+'.pending')
    raw=(json.dumps(report,indent=2)+'\n').encode()
    with temporary.open('xb')as f:f.write(raw);f.flush();os.fsync(f.fileno())
    os.replace(temporary,output/name)
    fd=os.open(output,os.O_RDONLY|os.O_DIRECTORY)
    try:os.fsync(fd)
    finally:os.close(fd)

def stop_owned_group(child):
    sent=[]
    def alive():
        child.poll() # Reap our leader before testing group absence.
        try:os.killpg(child.pid,0);return True
        except ProcessLookupError:return False
        except PermissionError:return True
    for action in (signal.SIGTERM,signal.SIGKILL):
        if not alive():break
        try:os.killpg(child.pid,action);sent.append(signal.Signals(action).name)
        except ProcessLookupError:pass
        deadline=time.monotonic()+5
        while alive()and time.monotonic()<deadline:time.sleep(.05)
    return child.poll()is not None and not alive(),sent

def command(argv,cwd,env,output,label,timeout,progress):
    stdout,stderr=output/(label+'.stdout'),output/(label+'.stderr')
    row={'argv':[str(v)for v in argv],'pid':None,'owned_process_group':None,'state':'PREPARED','exit_code':None,'timed_out':False,'interrupted':False,'cleanup_proven':True,'signals':[],'stdout':stdout.name,'stderr':stderr.name,'stdout_sha256':None,'stderr_sha256':None}
    child=None
    progress(row) # Durable argv/partial command exists before first spawn.
    try:
        check_interrupted()
        with stdout.open('xb')as out,stderr.open('xb')as err:
            child=subprocess.Popen([str(v)for v in argv],cwd=cwd,env=env,stdout=out,stderr=err,start_new_session=True)
            row.update(pid=child.pid,owned_process_group=child.pid,state='RUNNING',cleanup_proven=False);progress(row)
            deadline=time.monotonic()+timeout
            while child.poll()is None:
                check_interrupted()
                if time.monotonic()>=deadline:
                    row['timed_out']=True;raise subprocess.TimeoutExpired(row['argv'],timeout)
                time.sleep(.1)
            row['exit_code']=child.returncode;check_interrupted()
    except BaseException as error:
        row['interrupted']=isinstance(error,(KeyboardInterrupt,MethodInterrupted))or _INTERRUPTED is not None
        row['state']='INTERRUPTED'if row['interrupted']else'FAILED';row['error_kind']=type(error).__name__
        raise
    finally:
        if child is not None:
            row['cleanup_proven'],row['signals']=stop_owned_group(child);row['exit_code']=child.poll()
        if stdout.exists():row['stdout_sha256']=digest(regular(stdout))
        if stderr.exists():row['stderr_sha256']=digest(regular(stderr))
        if row['state']not in ('INTERRUPTED','FAILED'):row['state']='COMPLETED'if row['exit_code']==0 and row['cleanup_proven']else'FAILED'
        progress(row) # Waited/signal/stream custody survives BaseException.
    return row

def admit(repo,source):
    need(repo.is_absolute() and repo.resolve()==repo and METHOD==repo/'tools/gui-native-display-access','canonical committed new method required')
    b=json.loads(regular(METHOD/'source-bindings.json'))
    need(b.get('status')=='BOUND_REVIEWED_COMMITTED_METHODS' and b.get('producer_source')==source and re.fullmatch('[0-9a-f]{40}',source or '') and re.fullmatch('[0-9a-f]{40}',b.get('closing_source')or ''),'disabled new source refuses before imports/children/output')
    need(set(b['method_sha256'])==set(CODE) and all(digest(regular(METHOD/n))==b['method_sha256'][n]for n in CODE),'all three current code images required')
    accepted_review(b['method_source_review'],list(b['method_sha256'].values())+list(SHARED.values())+[digest(regular(METHOD/'README.md'))])
    review=json.loads(regular(Path(b['method_source_review']['path'])))
    payload_binding=dict(b);payload_binding['method_source_review']=None
    payload={'CLI':review['CLI'],'binding':payload_binding,'method_nonbinding_image_sha256':dict(b['method_sha256'],**{'README.md':digest(regular(METHOD/'README.md'))})}
    need(review['P_sha256']==digest((json.dumps(payload,sort_keys=True,separators=(',',':'),ensure_ascii=False)+'\n').encode()),'exact canonical null-selfref operational P required')
    need(b['benchmark_requested']is False,'this finite method never requests benchmark')
    return b

def shared(repo):
    # Execute ONLY cached,hash-pinned stdlib method definitions under a non-main
    # name. Never invoke the old qualifier/bridge/export entrypoints for S.
    if not _SHARED:
        for key,(relative,wanted)in zip(['account','language'],SHARED.items()):
            path=repo/relative;raw=regular(path);need(digest(raw)==wanted,'exact approved shared Python source')
            scope={'__file__':str(path),'__name__':'_display_access_pinned_'+key}
            exec(compile(raw,str(path),'exec'),scope)
            _SHARED[key]=scope
    return _SHARED

def source_bridge(repo,source):
    check_interrupted();b=admit(repo,source)
    need(not git(repo,'status','--porcelain','--untracked-files=all'),'clean committed current source')
    git(repo,'merge-base','--is-ancestor',b['closing_source'],source);git(repo,'merge-base','--is-ancestor',source,'HEAD')
    allowed=set(METHOD_NAMES)|{'STATUS.md','docs/execplans/gui-native-display-access-preferences.md'}
    changed=[x.decode()for x in git(repo,'diff','--no-renames','--name-only','-z',source,'HEAD').split(b'\0')if x]
    need(set(changed)<=allowed,'only new five methods/two named docs may bridge;old account and all product exact')
    tree={}
    for line in git(repo,'ls-tree','-rz',source).split(b'\0'):
        if line:head,name=line.split(b'\t',1);tree[name.decode()]=head.decode().split()
    names=[x.decode()for x in git(repo,'ls-files','-z').split(b'\0')if x]
    products=sorted(set(names)-allowed);need(len(products)>2000 and set(products)==set(tree)-allowed,'entire current producer product path closure')
    # Exact raw Git blob/mode checks for EVERY product path,not an old epoch's
    # bridge or a selected-field hash. This includes every old helper/method.
    for name in products:
        check_interrupted();raw=regular(repo/name);mode,kind,oid=tree[name]
        need(kind=='blob' and oid==hashlib.sha1(('blob '+str(len(raw))+'\0').encode()+raw).hexdigest() and bool((repo/name).stat().st_mode&0o111)==(mode=='100755'),'complete new product bytes/Git mode differ')
    for name in METHOD_NAMES:need(name in names and git(repo,'show','HEAD:'+name)==regular(repo/name),'five exact committed method images required')
    for path,wanted in SHARED.items():need(digest(regular(repo/path))==wanted,'old Python helper changed')
    need(b['unchanged_overlay_helpers']==FROZEN_OVERLAYS,'exact frozen account exporter plus original five overlays')
    for row in b['unchanged_overlay_helpers']:
        raw=regular(repo/row['path']);need(len(raw)==row['bytes']and digest(raw)==row['sha256']and git(repo,'show',source+':'+row['path'])==raw,'all six exact old overlays required')
    need(digest(regular(repo/'tools/gui-native-notification-inbox/qualify-pg.py'))==b['complete7_driver_sha256'],'actual in-producer complete7 driver')
    accepted_review(b['native_preferences_source_review'],[digest(regular(repo/p))for p in NATIVE])
    return {'producer_source':source,'ui_head':git(repo,'rev-parse','HEAD').decode().strip(),'UI_only_changed_paths':changed,'product_paths':products,'source_paths':sorted(set(products)|set(METHOD_NAMES)),'scope':'Full new producer Git byte/mode closure excluding only five new methods/two named docs;no old bridge approval reused'}

def baseline45(repo,fixtures,manifest,b):
    common=shared(repo)
    prior=manifest['previous_body_candidates'];need(len(prior)==45,'all45 actual newly generated prior candidates')
    old37=common['account']['physical_baseline'](repo,fixtures,{'previous_body_candidates':prior[:37]})
    # Unchanged reviewed language compare_bodies verifies all2587 e31 physical
    # PAX/memberbytes plus actual M016c eight bodies and old37 physical proof.
    baseline=b['accepted_prior45'];need(baseline['language_product_source']==BASELINE_SOURCE,'old accepted baseline is not new producer authority')
    old=common['language']['compare_bodies'](baseline)
    need(old['total']==45 and old['all2587_original_PAX_and_bytes_rechecked']is True,'entire e31/currentacceptedUI baseline')
    current=json.loads(regular(Path(baseline['account_run']['path'])/'result.json'))
    need(current['UI_method_source']==BASELINE_METHOD,'exact accepted account method epoch')
    for key in ['companion_review','feature_review']:
        spec=b['prior45_authorities'][key];accepted_review(spec,[BASELINE_SOURCE,BASELINE_METHOD,'426117d2dccb0db187841c223e6423ba329d3bbd91e414f24b214e5adca0394e'])
    rows=list(old37['rows'])
    need([(r['kind'],r['name'])for r in prior[37:]]==[('account-center',n)for n in ACCOUNT_STATES],'exact eight additional current prior identities/order')
    for row,want in zip(prior[37:],old['new8_literal_account_bodies']):
        need(row['path']=='account-center/'+row['name']+'.html','literal account body path')
        raw=regular(fixtures/row['path']);oldraw=regular(Path(baseline['account_run']['path'])/'fixtures'/(row['name']+'.html'))
        need(raw==oldraw and len(raw)==row['bytes']==want['bytes']and digest(raw)==row['sha256']==want['sha256'],'all eight newly rendered prior bodies literally equal e31/currentUI')
        rows.append(dict(row,equal=True,baseline_member=want['baseline_member'],baseline_original='accepted-e31:'+want['baseline_member']))
    return dict(old37,rows=rows,count=45,literal_byte_equal=True,account_archive_sha256=old['account_archive_sha256'],account_archive_manifest_sha256=old['account_archive_manifest_sha256'],account_full_physical_PAX_and_bytes=True,accepted_account_result_sha256=baseline['account_run']['result_sha256'])

def main():
    p=argparse.ArgumentParser(description=__doc__)
    for name in ['repo','scratch','go','node','cache','modcache','playwright-package','axe-file','chrome','pg-run']:p.add_argument('--'+name,type=Path)
    p.add_argument('--producer-source',required=True);p.add_argument('--check-bridge',action='store_true');args=p.parse_args()
    b=admit(args.repo,args.producer_source);check_interrupted()
    if args.check_bridge:print(json.dumps(source_bridge(args.repo,args.producer_source)));return 0
    actual_cli={'--'+name.replace('_','-'):str(getattr(args,name))for name in ['repo','scratch','go','node','cache','modcache','playwright_package','axe_file','chrome','pg_run','producer_source']}
    method_review=json.loads(regular(Path(b['method_source_review']['path'])))
    need(method_review['CLI']==actual_cli,'actual full CLI must equal independently reviewed P')
    need(all(getattr(args,n)is not None for n in ['scratch','go','node','cache','modcache','playwright_package','axe_file','chrome','pg_run']),'all explicit admitted inputs required')
    paths={}
    for n in ['go','node','cache','modcache','playwright_package','axe_file','chrome']:
        value=getattr(args,n);need(value.is_absolute(),'explicit absolute path');paths[n]=value.resolve(strict=True)
    dependencies=dependency_admission(paths,b) # BEFORE any shared import/child/output
    bridge_before=source_bridge(args.repo,args.producer_source);head=bridge_before['ui_head'];common=shared(args.repo)
    pg=common['account']['qualified_pg'](args.repo,args.pg_run,args.producer_source,b)
    scratch=args.scratch;need(scratch.is_absolute()and scratch.resolve()==scratch and scratch.is_dir()and scratch.stat().st_uid==os.getuid()and '_temp'in scratch.parts and not scratch.is_relative_to(args.repo),'owned task scratch required')
    need(shutil.disk_usage(scratch).free>1<<30,'one GiB free scratch required')
    check_interrupted()
    output=scratch/('run-'+args.producer_source[:12]+'-'+str(uuid.uuid4()));output.mkdir(mode=0o700)
    temp=Path(tempfile.mkdtemp(prefix='da-',dir=scratch));env={'PATH':os.defpath,'LANG':'C.UTF-8','LC_ALL':'C.UTF-8','HOME':str(temp),'TMPDIR':str(temp),'GOCACHE':str(paths['cache']),'GOMODCACHE':str(paths['modcache']),'GOTOOLCHAIN':'local','GOENV':'off','GOWORK':'off','GOPROXY':'off','GOSUMDB':'off','GOFLAGS':'-p=2','GOMAXPROCS':'2'}
    report={'schema':1,'status':'RUNNING','PG_producer_source':args.producer_source,'UI_method_source':head,'source_bridge':bridge_before,'qualified_PG':pg,'admitted_dependencies_before_process_output':dependencies,'benchmark_requested':False,'benchmark':{'status':'NOT_REQUESTED'},'scope':'Eight fixed synthetic preference states,JS/noJS/21strictAxe/45literalpriorbodies;no actions/realpreferences/identity/benchmark','commands':[],'failures':[],'owned_temp':{'path':str(temp),'removed':False}}
    attempted=False;closed=False
    def progress(row):
        if not any(old is row for old in report['commands']):report['commands'].append(row)
        report['active_command']=row['stdout']if row['state']=='RUNNING'else None
        durable_status(output,report)
    def run(argv,cwd,label,timeout):
        row=command(argv,cwd,env,output,label,timeout,progress);need(row['exit_code']==0 and not row['timed_out']and row['cleanup_proven'],label+' failed;retain exact raw/custody');return row
    try:
        check_interrupted()
        run([paths['go'],'version'],args.repo,'go-version',10);gv=regular(output/'go-version.stdout').decode().strip()
        run([paths['node'],'--version'],args.repo,'node-version',10);nv=regular(output/'node-version.stdout').decode().strip()
        check_interrupted();need(re.search(r'\bgo1\.27\.1\b',gv)and re.match(r'^v24\.',nv),'exact Go1.27.1/Node24 required');need(dependency_admission(paths,b)==dependencies,'dependency drift')
        fixtures=output/'fixtures';overlay=output/'overlay.json'
        replacements={'display_access_ui_qa_overlay_test.go':METHOD/'export_test.go.txt'}
        oldnames=['account_center_ui_qa_overlay_test.go','notification_inbox_ui_qa_overlay_test.go','notification_preferences_ui_qa_overlay_test.go','group_detail_ui_qa_overlay_test.go','group_ui_qa_overlay_test.go','family_ui_qa_overlay_test.go']
        for name,row in zip(oldnames,b['unchanged_overlay_helpers']):replacements[name]=args.repo/row['path']
        mapping={}
        for name,implementation in replacements.items():
            virtual=args.repo/'services/server/internal/web'/name;need(not virtual.exists()and not virtual.is_symlink(),'absent virtual source only');regular(implementation);mapping[str(virtual)]=str(implementation)
        with overlay.open('x')as f:json.dump({'Replace':mapping},f);f.write('\n')
        run([paths['go'],'test','-mod=readonly','-overlay='+str(overlay),'-count=1','-v','./internal/web','-run','^TestSocialDisplayAccessQAExport$','-args','-display-access-ui-producer-source',args.producer_source,'-display-access-ui-qa-output-dir',fixtures],args.repo/'services/server','export',180)
        raw=regular(output/'export.stdout').decode();need('=== RUN   TestSocialDisplayAccessQAExport'in raw and '--- PASS: TestSocialDisplayAccessQAExport ('in raw and not re.search(r'--- (FAIL|SKIP):',raw)and re.search(r'^ok\s+\S+\s+[0-9.]+s$',raw,re.M),'unskipped export/package PASS')
        mraw=regular(fixtures/'fixtures.json');manifest=json.loads(mraw);need(manifest['PG_producer_source']==args.producer_source and manifest['UI_method_source']==head and manifest['source_unchanged']is True and manifest['sources_before']==manifest['sources_after']and[r['name']for r in manifest['states']]==STATES,'full current export frame')
        for row in manifest['states']:
            raw=regular(fixtures/(row['name']+'.html'));need(len(raw)==row['bytes']and digest(raw)==row['sha256'],'actual new fixture bytes')
        proof=baseline45(args.repo,fixtures,manifest,b);proof_path=output/'prior-body-equality.json';proof_path.write_text(json.dumps(proof,indent=2)+'\n');report['previous_body_equality']=proof;report['fixture_manifest_sha256']=digest(mraw)
        attempted=True
        run([paths['node'],METHOD/'desktop-qa.mjs','--repo',args.repo,'--producer-source',args.producer_source,'--fixtures',fixtures,'--baseline-proof',proof_path,'--output',output/'desktop','--playwright-package',paths['playwright_package'],'--axe-file',paths['axe_file'],'--chrome',paths['chrome'],'--go',paths['go'],'--node',paths['node']],args.repo,'desktop',600)
        draw=regular(output/'desktop/desktop-results.json');actual=json.loads(draw)
        closed=actual['ownership']['serverStopped']is True and len(actual['ownership']['profiles'])==2 and all(x['closed']is True and x['removed']is True for x in actual['ownership']['profiles'])
        need(actual['status']=='pass'and actual['failures']==[]and actual['sourceUnchanged']is True and[r['name']for r in actual['states']]==STATES and[r['name']for r in actual['noJavaScriptStates']]==STATES and closed,'all8+8strict desktop andcleanup required')
        need(sum(len(r['audits'])for r in actual['states'])==13 and sum(len(r['audits'])for r in actual['noJavaScriptStates'])==8,'exact21unique strict wholeDoc audits required')
        need(source_bridge(args.repo,args.producer_source)==bridge_before,'source bridge drift');common['account']['qualified_pg'](args.repo,args.pg_run,args.producer_source,b)
        for path in tuple(common['language']['SEEN']):common['language']['file_hash'](path)
        need(dependency_admission(paths,b)==dependencies,'final exact dependency drift');report['desktop_report_sha256']=digest(draw);report['status']='PASS'
    except BaseException as error:
        report['status']='FAIL';report['interrupted']=isinstance(error,(KeyboardInterrupt,MethodInterrupted))or _INTERRUPTED is not None;report['failures'].append(type(error).__name__+': '+str(error));durable_status(output,report)
    finally:
        try:
            if attempted and not closed and (output/'desktop/desktop-results.json').exists():
                custody=json.loads(regular(output/'desktop/desktop-results.json'))['ownership'];closed=custody['serverStopped']is True and len(custody['profiles'])==2 and all(x['closed']is True and x['removed']is True for x in custody['profiles'])
            groups_closed=all(row.get('cleanup_proven')is True for row in report['commands'])
            report['all_owned_process_groups_cleanup_proven']=groups_closed
            if groups_closed and (not attempted or closed):shutil.rmtree(temp);report['owned_temp']['removed']=not temp.exists()
            else:report['failures'].append('cleanup unproven;exact owned temp retained')
        except BaseException as error:report['failures'].append('owned cleanup failed:'+type(error).__name__)
        artifacts=[]
        for file in sorted(output.rglob('*')):
            if file.is_symlink():report['failures'].append('output symlink retained');continue
            if file.is_file()and file.name not in ['result.json','status.json']:
                raw=regular(file);artifacts.append({'path':file.relative_to(output).as_posix(),'bytes':len(raw),'sha256':digest(raw)})
        report['artifacts']=artifacts
        if _INTERRUPTED is not None:report['failures'].append('scoped signal interruption');report['interrupted']=True
        if report['failures']:report['status']='FAIL'
        durable_status(output,report,'result.json');result_sha=digest(regular(output/'result.json'));durable_status(output,{'status':report['status'],'result_sha256':result_sha});print(json.dumps({'status':report['status'],'output':str(output),'result_sha256':result_sha}))
    return 0 if report['status']=='PASS'else 1

if __name__=='__main__':
    signal.signal(signal.SIGINT,interrupted);signal.signal(signal.SIGTERM,interrupted)
    sys.exit(main())
