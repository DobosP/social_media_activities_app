// Isolated presentation method: no credentials, provider or real mutation endpoint.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import http from 'node:http';
import crypto from 'node:crypto';
import zlib from 'node:zlib';
import {createRequire} from 'node:module';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {isDeepStrictEqual} from 'node:util';

const option=name=>{const i=process.argv.indexOf(name);assert.ok(i>=2&&process.argv[i+1],'explicit '+name+' required');return path.resolve(process.argv[i+1]);};
const repo=option('--repo'),fixtures=option('--fixtures'),output=option('--output'),playwright=option('--playwright-package'),axeFile=option('--axe-file'),chrome=option('--chrome'),baselineProofPath=option('--baseline-proof'),goFile=option('--go'),nodeFile=option('--node');
const producerIndex=process.argv.indexOf('--producer-source'),producerSource=process.argv[producerIndex+1];assert.ok(producerIndex>=2&&/^[0-9a-f]{40}$/.test(producerSource));
const hash=raw=>crypto.createHash('sha256').update(raw).digest('hex'),hashFile=file=>hash(fs.readFileSync(file));
const regular=file=>{assert.equal(fs.realpathSync(file),file);assert.ok(fs.lstatSync(file).isFile());return fs.readFileSync(file);};
// Disabled/source/dependency admission is independent of the parent Python
// driver. Only builtin reads/hashes occur before the first child/import/output.
const methodScript=fileURLToPath(import.meta.url);
assert.equal(fs.realpathSync(methodScript),methodScript);assert.equal(methodScript,path.join(repo,'tools/gui-native-display-access/desktop-qa.mjs'));
assert.equal(fs.realpathSync(repo),repo);
const admissionBinding=JSON.parse(regular(path.join(repo,'tools/gui-native-display-access/source-bindings.json')));
assert.equal(admissionBinding.status,'BOUND_REVIEWED_COMMITTED_METHODS');assert.equal(admissionBinding.producer_source,producerSource);assert.match(admissionBinding.closing_source||'',/^[0-9a-f]{40}$/);
// Independent standalone code/P admission BEFORE any child/import/output.
const methodRoot=path.join(repo,'tools/gui-native-display-access'),codeNames=['qualify.py','desktop-qa.mjs','export_test.go.txt'],codeBuffers=new Map();
assert.deepEqual(Object.keys(admissionBinding.method_sha256).sort(),[...codeNames].sort());
for(const name of codeNames){const raw=regular(path.join(methodRoot,name));assert.equal(hash(raw),admissionBinding.method_sha256[name],'new method code admission differs: '+name);codeBuffers.set(name,raw);}
const readmeBuffer=regular(path.join(methodRoot,'README.md')),ownReview=admissionBinding.method_source_review;
assert.deepEqual(Object.keys(ownReview).sort(),['expected','field','path','sha256']);assert.ok(path.isAbsolute(ownReview.path)&&(/\/_temp\//.test(ownReview.path)||/\/docs\//.test(ownReview.path)));
const ownReviewBuffer=regular(ownReview.path);assert.equal(hash(ownReviewBuffer),ownReview.sha256);
const operational=JSON.parse(ownReviewBuffer);assert.ok(['status','verdict'].includes(ownReview.field)&&ownReview.expected.startsWith('ACCEPT')&&!ownReview.expected.includes('NOT_ACCEPTED'));assert.equal(operational[ownReview.field],ownReview.expected);assert.deepEqual(operational.findings,[]);
const scalarStrings=v=>typeof v==='string'?[v]:Array.isArray(v)?v.flatMap(scalarStrings):v&&typeof v==='object'?Object.values(v).flatMap(scalarStrings):[];
const admittedScalars=new Set(scalarStrings(operational));
for(const digest of[...codeBuffers.values()].map(hash).concat([hash(readmeBuffer),'41eff00431eeae4212eb83f11434ac080c6fe5e3d2e45c2de0dac901d3b1f97f','c69b9025add1fd7c4aeb1ca891894fd38e4c5bf6dd75abe7c8ade7897f3d781d']))assert.ok(admittedScalars.has(digest),'operational review must contain exact code/README/shared helper digests');
const sortedJSON=value=>Array.isArray(value)?value.map(sortedJSON):value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(key=>[key,sortedJSON(value[key])])):value;
const nullBinding=structuredClone(admissionBinding);nullBinding.method_source_review=null;
const admittedCLI=operational.CLI,cliKeys=['--repo','--scratch','--go','--node','--cache','--modcache','--playwright-package','--axe-file','--chrome','--pg-run','--producer-source'];
assert.deepEqual(Object.keys(admittedCLI).sort(),cliKeys.sort());for(const name of cliKeys)assert.ok(typeof admittedCLI[name]==='string'&&admittedCLI[name]);
for(const[name,value]of[['--repo',repo],['--producer-source',producerSource],['--go',goFile],['--node',nodeFile],['--playwright-package',playwright],['--axe-file',axeFile],['--chrome',chrome]])assert.equal(admittedCLI[name],value,'standalone operational CLI pin '+name);
assert.ok(path.isAbsolute(admittedCLI['--scratch'])&&output.startsWith(admittedCLI['--scratch']+path.sep),'reviewed owned scratch/output prefix');
const admittedP={CLI:admittedCLI,binding:nullBinding,method_nonbinding_image_sha256:{...admissionBinding.method_sha256,'README.md':hash(readmeBuffer)}};
assert.equal(hash(Buffer.from(JSON.stringify(sortedJSON(admittedP))+'\n')),operational.P_sha256,'exact standalone canonical null-selfref P');
assert.equal(fs.realpathSync(process.execPath),nodeFile,'explicit Node must be the actual executing interpreter');
const dependencyHash=(file,executable)=>{
 assert.equal(fs.realpathSync(file),file);const before=fs.lstatSync(file,{bigint:true});assert.ok(before.isFile());if(executable)fs.accessSync(file,fs.constants.X_OK);else assert.ok(before.size<=64n*1024n*1024n);
 const keys=['dev','ino','mode','uid','gid','size','mtimeNs','ctimeNs','nlink'],same=info=>assert.ok(keys.every(key=>info[key]===before[key]),'dependency changed during exact read');
 const fd=fs.openSync(file,fs.constants.O_RDONLY|fs.constants.O_NOFOLLOW),digest=crypto.createHash('sha256');let count=0n;
 try{same(fs.fstatSync(fd,{bigint:true}));const buffer=Buffer.alloc(1<<20);for(;;){const n=fs.readSync(fd,buffer,0,buffer.length,null);if(!n)break;count+=BigInt(n);assert.ok(count<=before.size);digest.update(buffer.subarray(0,n));}same(fs.fstatSync(fd,{bigint:true}));assert.equal(count,before.size);}finally{fs.closeSync(fd);}
 same(fs.lstatSync(file,{bigint:true}));return digest.digest('hex');
};
assert.equal(fs.realpathSync(playwright),playwright);assert.ok(fs.lstatSync(playwright).isDirectory());
const dependencyAdmission=[['go',goFile,true],['node',nodeFile,true],['axe_file',axeFile,false],['chrome',chrome,true],['playwright_package',path.join(playwright,'package.json'),false]].map(([name,file,executable])=>({name,path:file,sha256:dependencyHash(file,executable)}));
assert.deepEqual(Object.fromEntries(dependencyAdmission.map(row=>[row.name,row.sha256])),admissionBinding.dependency_sha256,'all five reviewed inputs must match before any child/module/output');
// The qualifier path supplies __file__/argv identity only. Executed source
// is the admitted immutable cached stdin buffer,never a second mutable read.
const immutableQualifierRunner="import sys; p=sys.argv[1]; source=sys.stdin.buffer.read(1048577); assert len(source)<=1048576; sys.argv=[p]+sys.argv[2:]; exec(compile(source,p,'exec'),{'__file__':p,'__name__':'__main__'})";
const bridge=()=>JSON.parse(execFileSync('python3',['-I','-c',immutableQualifierRunner,path.join(methodRoot,'qualify.py'),'--repo',repo,'--producer-source',producerSource,'--check-bridge'],{encoding:'utf8',input:codeBuffers.get('qualify.py'),timeout:120000,env:{...process.env,PATH:'/usr/bin:/bin',LANG:'C.UTF-8',LC_ALL:'C.UTF-8'}}));
const bridgeBefore=bridge();
assert.ok(fixtures.includes('/_temp/')&&output.includes('/_temp/')&&!fs.existsSync(output));assert.equal(fs.realpathSync(fixtures),fixtures);
const manifestPath=path.join(fixtures,'fixtures.json'),manifest=JSON.parse(regular(manifestPath));
const stateNames=['display-public-en','display-public-ro','display-signed-dark-ro','display-signed-contrast-en','display-public-empty-csrf-en','access-none-en','access-mixed-ro','access-error-ro'];
assert.deepEqual(manifest.states.map(row=>row.name),stateNames);assert.equal(manifest.PG_producer_source,producerSource);assert.equal(manifest.UI_method_source,bridgeBefore.ui_head);
assert.equal(manifest.source_unchanged,true);assert.deepEqual(manifest.sources_before,manifest.sources_after);
const snapshot=()=>manifest.sources_after.map(row=>{assert.ok(typeof row.path==='string'&&!path.isAbsolute(row.path)&&!row.path.split('/').includes('..'));const raw=regular(path.join(repo,row.path));return{path:row.path,bytes:raw.length,sha256:hash(raw)};});
const sourcesBefore=snapshot();assert.deepEqual(sourcesBefore,manifest.sources_after.map(({path,bytes,sha256})=>({path,bytes,sha256})));
const baselineProof=JSON.parse(regular(baselineProofPath)),previous=baselineProof.rows;
assert.equal(baselineProof.archive_sha256,'bc10a80ae1a9b0f404e9434f2a0065d941a0cb4a336d5ea06eb39032da09c7ea');assert.equal(baselineProof.archive_manifest_sha256,'2787ff123c6b333d5aeed9650c22cdab92b8e2a76f189085a4eb52c5f3bc21d4');assert.equal(baselineProof.literal_byte_equal,true);assert.equal(previous.length,45);assert.equal(new Set(previous.map(row=>row.kind+':'+row.name)).size,45);
for(const[kind,count]of[['family',8],['group-create',10],['group-detail',7],['notification-preferences',6],['notification-inbox',6],['account-center',8]])assert.equal(previous.filter(row=>row.kind===kind).length,count);
for(const row of previous){assert.equal(row.equal,true);assert.match(row.path,/^(?:family|group-create|group-detail|notification-preferences|notification-inbox|account-center)\/[a-z-]+\.html$/);assert.equal(row.path,row.kind+'/'+row.name+'.html');const raw=regular(path.join(fixtures,row.path));assert.equal(raw.length,row.bytes);assert.equal(hash(raw),row.sha256);assert.ok(row.baseline_member&&row.baseline_original);}
assert.equal(manifest.baseline_keeper_sha256,baselineProof.archive_sha256);assert.equal(baselineProof.account_archive_sha256,'e31b656ba310cb09639a6ab7e1a2a2dc8d305e17ae7a1f30ce3c8bc5ca3bee26');assert.equal(baselineProof.account_archive_manifest_sha256,'6a0c5c50e3c8e77f46a36becc9617f2120476716350354b447716ee29089af8f');assert.equal(baselineProof.account_full_physical_PAX_and_bytes,true);
const normalization=manifest.normalization;assert.equal(normalization.schema,1);assert.equal(normalization.kind,'render-nonce-only');assert.equal(normalization.replacement,'synthetic-display-access-ui-nonce');assert.deepEqual(normalization.states.map(row=>row.name),stateNames);
for(const row of normalization.states){const state=manifest.states.find(s=>s.name===row.name);assert.match(row.raw_html_sha256,/^[0-9a-f]{64}$/);assert.equal(row.normalized_html_sha256,state.sha256);assert.equal(row.attribute_replacements,2);assert.equal(row.csp_replacements,1);}
const states=new Map(manifest.states.map(row=>[row.name,row]));
for(const state of manifest.states){const raw=regular(path.join(fixtures,state.name+'.html'));assert.equal(raw.length,state.bytes);assert.equal(hash(raw),state.sha256);assert.ok(!raw.includes(Buffer.from('PRIVATE-')));}
const served=new Map(),servedIdentity=[];
const sourceHash=file=>sourcesBefore.find(row=>path.join(repo,row.path)===file)?.sha256;
const cache=(url,file,wanted)=>{const raw=regular(file);assert.match(wanted||'',/^[0-9a-f]{64}$/);assert.equal(hash(raw),wanted,'frozen served input differs from admitted bytes');served.set(url,raw);servedIdentity.push({url,file,bytes:raw.length,sha256:wanted});};
for(const state of manifest.states)cache('/preview/'+state.name+'/',path.join(fixtures,state.name+'.html'),state.sha256);
for(const name of['css/base.css','css/display-access-native.css','js/hovercard.js','js/site.js','fonts/bricolage-grotesque-latin-wght-normal.woff2','fonts/bricolage-grotesque-latin-ext-wght-normal.woff2'])cache('/static/'+name,path.join(repo,'static',name),sourceHash(path.join(repo,'static',name)));
cache('/qa/axe.js',axeFile,admissionBinding.dependency_sha256.axe_file);cache('/sw.js',path.join(repo,'services/server/internal/web/assets/meetups-worker.js'),sourceHash(path.join(repo,'services/server/internal/web/assets/meetups-worker.js')));
const {initialColorsEqualSettled}=await import('../gui-native-family/ui/color-stability.mjs');
const require=createRequire(import.meta.url);const {chromium}=require(playwright);
fs.mkdirSync(output,{mode:0o700});
const report={schema:1,status:'RUNNING',scope:'Eight fixed synthetic preference fixtures,eight no-JS/21strict whole-document Axe/45physical priorbodies. FormData observation only; no token/session/export capture, language-save, action invocation, authenticated HTTP or production claim.',
  viewport:{width:1280,height:720},PGProducerSource:producerSource,gitBridgeBefore:bridgeBefore,fixtureManifestSHA256:hashFile(manifestPath),previousBodyEquality:previous,normalization,sourcesBefore,
  setup:{node:process.version,scriptSHA256:hashFile(fileURLToPath(import.meta.url)),playwrightVersion:JSON.parse(regular(path.join(playwright,'package.json'))).version,dependencyAdmission,axeSHA256:hashFile(axeFile),chrome},
  states:[],noJavaScriptStates:[],requests:[],assetResponses:[],requestFailures:[],pageErrors:[],consoleErrors:[],cspConsole:[],webSockets:[],fixturePolicyEnforcement:[],mockEndpoints:[],failures:[],ownership:{profiles:[],serverStopped:false}};
const check=(ok,message)=>{if(!ok)report.failures.push(message);};
const contexts=[],pending=new Set();let base,current='setup';const flushResponses=async()=>{while(pending.size)await Promise.all([...pending]);};
const server=http.createServer((request,response)=>{
  try{
    const url=new URL(request.url,'http://fixture.invalid');
    if(request.method!=='GET'){check(false,current+': mutation requested');response.writeHead(405);response.end('Isolated readonly fixture');return;}
    let raw,type;
    if(url.pathname==='/qa/axe.js'){raw=served.get('/qa/axe.js');type='text/javascript; charset=utf-8';}
    else if(url.pathname==='/favicon.ico'){response.writeHead(204);response.end();return;}
    else if(url.pathname.startsWith('/static/')){
      const name=decodeURIComponent(url.pathname.slice(8));assert.ok(name&&!path.isAbsolute(name)&&!name.includes('\\')&&!name.split('/').includes('..'));
      raw=served.get('/static/'+name);assert.ok(raw,'exact frozen asset allowlist');type=name.endsWith('.css')?'text/css':name.endsWith('.js')?'text/javascript; charset=utf-8':name.endsWith('.woff2')?'font/woff2':'application/octet-stream';
    }else if(url.pathname==='/sw.js'){
      raw=served.get('/sw.js');type='text/javascript; charset=utf-8';
      report.mockEndpoints.push({state:current,path:url.pathname,scope:'Exact current offline worker in isolated owned profile; no authenticated cache data',sha256:hash(raw)});
    }else{
      const match=/^\/preview\/([a-z-]+)\/$/.exec(url.pathname);assert.ok(match&&states.has(match[1]));const state=states.get(match[1]);
      raw=served.get('/preview/'+state.name+'/');assert.equal(hash(raw),state.sha256);type='text/html; charset=utf-8';
      for(const[name,value]of Object.entries(state.renderer_headers)){assert.ok(!/^set-cookie$/i.test(name));response.setHeader(name,value);}
      const actualMode=Object.keys(state.renderer_headers).find(name=>name.toLowerCase()==='content-security-policy')||Object.keys(state.renderer_headers).find(name=>name.toLowerCase()==='content-security-policy-report-only');
      assert.ok(actualMode);response.setHeader('Content-Security-Policy',state.renderer_headers[actualMode]);
      report.fixturePolicyEnforcement.push({state:state.name,actualRendererHeader:actualMode,enforcedSamePolicyForIsolatedFixture:true});
    }
    response.setHeader('Content-Type',type);if(!response.hasHeader('Cache-Control'))response.setHeader('Cache-Control','private, no-store');response.setHeader('X-Content-Type-Options','nosniff');response.end(raw);
  }catch(error){check(false,current+': unadmitted fixture/asset path');response.writeHead(404);response.end('Unknown fixture asset');}
});

function watch(page, label) {
  page.on('request', request => {
    const url = new URL(request.url());
    report.requests.push({state: label(), method: request.method(), path: url.pathname, local: url.origin === base, resourceType: request.resourceType()});
  });
  page.on('response', response => {
    const state = label(), url = new URL(response.url()), type = response.request().resourceType();
    check(response.status() < 400, state + ': bad response ' + url.pathname + ' ' + response.status());
    if (type === 'document') return;
    const task = (async () => {
      try {const raw = await response.body(); report.assetResponses.push({state, path: url.pathname, resourceType: type, status: response.status(), bytes: raw.length, sha256: hash(raw)});}
      catch (error) {check(false, state + ': asset body unavailable ' + url.pathname + ' ' + String(error));}
    })();
    pending.add(task); task.finally(() => pending.delete(task));
  });
  page.on('requestfailed', request => {
    const row = {state: label(), path: new URL(request.url()).pathname, failure: request.failure()?.errorText || 'unknown'};
    report.requestFailures.push(row); check(false, row.state + ': request failed ' + row.path);
  });
  page.on('pageerror', error => {report.pageErrors.push({state: label(), message: String(error)}); check(false, label() + ': product page error');});
  page.on('console', message => {
    if (message.type() === 'error') {report.consoleErrors.push({state: label(), text: message.text()}); check(false, label() + ': console error');}
    if (/Content Security Policy|violates the following.*directive|Refused to .*directive|CSP/i.test(message.text())) {
      report.cspConsole.push({state: label(), text: message.text()}); check(false, label() + ': CSP refusal/report');
    }
  });
}
async function settleAndColors(page) {
  return page.evaluate(async () => {
    const colors = () => [...document.querySelectorAll('.display-access-native .btn,.display-access-native .linkbtn')].map(element => {
      const style = getComputedStyle(element);
      return {action: element.closest('form')?.getAttribute('action') || '', label: element.textContent.trim(), color: style.color, backgroundColor: style.backgroundColor};
    });
    const before = colors(); document.documentElement.getBoundingClientRect();
    await document.fonts.ready;
    const frame = () => new Promise(resolve => requestAnimationFrame(resolve)), deadline = performance.now() + 4000, waits = [];
    for (;;) {
      await frame(); await frame();
      const active = document.getAnimations().filter(animation => animation.pending || animation.playState === 'running');
      if (!active.length) break;
      if (performance.now() >= deadline || active.some(animation => animation.effect?.getComputedTiming()?.iterations === Infinity)) throw new Error('Unsettled/infinite animation before whole-document audit');
      waits.push(...active.map(animation => ({type: animation.constructor.name, property: animation.transitionProperty || ''})));
      let timer;
      try {await Promise.race([Promise.allSettled(active.map(animation => animation.finished)), new Promise((_, reject) => {timer = setTimeout(() => reject(new Error('Animation completion deadline exceeded')), Math.max(1, deadline - performance.now()));})]);}
      finally {clearTimeout(timer);}
    }
    const after = colors(); await frame(); await frame();
    if (JSON.stringify(after) !== JSON.stringify(colors())) throw new Error('Computed styles changed after settling');
    return {before, after, waits, remainingAnimations: document.getAnimations().filter(animation => animation.pending || animation.playState === 'running').length};
  });
}
async function audit(page) {
  return page.evaluate(async () => {
    const raw = await window.axe.run(document, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa']}});
    const details = rows => rows.map(row => ({id: row.id, impact: row.impact, nodes: row.nodes.map(node => ({target: node.target, summary: node.failureSummary}))}));
    return {version: raw.testEngine.version, passes: raw.passes.map(row => row.id), violations: details(raw.violations), incomplete: details(raw.incomplete)};
  });
}

async function boundedPhase(label,work,milliseconds){
 let timer;try{return await Promise.race([Promise.resolve().then(work),new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error('Node-owned phase deadline: '+label)),milliseconds);})]);}finally{clearTimeout(timer);}
}
async function metricsFor(page){
 return page.evaluate(()=>{
  const clean=v=>(v||'').replace(/\s+/g,' ').trim(),main=document.querySelector('main#main'),form=main.querySelector('form.display-access-form');
  const ids=[...document.querySelectorAll('[id]')].map(e=>e.id),unresolved=[];
  for(const e of document.querySelectorAll('[for],[aria-labelledby],[aria-describedby]'))for(const a of['for','aria-labelledby','aria-describedby'])for(const id of(e.getAttribute(a)||'').split(/\s+/).filter(Boolean))if(!document.getElementById(id))unresolved.push(id);
  return {native:document.body.classList.contains('display-access-native'),language:document.documentElement.lang,theme:document.documentElement.dataset.theme,text:document.documentElement.dataset.text,motion:document.documentElement.dataset.motion,heading:clean(main.querySelector('h1')?.textContent),documentTitle:document.title,canonical:document.querySelector('link[rel=canonical]')?.getAttribute('href'),intro:clean(main.querySelector('.display-access-intro')?.textContent),actorLabel:clean(document.querySelector('header .nav-username')?.textContent),avatar:[...document.querySelectorAll('header .nav-avatar')].map(e=>({src:e.getAttribute('src'),width:e.width,height:e.height,alt:e.alt})),accountMenus:document.querySelectorAll('header details.nav-menu.nav-end').length,
   tabs:[...main.querySelectorAll('nav.tabs a')].map(e=>({label:clean(e.textContent),href:e.getAttribute('href'),current:e.getAttribute('aria-current')==='page',active:e.classList.contains('is-active')})),groups:[...form.querySelectorAll('fieldset')].map(e=>({name:e.querySelector('input')?.name,legend:clean(e.querySelector('legend')?.textContent)})),fields:[...form.querySelectorAll('input:not([type=hidden])')].map(e=>({name:e.name,type:e.type,value:e.value,checked:e.checked,label:clean(e.closest('label').querySelector('strong')?.textContent||e.closest('label').querySelector('span')?.textContent),detail:clean(e.closest('label').querySelector('.muted')?.textContent)})),messages:[...main.querySelectorAll('.messages [role=status]')].map(e=>clean(e.textContent)),
   fontsStatus:document.fonts.status,overflow:Math.max(document.documentElement.scrollWidth,document.body.scrollWidth)-innerWidth,duplicateIDs:ids.filter((x,i)=>ids.indexOf(x)!==i),unresolved,unlabelled:[...document.querySelectorAll('input:not([type=hidden]),select,textarea')].filter(e=>!e.labels?.length&&!e.getAttribute('aria-label')&&!e.getAttribute('aria-labelledby')).map(e=>e.id||e.name),stylesheetSources:[...document.querySelectorAll('link[rel=stylesheet]')].map(e=>e.getAttribute('href')),scriptSources:[...document.scripts].filter(e=>e.src).map(e=>e.getAttribute('src')),nonceAttributes:[...document.querySelectorAll('script[nonce]')].map(e=>e.nonce),owner:document.querySelector('#site-js')?.getAttribute('data-meetups-owner'),executableInline:[...document.scripts].filter(e=>!e.src&&e.type!=='application/json').length,eventAttributes:[...document.querySelectorAll('*')].flatMap(e=>[...e.attributes].filter(a=>/^on/i.test(a.name)).map(a=>a.name)),styleElements:document.querySelectorAll('style').length,inlineStyles:document.querySelectorAll('[style]').length,activePayload:main.querySelectorAll('script,img,svg,iframe,object,embed').length,privateText:/PRIVATE-|token_key|session_key|Bearer\s/.test(main.textContent),unreadPills:document.querySelectorAll('header .pill').length,
   forms:[...document.forms].map(e=>({
    primary:e===form,action:e.getAttribute('action'),method:e.method,
    csrf:[...e.querySelectorAll('input[name=csrfmiddlewaretoken]')].map(x=>x.value),
    fieldCount:e.querySelectorAll('input,select,textarea').length,
    data:[...new FormData(e).entries()].map(([name,value])=>[name,String(value)])
   }))};
 });
}

