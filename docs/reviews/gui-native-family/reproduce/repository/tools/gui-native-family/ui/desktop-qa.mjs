// Isolated actual native Social family fixture QA. No authentication/provider,
// mutation handler, live user browser, dependency installation or network service.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import crypto from 'node:crypto';
import zlib from 'node:zlib';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { initialColorsEqualSettled } from './color-stability.mjs';
const require=createRequire(import.meta.url);
const option=name=>{const i=process.argv.indexOf(name);assert.ok(i>=2&&process.argv[i+1],name);return path.resolve(process.argv[i+1]);};
const repo=option('--repo'),fixtures=option('--fixtures'),output=option('--output');
const playwright=option('--playwright-package'),axeFile=option('--axe-file'),chrome=option('--chrome');
const {chromium}=require(playwright);
assert.ok(output.includes('/_temp/')&&fixtures.includes('/_temp/')&&!fs.existsSync(output));
fs.mkdirSync(output,{mode:0o700});
const hash=b=>crypto.createHash('sha256').update(b).digest('hex');
const hashFile=f=>hash(fs.readFileSync(f));
const manifest=JSON.parse(fs.readFileSync(path.join(fixtures,'fixtures.json')));
assert.equal(manifest.states.length,8);
const sourceNames=['services/server/internal/web/family.go','services/server/internal/web/family_copy.go','services/server/internal/web/family_render.go','services/server/internal/web/family_projection_test.go','services/server/internal/web/family_benchmark_test.go','services/server/internal/web/renderer.go','services/server/internal/web/i18n.go','services/server/internal/web/templates.go','services/server/internal/web/pages/family-shell.gohtml','services/server/internal/web/pages/family-wards.gohtml','services/server/internal/web/pages/family-guardianship.gohtml','apps/web/templates/web/wards.html','apps/web/templates/web/guardianship.html','templates/base.html','locale/ro/LC_MESSAGES/django.po','static/css/base.css','static/css/family-native.css','static/fonts/bricolage-grotesque-latin-wght-normal.woff2','static/fonts/bricolage-grotesque-latin-ext-wght-normal.woff2'];
const snapshot=()=>sourceNames.map(name=>({path:name,sha256:hashFile(path.join(repo,name))}));
assert.equal(manifest.source_unchanged,true);
assert.deepEqual(snapshot(),manifest.sources_after.map(row=>({path:row.path,sha256:row.sha256})));
assert.deepEqual(manifest.sources_before,manifest.sources_after);
const report={schema:1,scope:'Actual isolated native fixture desktop Chromium/keyboard/axe and JS-disabled disclosures; not authenticated HTTP/session/PG/browser release qualification',viewport:{width:1280,height:720},sourcesBefore:snapshot(),fixtureManifestSHA256:hashFile(path.join(fixtures,'fixtures.json')),setup:{node:process.version,scriptSHA256:hashFile(fileURLToPath(import.meta.url)),colorPredicateSHA256:hashFile(fileURLToPath(new URL('./color-stability.mjs',import.meta.url))),axeFile,axeSHA256:hashFile(axeFile),playwrightVersion:require(path.join(playwright,'package.json')).version,chrome},states:[],noJavaScript:[],failures:[],requests:[],assetResponses:[],requestFailures:[],pageErrors:[],cspConsole:[],ownership:{userProfile:false,browserClosed:false,noJSContextClosed:false,serverStopped:false,profileRemoved:false}};
const check=(ok,message)=>{if(!ok)report.failures.push(message);};
const allowedStates=new Map(manifest.states.map(row=>[row.name,row]));
let base,context,noJSContext;
const server=http.createServer((req,res)=>{
  if(req.method!=='GET'){res.writeHead(405);res.end('Synthetic fixture only: no mutation handler.');return;}
  const url=new URL(req.url,'http://local.invalid');let file,type;
  if(url.pathname==='/qa/axe.js'){file=axeFile;type='text/javascript';}
  else if(url.pathname.startsWith('/static/')){
    const rel=decodeURIComponent(url.pathname.slice(8));
    if(!rel||rel.includes('..')||rel.includes('\\')||path.isAbsolute(rel)){res.writeHead(404);res.end();return;}
    file=path.join(repo,'static',rel);type=rel.endsWith('.css')?'text/css':rel.endsWith('.woff2')?'font/woff2':'application/octet-stream';
    if(!fs.existsSync(file)||fs.realpathSync(file)!==file||!fs.statSync(file).isFile()){res.writeHead(404);res.end();return;}
  }else{
    const match=/^\/preview\/([a-z-]+)\/$/.exec(url.pathname);
    if(!match||!allowedStates.has(match[1])){res.writeHead(404);res.end();return;}
    file=path.join(fixtures,match[1]+'.html');type='text/html; charset=utf-8';
  }
  res.setHeader('Content-Type',type);res.setHeader('Cache-Control','private, no-store');
  // Fixture serving is deliberately synthetic; actual common Renderer policy
  // is recorded by the Go exporter and authenticated middleware by HTTP tests.
  res.setHeader('Content-Security-Policy',"default-src 'none'; style-src 'self'; font-src 'self'; script-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'");
  fs.createReadStream(file).pipe(res);
});
const profile=fs.mkdtempSync(path.join(output,'owned-desktop-profile-'));
const attach=(page,label)=>{
  const safePath=value=>new URL(value).pathname;
  page.on('request',request=>report.requests.push({state:label(),method:request.method(),path:safePath(request.url()),local:new URL(request.url()).origin===base,resourceType:request.resourceType()}));
  page.on('response',response=>{const request=response.request(),row={state:label(),path:safePath(response.url()),resourceType:request.resourceType(),status:response.status()};if(row.resourceType!=='document')report.assetResponses.push(row);check(row.status<400,label()+': HTTP response failure '+row.resourceType+' '+row.path+' '+row.status);});
  page.on('requestfailed',request=>{const row={state:label(),path:safePath(request.url()),resourceType:request.resourceType(),failure:request.failure()?.errorText||'unknown'};report.requestFailures.push(row);check(false,label()+': request failure '+row.resourceType+' '+row.path);});
  page.on('pageerror',error=>{report.pageErrors.push({state:label(),message:String(error)});check(false,label()+': page error');});
  page.on('console',message=>{if(/Content Security Policy|violates the following.*directive|Refused to .*directive|CSP/i.test(message.text())){report.cspConsole.push({state:label(),type:message.type(),message:message.text()});check(false,label()+': CSP console refusal');}});
};
const metricsFor=page=>page.evaluate(()=>{
  const ids=[...document.querySelectorAll('[id]')].map(x=>x.id),unresolved=[];
  for(const e of document.querySelectorAll('[for],[aria-describedby],[aria-labelledby]'))for(const a of ['for','aria-describedby','aria-labelledby'])for(const id of(e.getAttribute(a)||'').split(/\s+/).filter(Boolean))if(!document.getElementById(id))unresolved.push(id);
  return{familyPage:document.body.classList.contains('family-page'),stylesheets:[...document.querySelectorAll('link[rel=stylesheet]')].map(link=>link.getAttribute('href')),language:document.documentElement.lang,theme:document.documentElement.dataset.theme,text:document.documentElement.dataset.text,motion:document.documentElement.dataset.motion,h1:document.querySelector('h1')?.textContent,canonical:document.querySelector('link[rel=canonical]')?.getAttribute('href'),overflow:Math.max(document.documentElement.scrollWidth,document.body.scrollWidth)-innerWidth,duplicateIDs:ids.filter((x,i)=>ids.indexOf(x)!==i),unresolved,unlabelled:[...document.querySelectorAll('input:not([type=hidden]),select,textarea')].filter(x=>!x.labels?.length&&!x.getAttribute('aria-label')&&!x.getAttribute('aria-labelledby')).map(x=>x.id||x.name),scripts:document.scripts.length,inline:document.querySelectorAll('[style],style,[onclick],[oninput],[onsubmit],[onchange]').length,currentPages:document.querySelectorAll('[aria-current=page]').length,details:[...document.querySelectorAll('main details')].map(d=>({open:d.open,summary:d.querySelector('summary')?.textContent})),status:document.querySelectorAll('[role=status]').length,forms:[...document.querySelectorAll('form')].map(f=>({action:f.getAttribute('action'),method:f.method,csrf:f.querySelectorAll('input[name=csrfmiddlewaretoken]').length,fields:[...f.querySelectorAll('input:not([type=hidden]),select,textarea')].map(e=>({name:e.name,type:e.type,value:e.value,checked:e.checked,required:e.required,autocomplete:e.autocomplete,id:e.id}))}))};
});
const keyboardChecks=async(page,state,label)=>{
  const result={};
  if(state.kind==='wards'&&state.ward_count){
    const form=page.locator('main form[action="/wards/41/limits/"]'),details=page.locator('main details').filter({has:page.locator('form[action="/wards/41/limits/"]')}),summary=details.locator('summary').first();
    check(!await details.evaluate(d=>d.open),label+': limits disclosure not initially closed');
    await summary.focus();await page.keyboard.press('Enter');check(await details.evaluate(d=>d.open),label+': Enter did not open native limits');
    check(await form.locator('select[name=earliest_start_hour]').inputValue()==='0'&&await form.locator('select[name=latest_start_hour]').inputValue()==='',label+': midnight and nil bounds conflated');
    const earliest=form.locator('select[name=earliest_start_hour]'),latest=form.locator('select[name=latest_start_hour]');await earliest.focus();await page.keyboard.press('Tab');
    result.boundsTab=await page.evaluate(()=>document.activeElement.id);check(result.boundsTab==='ward-41-latest',label+': native select Tab order');
    const day=form.locator('input[name=allowed_weekdays][value="2"]');check(!await day.isChecked(),label+': independent weekday selection lost');await day.focus();await page.keyboard.press('Space');check(await day.isChecked(),label+': keyboard checkbox did not toggle');await page.keyboard.press('Space');check(!await day.isChecked(),label+': keyboard checkbox did not restore');
    result.serialized=await form.evaluate(f=>{const data=new FormData(f);return{days:data.getAll('allowed_weekdays'),categories:data.getAll('allowed_categories'),earliest:data.get('earliest_start_hour'),latest:data.get('latest_start_hour'),maximum:data.get('max_open_joins')};});
    check(JSON.stringify(result.serialized.days)===JSON.stringify(['1','3']),label+': repeated exact weekday fields');
    check(result.serialized.earliest==='0'&&result.serialized.latest===''&&result.serialized.maximum==='2',label+': bounds form serialization');
    if(state.stored_category)check(result.serialized.categories.includes('stored-child-category'),label+': stored nested category lost on serialization');
    const ownSection=page.locator('main section.card').first(),revoke=ownSection.locator('form[action="/wards/41/revoke/"]'),revokeDetails=ownSection.locator('details').filter({has:page.locator('form[action="/wards/41/revoke/"]')});
    check(!await revoke.locator('button').isVisible(),label+': revoke form not deliberately disclosed');await revokeDetails.locator('summary').focus();await page.keyboard.press('Enter');
    check(await revoke.locator('button').isVisible(),label+': native revoke disclosure keyboard access');
    check((await revokeDetails.locator('p').innerText()).trim().length>0,label+': revocation consequence explanation absent');
    await revokeDetails.locator('summary').focus();await page.keyboard.press('Enter');check(!await revoke.locator('button').isVisible(),label+': revoke disclosure did not close');
    await summary.focus();await page.keyboard.press('Enter');check(!await details.evaluate(d=>d.open),label+': limits disclosure did not close');
  }
  if(state.invite){const invite=page.locator('main form[action="/wards/invite/"]');check(!await invite.evaluate(f=>f.checkValidity()),label+': required invite username accepted blank');}
  return result;
};
try{
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});base=`http://127.0.0.1:${server.address().port}`;
  context=await chromium.launchPersistentContext(profile,{executablePath:chrome,headless:true,viewport:report.viewport,locale:'ro-RO'});report.chromeVersion=context.browser().version();
  await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
  const page=context.pages()[0]||await context.newPage();let currentState='setup';attach(page,()=>currentState);
  for(const state of manifest.states){
    currentState=state.name;const raw=fs.readFileSync(path.join(fixtures,state.name+'.html'));assert.equal(hash(raw),state.sha256);assert.equal(raw.length,state.bytes);
    await page.goto(`${base}/preview/${state.name}/`,{waitUntil:'networkidle'});await page.evaluate(()=>document.fonts.ready);
    const metrics=await metricsFor(page);check(metrics.overflow<=0,state.name+': horizontal overflow');check(!metrics.duplicateIDs.length&&!metrics.unresolved.length&&!metrics.unlabelled.length,state.name+': labels/ID references');
    check(metrics.familyPage&&metrics.stylesheets.indexOf('/static/css/base.css')>=0&&metrics.stylesheets.indexOf('/static/css/family-native.css')>metrics.stylesheets.indexOf('/static/css/base.css'),state.name+': scoped family stylesheet missing or out of order');
    check(metrics.language===state.language&&metrics.theme===state.theme&&metrics.text===state.text&&metrics.motion===state.motion,state.name+': current language/display preferences');check(metrics.canonical===state.canonical,state.name+': canonical family path');
    check(metrics.scripts===0&&metrics.inline===0,state.name+': product JS/inline code');check(metrics.h1?.trim().length>0,state.name+': page heading');if(state.kind==='guardianship')check(metrics.currentPages===1,state.name+': current Guardians account tab');
    check(metrics.forms.every(f=>f.csrf===1&&f.method==='post'),state.name+': CSRF/method');check(metrics.forms.length===2+state.ward_count*3+(state.invite?1:0),state.name+': unexpected family form inventory');
    check(metrics.forms.every(f=>f.action==='/logout/'||f.action==='/i18n/setlang/'||state.kind==='wards'&&(f.action==='/wards/invite/'&&state.invite||/^\/wards\/(41|42)\/(limits|topics|revoke)\/$/.test(f.action))),state.name+': unauthorized action URL');
    check(!raw.toString().match(/name="(?:user_id|guardian_id|ward_id|token|password|code|topics\[\]|allowed_weekdays\[\]|allowed_categories\[\])"/),state.name+': identity/proof or wrong repeated fields acquired');
    for(const privateText of ['PRIVATE-','outside.invalid','synthetic-family-public-id','synthetic-family-ward-public-id'])check(!raw.includes(Buffer.from(privateText)),state.name+': unadmitted fixture field');
    if(state.kind==='guardianship')check(metrics.forms.every(f=>['/logout/','/i18n/setlang/'].includes(f.action))&&!raw.toString().includes('/revoke/')&&!raw.toString().includes('name="ward_username"'),state.name+': readonly ward gained family mutation');
    if(state.invite_unavailable)check(!metrics.forms.some(f=>f.action==='/wards/invite/')&&await page.locator('main [role=note]').count()>0,state.name+': unavailable invite unexplained/enabled');
    if(state.error)check(metrics.status===1,state.name+': public status/help absent');
    check(metrics.details.every(d=>!d.open),state.name+': native disclosures initially expanded');
    await page.screenshot({path:path.join(output,state.name+'-desktop.png'),fullPage:true});
    const keyboard=await keyboardChecks(page,state,state.name);
    await page.addScriptTag({url:base+'/qa/axe.js'});
    const {axe,steadyState}=await page.evaluate(async()=>{
      for(const d of document.querySelectorAll('details'))d.open=true;
      const buttons=[...document.querySelectorAll('.family-page .btn')];
      const colors=()=>buttons.map(button=>{const style=getComputedStyle(button);return{action:button.closest('form')?.getAttribute('action'),label:button.textContent.trim(),color:style.color,backgroundColor:style.backgroundColor,fontWeight:style.fontWeight,fontSize:style.fontSize,transitionProperty:style.transitionProperty,transitionDuration:style.transitionDuration};});
      // Initial computed style deliberately records the real just-disclosed
      // snapshot; forcing style/layout starts any newly visible transitions.
      const before=colors();document.documentElement.getBoundingClientRect();
      const started=performance.now(),deadline=started+4000,animations=[];
      const frame=()=>new Promise(resolve=>requestAnimationFrame(resolve));
      await document.fonts.ready;
      for(;;){
        await frame();await frame();
        const active=document.getAnimations().filter(animation=>animation.pending||animation.playState==='running');
        if(!active.length)break;
        if(performance.now()>=deadline)throw new Error('Rendering did not settle before accessibility audit');
        for(const animation of active){
          const timing=animation.effect?.getComputedTiming();
          if(timing?.iterations===Infinity)throw new Error('Infinite animation prevents steady-state accessibility audit');
          animations.push({type:animation.constructor.name,property:animation.transitionProperty||'',animationName:animation.animationName||'',duration:timing?.duration,iterations:timing?.iterations});
        }
        let timer;
        try{await Promise.race([Promise.allSettled(active.map(animation=>animation.finished)),new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error('Animation completion timed out before accessibility audit')),Math.max(1,deadline-performance.now()));})]);}
        finally{clearTimeout(timer);}
      }
      const after=colors();await frame();await frame();
      const confirmed=colors();
      if(JSON.stringify(after)!==JSON.stringify(confirmed))throw new Error('Button styles still changed after animation completion');
      const steadyState={before,after:confirmed,completedAnimationWaits:animations,waitedMs:performance.now()-started,remainingAnimations:document.getAnimations().filter(animation=>animation.pending||animation.playState==='running').length};
      if(steadyState.remainingAnimations)throw new Error('Animation restarted before accessibility audit');
      const result=await window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}});
      return{steadyState,axe:{version:result.testEngine.version,passes:result.passes.length,violations:result.violations.map(x=>({id:x.id,impact:x.impact,nodes:x.nodes.map(n=>({target:n.target,summary:n.failureSummary}))})),incomplete:result.incomplete.map(x=>({id:x.id,nodes:x.nodes.map(n=>({target:n.target,summary:n.failureSummary}))}))}};
    });
    steadyState.initialColorsEqualSettled=initialColorsEqualSettled(steadyState.before,steadyState.after);
    check(steadyState.initialColorsEqualSettled,state.name+': family button initial foreground/background differs from settled colors');
    check(!axe.violations.length&&!axe.incomplete.length,state.name+': axe findings/incomplete');
    report.states.push({name:state.name,fixtureSHA256:state.sha256,htmlBytes:raw.length,htmlGzipLevel9:zlib.gzipSync(raw,{level:9}).length,metrics,keyboard,steadyState,axe,screenshotSHA256:hashFile(path.join(output,state.name+'-desktop.png'))});
  }
  noJSContext=await context.browser().newContext({javaScriptEnabled:false,viewport:report.viewport,locale:'ro-RO'});await noJSContext.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
  const noJSPage=await noJSContext.newPage();let noJSState='no-js-setup';attach(noJSPage,()=>noJSState);
  for(const name of ['wards-en','guardians-ro']){const state=allowedStates.get(name);noJSState='no-js-'+name;await noJSPage.goto(`${base}/preview/${name}/`,{waitUntil:'networkidle'});await noJSPage.evaluate(()=>document.fonts.ready);const metrics=await metricsFor(noJSPage);check(metrics.scripts===0&&metrics.forms.every(f=>f.method==='post'&&f.csrf===1),noJSState+': no-JS form semantics');const keyboard=await keyboardChecks(noJSPage,state,noJSState);report.noJavaScript.push({name,javaScriptEnabled:false,metrics,keyboard});}
  check(report.requests.every(r=>r.method==='GET'&&r.local),'Fixture QA attempted a mutation/external request');
  check(report.assetResponses.some(r=>r.path==='/static/css/base.css'&&r.status===200),'Actual shared stylesheet was not loaded');
  check(report.assetResponses.some(r=>r.path==='/static/css/family-native.css'&&r.status===200),'Actual scoped family stylesheet was not loaded');
  for(const name of ['bricolage-grotesque-latin-wght-normal.woff2','bricolage-grotesque-latin-ext-wght-normal.woff2'])check(report.assetResponses.some(r=>r.path==='/static/fonts/'+name&&r.status===200),'Actual local font not loaded: '+name);
}catch(error){report.failures.push(String(error));}
finally{
  if(noJSContext){await noJSContext.close();report.ownership.noJSContextClosed=true;}
  if(context){await context.close();report.ownership.browserClosed=true;}
  await new Promise(resolve=>server.listening?server.close(resolve):resolve());report.ownership.serverStopped=!server.listening;fs.rmSync(profile,{recursive:true,force:true});report.ownership.profileRemoved=!fs.existsSync(profile);
  report.sourcesAfter=snapshot();report.sourceUnchanged=JSON.stringify(report.sourcesBefore)===JSON.stringify(report.sourcesAfter);check(report.sourceUnchanged,'Renderer/source/CSS changed during QA');report.status=report.failures.length?'fail':'pass';
  fs.writeFileSync(path.join(output,'desktop-results.json'),JSON.stringify(report,null,2)+'\n',{flag:'wx'});console.log(JSON.stringify({status:report.status,states:report.states.length,noJavaScript:report.noJavaScript.length,failures:report.failures,reportSHA256:hashFile(path.join(output,'desktop-results.json')),ownership:report.ownership}));if(report.failures.length)process.exitCode=1;
}
