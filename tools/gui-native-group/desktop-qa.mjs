// Isolated actual native Social group fixture QA. No authentication/provider,
// mutation handler, live user browser, dependency installation or network service.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import crypto from 'node:crypto';
import zlib from 'node:zlib';
import { createRequire } from 'node:module';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { initialColorsEqualSettled } from '../gui-native-family/ui/color-stability.mjs';
const require=createRequire(import.meta.url);
const option=name=>{const i=process.argv.indexOf(name);assert.ok(i>=2&&process.argv[i+1],name);return path.resolve(process.argv[i+1]);};
const repo=option('--repo'),fixtures=option('--fixtures'),output=option('--output');
const playwright=option('--playwright-package'),axeFile=option('--axe-file'),chrome=option('--chrome');
const {chromium}=require(playwright);
const producerIndex=process.argv.indexOf('--producer-source'),producerSource=process.argv[producerIndex+1];assert.ok(producerIndex>=2&&/^[0-9a-f]{40}$/.test(producerSource),'actual PG producer source required');
const bridge=()=>JSON.parse(execFileSync('python3',['-I',path.join(repo,'tools/gui-native-group/prepare-ui-overlay.py'),'--repo',repo,'--producer-source',producerSource,'--check-bridge'],{encoding:'utf8'}));
const bridgeBefore=bridge();
assert.ok(output.includes('/_temp/')&&fixtures.includes('/_temp/')&&!fs.existsSync(output));
fs.mkdirSync(output,{mode:0o700});
const hash=b=>crypto.createHash('sha256').update(b).digest('hex');
const hashFile=f=>hash(fs.readFileSync(f));
const manifest=JSON.parse(fs.readFileSync(path.join(fixtures,'fixtures.json')));
assert.equal(manifest.PG_producer_source,producerSource);assert.equal(manifest.UI_method_source,bridgeBefore.ui_head);
assert.equal(manifest.states.length,10);
assert.equal(manifest.family_byte_equality.length,8);
assert.equal(new Set(manifest.family_byte_equality.map(row=>row.name)).size,8);
assert.ok(manifest.family_byte_equality.every(row=>row.equal===true));
const sourceNames=["services/server/internal/web/group_create.go","services/server/internal/web/group_create_test.go","services/server/internal/web/group_create_copy_test.go","services/server/internal/web/forms.json","services/server/internal/web/forms.go","services/server/internal/web/social_forms.go","services/server/internal/web/social_actions.go","services/server/internal/web/renderer.go","services/server/internal/web/i18n.go","services/server/internal/web/family.go","services/server/internal/web/family_benchmark_test.go","services/server/internal/web/pages/group-create.gohtml","apps/web/templates/web/group_form.html","templates/base.html","locale/ro/LC_MESSAGES/django.po","static/css/base.css","static/css/group-create-native.css","static/fonts/bricolage-grotesque-latin-wght-normal.woff2","static/fonts/bricolage-grotesque-latin-ext-wght-normal.woff2","tools/gui-native-group/prepare-ui-overlay.py","tools/gui-native-group/fixture-export_test.go.txt","tools/gui-native-group/desktop-qa.mjs","tools/gui-native-group/family-html-baseline.json","tools/gui-native-family/ui/fixture-export_test.go.txt","tools/gui-native-family/ui/color-stability.mjs","tools/gui-native-group/extract-projection.py","services/server/internal/web/templates.go","services/server/internal/web/family_copy.go","services/server/internal/web/family_render.go","services/server/internal/web/family_projection_test.go","services/server/internal/web/pages/family-shell.gohtml","services/server/internal/web/pages/family-wards.gohtml","services/server/internal/web/pages/family-guardianship.gohtml","apps/web/templates/web/wards.html","apps/web/templates/web/guardianship.html","static/css/family-native.css","tools/gui-native-family/qualify.py","tools/gui-native-family/db-entry.sh","tools/gui-native-family/db-binding.json","tools/gui-native-family/test_qualify.py","services/server/tools/gui-native-family-bootstrap/main.go"];
const snapshot=()=>sourceNames.map(name=>({path:name,sha256:hashFile(path.join(repo,name))}));
assert.equal(manifest.source_unchanged,true);
assert.deepEqual(snapshot(),manifest.sources_after.map(row=>({path:row.path,sha256:row.sha256})));
assert.deepEqual(manifest.sources_before,manifest.sources_after);
const report={schema:1,scope:'Actual isolated native group fixture desktop/keyboard/axe/no-JS plus native eight-family body equality; not registered GET/prefill-query/auth/session/PG/release qualification',viewport:{width:1280,height:720},sourcesBefore:snapshot(),fixtureManifestSHA256:hashFile(path.join(fixtures,'fixtures.json')),setup:{node:process.version,scriptSHA256:hashFile(fileURLToPath(import.meta.url)),colorPredicateSHA256:hashFile(fileURLToPath(new URL('../gui-native-family/ui/color-stability.mjs',import.meta.url))),axeFile,axeSHA256:hashFile(axeFile),playwrightVersion:require(path.join(playwright,'package.json')).version,chrome},PGProducerSource:producerSource,gitBridgeBefore:bridgeBefore,familyByteEquality:manifest.family_byte_equality,states:[],noJavaScript:[],failures:[],requests:[],assetResponses:[],requestFailures:[],pageErrors:[],cspConsole:[],ownership:{userProfile:false,browserClosed:false,noJSContextClosed:false,serverStopped:false,profileRemoved:false}};
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
  return{groupPage:document.body.classList.contains('group-create-page'),stylesheets:[...document.querySelectorAll('link[rel=stylesheet]')].map(link=>link.getAttribute('href')),language:document.documentElement.lang,theme:document.documentElement.dataset.theme,text:document.documentElement.dataset.text,motion:document.documentElement.dataset.motion,h1:document.querySelector('h1')?.textContent,canonical:document.querySelector('link[rel=canonical]')?.getAttribute('href'),overflow:Math.max(document.documentElement.scrollWidth,document.body.scrollWidth)-innerWidth,duplicateIDs:ids.filter((x,i)=>ids.indexOf(x)!==i),unresolved,unlabelled:[...document.querySelectorAll('input:not([type=hidden]),select,textarea')].filter(x=>!x.labels?.length&&!x.getAttribute('aria-label')&&!x.getAttribute('aria-labelledby')).map(x=>x.id||x.name),scripts:document.scripts.length,inline:document.querySelectorAll('[style],style').length+[...document.querySelectorAll('*')].filter(e=>[...e.attributes].some(a=>/^on/i.test(a.name))).length,currentPages:document.querySelectorAll('[aria-current=page]').length,details:[...document.querySelectorAll('main details')].map(d=>({open:d.open,summary:d.querySelector('summary')?.textContent})),status:document.querySelectorAll('[role=status]').length,errorSummary:document.querySelector('main [role=alert]')?.textContent.trim()||'',invalidFields:[...document.querySelectorAll('main [aria-invalid=true]')].map(e=>e.name),forms:[...document.querySelectorAll('form')].map(f=>({action:f.getAttribute('action'),method:f.method,csrf:f.querySelectorAll('input[name=csrfmiddlewaretoken]').length,fields:[...f.querySelectorAll('input:not([type=hidden]),select,textarea')].map(e=>({name:e.name,type:e.type,value:e.value,checked:e.checked,required:e.required,autocomplete:e.autocomplete,id:e.id,label:e.labels?.[0]?.textContent||'',maxLengthAttr:e.getAttribute('maxlength')}))}))};
});
const keyboardChecks=async(page,state,label)=>{
  const form=page.locator('main form[action="/groups/new/"]'),city=form.locator('input[name=city]'),type=form.locator('select[name=activity_type]'),title=form.locator('input[name=title]'),description=form.locator('textarea[name=description]');
  const result={initialValidity:await form.evaluate(f=>f.checkValidity())};
  check(result.initialValidity===Boolean(state.control_values.city&&state.control_values.activity_type&&state.control_values.title),label+': original required field constraints');
  await city.focus();await page.keyboard.press('Tab');check(await page.evaluate(()=>document.activeElement.id)==='id_activity_type',label+': city/type Tab order');
  await type.press('Home');await type.press('ArrowDown');check(await type.inputValue()==='7',label+': native select keyboard choice');await type.selectOption(state.control_values.activity_type);
  await title.focus();await page.keyboard.press('Tab');check(await page.evaluate(()=>document.activeElement.id)==='id_description',label+': title/description Tab order');
  const original=await title.inputValue();await title.fill('Temporary synthetic keyboard value');check(await title.inputValue()==='Temporary synthetic keyboard value',label+': native text editing');await title.fill(original);
  const account=page.locator('header details.nav-menu.nav-end'),summary=account.locator('summary');check(!await account.evaluate(d=>d.open),label+': account disclosure initially open');await summary.focus();await page.keyboard.press('Enter');check(await account.evaluate(d=>d.open),label+': no-JS account disclosure keyboard open');await page.keyboard.press('Enter');check(!await account.evaluate(d=>d.open),label+': account disclosure keyboard close');
  result.serialized=await form.evaluate(f=>Object.fromEntries(new FormData(f).entries()));
  for(const [name,value] of Object.entries(state.control_values))check(result.serialized[name]===value,label+': exact native form serialization '+name);
  check(result.serialized.csrfmiddlewaretoken==='synthetic-social-group-qa-csrf',label+': same fixed synthetic CSRF serialization');
  check((await description.getAttribute('maxlength'))===null,label+': description gained UTF-16 maxlength');
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
    check(metrics.groupPage&&metrics.stylesheets.indexOf('/static/css/base.css')>=0&&metrics.stylesheets.indexOf('/static/css/group-create-native.css')>metrics.stylesheets.indexOf('/static/css/base.css'),state.name+': scoped group stylesheet order');
    check(metrics.language===state.language&&metrics.theme===state.theme&&metrics.text===state.text&&metrics.motion===state.motion,state.name+': current language/display preferences');check(metrics.canonical==='/groups/new/',state.name+': fixed group canonical');
    check(metrics.scripts===0&&metrics.inline===0&&metrics.h1?.trim().length>0,state.name+': native heading/no product JS/inline code');
    check(metrics.forms.length===3&&metrics.forms.every(f=>f.csrf===1&&f.method==='post'),state.name+': exact ordinary form inventory/CSRF/method');
    check(metrics.forms.every(f=>['/logout/','/i18n/setlang/','/groups/new/'].includes(f.action)),state.name+': unknown or query-bearing action');
    const form=metrics.forms.find(f=>f.action==='/groups/new/');
    const wanted=['city','activity_type','title','description',...(state.staff?['cohort']:[])];
    check(JSON.stringify(form?.fields.map(f=>f.name))===JSON.stringify(wanted),state.name+': exact field order/staff boundary');
    for(const field of form?.fields||[]){check(field.id==='id_'+field.name&&field.label===state.labels[field.name]&&field.value===state.control_values[field.name],state.name+': label/value '+field.name);check(field.required===['city','activity_type','title'].includes(field.name),state.name+': required semantics '+field.name);}
    check(form?.fields.find(f=>f.name==='description')?.maxLengthAttr===null,state.name+': description must keep Unicode-rune server bound');
    check(form?.fields.find(f=>f.name==='city')?.maxLengthAttr==='128'&&form?.fields.find(f=>f.name==='title')?.maxLengthAttr==='200',state.name+': unchanged original input maxlength');
    check(metrics.errorSummary===state.error_summary&&JSON.stringify(metrics.invalidFields)===JSON.stringify(state.error_field?[state.error_field]:[]),state.name+': classified accessible error summary/association');
    if(state.language==='ro'){check(state.labels.title==='Titlu'&&state.labels.description==='Descriere'&&(!state.staff||state.labels.cohort==='Grupă de vârstă'),state.name+': finite missing Romanian labels');}
    if(state.unknown_prefill)check(form?.fields.find(f=>f.name==='activity_type')?.value==='',state.name+': unknown prefill shape selected a type');
    if(state.unicode)check([...state.values.description].length===2000&&state.values.description.length===4000,state.name+': supplementary-plane description fixture');
    if(state.name==='group-escaping-ro')check(await page.locator('main img,main script').count()===0,state.name+': escaped user value became live markup');
    check(!raw.toString().match(/name="(?:user_id|owner|owner_id|is_staff|is_staff_curated|password|token|code)"/),state.name+': private/authority field acquired');
    for(const privateText of ['PRIVATE-','outside.invalid','PRIVATE-GROUP-PUBLIC-ID'])check(!raw.includes(Buffer.from(privateText)),state.name+': unadmitted fixture field');
    await page.screenshot({path:path.join(output,state.name+'-desktop.png'),fullPage:true});
    const keyboard=await keyboardChecks(page,state,state.name);
    await page.addScriptTag({url:base+'/qa/axe.js'});
    const {axe,steadyState}=await page.evaluate(async()=>{
      for(const d of document.querySelectorAll('details'))d.open=true;
      const buttons=[...document.querySelectorAll('.group-create-page .btn')];
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
    check(steadyState.initialColorsEqualSettled,state.name+': group button initial foreground/background differs from settled colors');
    check(!axe.violations.length&&!axe.incomplete.length,state.name+': axe findings/incomplete');
    report.states.push({name:state.name,fixtureSHA256:state.sha256,htmlBytes:raw.length,htmlGzipLevel9:zlib.gzipSync(raw,{level:9}).length,metrics,keyboard,steadyState,axe,screenshotSHA256:hashFile(path.join(output,state.name+'-desktop.png'))});
  }
  noJSContext=await context.browser().newContext({javaScriptEnabled:false,viewport:report.viewport,locale:'ro-RO'});await noJSContext.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
  const noJSPage=await noJSContext.newPage();let noJSState='no-js-setup';attach(noJSPage,()=>noJSState);
  for(const name of ['group-en','group-staff-ro']){const state=allowedStates.get(name);noJSState='no-js-'+name;await noJSPage.goto(`${base}/preview/${name}/`,{waitUntil:'networkidle'});await noJSPage.evaluate(()=>document.fonts.ready);const metrics=await metricsFor(noJSPage);check(metrics.scripts===0&&metrics.forms.every(f=>f.method==='post'&&f.csrf===1),noJSState+': no-JS form semantics');const keyboard=await keyboardChecks(noJSPage,state,noJSState);report.noJavaScript.push({name,javaScriptEnabled:false,metrics,keyboard});}
  check(report.requests.every(r=>r.method==='GET'&&r.local),'Fixture QA attempted a mutation/external request');
  check(report.assetResponses.some(r=>r.path==='/static/css/base.css'&&r.status===200),'Actual shared stylesheet was not loaded');
  check(report.assetResponses.some(r=>r.path==='/static/css/group-create-native.css'&&r.status===200),'Actual scoped group stylesheet was not loaded');
  for(const name of ['bricolage-grotesque-latin-wght-normal.woff2','bricolage-grotesque-latin-ext-wght-normal.woff2'])check(report.assetResponses.some(r=>r.path==='/static/fonts/'+name&&r.status===200),'Actual local font not loaded: '+name);
}catch(error){report.failures.push(String(error));}
finally{
  if(noJSContext){await noJSContext.close();report.ownership.noJSContextClosed=true;}
  if(context){await context.close();report.ownership.browserClosed=true;}
  await new Promise(resolve=>server.listening?server.close(resolve):resolve());report.ownership.serverStopped=!server.listening;fs.rmSync(profile,{recursive:true,force:true});report.ownership.profileRemoved=!fs.existsSync(profile);
  try{report.gitBridgeAfter=bridge();check(report.gitBridgeAfter.ui_head===report.gitBridgeBefore.ui_head,'Git HEAD changed during desktop QA');}catch(error){report.failures.push('Git bridge refused after QA: '+String(error));}
  report.sourcesAfter=snapshot();report.sourceUnchanged=JSON.stringify(report.sourcesBefore)===JSON.stringify(report.sourcesAfter);check(report.sourceUnchanged,'Renderer/source/CSS changed during QA');report.status=report.failures.length?'fail':'pass';
  fs.writeFileSync(path.join(output,'desktop-results.json'),JSON.stringify(report,null,2)+'\n',{flag:'wx'});console.log(JSON.stringify({status:report.status,states:report.states.length,noJavaScript:report.noJavaScript.length,failures:report.failures,reportSHA256:hashFile(path.join(output,'desktop-results.json')),ownership:report.ownership}));if(report.failures.length)process.exitCode=1;
}