async function keyboardChecks(page,state,label){
 const expected=state.expected,records=[],selectors='main#main a[href],main#main input[type=checkbox],main#main input[type=radio]:checked,main#main button',targets=page.locator(selectors);
 const choices=expected.display?expected.fields.filter(r=>r.checked):expected.fields;
 const wanted=[...expected.tabs.map(r=>'A:'+r.href),...choices.map(r=>'INPUT:'+r.name),'BUTTON:save'];
 check(await targets.count()===wanted.length,label+': exact independent focus-stop census');await page.locator('main#main').focus();
 for(let i=0;i<wanted.length;i++){
  await page.keyboard.press('Tab');const row=await targets.nth(i).evaluate(e=>{const s=getComputedStyle(e);return{active:e===document.activeElement,focusVisible:e.matches(':focus-visible'),outline:s.outlineStyle,width:s.outlineWidth,destination:e.tagName+':'+(e.tagName==='INPUT'?e.name:e.tagName==='BUTTON'?'save':e.getAttribute('href'))};});
  check(row.active&&row.focusVisible&&row.outline!=='none'&&parseFloat(row.width)>0&&row.destination===wanted[i],label+': actual Tab visible stop'+i);records.push(row);
 }
 // Only keyboard-toggle a primary checkbox,serialize its in-memory form,and
 // restore it. Never activate Save,language auto-submit,logout or navigation.
 if(!expected.display){const first=page.locator('main form.display-access-form input[type=checkbox]').first(),initial=await first.isChecked();await first.focus();await page.keyboard.press('Space');check(await first.isChecked()===!initial,label+': checkbox Space toggled');const serialized=await first.evaluate(e=>[...new FormData(e.form).entries()].map(([k,v])=>[k,String(v)]));check(serialized.some(([k])=>k===expected.fields[0].name)===!initial,label+': toggled FormData presence');await page.keyboard.press('Space');check(await first.isChecked()===initial,label+': checkbox restored');records.push({checkboxSpaceSerialization:serialized});}
 const account=page.locator('header details.nav-menu.nav-end');check(await account.count()===(expected.signed?1:0),label+': source account menu presence');
 if(expected.signed){const summary=account.locator('summary');await summary.focus();await page.keyboard.press('Enter');check(await account.evaluate(e=>e.open),label+': real keyboard account open');await page.keyboard.press('Tab');const first=await account.locator('a').first().evaluate(e=>{const s=getComputedStyle(e);return{active:e===document.activeElement,focusVisible:e.matches(':focus-visible'),outline:s.outlineStyle,width:s.outlineWidth};});check(first.active&&first.focusVisible&&first.outline!=='none'&&parseFloat(first.width)>0,label+': menu first link actual Tab');records.push({firstAccount:first});await summary.focus();await page.keyboard.press('Enter');check(!await account.evaluate(e=>e.open),label+': menu keyboard closed');}
 return records;
}

