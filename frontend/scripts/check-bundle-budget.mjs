import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

export const ENTRY = 'src/main.tsx';
export const INITIAL_BUDGET_BYTES = 40 * 1024;

/**
 * Follow only static Vite-manifest imports. Dynamic route chunks are intentionally excluded:
 * they are not fetched on first paint. CSS referenced by the entry/static chunks is included.
 */
export function collectInitialAssets(manifest, entry = ENTRY) {
  const visitedChunks = new Set();
  const assets = new Set();

  function visit(key) {
    if (visitedChunks.has(key)) return;
    const chunk = manifest[key];
    if (!chunk) throw new Error(`Vite manifest is missing chunk ${key}`);
    visitedChunks.add(key);
    if (chunk.file) assets.add(chunk.file);
    for (const css of chunk.css ?? []) assets.add(css);
    for (const imported of chunk.imports ?? []) visit(imported);
  }

  visit(entry);
  return [...assets].sort();
}

export function measureInitialGzip(outputDir, manifest, entry = ENTRY) {
  return collectInitialAssets(manifest, entry).map((asset) => {
    const bytes = gzipSync(readFileSync(resolve(outputDir, asset)), { level: 9 }).byteLength;
    return { asset, bytes };
  });
}

function run() {
  const scriptDir = dirname(fileURLToPath(import.meta.url));
  const outputDir = resolve(scriptDir, '../../static/frontend');
  const manifest = JSON.parse(readFileSync(resolve(outputDir, '.vite/manifest.json'), 'utf8'));
  const measured = measureInitialGzip(outputDir, manifest);
  const total = measured.reduce((sum, item) => sum + item.bytes, 0);

  for (const { asset, bytes } of measured) {
    console.log(`${(bytes / 1024).toFixed(2)} KiB gzip  ${asset}`);
  }
  console.log(
    `Initial static JS+CSS: ${(total / 1024).toFixed(2)} KiB gzip ` +
      `(budget ${(INITIAL_BUDGET_BYTES / 1024).toFixed(0)} KiB)`,
  );
  if (total > INITIAL_BUDGET_BYTES) {
    console.error(`Initial bundle exceeds its budget by ${total - INITIAL_BUDGET_BYTES} bytes.`);
    process.exitCode = 1;
  }
}

const invokedPath = process.argv[1] ? resolve(process.argv[1]) : '';
if (invokedPath === fileURLToPath(import.meta.url)) run();
