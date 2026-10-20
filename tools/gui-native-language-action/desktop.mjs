// Standalone real-handler/synthetic-page interaction evidence only.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const hash=raw=>crypto.createHash('sha256').update(raw).digest('hex');
const option=name=>{const i=process.argv.indexOf(name);assert.ok(i>=2&&process.argv[i+1]);return path.resolve(process.argv[i+1]);};
const script=fileURLToPath(import.meta.url),here=path.dirname(script);assert.equal(here,'/home/dobo/work/_worktrees/social_media_activities_app/feat__gui-app-migration-a/tools/gui-native-language-action');
const sameStat=(a,b)=>['dev','ino','mode','uid','gid','size','mtimeNs','ctimeNs','nlink'].every(k=>a[k]===b[k]);
const read=(file,cap=1<<20)=>{assert.equal(fs.realpathSync(file),file);const before=fs.lstatSync(file,{bigint:true});assert.ok(before.isFile()&&before.size<=BigInt(cap));const fd=fs.openSync(file,fs.constants.O_RDONLY|fs.constants.O_NOFOLLOW);let raw;try{assert.ok(sameStat(before,fs.fstatSync(fd,{bigint:true})));raw=fs.readFileSync(fd);assert.ok(sameStat(before,fs.fstatSync(fd,{bigint:true})));}finally{fs.closeSync(fd);}assert.ok(sameStat(before,fs.lstatSync(file,{bigint:true})));assert.equal(raw.length,Number(before.size));return raw;};
const bindingPath=option('--binding');assert.equal(bindingPath,'/home/dobo/work/_temp/la-ui/admission/binding.json');assert.equal(fs.realpathSync(path.dirname(bindingPath)),path.dirname(bindingPath));assert.equal(fs.statSync(path.dirname(bindingPath)).uid,process.getuid());
const bindingDigestIndex=process.argv.indexOf('--binding-sha256'),bindingDigest=process.argv[bindingDigestIndex+1];assert.ok(bindingDigestIndex>=2&&/^[a-f0-9]{64}$/.test(bindingDigest));
const bindingRaw=read(bindingPath);assert.equal(hash(bindingRaw),bindingDigest);const binding=JSON.parse(bindingRaw);
// Disabled input stops before runtime-input reads, output traversal, dependency
// import, browser launch or other child effects.
assert.equal(binding.status,'BOUND_REVIEWED_LANGUAGE_COMPANION');
assert.match(binding.account_closing_source||'',/^[a-f0-9]{40}$/);assert.match(binding.language_product_source||'',/^[a-f0-9]{40}$/);
assert.ok(binding.source_review&&binding.complete7_review&&binding.account_operational_review);
const sourceRaw={};for(const name of ['qualify.py','desktop.mjs','fixture_test.go.txt']){sourceRaw[name]=read(path.join(here,name));assert.equal(hash(sourceRaw[name]),binding.method_sha256[name]);}
const reviewed=(spec)=>{assert.ok([here,'/home/dobo/work/_temp/la-ui/admission','/home/dobo/work/_temp/feat__gui-app-migration-a/social-family','/home/dobo/work/_temp/gui-functional-hub/language-companion-proposal'].some(root=>spec.path.startsWith(root+'/'))&&spec.path.endsWith('.json')&&spec.expected.startsWith('ACCEPT'));assert.match(spec.sha256,/^[a-f0-9]{64}$/);const raw=read(spec.path,4<<20);assert.equal(hash(raw),spec.sha256);const value=JSON.parse(raw);assert.equal(value[spec.field],spec.expected);assert.deepEqual(value.findings,[]);return value;};
const canonical=value=>Array.isArray(value)?value.map(canonical):value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(key=>[key,canonical(value[key])])):value;
const sourceReview=reviewed(binding.source_review),payload=structuredClone(binding);payload.source_review=null;
assert.equal(sourceReview.binding_payload_sha256,hash(Buffer.from(JSON.stringify(canonical(payload))+'\n')));
assert.deepEqual(sourceReview.method_sha256,binding.method_sha256);
for(const spec of [binding.complete7_review,binding.account_operational_review]){const value=reviewed(spec);assert.equal(value[spec.source_field],binding.language_product_source);}
const inputPath=option('--runtime-input');assert.ok(inputPath.startsWith('/home/dobo/work/_temp/la-ui/run-')&&path.basename(inputPath)==='runtime-input.json');const runtime=JSON.parse(read(inputPath));assert.equal(inputPath,path.join(runtime.output,'runtime-input.json'));assert.equal(runtime.binding_sha256,hash(bindingRaw));assert.equal(runtime.product,binding.language_product_source);
assert.equal(fs.realpathSync(runtime.output),runtime.output);assert.equal(path.dirname(runtime.output),'/home/dobo/work/_temp/la-ui');assert.match(path.basename(runtime.output),/^run-[a-zA-Z0-9_-]+$/);assert.equal(fs.statSync(runtime.output).uid,process.getuid());
assert.equal(fs.realpathSync(process.execPath),binding.dependencies.node.path);
const dependencyHash=(file,executable)=>{
 assert.equal(fs.realpathSync(file),file);const before=fs.lstatSync(file,{bigint:true});assert.ok(before.isFile());if(executable)fs.accessSync(file,fs.constants.X_OK);else assert.ok(before.size<=64n*1024n*1024n);
 const keys=['dev','ino','mode','uid','gid','size','mtimeNs','ctimeNs','nlink'],same=info=>assert.ok(keys.every(key=>info[key]===before[key]),'dependency changed during exact read');
 const fd=fs.openSync(file,fs.constants.O_RDONLY|fs.constants.O_NOFOLLOW),digest=crypto.createHash('sha256');let count=0n;
 try{same(fs.fstatSync(fd,{bigint:true}));const buffer=Buffer.alloc(1<<20);for(;;){const n=fs.readSync(fd,buffer,0,buffer.length,null);if(!n)break;count+=BigInt(n);assert.ok(count<=before.size);digest.update(buffer.subarray(0,n));}same(fs.fstatSync(fd,{bigint:true}));assert.equal(count,before.size);}finally{fs.closeSync(fd);}
 same(fs.lstatSync(file,{bigint:true}));return digest.digest('hex');
};
for(const [name,row]of Object.entries(binding.dependencies)){const file=name==='playwright_package'?path.join(row.path,'package.json'):row.path;assert.equal(dependencyHash(file,['go','node','chrome'].includes(name)),row.sha256);}
const origin=new URL(runtime.origin);assert.equal(origin.protocol,'http:');assert.equal(origin.hostname,'127.0.0.1');assert.ok(origin.port&&origin.pathname==='/');
const require=createRequire(import.meta.url);const {chromium}=require(binding.dependencies.playwright_package.path);
const report={schema:1,status:'RUNNING',scope:'Standalone actual registered language POST and pinned CheckCSRF with fixed synthetic GET pages; not assembled App/session identity or account presentation qualification.',product:runtime.product,viewport:{width:1280,height:720},flows:[],refusals:[],audits:[],requests:[],failures:[],ownership:{profiles:[],contextsClosed:false,profilesRemoved:false}};
const ensure=(ok,message)=>{if(!ok){report.failures.push(message);throw new Error(message);}};
const allowedGet=new Set(['/','/settings/','/settings/api-token/revoke/','/static/js/site.js','/qa/axe.js','/favicon.ico']);
const contexts=[],profiles=[];let label='setup';
const cookieValue=async context=>(await context.cookies()).find(c=>c.name==='django_language')?.value;
async function locale(page,choice){ensure(await page.locator('html').getAttribute('lang')===choice,'localized GET mismatch');for(const id of ['settings-language','footer-language'])ensure(await page.locator('#'+id).inputValue()===choice,'localized selection mismatch');}
async function audit(page) {
  return page.evaluate(async () => {
    const raw = await window.axe.run(document, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa']}});
    const details = rows => rows.map(row => ({id: row.id, impact: row.impact, nodes: row.nodes.map(node => ({target: node.target, summary: node.failureSummary}))}));
    return {version: raw.testEngine.version, passes: raw.passes.map(row => row.id), violations: details(raw.violations), incomplete: details(raw.incomplete)};
  });
}
async function strictAudit(page,name){await page.addScriptTag({url:origin.origin+'/qa/axe.js'});const result=await audit(page);ensure(result.violations.length===0&&result.incomplete.length===0,'strict whole-document synthetic audit failed');report.audits.push({name,...result});}
async function makeContext(js){const profile=path.join(runtime.output,js?'profile-js':'profile-nojs');fs.mkdirSync(profile,{mode:0o700});profiles.push(profile);report.ownership.profiles.push({name:path.basename(profile),owned:true,removed:false});const context=await chromium.launchPersistentContext(profile,{executablePath:binding.dependencies.chrome.path,headless:true,javaScriptEnabled:js,viewport:{width:1280,height:720},reducedMotion:'reduce',serviceWorkers:'block',args:['--disable-background-networking','--disable-component-update','--disable-sync','--no-first-run','--disable-default-apps']});contexts.push(context);
 await context.routeWebSocket('**/*',socket=>{report.failures.push('WebSocket refused');socket.close();});
 await context.route('**/*',async route=>{const request=route.request(),url=new URL(request.url());const ok=url.origin===origin.origin&&url.search===''&&((request.method()==='GET'&&allowedGet.has(url.pathname))||(request.method()==='POST'&&url.pathname==='/i18n/setlang/'));report.requests.push({case:label,method:request.method(),path:allowedGet.has(url.pathname)||url.pathname==='/i18n/setlang/'?url.pathname:'UNADMITTED',admitted:ok});if(!ok){report.failures.push('Unadmitted request refused');await route.abort();}else await route.continue();});
 const page=await context.newPage();page.on('pageerror',()=>report.failures.push('Product page error'));page.on('console',m=>{if(m.type()==='error'&&!m.text().includes('the server responded with a status of'))report.failures.push('Browser console error');});
 await context.addCookies([{name:'csrftoken',value:'c'.repeat(52),url:origin.origin,sameSite:'Lax'}]);return{context,page};}
async function navigate(page,context,choice,target='/settings/'){await context.clearCookies({name:'django_language'});await context.addCookies([{name:'django_language',value:choice,url:origin.origin,sameSite:'Lax'}]);await context.setExtraHTTPHeaders({'Accept-Language':choice});await page.goto(origin.origin+target,{waitUntil:'networkidle'});await locale(page,choice);}
async function flow(page,context,js,from,to,target='/settings/'){label=(js?'js':'nojs')+'-'+from+'-'+to+(target.includes('revoke')?'-error':'');await navigate(page,context,from,target);await context.setExtraHTTPHeaders({'Accept-Language':from});ensure(await cookieValue(context)===from,'initial preference differs');const select=page.locator(js?'#footer-language':'#settings-language');
 const observed=page.waitForResponse(response=>new URL(response.url()).pathname==='/i18n/setlang/'&&response.request().method()==='POST');const navigation=page.waitForNavigation({waitUntil:'networkidle'});
 if(js)await select.selectOption(to);else{await select.selectOption(to);await page.getByRole('button',{name:'Save',exact:true}).click();}
 const response=await observed;const followed=await navigation;ensure(followed&&followed.status()===200&&followed.request().method()==='GET'&&followed.url()===origin.origin+'/settings/','followed local GET differs');const headers=await response.allHeaders();ensure(response.status()===302&&headers.location==='/settings/','real POST redirect differs');ensure(await cookieValue(context)===to,'preference cookie did not change');await locale(page,to);
 const cookies=await context.cookies();ensure(cookies.length===2&&cookies.every(c=>['csrftoken','django_language'].includes(c.name)),'unexpected cookie admitted');const preference=cookies.find(c=>c.name==='django_language');ensure(preference.path==='/'&&preference.sameSite==='Lax'&&!preference.httpOnly&&!preference.secure,'local display-cookie attributes differ');
 report.flows.push({case:label,realPOSTObserved:true,status:302,localLocation:'/settings/',savedLanguage:to,followedGETLocalized:true,cookieTakesPrecedenceOverOppositeHeader:true,unrelatedActions:0});if(js)await strictAudit(page,label);}
try{
 const js=await makeContext(true);await flow(js.page,js.context,true,'en','ro');await flow(js.page,js.context,true,'ro','en');await flow(js.page,js.context,true,'en','ro','/settings/api-token/revoke/');
 const plain=await makeContext(false);await flow(plain.page,plain.context,false,'en','ro');await flow(plain.page,plain.context,false,'ro','en');
 label='finite-refusals';await navigate(js.page,js.context,'en');
 for(const row of [{name:'missing-csrf',body:'language=ro&next=%2Fsettings%2F',status:403},{name:'bad-explicit-csrf',body:'language=ro&next=%2Fsettings%2F&csrfmiddlewaretoken='+('c'.repeat(52)),header:'incorrect',status:403},{name:'unsupported-choice',body:'language=fr&next=%2Fsettings%2F',status:400},{name:'duplicate-choice',body:'language=ro&language=en',status:400},{name:'oversize',body:'language=ro&next='+('x'.repeat(4096)),status:413}]){
 const before=await cookieValue(js.context);const observed=js.page.waitForResponse(r=>new URL(r.url()).pathname==='/i18n/setlang/'&&r.request().method()==='POST');await js.page.evaluate(async row=>{await fetch('/i18n/setlang/',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded',...(row.header?{'X-CSRFToken':row.header}:{})},body:row.body,redirect:'manual'});},row);const response=await observed;const headers=await response.allHeaders();ensure(response.status()===row.status&&!('set-cookie'in headers)&&await cookieValue(js.context)===before,'refusal changed preference or status');report.refusals.push({case:row.name,status:row.status,cookieUnchanged:true,noSetCookie:true});}
 ensure(report.flows.length===5&&report.refusals.length===5&&report.audits.length===3&&report.failures.length===0,'finite evidence census differs');report.status='PASS_STANDALONE_LANGUAGE_INTERACTION_ONLY';
}catch(error){report.status='FAIL';if(!report.failures.length)report.failures.push('Synthetic companion failed');process.exitCode=1;}
finally{let closed=true;for(const context of contexts){try{await context.close();}catch{closed=false;}}report.ownership.contextsClosed=closed;if(closed){for(const profile of profiles){fs.rmSync(profile,{recursive:true});report.ownership.profiles.find(r=>r.name===path.basename(profile)).removed=!fs.existsSync(profile);}report.ownership.profilesRemoved=profiles.every(p=>!fs.existsSync(p));}if(!closed||!report.ownership.profilesRemoved){report.status='FAIL';report.failures.push('Owned browser cleanup unproven');process.exitCode=1;}for(const name of Object.keys(sourceRaw))ensure(hash(read(path.join(here,name)))===binding.method_sha256[name],'method source changed');ensure(hash(read(bindingPath))===hash(bindingRaw),'binding changed');fs.writeFileSync(path.join(runtime.output,'desktop-result.json'),JSON.stringify(report,null,2)+'\n',{flag:'wx',mode:0o600});}
