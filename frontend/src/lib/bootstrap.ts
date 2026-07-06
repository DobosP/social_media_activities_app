/**
 * The Django↔React data contract (ADR-0016 Phase 2).
 *
 * A migrated screen's Django view answers two ways:
 * - full page: web/spa.html embeds this payload as a nonced JSON island;
 * - `?_data=1`: the same payload as JSON, fetched by route loaders on
 *   client-side navigation.
 */
export interface SpaPayload<T = unknown> {
  route: string;
  title: string;
  /** CSRF token for plain-form POSTs rendered by React screens. */
  csrf: string;
  data: T;
}

export function readIsland(): SpaPayload | null {
  const el = document.getElementById('spa-bootstrap');
  if (!el?.textContent) return null;
  try {
    return JSON.parse(el.textContent) as SpaPayload;
  } catch {
    return null;
  }
}

export async function fetchPayload(url: string, signal?: AbortSignal): Promise<SpaPayload> {
  const u = new URL(url, window.location.origin);
  u.searchParams.set('_data', '1');
  const res = await fetch(u, {
    credentials: 'same-origin',
    signal,
    headers: { Accept: 'application/json' },
  });
  if (!res.ok) throw new Error(`soft-nav failed: ${res.status}`);
  const type = res.headers.get('content-type') ?? '';
  if (!type.includes('application/json')) {
    // e.g. a session-expiry redirect landed on the HTML login page.
    throw new Error('soft-nav got a non-JSON response');
  }
  return (await res.json()) as SpaPayload;
}

/**
 * Keep the server-rendered chrome honest during client-side navigation:
 * document title plus the bottom tab bar's active state (base.html renders it
 * from the resolved URL name; on soft-nav we re-derive it from the pathname).
 */
export function syncChrome(title: string): void {
  document.title = title;
  const path = window.location.pathname;
  document.querySelectorAll<HTMLAnchorElement>('.tabbar a').forEach((a) => {
    const href = a.getAttribute('href') ?? '';
    const active = href !== '/' ? path.startsWith(href) : path === '/';
    a.classList.toggle('is-active', active);
    if (active) a.setAttribute('aria-current', 'page');
    else a.removeAttribute('aria-current');
  });
}
