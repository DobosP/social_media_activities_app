import { SmartLink } from './SmartLink';
import type { Crumb } from '../screens/types';

/** Same markup/classes as the legacy .breadcrumbs nav (base.css styles them). */
export function Breadcrumbs({ crumbs }: { crumbs: Crumb[] }) {
  return (
    <nav className="breadcrumbs" aria-label="…">
      <ol>
        {crumbs.map((c) =>
          c.url ? (
            <li key={c.name}>
              <SmartLink href={c.url}>{c.name}</SmartLink>
            </li>
          ) : (
            <li key={c.name}>
              <span aria-current="page">{c.name}</span>
            </li>
          ),
        )}
      </ol>
    </nav>
  );
}
