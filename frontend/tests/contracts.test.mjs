import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import { dirname, resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { collectInitialAssets, measureInitialGzip } from '../scripts/check-bundle-budget.mjs';

const frontendDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');

test('screen registry preserves one eager route and lazy-loads the rest', () => {
  const source = readFileSync(resolve(frontendDir, 'src/screens/registry.ts'), 'utf8');
  const routeIds = [...source.matchAll(/\{\s*route:\s*'([^']+)'\s*,\s*path:/g)].map(
    (match) => match[1],
  );

  assert.equal(routeIds.length, 20);
  assert.equal(new Set(routeIds).size, routeIds.length);
  assert.match(source, /import \{ HomeScreen \} from '\.\/HomeScreen';/);
  assert.doesNotMatch(source, /import \{[^\n]*Screen[^\n]*\} from '\.\/(?!HomeScreen)/);
  assert.equal((source.match(/lazy\(\(\) =>/g) ?? []).length, routeIds.length - 1);
});

test('Preact compatibility and Suspense remain load-bearing frontend contracts', () => {
  const viteConfig = readFileSync(resolve(frontendDir, 'vite.config.ts'), 'utf8');
  const main = readFileSync(resolve(frontendDir, 'src/main.tsx'), 'utf8');
  const packageJson = JSON.parse(readFileSync(resolve(frontendDir, 'package.json'), 'utf8'));

  assert.match(viteConfig, /from '@preact\/preset-vite'/);
  assert.doesNotMatch(viteConfig, /@vitejs\/plugin-react/);
  assert.match(main, /<Suspense fallback=/);
  assert.equal(packageJson.devDependencies['@vitejs/plugin-react'], undefined);
  assert.equal(packageJson.devDependencies['@types/react'], undefined);
  assert.equal(packageJson.devDependencies['@types/react-dom'], undefined);
});

test('bundle walker follows recursive static imports and excludes dynamic routes', () => {
  const outputDir = mkdtempSync(resolve(os.tmpdir(), 'social-bundle-budget-'));
  const manifest = {
    entry: {
      file: 'assets/entry.js',
      imports: ['shared'],
      dynamicImports: ['lazy'],
      css: ['assets/app.css'],
    },
    shared: { file: 'assets/shared.js' },
    lazy: { file: 'assets/lazy.js' },
  };
  mkdirSync(resolve(outputDir, 'assets'));
  writeFileSync(resolve(outputDir, 'assets/entry.js'), 'entry'.repeat(100));
  writeFileSync(resolve(outputDir, 'assets/shared.js'), 'shared'.repeat(100));
  writeFileSync(resolve(outputDir, 'assets/lazy.js'), 'lazy'.repeat(100));
  writeFileSync(resolve(outputDir, 'assets/app.css'), 'css'.repeat(100));

  try {
    assert.deepEqual(collectInitialAssets(manifest, 'entry'), [
      'assets/app.css',
      'assets/entry.js',
      'assets/shared.js',
    ]);
    const measured = measureInitialGzip(outputDir, manifest, 'entry');
    assert.deepEqual(
      measured.map(({ asset }) => asset),
      ['assets/app.css', 'assets/entry.js', 'assets/shared.js'],
    );
    assert.ok(measured.every(({ bytes }) => bytes > 0));
  } finally {
    rmSync(outputDir, { recursive: true, force: true });
  }
});
