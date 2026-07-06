import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The SPA is served by Django: `vite build` emits hashed assets into
// static/frontend/ (picked up by collectstatic/WhiteNoise) plus a manifest that
// the `spa_assets` template tag reads to inject <script>/<link> with the CSP
// nonce. In dev, `vite` serves the SPA standalone and proxies API + static
// requests to runserver so everything stays same-origin.
export default defineConfig({
  plugins: [react()],
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
      '/static': { target: 'http://127.0.0.1:8000', changeOrigin: true },
    },
  },
});
