import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

// Executes the actual widget with synthetic presentation inputs. This is neither
// a browser/computed-style/CSP test nor a backend/cohort/privacy qualification.
const script = fs.readFileSync(new URL('../../static/js/community-graph.js', import.meta.url), 'utf8');
const stylesheet = fs.readFileSync(new URL('../../static/css/base.css', import.meta.url), 'utf8');
const untouched = 'synthetic original graph content';
const oneNode = () => ({ nodes: [{ kind: 'community', label: 'Synthetic community', activity_count: 2 }], links: [] });

async function run(settings = {}) {
  const element = { innerHTML: untouched, clientWidth: 640, clientHeight: 480 };
  const calls = { fetch: [], json: 0, graph: [], canvas: [], listeners: [] };
  const data = settings.data ?? oneNode();
  const graph = {};
  for (const method of ['backgroundColor', 'graphData', 'nodeLabel', 'nodeColor', 'nodeVal', 'linkColor', 'linkWidth', 'cooldownTicks', 'onNodeClick', 'width', 'height']) {
    graph[method] = (...args) => { calls.graph.push([method, ...args]); return graph; };
  }
  const context = {
    document: {
      getElementById(name) { return name === 'mz-graph' && settings.mount !== false ? element : null; },
      createElement(name) {
        assert.equal(name, 'canvas');
        if (settings.canvasThrows) throw new Error('synthetic canvas refusal');
        return { getContext(kind) { calls.canvas.push(kind); return settings.noContext ? null : {}; } };
      },
    },
    navigator: { connection: { saveData: settings.saveData === true } },
    window: {
      WebGLRenderingContext: settings.noWebGLConstructor ? undefined : function SyntheticWebGL() {},
      matchMedia(query) {
        assert.equal(query, '(prefers-reduced-motion: reduce)');
        return { matches: settings.reduce === true };
      },
      addEventListener(name, callback) { calls.listeners.push([name, callback]); },
    },
    fetch(url, options) {
      calls.fetch.push({ url, accept: options.headers.Accept });
      if (settings.fetchRejects) return Promise.reject(new Error('synthetic fetch refusal'));
      return Promise.resolve({
        ok: settings.httpOK !== false,
        json() {
          calls.json++;
          return settings.jsonRejects ? Promise.reject(new Error('synthetic JSON refusal')) : Promise.resolve(data);
        },
      });
    },
  };
  if (settings.library !== false) {
    context.ForceGraph3D = () => (target) => {
      assert.equal(target, element);
      calls.graph.push(['mount', target]);
      return graph;
    };
  }
  vm.runInNewContext(script, context, { filename: 'actual-community-graph.js', timeout: 1000 });
  // All fixture promises settle in microtasks; the next event-loop turn observes
  // their real then/catch chain without a guessed delay or access to widget state.
  await new Promise((resolve) => setImmediate(resolve));
  return { element, calls, data };
}

function expectFallback(fixture, message) {
  assert.equal(fixture.element.innerHTML,
    '<p class="muted graph-fallback">' + message + ' <a href="/communities/">Browse the list</a>.</p>');
  assert.doesNotMatch(fixture.element.innerHTML, /\bstyle\s*=/i);
  assert.equal(fixture.calls.graph.length, 0);
  assert.equal(fixture.calls.listeners.length, 0);
}

function expectGraphRequest(fixture) {
  assert.deepEqual(fixture.calls.fetch, [{ url: '/api/communities/communities/graph/', accept: 'application/json' }]);
}

test('absent graph mount preserves content and does not request data', { timeout: 2000 }, async () => {
  const fixture = await run({ mount: false });
  assert.equal(fixture.element.innerHTML, untouched);
  assert.deepEqual(fixture.calls, { fetch: [], json: 0, graph: [], canvas: [], listeners: [] });
});

for (const [name, settings] of [
  ['save-data', { saveData: true }],
  ['reduced-motion', { reduce: true }],
  ['no-WebGL-constructor', { noWebGLConstructor: true }],
  ['no-WebGL-context', { noContext: true }],
  ['canvas-refusal', { canvasThrows: true }],
]) {
  test('capability fallback: ' + name, { timeout: 2000 }, async () => {
    const fixture = await run(settings);
    expectFallback(fixture, 'The 3D view is off on this device.');
    assert.deepEqual(fixture.calls.fetch, []);
    assert.equal(fixture.calls.json, 0);
    if (settings.saveData || settings.reduce || settings.noWebGLConstructor || settings.canvasThrows) {
      assert.deepEqual(fixture.calls.canvas, []);
    } else {
      assert.deepEqual(fixture.calls.canvas, ['webgl', 'experimental-webgl']);
    }
  });
}

for (const [name, settings, message, jsonCalls] of [
  ['empty graph', { data: { nodes: [], links: [] } }, 'No communities to graph yet.', 1],
  ['missing nodes', { data: { links: [] } }, 'No communities to graph yet.', 1],
  ['non-OK response', { httpOK: false }, 'No communities to graph yet.', 0],
  ['missing library', { library: false }, "The 3D library didn't load.", 1],
  ['fetch rejection', { fetchRejects: true }, "Couldn't load the graph.", 0],
  ['JSON rejection', { jsonRejects: true }, "Couldn't load the graph.", 1],
]) {
  test('response fallback: ' + name, { timeout: 2000 }, async () => {
    const fixture = await run(settings);
    expectFallback(fixture, message);
    expectGraphRequest(fixture);
    assert.equal(fixture.calls.json, jsonCalls);
  });
}

test('usable graph is not replaced by fallback presentation', { timeout: 2000 }, async () => {
  const fixture = await run();
  expectGraphRequest(fixture);
  assert.equal(fixture.calls.json, 1);
  assert.equal(fixture.element.innerHTML, untouched);
  assert.equal(fixture.calls.graph[0][0], 'mount');
  assert.equal(fixture.calls.graph.filter(([name]) => name === 'mount').length, 1);
  assert.equal(fixture.calls.graph.find(([name]) => name === 'graphData')[1], fixture.data);
  assert.equal(fixture.calls.listeners.length, 1);
  assert.equal(fixture.calls.listeners[0][0], 'resize');
});

test('source stylesheet maps only the graph fallback class to the original one-rem padding', () => {
  // Source mapping only: no claim about browser cascade, computed style or CSP.
  const rule = '.graph-canvas .graph-fallback { padding: 1rem; }';
  assert.equal(stylesheet.split(rule).length, 2);
  assert.equal(stylesheet.split('.graph-fallback').length, 2);
});
