// Isolated native group-detail desktop proof. Actual product scripts/config are
// unchanged; only explicit synthetic GET/media/hovercard/SW/WS endpoints exist.
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

const require = createRequire(import.meta.url);
const option = name => {
  const index = process.argv.indexOf(name);
  assert.ok(index >= 2 && process.argv[index + 1], 'explicit ' + name + ' required');
  return path.resolve(process.argv[index + 1]);
};
const repo = option('--repo'), fixtures = option('--fixtures'), output = option('--output');
const playwright = option('--playwright-package'), axeFile = option('--axe-file'), chrome = option('--chrome');
const producerIndex = process.argv.indexOf('--producer-source');
const producerSource = process.argv[producerIndex + 1];
assert.ok(producerIndex >= 2 && /^[0-9a-f]{40}$/.test(producerSource), 'literal PG producer SHA required');
const {chromium} = require(playwright);
const hash = raw => crypto.createHash('sha256').update(raw).digest('hex');
const hashFile = file => hash(fs.readFileSync(file));
const regular = file => {
  assert.equal(fs.realpathSync(file), file, 'unalias input');
  assert.ok(fs.lstatSync(file).isFile(), 'regular input');
  return fs.readFileSync(file);
};
const bridge = () => JSON.parse(execFileSync('python3', ['-I', path.join(repo, 'tools/gui-native-group-detail/prepare-ui-overlay.py'), '--repo', repo, '--producer-source', producerSource, '--check-bridge'], {encoding: 'utf8'}));
const bridgeBefore = bridge();
assert.ok(fixtures.includes('/_temp/') && output.includes('/_temp/') && !fs.existsSync(output));
assert.equal(fs.realpathSync(fixtures), fixtures);
fs.mkdirSync(output, {mode: 0o700});
const manifestPath = path.join(fixtures, 'fixtures.json');
const manifest = JSON.parse(regular(manifestPath));
const stateNames = ['owner-en', 'member-ro', 'nonmember-en', 'child-ro', 'curator-ro', 'same-cohort-curator-en', 'errors-escape-ro'];
assert.deepEqual(manifest.states.map(state => state.name), stateNames);
assert.equal(manifest.PG_producer_source, producerSource);
assert.equal(manifest.UI_method_source, bridgeBefore.ui_head);
assert.equal(manifest.source_unchanged, true);
assert.deepEqual(manifest.sources_before, manifest.sources_after);
assert.ok(manifest.sources_after.length > 0);
const normalization = manifest.normalization;
assert.equal(normalization.schema, 1);
assert.equal(normalization.kind, 'render-nonce-only');
assert.equal(normalization.replacement, 'synthetic-group-detail-ui-nonce');
assert.deepEqual(normalization.states.map(row => row.name), stateNames);
for (const row of normalization.states) {
  const state = manifest.states.find(state => state.name === row.name);
  assert.match(row.raw_html_sha256, /^[0-9a-f]{64}$/);
  assert.equal(row.normalized_html_sha256, state.sha256);
  assert.ok(Number.isInteger(row.attribute_replacements) && row.attribute_replacements >= 0);
  assert.equal(row.csp_replacements, 1);
}
const snapshot = () => manifest.sources_after.map(row => {
  assert.ok(typeof row.path === 'string' && !path.isAbsolute(row.path) && !row.path.split('/').includes('..'));
  const raw = regular(path.join(repo, row.path));
  return {path: row.path, bytes: raw.length, sha256: hash(raw)};
});
const sourcesBefore = snapshot();
assert.deepEqual(sourcesBefore, manifest.sources_after.map(({path, bytes, sha256}) => ({path, bytes, sha256})));
const previous = manifest.previous_body_equality;
assert.equal(previous.length, 18);
assert.equal(previous.filter(row => row.kind === 'family').length, 8);
assert.equal(previous.filter(row => row.kind === 'group-create').length, 10);
assert.equal(new Set(previous.map(row => row.kind + ':' + row.name)).size, 18);
for (const row of previous) {
  assert.equal(row.equal, true);
  assert.ok(/^(?:family|group-create)\/[a-z-]+\.html$/.test(row.path));
  const raw = regular(path.join(fixtures, row.path));
  assert.equal(raw.length, row.bytes); assert.equal(hash(raw), row.sha256);
}
const states = new Map(manifest.states.map(state => [state.name, state]));
const report = {
  schema: 1, status: 'RUNNING',
  scope: 'Seven isolated native group-detail shapes with unchanged scripts/config, local synthetic read-only endpoints, seven no-JS shapes and eighteen prior body equalities. No authenticated route, DB, real WebSocket/provider, authority or production claim.',
  viewport: {width: 1280, height: 720}, PGProducerSource: producerSource,
  gitBridgeBefore: bridgeBefore, fixtureManifestSHA256: hashFile(manifestPath),
  previousBodyEquality: previous, normalization, sourcesBefore,
  setup: {node: process.version, scriptSHA256: hashFile(fileURLToPath(import.meta.url)), playwrightVersion: require(path.join(playwright, 'package.json')).version, axeFile, axeSHA256: hashFile(axeFile), chrome},
  states: [], noJavaScriptStates: [], requests: [], assetResponses: [], requestFailures: [], pageErrors: [], consoleErrors: [], cspConsole: [], webSockets: [], mockEndpoints: [], fixturePolicyEnforcement: [], failures: [],
  ownership: {profiles: [], serverStopped: false},
};
const check = (ok, message) => { if (!ok) report.failures.push(message); };
const contexts = [], pending = new Set();
let base, current = 'setup', activeState, activeNoJS = false;
const flushResponses = async () => { while (pending.size) await Promise.all([...pending]); };
const localPNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64');
const allowedCards = new Set(), allowedMedia = new Set();
for (const state of manifest.states) {
  const raw = regular(path.join(fixtures, state.name + '.html'));
  assert.equal(raw.length, state.bytes); assert.equal(hash(raw), state.sha256);
  const html = raw.toString();
  assert.ok(!/<style(?:\s|>)/i.test(html), 'no rendered style element');
  for (const match of html.matchAll(/data-hovercard-user="([0-9a-f-]+)"/g)) allowedCards.add('/people/' + match[1] + '/card/');
  for (const match of html.matchAll(/(?:src|href)="(\/api\/media\/[^"?]+\/)"/g)) allowedMedia.add(match[1]);
}
const server = http.createServer((request, response) => {
  const url = new URL(request.url, 'http://fixture.invalid');
  if (request.method !== 'GET') {
    check(false, current + ': synthetic server received mutation');
    response.writeHead(405); response.end('Read-only synthetic fixture'); return;
  }
  let raw, type;
  if (url.pathname === '/qa/axe.js') {raw = regular(axeFile); type = 'text/javascript; charset=utf-8';}
  else if (url.pathname.startsWith('/static/')) {
    const name = decodeURIComponent(url.pathname.slice(8));
    if (!name || path.isAbsolute(name) || name.includes('\\') || name.split('/').includes('..')) {
      response.writeHead(404); response.end(); return;
    }
    const file = path.join(repo, 'static', name);
    if (!fs.existsSync(file) || fs.realpathSync(file) !== file || !fs.lstatSync(file).isFile()) {
      response.writeHead(404); response.end(); return;
    }
    raw = regular(file);
    type = name.endsWith('.css') ? 'text/css' : name.endsWith('.js') ? 'text/javascript; charset=utf-8' : name.endsWith('.woff2') ? 'font/woff2' : 'application/octet-stream';
  } else if (url.pathname === '/sw.js') {
    raw = regular(path.join(repo, 'services/server/internal/web/assets/meetups-worker.js'));
    type = 'text/javascript; charset=utf-8';
    report.mockEndpoints.push({state: current, path: url.pathname, scope: 'unchanged current offline worker, isolated throwaway profile', sha256: hash(raw)});
  } else if (allowedCards.has(url.pathname)) {
    raw = Buffer.from('<p>Generated fixture profile preview</p>'); type = 'text/html; charset=utf-8';
    report.mockEndpoints.push({state: current, path: url.pathname, scope: 'synthetic scoped hovercard response; no visibility authority claim'});
  } else if (allowedMedia.has(url.pathname)) {
    raw = localPNG; type = 'image/png';
    report.mockEndpoints.push({state: current, path: url.pathname, scope: 'fixed generated image; no codec, signing or media authorization claim', sha256: hash(raw)});
  } else {
    const match = /^\/preview\/([a-z-]+)\/$/.exec(url.pathname);
    if (!match || !states.has(match[1])) {response.writeHead(404); response.end(); return;}
    const state = states.get(match[1]);
    raw = regular(path.join(fixtures, state.name + '.html')); type = 'text/html; charset=utf-8';
    for (const [name, value] of Object.entries(state.renderer_headers)) {
      assert.ok(!/^set-cookie$/i.test(name), 'fixture Renderer cannot create authenticated cookies');
      response.setHeader(name, value);
    }
    const actualMode = Object.keys(state.renderer_headers).find(name => name.toLowerCase() === 'content-security-policy') ||
      Object.keys(state.renderer_headers).find(name => name.toLowerCase() === 'content-security-policy-report-only');
    assert.ok(actualMode, 'actual Renderer CSP receipt required');
    const policy = state.renderer_headers[actualMode];
    response.setHeader('Content-Security-Policy', policy);
    report.fixturePolicyEnforcement.push({state: state.name, actualRendererHeader: actualMode, enforcedSamePolicyForIsolatedFixture: true});
  }
  response.setHeader('Content-Type', type);
  // Assets are current exact bytes; no-cache prevents an earlier fixture state
  // from masquerading as a successful current asset load.
  if (!response.hasHeader('Cache-Control')) response.setHeader('Cache-Control', 'private, no-store');
  response.setHeader('X-Content-Type-Options', 'nosniff');
  response.end(raw);
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
async function metricsFor(page) {
  return page.evaluate(() => {
    const main = document.querySelector('main#main'), ids = [...document.querySelectorAll('[id]')].map(element => element.id), unresolved = [];
    for (const element of document.querySelectorAll('[for],[aria-labelledby],[aria-describedby]'))
      for (const name of ['for', 'aria-labelledby', 'aria-describedby'])
        for (const id of (element.getAttribute(name) || '').split(/\s+/).filter(Boolean)) if (!document.getElementById(id)) unresolved.push(id);
    const config = id => {const element = document.getElementById(id); return element ? JSON.parse(element.textContent) : null;};
    return {
      native: document.body.classList.contains('group-detail-page'), language: document.documentElement.lang,
      theme: document.documentElement.dataset.theme, text: document.documentElement.dataset.text, motion: document.documentElement.dataset.motion,
      heading: main?.querySelector('h1')?.textContent.trim(), canonical: document.querySelector('link[rel=canonical]')?.getAttribute('href'),
      fontsStatus: document.fonts.status, overflow: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - innerWidth,
      duplicateIDs: ids.filter((value, index) => ids.indexOf(value) !== index), unresolved,
      unlabelled: [...document.querySelectorAll('input:not([type=hidden]),select,textarea')].filter(element => !element.labels?.length && !element.getAttribute('aria-label') && !element.getAttribute('aria-labelledby')).map(element => element.id || element.name),
      stylesheetSources: [...document.querySelectorAll('link[rel=stylesheet]')].map(element => element.getAttribute('href')),
      scriptSources: [...document.scripts].filter(element => element.src).map(element => element.getAttribute('src')),
      executableInline: [...document.scripts].filter(element => !element.src && element.type !== 'application/json').length,
      eventAttributes: [...document.querySelectorAll('*')].flatMap(element => [...element.attributes].filter(attribute => /^on/i.test(attribute.name)).map(attribute => attribute.name)),
      styleElements: document.querySelectorAll('style').length,
      inlineStyleCount: document.querySelectorAll('[style]').length,
      nonceAttributes: [...document.querySelectorAll('script[nonce]')].map(element => element.nonce),
      scriptOrder: [...document.scripts].map(element => element.id || element.getAttribute('src')),
      chatConfig: config('thread-chat-config'), nudgeConfig: config('presend-nudge'),
      roster: [...main.querySelectorAll('ul.members li')].map(element => element.textContent.trim()),
      rosterVisible: !!main.querySelector('ul.members'),
      rootPostIDs: [...main.querySelectorAll('#thread-list > article.post')].map(element => Number(element.id.slice(5))),
      replyIDs: [...main.querySelectorAll('#thread-list > .replies article.post')].map(element => Number(element.id.slice(5))),
      postTexts: [...main.querySelectorAll('#thread-list article.post')].map(element => element.querySelector(':scope > .meta')?.nextElementSibling?.innerText || ''),
      announcements: [...main.querySelectorAll('.post-announcement')].map(element => element.innerText.trim()),
      feedTitles: [...main.querySelectorAll('.activity-card h3 a')].map(element => element.textContent.trim()),
      reportPostIDs: [...main.querySelectorAll('a[href^="/report/"]')].map(element => Number(new URL(element.href).searchParams.get('id'))).sort((a, b) => a - b),
      thread: !!main.querySelector('#thread-list'), compose: !!main.querySelector('#compose'),
      mainActions: [...main.querySelectorAll('form')].map(form => form.getAttribute('action')),
      membershipActions: [...main.querySelectorAll('.group-membership form')].map(form => form.getAttribute('action')),
      forms: [...document.forms].map(form => ({
        action: form.getAttribute('action'), method: form.method,
        csrf: [...form.querySelectorAll('input[name=csrfmiddlewaretoken]')].map(element => element.value),
        fields: [...form.querySelectorAll('input,textarea,select')].map(element => ({name: element.name, type: element.type, value: element.value})),
        data: [...new FormData(form).entries()].map(([name, value]) => [name, String(value)]),
      })),
    };
  });
}
async function keyboardChecks(page, state, label) {
  const records = [];
  await page.locator('main#main').focus(); await page.keyboard.press('Tab');
  const first = await page.evaluate(() => ({href: document.activeElement?.getAttribute('href'), focus: getComputedStyle(document.activeElement).outlineStyle}));
  check(first.href === '/groups/' && first.focus !== 'none', label + ': visible first main keyboard destination'); records.push(first);
  const account = page.locator('header details.nav-menu.nav-end'), summary = account.locator('summary');
  await summary.focus(); await page.keyboard.press('Enter');
  check(await account.evaluate(element => element.open), label + ': keyboard account disclosure open');
  await page.keyboard.press('Enter');
  check(!await account.evaluate(element => element.open), label + ': keyboard account disclosure close');
  const compose = page.locator('#compose textarea[name=body]');
  if (await compose.count()) {
    const original = await compose.inputValue(); await compose.focus();
    await compose.fill('Generated keyboard fixture text'); check(await compose.inputValue() === 'Generated keyboard fixture text', label + ': ordinary textarea editing');
    await compose.fill(original);
    records.push({composeValueRestored: await compose.inputValue() === original});
  }
  const question = page.locator('select[name=prompt]');
  if (await question.count()) {
    const options = await question.locator('option').evaluateAll(rows => rows.map(row => row.value)), original = await question.inputValue();
    check(options.length === 5, label + ': fixed ask-organiser choices');
    await question.focus(); await question.press('Home'); await question.press('ArrowDown');
    check(await question.inputValue() === options[1], label + ': native question keyboard select');
    await question.selectOption(original); records.push({questionValues: options, restored: await question.inputValue() === original});
  }
  // Read ordinary form payloads without dispatching a mutation. Actual CSRF,
  // authorization, audit and transport are qualified by the complete App suite.
  for (const form of await page.locator('main form').all()) {
    const row = await form.evaluate(element => ({action: element.getAttribute('action'), method: element.method, fields: [...new FormData(element).entries()].map(([name, value]) => [name, String(value)])}));
    check(row.method === 'post' && row.fields.filter(([name, value]) => name === 'csrfmiddlewaretoken' && value === state.synthetic_csrf).length === 1, label + ': ordinary form serialization');
    records.push(row);
  }
  return records;
}
async function confirmationChecks(page, state, label) {
  const expected = state.expected.confirmations;
  assert.ok(Array.isArray(expected), 'source-projected confirmation inventory required');
  const records = [];
  for (const wanted of expected) {
    assert.match(wanted.action, /^\/groups\/[1-9][0-9]*\/(?:archive\/|posts\/[1-9][0-9]*\/delete\/)$/);
    assert.ok(typeof wanted.text === 'string' && wanted.text.length > 0);
    const form = page.locator('main form[action=' + JSON.stringify(wanted.action) + ']');
    assert.equal(await form.count(), 1, 'exact current confirmation form');
    check(await form.getAttribute('data-confirm') === wanted.text, label + ': source-projected current confirmation text');
    const button = form.locator('button[type=submit]');
    assert.equal(await button.count(), 1, 'one ordinary confirmation submit button');
    const urlBefore = page.url(), requestStart = report.requests.length, dialogs = [];
    await button.focus();
    let timer, onDialog;
    const handled = new Promise((resolve, reject) => {
      timer = setTimeout(() => reject(new Error('Actual native confirmation dialog did not appear')), 5000);
      onDialog = dialog => {
        const row = {type: dialog.type(), message: dialog.message(), dismissed: false};
        dialogs.push(row);
        check(row.type === 'confirm' && row.message === wanted.text, label + ': actual native confirmation type/text');
        // The actual unchanged site.js receives false, then prevents the real
        // submit. No request interception or form replacement supplies this proof.
        dialog.dismiss().then(() => {row.dismissed = true; resolve();}, reject);
      };
      page.on('dialog', onDialog);
    });
    try {
      await Promise.all([button.press('Enter', {timeout: 5000}), handled]);
    } finally {
      clearTimeout(timer);
      page.off('dialog', onDialog);
    }
    await flushResponses();
    const requests = report.requests.slice(requestStart);
    check(dialogs.length === 1 && dialogs[0].dismissed, label + ': one cancelled native confirmation required');
    check(page.url() === urlBefore && requests.every(row => row.method === 'GET'), label + ': cancelled confirmation navigated or dispatched a mutation');
    records.push({action: wanted.action, expectedText: wanted.text, dialogs, urlUnchanged: page.url() === urlBefore, mutationRequests: requests.filter(row => row.method !== 'GET')});
  }
  const actualCount = await page.locator('main form[data-confirm]').count();
  check(actualCount === expected.length, label + ': complete archive/delete confirmation inventory');
  return {executed: true, expectedCount: expected.length, records};
}
async function settleAndColors(page) {
  return page.evaluate(async () => {
    const colors = () => [...document.querySelectorAll('.group-detail-page .btn,.group-detail-page .rx-btn,.group-detail-page .linkbtn')].map(element => {
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
async function exercise(page, state, noJS) {
  current = (noJS ? 'no-js:' : '') + state.name; activeState = state; activeNoJS = noJS;
  const label = current, assetStart = report.assetResponses.length, socketStart = report.webSockets.length;
  const response = await page.goto(base + '/preview/' + state.name + '/', {waitUntil: 'networkidle'});
  // In JS-disabled contexts CDP evaluation remains available for synchronous
  // DOM inspection, but document.fonts/RAF promises cannot run: never await them.
  if (!noJS) await page.evaluate(() => document.fonts.ready);
  await flushResponses();
  const metrics = await metricsFor(page), expected = state.expected;
  check(metrics.native && metrics.heading === expected.title, label + ': native complete group heading');
  check(metrics.language === state.language && metrics.theme === state.theme && metrics.text === state.text && metrics.motion === state.motion, label + ': current display/language');
  check(metrics.canonical === state.canonical && metrics.overflow <= 0 && metrics.fontsStatus === 'loaded', label + ': canonical/layout/local font readiness');
  check(!metrics.duplicateIDs.length && !metrics.unresolved.length && !metrics.unlabelled.length, label + ': native IDs/labels/references');
  check(JSON.stringify(metrics.stylesheetSources) === JSON.stringify(['/static/css/base.css', '/static/css/group-detail-native.css']), label + ': existing base plus scoped stylesheet order');
  check(JSON.stringify(metrics.scriptSources) === JSON.stringify(expected.script_sources), label + ': exact current product script order');
  check(!metrics.executableInline, label + ': unexpected executable inline script');
  check(!metrics.eventAttributes.length && !metrics.styleElements && (!noJS || !metrics.inlineStyleCount), label + ': live event attributes/style elements or no-JS inline style');
  check(isDeepStrictEqual(metrics.chatConfig, expected.chat_config) && isDeepStrictEqual(metrics.nudgeConfig, expected.nudge_config), label + ': exact admitted current JSON configs');
  const normalized = normalization.states.find(row => row.name === state.name);
  check(metrics.nonceAttributes.length === normalized.attribute_replacements &&
    metrics.nonceAttributes.every(value => value === normalization.replacement), label + ': admitted nonce-only normalization count/value');
  if (expected.live_thread && expected.nudge_config) check(metrics.scriptOrder.indexOf('/static/js/presend-nudge.js') < metrics.scriptOrder.indexOf('/static/js/thread-chat.js'), label + ': presend guard precedes live composer');
  check(metrics.rosterVisible === expected.roster_visible && metrics.roster.length === expected.roster_count, label + ': admitted roster/empty distinction');
  if (expected.roster_names) check(JSON.stringify(metrics.roster) === JSON.stringify(expected.roster_names), label + ': escaped admitted roster names/order');
  check(JSON.stringify(metrics.rootPostIDs) === JSON.stringify(expected.post_ids) && JSON.stringify(metrics.replyIDs) === JSON.stringify(expected.reply_ids), label + ': exact root/reply inventory/order');
  check(metrics.announcements.length === expected.announcement_count && JSON.stringify(metrics.feedTitles) === JSON.stringify(expected.feed_titles), label + ': own broadcasts and authorized feed preserved');
  // These finite fixtures use only **strong** decoration in their raw bodies.
  // Exact source BodyMarkup/escaping is independently asserted by pure tests;
  // this browser witness checks the supplied readable content and order.
  const plain = value => value.replaceAll('**', '').replace(/\s+/g, ' ').trim();
  check(metrics.postTexts.length === expected.post_texts.length &&
    metrics.postTexts.every((text, index) => plain(text) === plain(expected.post_texts[index])), label + ': existing readable root/reply bodies/order');
  check(metrics.announcements.length === expected.announcement_texts.length &&
    metrics.announcements.every((text, index) => plain(text).includes(plain(expected.announcement_texts[index]))), label + ': existing escaped static announcement content');
  check(metrics.thread === expected.show_thread && metrics.compose === expected.can_post, label + ': thread/composer current predicates');
  check(metrics.mainActions.includes(state.canonical + 'announce/') === expected.can_announce &&
    metrics.mainActions.includes(state.canonical + 'ask/') === expected.can_ask, label + ': exact owner announcement/minor question predicates');
  check(metrics.membershipActions.includes(state.canonical + 'archive/') === expected.owner &&
    metrics.membershipActions.includes(state.canonical + 'leave/') === (!expected.owner && expected.member) &&
    metrics.membershipActions.includes(state.canonical + 'join/') === (!expected.owner && !expected.member), label + ': owner/member/nonmember ordinary action boundary');
  check(JSON.stringify(metrics.reportPostIDs) === JSON.stringify([...expected.report_post_ids].sort((a, b) => a - b)), label + ': exact member/other-author Report targets');
  check(JSON.stringify(metrics.forms.map(form => form.action).sort()) === JSON.stringify([...expected.form_actions].sort()), label + ': complete unchanged ordinary action multiset');
  for (const form of metrics.forms) {
    check(form.method === 'post' && form.csrf.length === 1 && form.csrf[0] === state.synthetic_csrf, label + ': existing method/CSRF');
    check(form.action.startsWith('/') && !form.action.startsWith('//') && !form.action.includes('?') && !form.action.includes('#'), label + ': local fixed ordinary form action');
    check(form.fields.every(field => !/[\[\]]/.test(field.name) && !['owner', 'owner_id', 'user_id', 'is_staff', 'cohort'].includes(field.name)), label + ': no invented authority/bracket fields');
  }
  const headers = await response.allHeaders();
  for (const [name, value] of Object.entries(state.renderer_headers)) check(headers[name.toLowerCase()] === value, label + ': retained actual Renderer header ' + name);
  check(!headers['set-cookie'], label + ': synthetic renderer emitted session cookie');
  const socketRows = report.webSockets.slice(socketStart);
  check(socketRows.length === (!noJS && expected.live_thread ? 1 : 0), label + ': exact actual-config WebSocket count/static fallback disconnection');
  // Playwright's screenshotter itself waits on document.fonts.ready. Retain
  // screenshots for JS visits; no-JS keeps all synchronous DOM/keyboard/form/
  // positive-asset checks without invoking a hidden page-font promise.
  let screenshotName;
  if (!noJS) {
    screenshotName = state.name + '-desktop.png';
    await page.screenshot({path: path.join(output, screenshotName), fullPage: true});
  }
  const keyboard = await keyboardChecks(page, state, label), audits = [];
  const confirmations = noJS ? {executed: false, reason: 'Product JavaScript disabled; all ordinary forms/CSRF/FormData remain checked'} : await confirmationChecks(page, state, label);
  let steadyState;
  if (!noJS) {
    await page.addScriptTag({url: base + '/qa/axe.js'});
    await page.evaluate(() => {for (const detail of document.querySelectorAll('details')) detail.open = true;});
    steadyState = await settleAndColors(page);
    steadyState.initialColorsEqualSettled = initialColorsEqualSettled(steadyState.before, steadyState.after);
    check(steadyState.initialColorsEqualSettled && !steadyState.remainingAnimations, label + ': initial/settled button colors differ');
    for (const mode of ['all-open', 'header-closed', 'header-keyboard-open']) {
      if (mode === 'header-closed') await page.evaluate(() => {for (const element of document.querySelectorAll('header details')) element.open = false;});
      if (mode === 'header-keyboard-open') {
        const summary = page.locator('header details.nav-menu.nav-end > summary'); await summary.focus(); await page.keyboard.press('Enter');
        check(await summary.evaluate(element => element.parentElement.open), label + ': actual keyboard-open audit state');
      }
      if (mode !== 'all-open') await settleAndColors(page);
      const axe = await audit(page), filename = state.name + '-audit-' + mode + '.png';
      check(!axe.violations.length && !axe.incomplete.length, label + ': ' + mode + ' whole-document Axe findings/incomplete');
      const geometry = await page.evaluate(() => ({
        scroll: {x: scrollX, y: scrollY}, header: (() => {const element = document.querySelector('header'), rect = element.getBoundingClientRect(), style = getComputedStyle(element); return {x: rect.x, y: rect.y, width: rect.width, height: rect.height, color: style.color, backgroundColor: style.backgroundColor};})(),
        details: [...document.querySelectorAll('details')].map(element => ({classes: element.className, open: element.open})),
      }));
      await page.screenshot({path: path.join(output, filename), fullPage: false});
      audits.push({mode, axe, geometry, screenshot: filename, screenshotSHA256: hashFile(path.join(output, filename))});
    }
  }
  await flushResponses();
  const required = ['/static/css/base.css', '/static/css/group-detail-native.css',
    '/static/fonts/bricolage-grotesque-latin-wght-normal.woff2', '/static/fonts/bricolage-grotesque-latin-ext-wght-normal.woff2',
    ...(!noJS ? [...expected.script_sources, '/qa/axe.js'] : [])];
  const assets = required.map(name => {
    const file = name === '/qa/axe.js' ? axeFile : path.join(repo, name.slice(1)), raw = regular(file);
    const wanted = {path: name, bytes: raw.length, sha256: hash(raw)};
    const actual = report.assetResponses.slice(assetStart).filter(row => row.state === label && row.path === name);
    check(actual.length >= 1 && actual.every(row => row.status === 200 && row.bytes === wanted.bytes && row.sha256 === wanted.sha256), label + ': positive exact current asset absent/different ' + name);
    return {expected: wanted, actual};
  });
  return {name: state.name, javaScriptEnabled: !noJS, fixtureSHA256: state.sha256, htmlBytes: state.bytes, htmlGzipLevel9: zlib.gzipSync(regular(path.join(fixtures, state.name + '.html')), {level: 9}).length,
    metrics, keyboard, assets, steadyState, audits, webSockets: socketRows, confirmations,
    ...(screenshotName ? {screenshot: screenshotName, screenshotSHA256: hashFile(path.join(output, screenshotName))} : {})};
}
try {
  await new Promise((resolve, reject) => {server.once('error', reject); server.listen(0, '127.0.0.1', resolve);});
  base = 'http://127.0.0.1:' + server.address().port;
  for (const noJS of [false, true]) {
    // A separately owned short TMPDIR sibling avoids Chrome's Unix socket path
    // limit; never use a real browser/user profile or a deeply nested result path.
    const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'gd-'));
    const custody = {path: profile, closed: false, removed: false};
    report.ownership.profiles.push(custody);
    const context = await chromium.launchPersistentContext(profile, {executablePath: chrome, headless: true, viewport: report.viewport, locale: 'ro-RO', javaScriptEnabled: !noJS});
    contexts.push({context, profile, custody}); report.chromeVersion = context.browser().version();
    await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
    await context.routeWebSocket(/.*/, socket => {
      const url = new URL(socket.url()), expected = activeState?.expected;
      const row = {state: current, path: url.pathname, local: url.host === new URL(base).host, messages: [], scope: 'Playwright local mocked transport; no external/peer/authority data'};
      report.webSockets.push(row);
      const admitted = !activeNoJS && expected?.live_thread && expected.chat_config && row.local && url.pathname === '/ws/chat/' + expected.chat_config.threadId + '/';
      check(admitted, current + ': unexpected/metadata-fallback WebSocket');
      if (!admitted) {void socket.close(); return;}
      // Do not call connectToServer: the installed Playwright API opens only a
      // mocked browser socket. No peer/server messages or permissions are made up.
      socket.onMessage(message => {
        let value; try {value = JSON.parse(String(message));} catch {check(false, row.state + ': malformed synthetic socket message'); return;}
        check(value && value.type === 'typing' && Object.keys(value).length === 1, row.state + ': unexpected outbound fixture socket payload');
        row.messages.push(value);
      });
    });
    const page = context.pages()[0] || await context.newPage(); watch(page, () => current);
    for (const state of manifest.states) {
      const row = await exercise(page, state, noJS);
      (noJS ? report.noJavaScriptStates : report.states).push(row);
    }
    await context.close(); custody.closed = true;
  }
  check(report.requests.every(row => row.method === 'GET' && row.local), 'External or mutation HTTP request occurred');
  check(report.states.length === 7 && report.noJavaScriptStates.length === 7, 'Incomplete seven-by-two actual matrix');
} catch (error) {report.failures.push(String(error));
} finally {
  for (const row of contexts) if (!row.custody.closed) {
    try {await row.context.close(); row.custody.closed = true;} catch (error) {check(false, 'Owned browser close failed: ' + String(error));}
  }
  await flushResponses();
  for (const custody of report.ownership.profiles) {
    if (custody.closed) {
      try {fs.rmSync(custody.path, {recursive: true, force: false}); custody.removed = !fs.existsSync(custody.path);}
      catch (error) {check(false, 'Owned profile removal failed: ' + String(error));}
    }
    check(custody.closed && custody.removed, 'Owned browser/profile cleanup incomplete');
  }
  await new Promise(resolve => server.listening ? server.close(resolve) : resolve());
  report.ownership.serverStopped = !server.listening;
  try {
    report.gitBridgeAfter = bridge();
    check(report.gitBridgeAfter.ui_head === report.gitBridgeBefore.ui_head, 'Git HEAD changed during desktop QA');
    report.sourcesAfter = snapshot(); report.sourceUnchanged = JSON.stringify(report.sourcesBefore) === JSON.stringify(report.sourcesAfter);
    check(report.sourceUnchanged, 'Bound product/method/source bytes changed during desktop QA');
  } catch (error) {check(false, 'Final source bridge refused: ' + String(error));}
  report.status = report.failures.length ? 'fail' : 'pass';
  fs.writeFileSync(path.join(output, 'desktop-results.json'), JSON.stringify(report, null, 2) + '\n', {flag: 'wx'});
  console.log(JSON.stringify({status: report.status, states: report.states.length, noJavaScriptStates: report.noJavaScriptStates.length, priorBodies: previous.length, failures: report.failures, reportSHA256: hashFile(path.join(output, 'desktop-results.json')), ownership: report.ownership}));
  if (report.failures.length) process.exitCode = 1;
}
