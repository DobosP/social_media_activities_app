import type { ThemeOverride } from '@roedu/ui';

/**
 * "Aurora Social" — this app's bespoke identity for @roedu/ui.
 * Values MIRROR the legacy tokens in static/css/base.css (light `:root`,
 * `[data-theme="dark"]`, `[data-theme="contrast"]` blocks) so Django-rendered
 * pages and React screens are visually identical during the migration.
 * Change a colour in BOTH places or the two layers drift.
 */
export const auroraLight: ThemeOverride = {
  color: {
    bg: '#f6f6fb',
    surface: '#ffffff',
    surfaceMuted: '#eef0f8',
    text: '#17181d',
    textMuted: '#5a6072',
    border: '#e4e6f0',
    primary: '#4f46e5',
    primaryText: '#ffffff',
    accent: '#0d9488',
    danger: '#b91c1c',
    success: '#0f6a4f',
    focus: '#4f46e5',
  },
  font: {
    heading: "'Bricolage Grotesque', ui-sans-serif, system-ui, sans-serif",
  },
  radius: { sm: '10px', md: '14px', lg: '20px', pill: '999px' },
};

export const auroraDark: ThemeOverride = {
  color: {
    bg: '#0e0f16',
    surface: '#171926',
    surfaceMuted: '#14161f',
    text: '#eceefa',
    textMuted: '#9aa0bd',
    border: '#262a3b',
    primary: '#7c83ff',
    primaryText: '#0b0c15',
    accent: '#2dd4bf',
    danger: '#f87171',
    success: '#7ee0d0',
    focus: '#7c83ff',
  },
  font: {
    heading: "'Bricolage Grotesque', ui-sans-serif, system-ui, sans-serif",
  },
  radius: { sm: '10px', md: '14px', lg: '20px', pill: '999px' },
};

/** Maximal black-on-white, matching the legacy `contrast` display preference. */
export const auroraContrast: ThemeOverride = {
  color: {
    bg: '#ffffff',
    surface: '#ffffff',
    surfaceMuted: '#ffffff',
    text: '#000000',
    textMuted: '#1a1a1a',
    border: '#000000',
    primary: '#1e1b8f',
    primaryText: '#ffffff',
    accent: '#00453f',
    danger: '#8a0000',
    success: '#00401a',
    focus: '#1e1b8f',
  },
  font: {
    heading: "'Bricolage Grotesque', ui-sans-serif, system-ui, sans-serif",
  },
  radius: { sm: '10px', md: '14px', lg: '20px', pill: '999px' },
};

/** Decorative brand gradient (heroes, CTA chrome) — never behind body text. */
export const brandGradient = 'linear-gradient(135deg, #7c83ff 0%, #2dd4bf 100%)';

/**
 * Pick the theme from the server-stamped display preference
 * (`<html data-theme="auto|light|dark|contrast">`, cookie-backed — changing it
 * round-trips through Django, so a one-time read per page load is enough).
 */
export function themeFromDocument(): ThemeOverride {
  const pref = document.documentElement.dataset.theme ?? 'auto';
  if (pref === 'contrast') return auroraContrast;
  if (pref === 'dark') return auroraDark;
  if (pref === 'light') return auroraLight;
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? auroraDark : auroraLight;
}
