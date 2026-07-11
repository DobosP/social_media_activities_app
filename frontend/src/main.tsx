import { StrictMode, Suspense } from 'react';
import { createRoot } from 'react-dom/client';
import { RouterProvider } from 'react-router-dom';
import '@roedu/ui/styles.css';
import { ThemeProvider } from '@roedu/ui';
import './styles/app.css';
import { themeFromDocument } from './theme';
import { makeRouter } from './lib/router';
import { App as PreviewApp } from './App';

const el = document.getElementById('root');
if (el) {
  // Migrated screens (web/spa.html) carry data-route + a bootstrap island and
  // get the router; the DEBUG design-preview page mounts the showcase instead.
  const isSpaShell = el.dataset.route !== undefined;
  createRoot(el).render(
    <StrictMode>
      <ThemeProvider theme={themeFromDocument()}>
        <Suspense fallback={<div role="status">Se încarcă…</div>}>
          {isSpaShell ? <RouterProvider router={makeRouter()} /> : <PreviewApp />}
        </Suspense>
      </ThemeProvider>
    </StrictMode>,
  );
}