async function exercise(page,state,noJS){
 current=(noJS?'no-js:':'')+state.name;const label=current,assetStart=report.assetResponses.length,response=await page.goto(base+'/preview/'+state.name+'/',{waitUntil:'networkidle'});
 if(!noJS)await boundedPhase(label+': fonts',()=>page.evaluate(()=>document.fonts.ready),4000);await flushResponses();
 const metrics=await metricsFor(page),expected=state.expected,clean=v=>(v||'').replace(/\s+/g,' ').trim();
 check(metrics.native&&metrics.heading===clean(expected.title)&&metrics.documentTitle===expected.title+' · '+expected.site,label+': source title/site');check(metrics.language===state.language&&metrics.theme===state.theme&&metrics.text===state.text&&metrics.motion===state.motion,label+': theme/text/motion/language');check(metrics.canonical===expected.canonical&&metrics.canonical===state.canonical&&metrics.overflow<=0&&metrics.fontsStatus==='loaded',label+': canonical/layout/fonts');check(metrics.intro===clean(expected.intro),label+': original device/unknown-accessibility copy');
 check(metrics.actorLabel===clean(expected.actor_label)&&metrics.accountMenus===(expected.signed?1:0)&&metrics.avatar.length===(expected.avatar_uri?1:0),label+': public/defaultself/error shell');if(expected.avatar_uri)check(metrics.avatar[0].src===expected.avatar_uri&&metrics.avatar[0].width===26&&metrics.avatar[0].height===26&&metrics.avatar[0].alt==='',label+': exact generated-only GET avatar');
 check(isDeepStrictEqual(metrics.tabs,expected.tabs.map(row=>({...row,label:clean(row.label),active:row.current}))),label+': signed-display-only source tabs');check(isDeepStrictEqual(metrics.groups,expected.groups.map(r=>({...r,legend:clean(r.legend)}))),label+': fixed display fieldsets');check(isDeepStrictEqual(metrics.fields,expected.fields.map(r=>({...r,label:clean(r.label),detail:clean(r.detail)}))),label+': every label/name/value/checked/default-on/quiet detail');check(isDeepStrictEqual(metrics.messages,expected.messages.map(clean)),label+': exact escaped POSTerror feedback');
 check(!metrics.duplicateIDs.length&&!metrics.unresolved.length&&!metrics.unlabelled.length,label+': all labels and references');check(isDeepStrictEqual(metrics.stylesheetSources,['/static/css/base.css','/static/css/display-access-native.css'])&&isDeepStrictEqual(metrics.scriptSources,expected.script_sources),label+': exact current asset order');check(!metrics.executableInline&&!metrics.eventAttributes.length&&!metrics.styleElements&&!metrics.inlineStyles&&!metrics.activePayload&&!metrics.privateText&&!metrics.unreadPills,label+': no active/private/inline/unread inventions');check(metrics.nonceAttributes.length===2&&metrics.nonceAttributes.every(n=>n===normalization.replacement)&&metrics.owner===expected.owner,label+': actual nonce and public owner');
 const wantedActions=[expected.display?'/display/':null,'/i18n/setlang/',...(expected.signed?['/logout/']:[])];check(isDeepStrictEqual(metrics.forms.map(r=>r.action).sort(),wantedActions.sort()),label+': exact form action census');
 for(const form of metrics.forms){check(form.method==='post'&&form.csrf.length===1&&form.csrf[0]===state.synthetic_csrf,label+': source CSRF including empty public fallback');let wanted=[['csrfmiddlewaretoken',state.synthetic_csrf]];
  if(form.primary)wanted.push(...expected.fields.filter(r=>r.checked).map(r=>[r.name,r.value]));else if(form.action==='/i18n/setlang/')wanted.push(['next',expected.path],['language',state.language]);
  check(isDeepStrictEqual(form.data,wanted),label+': observational FormData no subject/extra input');const count=form.primary?expected.fields.length+1:form.action==='/i18n/setlang/'?3:1;check(form.fieldCount===count,label+': exact owning/all field census');
 }
 const headers=await response.allHeaders();for(const[name,value]of Object.entries(state.renderer_headers))check(headers[name.toLowerCase()]===value,label+': exact normalized rendererheader '+name);check(!headers['set-cookie'],label+': no fixture cookie');
 let screenshot;if(!noJS){screenshot=state.name+'-desktop.png';await page.screenshot({path:path.join(output,screenshot),fullPage:true});}
 const keyboard=await keyboardChecks(page,state,label),audits=[];let steadyState;
 // No-JS product execution stays disabled. DevTools evaluates only the
 // cached/hash-admitted Axe source after the original no-JS DOM checks; it
 // does not activate site.js,change JavaScriptEnabled,or append a product tag.
 if(noJS)await boundedPhase(label+': noJS Axe instrumentation',()=>page.evaluate(served.get('/qa/axe.js').toString('utf8')+'\n;void 0;'),4000);
 else await boundedPhase(label+': JS Axe instrumentation',()=>page.addScriptTag({url:base+'/qa/axe.js'}),4000);
 await page.evaluate(()=>{for(const d of document.querySelectorAll('header details'))d.open=false;});
 steadyState=await boundedPhase(label+': fonts and all RAF settling',()=>settleAndColors(page),4000);steadyState.initialColorsEqualSettled=initialColorsEqualSettled(steadyState.before,steadyState.after);check(steadyState.initialColorsEqualSettled&&!steadyState.remainingAnimations,label+': stable actual initial/settled colors');
 const modes=noJS?['normal-no-js']:expected.signed?['normal-js','account-keyboard-open']:['normal-js'];
 for(const mode of modes){
  if(mode==='account-keyboard-open'){const summary=page.locator('header details.nav-menu.nav-end > summary');check(expected.signed&&await summary.count()===1,label+': real signed account summary');await summary.focus();await page.keyboard.press('Enter');check(await summary.evaluate(e=>e.parentElement.open),label+': actual keyboard-open audit');await boundedPhase(label+': open-menu fonts and all RAF settling',()=>settleAndColors(page),4000);}
  if(!expected.signed)check(await page.locator('header details.nav-menu.nav-end').count()===0,label+': public account-menu absence');
  const axe=await boundedPhase(label+': strict whole-document Axe',()=>audit(page),8000),filename=state.name+'-audit-'+mode+'.png';check(!axe.violations.length&&!axe.incomplete.length,label+': strict natural-scroll whole-document Axe '+mode);const geometry=await page.evaluate(()=>{const r=document.querySelector('header').getBoundingClientRect();return{scroll:{x:scrollX,y:scrollY},header:{x:r.x,y:r.y,width:r.width,height:r.height},details:[...document.querySelectorAll('header details')].map(e=>({open:e.open,classes:e.className}))};});await page.screenshot({path:path.join(output,filename),fullPage:false});audits.push({mode,accountMenuPresent:expected.signed,productJavaScriptEnabled:!noJS,instrumentation:noJS?'privileged DevTools evaluate of cached admitted Axe only':'same-origin admitted Axe script',axeSHA256:hash(served.get('/qa/axe.js')),axe,geometry,screenshot:filename,screenshotSHA256:hashFile(path.join(output,filename))});
 }
 if(noJS)check(report.assetResponses.slice(assetStart).every(r=>r.state!==label||r.resourceType!=='script'),label+': product scripts stayed disabled');
 await flushResponses();const required=['/static/css/base.css','/static/css/display-access-native.css','/static/fonts/bricolage-grotesque-latin-wght-normal.woff2','/static/fonts/bricolage-grotesque-latin-ext-wght-normal.woff2',...(!noJS?[...expected.script_sources,'/qa/axe.js']:[])];const assets=required.map(name=>{const raw=served.get(name),wanted={path:name,bytes:raw.length,sha256:hash(raw)},actual=report.assetResponses.slice(assetStart).filter(r=>r.state===label&&r.path===name);check(actual.length>=1&&actual.every(r=>r.status===200&&r.bytes===wanted.bytes&&r.sha256===wanted.sha256),label+': exact positive frozen currentasset '+name);return{expected:wanted,actual};});
 return{name:state.name,javaScriptEnabled:!noJS,fixtureSHA256:state.sha256,htmlBytes:state.bytes,syntheticSourceSHA256:state.synthetic_source_sha256,metrics,keyboard,assets,steadyState,audits,...(screenshot?{screenshot,screenshotSHA256:hashFile(path.join(output,screenshot))}:{})};
}


