import { SmartLink } from './SmartLink';
import type { Tab } from '../screens/types3';

/** The legacy .tabs sub-nav (base.css styles it); active = current pathname. */
export function TabStrip({ tabs }: { tabs: Tab[] }) {
  const path = window.location.pathname;
  return (
    <div className="tabs">
      {tabs.map((t) => (
        <SmartLink key={t.url} href={t.url} className={t.url === path ? 'is-active' : undefined}>
          {t.label}
          {t.pill ? <span className="pill">{t.pill}</span> : null}
        </SmartLink>
      ))}
    </div>
  );
}
