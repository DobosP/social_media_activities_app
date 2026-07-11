import { defineConfig } from 'vite';
import preact from '@preact/preset-vite';

// The SPA is served by Django: `vite build` emits hashed assets into
// static/frontend/ (picked up by collectstatic/WhiteNoise) plus a manifest that
// the `spa_assets` template tag reads to inject <script>/<link> with the CSP
// nonce. In dev, `vite` serves the SPA standalone and proxies API + static
// requests to runserver so everything stays same-origin.
export default defineConfig({
  // Preact's official compatibility preset aliases React imports from this app,
  // React Router, and @roedu/ui. Source code stays React-compatible while the
  // browser receives the much smaller Preact runtime.
  plugins: [preact()],
  base: '/static/frontend/',
  build: {
    outDir: '../static/frontend',
    emptyOutDir: true,
    manifest: true,
    sourcemap: false,
    rollupOptions: {
      input: 'src/main.tsx',
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8000', changeOrigin: true },
      // Do not proxy this frontend's own /static/frontend/ base back to
      // Django; Vite 8 otherwise follows its base redirect into the proxy.
      '^/static/(?!frontend/)': { target: 'http://127.0.0.1:8000', changeOrigin: true },
    },
  },
});