try{
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});base='http://127.0.0.1:'+server.address().port;
  for(const noJS of[false,true]){
    const profile=fs.mkdtempSync(path.join(os.tmpdir(),'da-')),custody={path:profile,closed:false,removed:false};report.ownership.profiles.push(custody);
    const context=await chromium.launchPersistentContext(profile,{executablePath:chrome,headless:true,viewport:report.viewport,locale:'ro-RO',javaScriptEnabled:!noJS});contexts.push({context,profile,custody});report.chromeVersion=context.browser().version();
    await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
    await context.routeWebSocket(/.*/,socket=>{report.webSockets.push({state:current,path:new URL(socket.url()).pathname});check(false,current+': preference fixture opened unexpected WebSocket');void socket.close();});
    const page=context.pages()[0]||await context.newPage();watch(page,()=>current);
    for(const state of manifest.states){const row=await exercise(page,state,noJS);(noJS?report.noJavaScriptStates:report.states).push(row);}
    await context.close();custody.closed=true;
  }
  check(report.requests.every(r=>r.method==='GET'&&r.local),'External/mutation HTTP request occurred');check(!report.webSockets.length,'Unexpected live transport');check(report.states.length===8&&report.noJavaScriptStates.length===8,'Incomplete eight-by-two matrix');check(report.states.reduce((n,r)=>n+r.audits.length,0)===13&&report.noJavaScriptStates.reduce((n,r)=>n+r.audits.length,0)===8,'Exact8normalJS+5signedOpen+8noJS unique strict audit census');
}catch(error){report.failures.push(String(error));
}finally{
  for(const row of contexts)if(!row.custody.closed){try{await row.context.close();row.custody.closed=true;}catch(error){check(false,'Owned browser close failed: '+String(error));}}
  await flushResponses();
  for(const custody of report.ownership.profiles){if(custody.closed){try{fs.rmSync(custody.path,{recursive:true,force:false});custody.removed=!fs.existsSync(custody.path);}catch(error){check(false,'Owned profile removal failed: '+String(error));}}check(custody.closed&&custody.removed,'Owned browser/profile cleanup incomplete');}
  await new Promise(resolve=>server.listening?server.close(resolve):resolve());report.ownership.serverStopped=!server.listening;
  try{report.gitBridgeAfter=bridge();check(isDeepStrictEqual(report.gitBridgeAfter,bridgeBefore),'Git/source bridge changed');report.sourcesAfter=snapshot();report.sourceUnchanged=isDeepStrictEqual(report.sourcesBefore,report.sourcesAfter);check(report.sourceUnchanged,'Product/method source changed');}catch(error){check(false,'Final source refused: '+String(error));}
  for(const[name,raw]of codeBuffers)check(hash(raw)===admissionBinding.method_sha256[name]&&hash(regular(path.join(methodRoot,name)))===hash(raw),'admitted code buffer/current file drift');check(hash(regular(path.join(methodRoot,'README.md')))===hash(readmeBuffer)&&hash(regular(ownReview.path))===hash(ownReviewBuffer),'admitted README/review drift');for(const row of servedIdentity)check(hash(served.get(row.url))===row.sha256&&hash(regular(row.file))===row.sha256,'served immutable/source input drift');report.servedIdentity=servedIdentity;report.status=report.failures.length?'fail':'pass';fs.writeFileSync(path.join(output,'desktop-results.json'),JSON.stringify(report,null,2)+'\n',{flag:'wx'});
  console.log(JSON.stringify({status:report.status,states:report.states.length,noJavaScriptStates:report.noJavaScriptStates.length,priorBodies:previous.length,failures:report.failures,reportSHA256:hashFile(path.join(output,'desktop-results.json')),ownership:report.ownership}));if(report.failures.length)process.exitCode=1;
}
