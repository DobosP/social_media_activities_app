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
import {initialColorsEqualSettled} from '../gui-native-family/ui/color-stability.mjs';

const require=createRequire(import.meta.url);
const option=name=>{const i=process.argv.indexOf(name);assert.ok(i>=2&&process.argv[i+1],'explicit '+name+' required');return path.resolve(process.argv[i+1]);};
const repo=option('--repo'),fixtures=option('--fixtures'),output=option('--output'),playwright=option('--playwright-package'),axeFile=option('--axe-file'),chrome=option('--chrome');
const producerIndex=process.argv.indexOf('--producer-source'),producerSource=process.argv[producerIndex+1];assert.ok(producerIndex>=2&&/^[0-9a-f]{40}$/.test(producerSource));
const {chromium}=require(playwright);
const hash=raw=>crypto.createHash('sha256').update(raw).digest('hex'),hashFile=file=>hash(fs.readFileSync(file));
const regular=file=>{assert.equal(fs.realpathSync(file),file);assert.ok(fs.lstatSync(file).isFile());return fs.readFileSync(file);};
const bridge=()=>JSON.parse(execFileSync('python3',['-I',path.join(repo,'tools/gui-native-notification-inbox/qualify.py'),'--repo',repo,'--producer-source',producerSource,'--check-bridge'],{encoding:'utf8'}));
const bridgeBefore=bridge();
assert.ok(fixtures.includes('/_temp/')&&output.includes('/_temp/')&&!fs.existsSync(output));assert.equal(fs.realpathSync(fixtures),fixtures);fs.mkdirSync(output,{mode:0o700});
const manifestPath=path.join(fixtures,'fixtures.json'),manifest=JSON.parse(regular(manifestPath));
const stateNames=['empty-en','one-unread-ro','mixed-en','all-read-ro','escaped-ro','unsafe-links-en'];
assert.deepEqual(manifest.states.map(row=>row.name),stateNames);assert.equal(manifest.PG_producer_source,producerSource);assert.equal(manifest.UI_method_source,bridgeBefore.ui_head);
assert.equal(manifest.source_unchanged,true);assert.deepEqual(manifest.sources_before,manifest.sources_after);
const snapshot=()=>manifest.sources_after.map(row=>{assert.ok(typeof row.path==='string'&&!path.isAbsolute(row.path)&&!row.path.split('/').includes('..'));const raw=regular(path.join(repo,row.path));return{path:row.path,bytes:raw.length,sha256:hash(raw)};});
const sourcesBefore=snapshot();assert.deepEqual(sourcesBefore,manifest.sources_after.map(({path,bytes,sha256})=>({path,bytes,sha256})));
const previous=manifest.previous_body_equality;assert.equal(previous.length,31);assert.equal(new Set(previous.map(row=>row.kind+':'+row.name)).size,31);
for(const[kind,count]of[['family',8],['group-create',10],['group-detail',7],['notification-preferences',6]])assert.equal(previous.filter(row=>row.kind===kind).length,count);
for(const row of previous){assert.equal(row.equal,true);assert.match(row.path,/^(?:family|group-create|group-detail|notification-preferences)\/[a-z-]+\.html$/);assert.equal(row.path,row.kind+'/'+row.name+'.html');const raw=regular(path.join(fixtures,row.path));assert.equal(raw.length,row.bytes);assert.equal(hash(raw),row.sha256);}
assert.equal(manifest.baseline_keeper_sha256,'29a9ff0d2e420d5454aaebd324d0bc686bc22cf14b6f04ce86e4b8ce7e71d7e5');
const normalization=manifest.normalization;assert.equal(normalization.schema,1);assert.equal(normalization.kind,'render-nonce-only');assert.equal(normalization.replacement,'synthetic-notification-inbox-ui-nonce');assert.deepEqual(normalization.states.map(row=>row.name),stateNames);
for(const row of normalization.states){const state=manifest.states.find(s=>s.name===row.name);assert.match(row.raw_html_sha256,/^[0-9a-f]{64}$/);assert.equal(row.normalized_html_sha256,state.sha256);assert.equal(row.attribute_replacements,2);assert.equal(row.csp_replacements,1);}
const states=new Map(manifest.states.map(row=>[row.name,row]));
for(const state of manifest.states){const raw=regular(path.join(fixtures,state.name+'.html'));assert.equal(raw.length,state.bytes);assert.equal(hash(raw),state.sha256);assert.ok(!raw.includes(Buffer.from('PRIVATE-')));}
const report={schema:1,status:'RUNNING',scope:'Six isolated real Renderer inbox fixtures, six no-JS states,18 natural whole-document Axe audits and31 prior native bodies. Read-all FormData is serialized only; authenticated HTTP/methods/current self scope and beyond100 writes remain actual seven-package PG; no provider or production proof.',
  viewport:{width:1280,height:720},PGProducerSource:producerSource,gitBridgeBefore:bridgeBefore,fixtureManifestSHA256:hashFile(manifestPath),previousBodyEquality:previous,normalization,sourcesBefore,
  setup:{node:process.version,scriptSHA256:hashFile(fileURLToPath(import.meta.url)),playwrightVersion:require(path.join(playwright,'package.json')).version,axeSHA256:hashFile(axeFile),chrome},
  states:[],noJavaScriptStates:[],requests:[],assetResponses:[],requestFailures:[],pageErrors:[],consoleErrors:[],cspConsole:[],webSockets:[],fixturePolicyEnforcement:[],mockEndpoints:[],failures:[],ownership:{profiles:[],serverStopped:false}};
