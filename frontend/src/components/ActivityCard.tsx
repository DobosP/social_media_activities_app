import { Badge } from '@roedu/ui';
import { SmartLink } from './SmartLink';

/**
 * Mirrors the legacy _activity_card.html contract (ADR-0007): one contextual
 * cover photo OR a server-generated procedural accent — never galleries, never
 * engagement metrics. All display strings arrive pre-translated/formatted from
 * Django so i18n and date formatting stay server-owned.
 */
export interface ActivityCardData {
  pk: number;
  url: string;
  title: string;
  /** photo: cover image; accent: deterministic server-composed SVG banner. */
  visual: { kind: 'photo'; url: string; alt: string } | { kind: 'accent'; svg: string } | null;
  /** Pre-translated chip labels, in render order (type, cost, difficulty, …). */
  tags: string[];
  /** "D j M, H:i · place, city[ · N km away]" — assembled server-side. */
  meta: string;
  /** Truncated description (may be empty). */
  description: string;
  /** Recommendation annotation ("because you like X" / "87% match"), optional. */
  score: string | null;
}

export function ActivityCard({ a, showVisual = true }: { a: ActivityCardData; showVisual?: boolean }) {
  return (
    <article className="sa-acard">
      {showVisual && a.visual && (
        <div className="sa-acard__visual" aria-hidden={a.visual.kind === 'accent' ? true : undefined}>
          {a.visual.kind === 'photo' ? (
            <img src={a.visual.url} alt={a.visual.alt} loading="lazy" decoding="async" />
          ) : (
            // Server-composed procedural SVG (numbers + hsl() only — see
            // apps/web/templatetags/avatars.py activity_accent): same trust
            // basis as the template's mark_safe.
            <span dangerouslySetInnerHTML={{ __html: a.visual.svg }} />
          )}
        </div>
      )}
      <div className="sa-acard__body">
        <h3 className="sa-acard__title">
          <SmartLink href={a.url}>{a.title}</SmartLink>
        </h3>
        <div className="sa-acard__meta">
          {a.tags.map((t) => (
            <Badge key={t} tone="neutral" size="sm">
              {t}
            </Badge>
          ))}
          {a.score && <Badge tone="accent" size="sm">{a.score}</Badge>}
        </div>
        <p className="sa-acard__meta">{a.meta}</p>
        {a.description && <p className="sa-acard__meta">{a.description}</p>}
      </div>
    </article>
  );
}
