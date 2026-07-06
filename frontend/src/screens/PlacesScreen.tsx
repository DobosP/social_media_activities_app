import { useNavigate } from 'react-router-dom';
import { Badge, Button } from '@roedu/ui';
import type { ScreenProps } from './registry';
import type { PlacesData } from './types';

export function PlacesScreen({ payload }: ScreenProps) {
  const data = payload.data as PlacesData;
  const { ui, filters } = data;
  const navigate = useNavigate();

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>
      <p className="muted">
        {ui.lead} <a href={data.urls.map}>{ui.toMap} →</a>
      </p>

      <form
        className="sa-toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          const fd = new FormData(e.currentTarget);
          const qs = new URLSearchParams();
          for (const k of ['city', 'activity'] as const) {
            const v = String(fd.get(k) ?? '');
            if (v) qs.set(k, v);
          }
          navigate(`${data.urls.action}?${qs}`);
        }}
      >
        <label>
          {ui.city} <input type="text" name="city" defaultValue={filters.city} />
        </label>
        <label>
          {ui.activity} <input type="text" name="activity" defaultValue={filters.activity} />
        </label>
        <Button type="submit" size="sm">
          {ui.filter}
        </Button>
      </form>

      {data.flags.truncated && <p className="muted">{ui.truncated}</p>}

      {data.places.length === 0 ? (
        <p className="sa-empty">{ui.empty}</p>
      ) : (
        <ul className="members">
          {data.places.map((p) => (
            <li key={p.pk}>
              <a href={p.url}>{p.name}</a>
              {(p.street || p.city) && (
                <span className="muted">
                  {' · '}
                  {p.street}
                  {p.street && p.city ? `, ${p.city}` : p.city}
                </span>
              )}
              {p.distance && <span className="muted"> · {p.distance}</span>}{' '}
              {p.activities.map((name) => (
                <Badge key={name} tone="neutral" size="sm">
                  {name}
                </Badge>
              ))}
              {p.accessMatch && (
                <Badge tone="success" size="sm">
                  ✓ {ui.accessMatch}
                </Badge>
              )}
              {p.accessTags.map((t) => (
                <Badge key={t.label} tone={t.state === 'true' ? 'success' : 'neutral'} size="sm">
                  {t.label}
                  {t.state === 'limited' ? ` ${ui.limited}` : ''}
                </Badge>
              ))}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
