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
            <li key={p.pk} className="sa-place-row">
              {p.visual && (
                <figure className="sa-place-row__visual">
                  {p.visual.kind === 'photo' ? (
                    <>
                      <img src={p.visual.url} alt={p.visual.alt} loading="lazy" decoding="async" />
                      {p.visual.attribution && (
                        <figcaption className="muted">
                          {p.visual.sourcePageUrl ? (
                            <a href={p.visual.sourcePageUrl} rel="noopener noreferrer" target="_blank">
                              {p.visual.attribution}
                            </a>
                          ) : (
                            p.visual.attribution
                          )}
                        </figcaption>
                      )}
                    </>
                  ) : (
                    <span aria-hidden="true" dangerouslySetInnerHTML={{ __html: p.visual.svg }} />
                  )}
                </figure>
              )}
              <div className="sa-place-row__body">
                <a href={p.url}>{p.name}</a>
                {(p.street || p.city) && (
                  <span className="muted">
                    {' · '}
                    {p.street}
                    {p.street && p.city ? `, ${p.city}` : p.city}
                  </span>
                )}
                {p.distance && <span className="muted"> · {p.distance}</span>}
                <span className="sa-place-row__chips">
                  {p.categoryChips.map((name) => (
                    <Badge key={name} tone="neutral" size="sm">
                      {name}
                    </Badge>
                  ))}
                  {p.accessMatch && (
                    <Badge tone="success" size="sm">
                      ✓ {ui.accessMatch}
                    </Badge>
                  )}
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