const check=(ok,message)=>{if(!ok)report.failures.push(message);};
const contexts=[],pending=new Set();let base,current='setup';const flushResponses=async()=>{while(pending.size)await Promise.all([...pending]);};
const server=http.createServer((request,response)=>{
  try{
    const url=new URL(request.url,'http://fixture.invalid');
    if(request.method!=='GET'){check(false,current+': mutation requested');response.writeHead(405);response.end('Isolated readonly fixture');return;}
    let raw,type;
    if(url.pathname==='/qa/axe.js'){raw=regular(axeFile);type='text/javascript; charset=utf-8';}
    else if(url.pathname==='/favicon.ico'){response.writeHead(204);response.end();return;}
    else if(url.pathname.startsWith('/static/')){
      const name=decodeURIComponent(url.pathname.slice(8));assert.ok(name&&!path.isAbsolute(name)&&!name.includes('\\')&&!name.split('/').includes('..'));
      raw=regular(path.join(repo,'static',name));type=name.endsWith('.css')?'text/css':name.endsWith('.js')?'text/javascript; charset=utf-8':name.endsWith('.woff2')?'font/woff2':'application/octet-stream';
    }else if(url.pathname==='/sw.js'){
      raw=regular(path.join(repo,'services/server/internal/web/assets/meetups-worker.js'));type='text/javascript; charset=utf-8';
      report.mockEndpoints.push({state:current,path:url.pathname,scope:'Exact current offline worker in isolated owned profile; no authenticated cache data',sha256:hash(raw)});
    }else{
      const match=/^\/preview\/([a-z-]+)\/$/.exec(url.pathname);assert.ok(match&&states.has(match[1]));const state=states.get(match[1]);
      raw=regular(path.join(fixtures,state.name+'.html'));type='text/html; charset=utf-8';
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
    const colors = () => [...document.querySelectorAll('.notification-inbox-native .btn,.notification-inbox-native .linkbtn')].map(element => {
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


async function metricsFor(page){
 return page.evaluate(()=>{
  const clean=value=>(value||'').replace(/\s+/g,' ').trim(),main=document.querySelector('main#main'),form=main?.querySelector('form[action="/notifications/read-all/"]');
  const ids=[...document.querySelectorAll('[id]')].map(e=>e.id),unresolved=[];
  for(const element of document.querySelectorAll('[for],[aria-labelledby],[aria-describedby]'))for(const name of['for','aria-labelledby','aria-describedby'])for(const id of(element.getAttribute(name)||'').split(/\s+/).filter(Boolean))if(!document.getElementById(id))unresolved.push(id);
  const rows=[...main.querySelectorAll('[data-notification-row]')].map(row=>({title:clean(row.querySelector('[data-notification-title]')?.textContent),body:clean(row.querySelector('[data-notification-body]')?.textContent),why:clean(row.querySelector('[data-notification-why]')?.textContent),url:row.querySelector('[data-notification-link]')?.getAttribute('href')||'',when:clean(row.querySelector('[data-notification-time]')?.textContent),iso:row.querySelector('[data-notification-time]')?.getAttribute('datetime'),unread:row.dataset.notificationUnread==='true',accent:row.classList.contains('card-accent'),tag:clean(row.querySelector('.tag')?.textContent),linkLabel:clean(row.querySelector('[data-notification-link]')?.textContent),titleTag:row.querySelector('[data-notification-title]')?.tagName,bodyPresent:!!row.querySelector('[data-notification-body]'),reasonPresent:!!row.querySelector('[data-notification-why]')}));
  return{native:document.body.classList.contains('notification-inbox-native'),language:document.documentElement.lang,theme:document.documentElement.dataset.theme,text:document.documentElement.dataset.text,motion:document.documentElement.dataset.motion,
   heading:clean(main.querySelector('h1')?.textContent),empty:clean(main.querySelector('#notification-inbox-empty')?.textContent),settings:{href:main.querySelector('a[href="/notifications/preferences/"]')?.getAttribute('href'),text:clean(main.querySelector('a[href="/notifications/preferences/"]')?.textContent)},markAll:form?{text:clean(form.querySelector('button')?.textContent),type:form.querySelector('button')?.type,disabled:form.querySelector('button')?.disabled,data:[...new FormData(form).entries()].map(([name,value])=>[name,String(value)])}:null,
   rows,canonical:document.querySelector('link[rel=canonical]')?.getAttribute('href'),fontsStatus:document.fonts.status,overflow:Math.max(document.documentElement.scrollWidth,document.body.scrollWidth)-innerWidth,
   duplicateIDs:ids.filter((x,i)=>ids.indexOf(x)!==i),unresolved,unlabelled:[...document.querySelectorAll('input:not([type=hidden]),select,textarea')].filter(e=>!e.labels?.length&&!e.getAttribute('aria-label')&&!e.getAttribute('aria-labelledby')).map(e=>e.id||e.name),
   stylesheetSources:[...document.querySelectorAll('link[rel=stylesheet]')].map(e=>e.getAttribute('href')),scriptSources:[...document.scripts].filter(e=>e.src).map(e=>e.getAttribute('src')),nonceAttributes:[...document.querySelectorAll('script[nonce]')].map(e=>e.nonce),owner:document.querySelector('#site-js')?.getAttribute('data-meetups-owner'),
   executableInline:[...document.scripts].filter(e=>!e.src&&e.type!=='application/json').length,eventAttributes:[...document.querySelectorAll('*')].flatMap(e=>[...e.attributes].filter(a=>/^on/i.test(a.name)).map(a=>a.name)),styleElements:document.querySelectorAll('style').length,inlineStyles:document.querySelectorAll('[style]').length,activePayload:main.querySelectorAll('script,img,svg,iframe,object,embed').length,privateText:/PRIVATE-|recipient_id|notification_id/.test(main.textContent),
   mainControls:main.querySelectorAll('input:not([type=hidden]),textarea,select').length,
   forms:[...document.forms].map(e=>({action:e.getAttribute('action'),method:e.method,csrf:[...e.querySelectorAll('input[name=csrfmiddlewaretoken]')].map(x=>x.value),fields:[...e.querySelectorAll('input,select,textarea')].map(x=>({name:x.name,type:x.type,value:x.value})),data:[...new FormData(e).entries()].map(([name,value])=>[name,String(value)])}))};
 });
}
async function keyboardChecks(page,state,label){
 const records=[],targets=page.locator('main#main button,main#main a[href]'),expected=(state.expected.rows.length?['BUTTON:/notifications/read-all/']:[]).concat(['A:/notifications/preferences/'],state.expected.rows.filter(row=>row.url).map(row=>'A:'+row.url));
 check(await targets.count()===expected.length,label+': exact current main keyboard target census');await page.locator('main#main').focus();
 for(let i=0;i<expected.length;i++){await page.keyboard.press('Tab');const row=await targets.nth(i).evaluate(e=>{const s=getComputedStyle(e);return{active:e===document.activeElement,focusVisible:e.matches(':focus-visible'),outline:s.outlineStyle,width:s.outlineWidth,destination:e.tagName+':'+(e.getAttribute('href')||e.closest('form')?.getAttribute('action'))};});check(row.active&&row.focusVisible&&row.outline!=='none'&&parseFloat(row.width)>0&&row.destination===expected[i],label+': real Tab visible target '+i);records.push(row);}
 // No Enter/click on submit targets, form.submit or requestSubmit. Serialization
 // is observational and cannot mark notices read. Real writes remain App/PG.
 const account=page.locator('header details.nav-menu.nav-end'),summary=account.locator('summary');await summary.focus();await page.keyboard.press('Enter');check(await account.evaluate(e=>e.open),label+': account menu keyboard open');await page.keyboard.press('Tab');const firstAccount=await account.locator('a').first().evaluate(e=>{const s=getComputedStyle(e);return{active:e===document.activeElement,focusVisible:e.matches(':focus-visible'),outline:s.outlineStyle,width:s.outlineWidth};});check(firstAccount.active&&firstAccount.focusVisible&&firstAccount.outline!=='none'&&parseFloat(firstAccount.width)>0,label+': real Tab into account menu');records.push({firstAccount});await summary.focus();await page.keyboard.press('Enter');check(!await account.evaluate(e=>e.open),label+': account menu keyboard closed');return records;
}
async function exercise(page,state,noJS){
  current=(noJS?'no-js:':'')+state.name;const label=current,assetStart=report.assetResponses.length,response=await page.goto(base+'/preview/'+state.name+'/',{waitUntil:'networkidle'});
  // Disabled-JS contexts permit synchronous CDP inspection, never font/RAF promises.
  if(!noJS)await page.evaluate(()=>document.fonts.ready);await flushResponses();
  const metrics=await metricsFor(page),expected=state.expected,clean=value=>(value||'').replace(/\s+/g,' ').trim();
  check(metrics.native&&metrics.heading===clean(expected.title),label+': complete native title');
  check(metrics.language===state.language&&metrics.theme===state.theme&&metrics.text===state.text&&metrics.motion===state.motion,label+': current language/contrast/text/motion settings');
  check(metrics.canonical===state.canonical&&metrics.overflow<=0&&metrics.fontsStatus==='loaded',label+': canonical/layout/font readiness');
  check(!metrics.duplicateIDs.length&&!metrics.unresolved.length&&!metrics.unlabelled.length,label+': complete native IDs/labels/reference closure');
  check(isDeepStrictEqual(metrics.rows.map(({title,body,why,url,when,iso,unread})=>({title,body,why,url,when,iso,unread})),expected.rows.map(row=>({...row,title:clean(row.title),body:clean(row.body),why:clean(row.why)}))),label+': exact source ordered rows/date/safe URL/read presence/plain display values');
  check(metrics.rows.every((row,i)=>row.accent===row.unread&&row.tag===(row.unread?expected.new:'')&&row.linkLabel===(row.url?expected.view:'')&&row.titleTag==='H2'&&row.bodyPresent===!!expected.rows[i].body&&row.reasonPresent===!!expected.rows[i].why),label+': unread class/tag and optional body/reason/link semantics');
  check(metrics.empty===(expected.rows.length?'':clean(expected.empty))&&isDeepStrictEqual(metrics.settings,{href:'/notifications/preferences/',text:clean(expected.settings)}),label+': current empty/settings feedback');
  const wantedMark=expected.rows.length?{text:clean(expected.mark_all),type:'submit',disabled:false,data:[['csrfmiddlewaretoken',state.synthetic_csrf]]}:null;
  check(isDeepStrictEqual(metrics.markAll,wantedMark)&&metrics.mainControls===0,label+': source row presence controls ordinary read-all with only CSRF, including all-read');
  check(isDeepStrictEqual(metrics.stylesheetSources,['/static/css/base.css','/static/css/notification-inbox-native.css'])&&isDeepStrictEqual(metrics.scriptSources,expected.script_sources),label+': exact current frozen/new stylesheet and shared-script order');
  check(!metrics.executableInline&&!metrics.eventAttributes.length&&!metrics.styleElements&&!metrics.inlineStyles&&!metrics.activePayload&&!metrics.privateText,label+': no private/active/inline style/event code');
  check(metrics.nonceAttributes.length===2&&metrics.nonceAttributes.every(n=>n===normalization.replacement)&&metrics.owner===expected.owner,label+': actual normalized nonce2scripts and request-actor public owner');
  check(isDeepStrictEqual(metrics.forms.map(f=>f.action).sort(),[...expected.form_actions].sort()),label+': exact current ordinary actions');
  for(const form of metrics.forms){check(form.method==='post'&&form.csrf.length===1&&form.csrf[0]===state.synthetic_csrf,label+': current native CSRF/method');check(form.action.startsWith('/')&&!form.action.startsWith('//')&&!form.action.includes('?')&&!form.action.includes('#'),label+': fixed local form action');check(form.fields.every(f=>!f.name.includes('[')&&!f.name.includes(']')&&!['user_id','owner','owner_id','recipient_id','notification_id','role','cohort'].includes(f.name)),label+': no invented authority/row fields');if(form.action==='/notifications/read-all/')check(form.fields.length===1&&form.data.length===1&&form.fields[0].name==='csrfmiddlewaretoken',label+': only current csrf read-all payload');}
  const languageForm=metrics.forms.find(f=>f.action==='/i18n/setlang/');check(isDeepStrictEqual(languageForm?.data,[['csrfmiddlewaretoken',state.synthetic_csrf],['next','/notifications/'],['language',state.language]]),label+': current language choice/return serialized without auto-submitting');
  const headers=await response.allHeaders();for(const[name,value]of Object.entries(state.renderer_headers))check(headers[name.toLowerCase()]===value,label+': actual normalized renderer header '+name);check(!headers['set-cookie'],label+': fixture emitted cookie');
  let screenshot;if(!noJS){screenshot=state.name+'-desktop.png';await page.screenshot({path:path.join(output,screenshot),fullPage:true});}
  const keyboard=await keyboardChecks(page,state,label),audits=[];let steadyState;
  if(!noJS){
    await page.addScriptTag({url:base+'/qa/axe.js'});await page.evaluate(()=>{for(const detail of document.querySelectorAll('details'))detail.open=true;});
    steadyState=await settleAndColors(page);steadyState.initialColorsEqualSettled=initialColorsEqualSettled(steadyState.before,steadyState.after);check(steadyState.initialColorsEqualSettled&&!steadyState.remainingAnimations,label+': initial/settled colors differ');
    for(const mode of['all-open','header-closed','header-keyboard-open']){
      if(mode==='header-closed')await page.evaluate(()=>{for(const element of document.querySelectorAll('header details'))element.open=false;});
      if(mode==='header-keyboard-open'){const summary=page.locator('header details.nav-menu.nav-end > summary');await summary.focus();await page.keyboard.press('Enter');check(await summary.evaluate(e=>e.parentElement.open),label+': real keyboard-open audit');}
      if(mode!=='all-open')await settleAndColors(page);
      const axe=await audit(page),filename=state.name+'-audit-'+mode+'.png';check(!axe.violations.length&&!axe.incomplete.length,label+': strict naturally scrolled whole-document Axe '+mode);
      const geometry=await page.evaluate(()=>{const e=document.querySelector('header'),r=e.getBoundingClientRect(),s=getComputedStyle(e);return{scroll:{x:scrollX,y:scrollY},header:{x:r.x,y:r.y,width:r.width,height:r.height,color:s.color,backgroundColor:s.backgroundColor},details:[...document.querySelectorAll('details')].map(d=>({classes:d.className,open:d.open}))};});
      await page.screenshot({path:path.join(output,filename),fullPage:false});audits.push({mode,axe,geometry,screenshot:filename,screenshotSHA256:hashFile(path.join(output,filename))});
    }
  }
  await flushResponses();
  const required=['/static/css/base.css','/static/css/notification-inbox-native.css','/static/fonts/bricolage-grotesque-latin-wght-normal.woff2','/static/fonts/bricolage-grotesque-latin-ext-wght-normal.woff2',...(!noJS?[...expected.script_sources,'/qa/axe.js']:[])];
  const assets=required.map(name=>{const file=name==='/qa/axe.js'?axeFile:path.join(repo,name.slice(1)),raw=regular(file),wanted={path:name,bytes:raw.length,sha256:hash(raw)},actual=report.assetResponses.slice(assetStart).filter(row=>row.state===label&&row.path===name);check(actual.length>=1&&actual.every(r=>r.status===200&&r.bytes===wanted.bytes&&r.sha256===wanted.sha256),label+': exact positive current asset missing/different '+name);return{expected:wanted,actual};});
  return{name:state.name,javaScriptEnabled:!noJS,fixtureSHA256:state.sha256,htmlBytes:state.bytes,htmlGzipLevel9:zlib.gzipSync(regular(path.join(fixtures,state.name+'.html')),{level:9}).length,metrics,keyboard,assets,steadyState,audits,...(screenshot?{screenshot,screenshotSHA256:hashFile(path.join(output,screenshot))}:{})};
}
try{
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});base='http://127.0.0.1:'+server.address().port;
  for(const noJS of[false,true]){
    const profile=fs.mkdtempSync(path.join(os.tmpdir(),'ni-')),custody={path:profile,closed:false,removed:false};report.ownership.profiles.push(custody);
    const context=await chromium.launchPersistentContext(profile,{executablePath:chrome,headless:true,viewport:report.viewport,locale:'ro-RO',javaScriptEnabled:!noJS});contexts.push({context,profile,custody});report.chromeVersion=context.browser().version();
    await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
    await context.routeWebSocket(/.*/,socket=>{report.webSockets.push({state:current,path:new URL(socket.url()).pathname});check(false,current+': notification inbox opened unexpected WebSocket');void socket.close();});
    const page=context.pages()[0]||await context.newPage();watch(page,()=>current);
    for(const state of manifest.states){const row=await exercise(page,state,noJS);(noJS?report.noJavaScriptStates:report.states).push(row);}
    await context.close();custody.closed=true;
  }
  check(report.requests.every(r=>r.method==='GET'&&r.local),'External/mutation HTTP request occurred');check(!report.webSockets.length,'Unexpected live transport');check(report.states.length===6&&report.noJavaScriptStates.length===6,'Incomplete six-by-two matrix');
}catch(error){report.failures.push(String(error));
}finally{
  for(const row of contexts)if(!row.custody.closed){try{await row.context.close();row.custody.closed=true;}catch(error){check(false,'Owned browser close failed: '+String(error));}}
  await flushResponses();
  for(const custody of report.ownership.profiles){if(custody.closed){try{fs.rmSync(custody.path,{recursive:true,force:false});custody.removed=!fs.existsSync(custody.path);}catch(error){check(false,'Owned profile removal failed: '+String(error));}}check(custody.closed&&custody.removed,'Owned browser/profile cleanup incomplete');}
  await new Promise(resolve=>server.listening?server.close(resolve):resolve());report.ownership.serverStopped=!server.listening;
  try{report.gitBridgeAfter=bridge();check(isDeepStrictEqual(report.gitBridgeAfter,bridgeBefore),'Git/source bridge changed');report.sourcesAfter=snapshot();report.sourceUnchanged=isDeepStrictEqual(report.sourcesBefore,report.sourcesAfter);check(report.sourceUnchanged,'Product/method source changed');}catch(error){check(false,'Final source refused: '+String(error));}
  report.status=report.failures.length?'fail':'pass';fs.writeFileSync(path.join(output,'desktop-results.json'),JSON.stringify(report,null,2)+'\n',{flag:'wx'});
  console.log(JSON.stringify({status:report.status,states:report.states.length,noJavaScriptStates:report.noJavaScriptStates.length,priorBodies:previous.length,failures:report.failures,reportSHA256:hashFile(path.join(output,'desktop-results.json')),ownership:report.ownership}));if(report.failures.length)process.exitCode=1;
}
