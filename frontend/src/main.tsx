import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@roedu/ui/styles.css';
import { ThemeProvider } from '@roedu/ui';
import { themeFromDocument } from './theme';
import { App } from './App';

const el = document.getElementById('root');
if (el) {
  createRoot(el).render(
    <StrictMode>
      <ThemeProvider theme={themeFromDocument()}>
        <App />
      </ThemeProvider>
    </StrictMode>,
  );
}
