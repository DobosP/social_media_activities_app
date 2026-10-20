import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import crypto from 'node:crypto';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const args=Object.fromEntries(process.argv.slice(2).reduce((rows,item,i,all)=>i%2?rows:[...rows,[item,all[i+1]]],[]));
const take=name=>{const value=args['--'+name];assert.ok(value&&path.isAbsolute(value)&&path.normalize(value)===value,'absolute --'+name);return value;};
const repo=take('repo'),fixtures=take('fixtures'),output=take('output'),node=take('node'),playwright=take('playwright-package'),chrome=take('chrome'),axe=take('axe-file'),bindings=take('bindings');
assert.equal(process.argv.length-2,16,'eight explicit CLI pairs');assert.deepEqual(Object.keys(args).sort(),['--repo','--fixtures','--output','--node','--playwright-package','--chrome','--axe-file','--bindings'].sort());
assert.equal(fs.realpathSync(process.execPath),fs.realpathSync(node));assert.match(process.versions.node,/^24\./);
const hash=raw=>crypto.createHash('sha256').update(raw).digest('hex');
const fileHash=file=>{const fd=fs.openSync(file,'r'),h=crypto.createHash('sha256'),chunk=Buffer.alloc(65536);try{for(let n;(n=fs.readSync(fd,chunk,0,chunk.length,null));)h.update(chunk.subarray(0,n));}finally{fs.closeSync(fd);}return h.digest('hex');};
const regular=file=>{assert.ok(fs.lstatSync(file).isFile(),'regular source '+file);return fs.readFileSync(file);};
const pinned=JSON.parse(regular(bindings)).dependency_sha256;
const dependencies=[['node',node],['chrome',chrome],['axe_file',axe],['playwright_package',path.join(playwright,'package.json')]].map(([name,file])=>{const sha256=fileHash(file);assert.equal(sha256,pinned[name],'existing dependency pin '+name);return{name,path:file,sha256};});
const require=createRequire(import.meta.url),{chromium}=require(playwright);
const manifestBytes=regular(path.join(fixtures,'manifest.json')),manifest=JSON.parse(manifestBytes);
assert.equal(manifest.kind,'current-native-completion-desktop-fixtures');assert.equal(manifest.states.length,78);
const states=new Map(),byTemplate=new Map(),assets=new Map();
for(const state of manifest.states){assert.match(state.id,/^[a-z_-]+-(en|ro)$/);assert.equal(state.language,state.id.endsWith('-en')?'en':'ro');assert.ok(!states.has(state.id));const raw=regular(path.join(fixtures,state.id+'.html'));assert.equal(raw.length,state.bytes);assert.equal(hash(raw),state.sha256);assert.equal(state.fragment,state.template==='web/_person_card.html');states.set(state.id,{...state,raw});byTemplate.set(state.template,(byTemplate.get(state.template)||0)+1);}
const expectedTemplates=['my_meetups','my_venues','donate','transparency','campaigns','partners','my_donations','series_list','series_detail','series_form','gauges','gauge_detail','gauge_form','gauge_convert','person','_person_card','activity_log','safety_record','my_privacy','verify_age','account_delete','report','privacy','terms','landing','communities_graph','discover','places','place_detail','event_detail','open_data','activity_form','activity_edit','activity_detail','activity_safe_exit','place_propose','place_claim','places_pending','messages'].map(name=>'web/'+name+'.html').sort();
assert.deepEqual([...byTemplate.keys()].sort(),expectedTemplates);assert.ok([...byTemplate.values()].every(n=>n===2));for(const state of states.values())assert.equal(state.id,state.template.slice(4,-5)+'-'+state.language);
fs.mkdirSync(output,{mode:0o700});const profile=path.join(output,'.owned-browser-profile');
const report={status:'RUNNING',scope:'78 current synthetic Go fixtures; desktop 1280x900, existing static assets and exact exported headers. No registered HTTP/authentication, device/mobile, provider, mutation, encrypted-chat, map-tile or production claim.',viewport:{width:1280,height:900},unexercised:['Service workers and offline/PWA behavior (workers blocked)','Geolocation and external providers (no grants/requests)','Current registered GET/authentication transport (renderer export only)'],exporterSHA256:fileHash(path.join(repo,'services/server/internal/web/completion_preview_test.go')),rendererSHA256:fileHash(path.join(repo,'services/server/internal/web/renderer.go')),bindingSHA256:fileHash(bindings),manifestSHA256:hash(manifestBytes),runnerSHA256:fileHash(fileURLToPath(import.meta.url)),dependencies,states:[],assets:[],blocked:[],stubbed:[],failures:[],cleanup:{browserClosed:false,profileRemoved:false,serverClosed:false}};
const save=()=>fs.writeFileSync(path.join(output,'result.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});
const mime=file=>file.endsWith('.css')?'text/css':file.endsWith('.js')?'text/javascript':file.endsWith('.woff2')?'font/woff2':file.endsWith('.svg')?'image/svg+xml':file.endsWith('.png')?'image/png':'application/octet-stream';
const stubs=new Map([['/healthz',{ok:true}],['/api/places/',{results:[]}],['/api/communities/communities/graph/',{nodes:[],links:[]}],['/api/messaging/keys/',{}],['/api/messaging/conversations/',[]],['/api/messaging/guardian/conversations/',[]]]);
let current='setup';const server=http.createServer((request,response)=>{try{
 const u=new URL(request.url,'http://readonly.invalid');if(request.method!=='GET'){response.writeHead(405);response.end('Read-only desktop fixture');report.failures.push(current+': server received '+request.method);return;}
 if(u.pathname==='/favicon.ico'){response.writeHead(204);response.end();return;}
 if(u.pathname==='/api/media/file/synthetic-signed-reference/'){const raw=Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="160" height="160" viewBox="0 0 160 160"><rect width="160" height="160" fill="#334155"/><circle cx="80" cy="62" r="28" fill="#cbd5e1"/><path d="M24 160v-18a56 56 0 0 1 112 0v18" fill="#cbd5e1"/></svg>');report.stubbed.push({state:current,path:u.pathname,type:'image/svg+xml',bytes:raw.length,sha256:hash(raw),scope:'Exact synthetic person fixture image only; real media/authorization unqualified'});response.writeHead(200,{'Content-Type':'image/svg+xml','Cache-Control':'no-store'});response.end(raw);return;}
 if(stubs.has(u.pathname)){report.stubbed.push({state:current,path:u.pathname});response.writeHead(200,{'Content-Type':'application/json','Cache-Control':'no-store'});response.end(JSON.stringify(stubs.get(u.pathname)));return;}
 if(u.pathname.startsWith('/preview/')){const id=u.pathname.split('/')[2],s=states.get(id);assert.ok(s&&u.pathname==='/preview/'+id+'/');let raw=s.raw;
  if(s.fragment)raw=Buffer.from('<!doctype html><html lang="'+s.language+'" data-theme="'+(s.language==='ro'?'dark':'light')+'" data-text="'+(s.language==='ro'?'large':'normal')+'" data-motion="'+(s.language==='ro'?'reduce':'auto')+'"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Declared person-card test host</title><link rel="stylesheet" href="/static/css/base.css"><link rel="stylesheet" href="/static/css/trust-native.css"></head><body class="trust-native"><main>'+raw.toString('utf8')+'</main></body></html>');
  for(const [name,values]of Object.entries(s.headers)){if(s.fragment&&name.toLowerCase()==='content-length')continue;response.setHeader(name,values);}
  response.writeHead(200);response.end(raw);return;}
 const local=u.pathname==='/qa/axe.js'?axe:u.pathname.startsWith('/static/')?path.join(repo,'static',decodeURIComponent(u.pathname.slice(8))):null;
 assert.ok(local&&(local===axe||local.startsWith(path.join(repo,'static')+path.sep))&&!u.pathname.includes('..')&&!u.pathname.includes('\\'),'finite local asset');
 let asset=assets.get(local);if(!asset){const raw=regular(local);asset={raw,file:local,url:u.pathname,bytes:raw.length,sha256:hash(raw)};assets.set(local,asset);report.assets.push({file:local,url:u.pathname,bytes:raw.length,sha256:asset.sha256});}
 response.writeHead(200,{'Content-Type':mime(local),'Cache-Control':'no-store'});response.end(asset.raw);
 }catch(error){response.writeHead(404);response.end('Unknown local fixture');report.failures.push(current+': local route '+request.url+': '+error.message);}});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const origin='http://127.0.0.1:'+server.address().port;
let context;
try{
 context=await chromium.launchPersistentContext(profile,{executablePath:chrome,headless:true,viewport:report.viewport,serviceWorkers:'block',permissions:[]});
 report.browser=context.browser()?.version()||'persistent Chromium';
 await context.route('**/*',async route=>{const req=route.request(),u=new URL(req.url());if(u.origin!==origin||req.method()!=='GET'){report.blocked.push({state:current,method:req.method(),url:u.origin+u.pathname,reason:u.origin!==origin?'external network prohibited':'all mutations prohibited'});if(u.origin!==origin&&!/^(?:[a-z]\.tile\.openstreetmap\.org|tiles\.openfreemap\.org)$/.test(u.hostname))report.failures.push(current+': unexpected external URL '+u.origin+u.pathname);if(u.origin===origin&&req.method()!=='GET'&&!['/api/messaging/keys/','/api/v1/ops/csp-report/'].includes(u.pathname))report.failures.push(current+': unexpected mutation attempt '+u.pathname);await route.abort('blockedbyclient');return;}await route.continue();});
 await context.routeWebSocket('**/*',socket=>{report.blocked.push({state:current,method:'WEBSOCKET',url:socket.url(),reason:'live connections outside desktop fixture scope'});socket.close({code:1000,reason:'Read-only desktop fixture'});});
 for(const state of states.values()){
  current=state.id;const page=await context.newPage(),row={id:state.id,template:state.template,language:state.language,fragmentHost:state.fragment,originalSHA256:state.sha256,headers:state.headers,errors:[],consoleErrors:[],csp:[],audits:[],controls:[],limitations:[]};report.states.push(row);
  page.on('pageerror',error=>row.errors.push(error.message));page.on('console',message=>{if(message.type()==='error')row.consoleErrors.push(message.text());});
  page.on('dialog',dialog=>dialog.dismiss());
  await page.addInitScript(()=>{globalThis.__desktopCSP=[];document.addEventListener('securitypolicyviolation',event=>globalThis.__desktopCSP.push({directive:event.effectiveDirective,blockedURI:event.blockedURI,disposition:event.disposition}));document.addEventListener('submit',event=>{event.preventDefault();event.stopImmediatePropagation();},true);});
  try{
   await page.emulateMedia({colorScheme:state.language==='ro'?'dark':'light',reducedMotion:state.language==='ro'?'reduce':'no-preference'});
   const response=await page.goto(origin+'/preview/'+state.id+'/',{waitUntil:'domcontentloaded',timeout:15000});assert.equal(response.status(),200);
   const servedHeaders=await response.allHeaders();for(const [key,value]of Object.entries(state.headers)){if(state.fragment&&key.toLowerCase()==='content-length')continue;assert.equal(servedHeaders[key.toLowerCase()],value.join(', '),'exact exported header '+key);}
   await page.evaluate(()=>document.fonts.ready);await page.waitForTimeout(250);row.appearance=await page.evaluate(()=>({language:document.documentElement.lang,theme:document.documentElement.dataset.theme,background:getComputedStyle(document.body).backgroundColor}));assert.equal(row.appearance.language,state.language);assert.ok(!(await page.locator('body').innerText()).includes('PRIVATE-'),'unselected synthetic private marker visible');
   const policy=Object.entries(state.headers).find(([name])=>/^content-security-policy(?:-report-only)?$/i.test(name))?.[1]?.join(' '),nonce=policy?.match(/'nonce-([^']+)'/)?.[1];assert.ok(nonce,'exported nonce policy');row.scriptNonceCount=await page.locator('script').evaluateAll((scripts,wanted)=>{if(scripts.some(script=>script.nonce!==wanted))throw new Error('current script lost its exported nonce');return scripts.length;},nonce);
   await page.addScriptTag({url:origin+'/qa/axe.js'});
   row.controls=await page.evaluate(()=>[...document.forms].map(form=>({method:form.method,action:new URL(form.action).pathname,csrf:!!form.querySelector('[name="csrfmiddlewaretoken"]'),fields:[...form.elements].filter(el=>el.name).map(el=>({name:el.name,type:el.type,required:el.required,disabled:el.disabled,maxLength:el.maxLength,emptyRequiredRejected:(()=>{if(!el.required||el.disabled||el.type==='file')return null;const value=el.value,checked=el.checked;el.value='';if(el.type==='checkbox')el.checked=false;const invalid=!el.checkValidity();el.value=value;el.checked=checked;return invalid;})()}))})));
   for(const form of row.controls){if(form.method==='post')assert.ok(form.csrf,'existing POST CSRF control');for(const field of form.fields)if(field.emptyRequiredRejected!==null)assert.ok(field.emptyRequiredRejected,'native required constraint '+field.name);}
   const summary=page.locator('summary').first();if(await summary.count()){await summary.focus();const beforeOpen=await summary.evaluate(el=>el.parentElement.open);await page.keyboard.press('Enter');row.keyboard=await page.evaluate(()=>({focused:document.activeElement?.tagName,outline:getComputedStyle(document.activeElement).outlineStyle,shadow:getComputedStyle(document.activeElement).boxShadow,summaryToggled:document.activeElement?.parentElement?.open}));assert.equal(row.keyboard.focused,'SUMMARY');assert.notEqual(row.keyboard.summaryToggled,beforeOpen,'Enter must toggle the focused details');assert.ok(row.keyboard.outline!=='none'||row.keyboard.shadow!=='none','visible keyboard focus');}else{await page.keyboard.press('Tab');row.keyboard=await page.evaluate(()=>({focused:document.activeElement?.tagName}));}
   for(const mode of ['closed','details-open']){
    await page.locator('details').evaluateAll((details,open)=>details.forEach(detail=>detail.open=open),mode==='details-open');
    const geometry=await page.evaluate(()=>({width:innerWidth,scrollWidth:document.documentElement.scrollWidth,openDetails:document.querySelectorAll('details[open]').length}));assert.ok(geometry.scrollWidth<=geometry.width+1,'desktop horizontal overflow');
    const audit=await page.evaluate(async()=>{const r=await axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}});const compact=rows=>rows.map(x=>({id:x.id,impact:x.impact,nodes:x.nodes.map(n=>({target:n.target,summary:n.failureSummary}))}));return{violations:compact(r.violations),incomplete:compact(r.incomplete),passes:r.passes.length};});
    const screenshot=state.id+'-'+mode+'.png';await page.screenshot({path:path.join(output,screenshot),fullPage:false});row.audits.push({mode,geometry,...audit,screenshot,screenshotSHA256:fileHash(path.join(output,screenshot))});if(audit.violations.length||audit.incomplete.length)report.failures.push(state.id+': Axe '+mode);
   }
   row.csp=await page.evaluate(()=>globalThis.__desktopCSP);const blockedHere=report.blocked.some(x=>x.state===state.id);row.unexpectedConsoleErrors=row.consoleErrors.filter(text=>!(blockedHere&&text.includes('net::ERR_BLOCKED_BY_CLIENT')));if(row.errors.length||row.csp.length||row.unexpectedConsoleErrors.length)report.failures.push(state.id+': JavaScript/CSP errors');
   const blocked=report.blocked.filter(x=>x.state===state.id);if(blocked.some(x=>x.url.includes('.tile.openstreetmap.org/')||x.url.includes('tiles.openfreemap.org/')))row.limitations.push('Map tiles and geolocation unexercised; external tile requests blocked.');
   if(state.template==='web/communities_graph.html')row.limitations.push('Finite empty graph stub; nonempty WebGL layout and live graph data unexercised.');
   if(state.template==='web/messages.html')row.limitations.push('Read-only messaging shell; automatic key-enrollment POST blocked before network, crypto persistence/conversations and E2EE transport unqualified. Fresh synthetic browser keys are neither exported nor retained.');
   if(blocked.some(x=>x.method==='WEBSOCKET'))row.limitations.push('Live WebSocket transport blocked; static thread controls only.');
   row.status='AUDITED';
  }catch(error){row.status='ERROR';row.error=error.stack;report.failures.push(state.id+': '+error.message);}
  finally{await page.close();save();console.log(state.id+' '+row.status);}
 }
 for(const asset of report.assets)assert.equal(fileHash(asset.file),asset.sha256,'served source changed during run '+asset.file);
 assert.equal(fileHash(path.join(fixtures,'manifest.json')),report.manifestSHA256);
}catch(error){report.failures.push('runner: '+error.stack);}
finally{
 try{if(context){await context.close();report.cleanup.browserClosed=true;}}catch(error){report.failures.push('browser cleanup: '+error.message);}
 try{if(fs.existsSync(profile))fs.rmSync(profile,{recursive:true});report.cleanup.profileRemoved=!fs.existsSync(profile);}catch(error){report.failures.push('profile cleanup: '+error.message);}
 try{await new Promise((resolve,reject)=>server.close(error=>error?reject(error):resolve()));report.cleanup.serverClosed=true;}catch(error){report.failures.push('server cleanup: '+error.message);}
 report.status=report.failures.length?'NEEDS_REVIEW':'PASS_DESKTOP_SCOPE';try{save();}catch(error){console.error('final result save: '+error.message);process.exitCode=1;}
}
if(report.failures.length)process.exitCode=1;
