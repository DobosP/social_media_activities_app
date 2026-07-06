import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button } from '@roedu/ui';
import { ActivityCard, type ActivityCardData } from '../components/ActivityCard';
import type { ScreenProps } from './registry';
import type { BrowseData } from './types';

/**
 * The focused card deck — presentation-only navigation over ONE server page
 * (parity with static/js/browse-modes.js): swipes/keys/shuffle never touch the
 * network, never record anything (ADR-0007). Transforms go through the CSSOM
 * (node.style) — no style attributes under the strict CSP.
 */
function Deck({ cards, shuffleLabel }: { cards: ActivityCardData[]; shuffleLabel: string }) {
  const [order, setOrder] = useState<number[]>(() => cards.map((_, i) => i));
  const [current, setCurrent] = useState(0);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const trackRef = useRef<HTMLDivElement | null>(null);
  const drag = useRef<{ startX: number; delta: number; pointerId: number } | null>(null);

  const clamp = useCallback(
    (i: number) => Math.max(0, Math.min(cards.length - 1, i)),
    [cards.length],
  );

  const recenter = useCallback((extra = 0) => {
    const wrap = wrapRef.current;
    const track = trackRef.current;
    const item = track?.children[current] as HTMLElement | undefined;
    if (!wrap || !track || !item) return;
    const offset = wrap.clientWidth / 2 - item.offsetWidth / 2 - item.offsetLeft + extra;
    track.style.transform = `translateX(${offset}px)`;
  }, [current]);

  useLayoutEffect(() => {
    recenter();
    const track = trackRef.current;
    if (track) {
      Array.from(track.children).forEach((el, i) => {
        el.toggleAttribute('inert', i !== current);
        el.setAttribute('aria-hidden', i !== current ? 'true' : 'false');
      });
    }
  }, [current, order, recenter]);

  useEffect(() => {
    const onResize = () => recenter();
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, [recenter]);

  const shuffle = () => {
    const next = [...order];
    for (let i = next.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [next[i], next[j]] = [next[j], next[i]];
    }
    setOrder(next);
    setCurrent(0);
  };

  return (
    <div
      className="sa-deck"
      ref={wrapRef}
      tabIndex={0}
      role="group"
      onKeyDown={(e) => {
        if (e.key === 'ArrowRight') setCurrent((c) => clamp(c + 1));
        if (e.key === 'ArrowLeft') setCurrent((c) => clamp(c - 1));
      }}
      onPointerDown={(e) => {
        drag.current = { startX: e.clientX, delta: 0, pointerId: e.pointerId };
        (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
      }}
      onPointerMove={(e) => {
        if (!drag.current) return;
        drag.current.delta = e.clientX - drag.current.startX;
        recenter(drag.current.delta);
      }}
      onPointerUp={() => {
        const delta = drag.current?.delta ?? 0;
        drag.current = null;
        if (delta < -60) setCurrent((c) => clamp(c + 1));
        else if (delta > 60) setCurrent((c) => clamp(c - 1));
        else recenter();
      }}
      onPointerCancel={() => {
        drag.current = null;
        recenter();
      }}
    >
      <div className="sa-deck__track" ref={trackRef}>
        {order.map((idx, i) => (
          <div key={cards[idx].pk} className={`sa-deck__item${i === current ? ' is-current' : ''}`}>
            <ActivityCard a={cards[idx]} />
          </div>
        ))}
      </div>
      <div className="sa-deck__nav">
        <Button size="sm" variant="secondary" aria-label="←" onClick={() => setCurrent((c) => clamp(c - 1))}>
          ←
        </Button>
        <span className="sa-deck__count">
          {current + 1} / {cards.length}
        </span>
        <Button size="sm" variant="secondary" aria-label="→" onClick={() => setCurrent((c) => clamp(c + 1))}>
          →
        </Button>
        <Button size="sm" variant="ghost" onClick={shuffle}>
          {shuffleLabel}
        </Button>
      </div>
    </div>
  );
}

export function BrowseScreen({ payload }: ScreenProps) {
  const data = payload.data as BrowseData;
  const { ui, filters, page } = data;
  const navigate = useNavigate();
  const [view, setView] = useState<'list' | 'cards'>(data.viewMode);

  // Presentation-only toggle: mirror legacy browse-modes.js (replaceState, no refetch).
  const switchView = (v: 'list' | 'cards') => {
    setView(v);
    const url = new URL(window.location.href);
    url.searchParams.set('view', v);
    url.searchParams.delete('page');
    window.history.replaceState(null, '', url);
  };

  const withParams = (params: Record<string, string | null>) => {
    const search = new URLSearchParams(data.baseQs);
    search.set('view', view);
    for (const [k, v] of Object.entries(params)) {
      if (v === null) search.delete(k);
      else search.set(k, v);
    }
    return `${data.urls.action}?${search}`;
  };

  return (
    <div className="sa-screen">
      <div className="sa-section-head">
        <h1>{ui.title}</h1>
        <a className="sa-more" href={data.urls.organizeNew}>
          {ui.organizeOne}
        </a>
      </div>

      <div className="sa-toolbar" role="search">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const q = String(new FormData(e.currentTarget).get('q') ?? '');
            navigate(withParams({ q: q || null, page: null }));
          }}
        >
          <input type="search" name="q" defaultValue={filters.query} aria-label={ui.search} placeholder={ui.search} />
          <Button type="submit" size="sm" variant="secondary">
            {ui.search}
          </Button>
          {filters.query && (
            <Button size="sm" variant="ghost" onClick={() => navigate(withParams({ q: null, page: null }))}>
              {ui.clear}
            </Button>
          )}
        </form>
        <Button
          size="sm"
          variant={filters.beginnersOnly ? 'primary' : 'ghost'}
          onClick={() =>
            navigate(withParams({ beginners: filters.beginnersOnly ? null : 'true', page: null }))
          }
        >
          {filters.beginnersOnly ? ui.showAll : ui.beginnersOnly}
        </Button>
        <div className="seg" role="tablist">
          <button
            type="button"
            className={`seg-btn${view === 'list' ? ' is-active' : ''}`}
            aria-current={view === 'list' ? 'true' : undefined}
            onClick={() => switchView('list')}
          >
            {ui.list}
          </button>
          <button
            type="button"
            className={`seg-btn${view === 'cards' ? ' is-active' : ''}`}
            aria-current={view === 'cards' ? 'true' : undefined}
            onClick={() => switchView('cards')}
          >
            {ui.cards}
          </button>
        </div>
      </div>

      {filters.didYouMean && (
        <p className="muted">
          <a
            href={`${data.urls.action}?${filters.didYouMeanQ}`}
            onClick={(e) => {
              e.preventDefault();
              navigate(`${data.urls.action}?${filters.didYouMeanQ}`);
            }}
          >
            {filters.didYouMean}
          </a>
        </p>
      )}

      {data.cards.length === 0 ? (
        <p className="sa-empty">{ui.empty}</p>
      ) : view === 'cards' ? (
        <Deck cards={data.cards} shuffleLabel={ui.shuffle} />
      ) : (
        <div className="sa-card-grid">
          {data.cards.map((a) => (
            <ActivityCard key={a.pk} a={a} />
          ))}
        </div>
      )}

      {page.numPages > 1 && (
        <nav className="sa-deck__nav" aria-label={`${page.number} / ${page.numPages}`}>
          {page.previous && (
            <Button size="sm" variant="secondary" onClick={() => navigate(withParams({ page: String(page.previous) }))}>
              {ui.prev}
            </Button>
          )}
          <span className="sa-deck__count">
            {page.number} / {page.numPages}
          </span>
          {page.next && (
            <Button size="sm" variant="secondary" onClick={() => navigate(withParams({ page: String(page.next) }))}>
              {ui.next}
            </Button>
          )}
        </nav>
      )}
    </div>
  );
}
