import { useNavigate } from 'react-router-dom';
import { Button } from '@roedu/ui';
import { EventCard } from '../components/EventCard';
import { SmartLink } from '../components/SmartLink';
import type { ScreenProps } from './registry';
import type { EventsData } from './types';

export function EventsScreen({ payload }: ScreenProps) {
  const data = payload.data as EventsData;
  const { ui, filters } = data;
  const navigate = useNavigate();

  const search = (params: Record<string, string>) => {
    const qs = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) if (v) qs.set(k, v);
    navigate(`${data.urls.action}?${qs}`);
  };

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>
      <p className="muted">
        <a href={data.urls.rss}>{ui.subscribe}</a>
        {' · '}
        <SmartLink href={data.urls.thingsIndex}>{ui.browseBy}</SmartLink>
      </p>
      <p className="muted">{ui.lead}</p>

      <form
        className="sa-toolbar"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          const fd = new FormData(e.currentTarget);
          search({
            q: String(fd.get('q') ?? ''),
            area: String(fd.get('area') ?? ''),
            activity: filters.activity,
          });
        }}
      >
        <input
          type="search"
          name="q"
          defaultValue={filters.query}
          minLength={2}
          placeholder={ui.searchPlaceholder}
          aria-label={ui.searchLabel}
        />
        {data.areas.length > 0 && (
          <select name="area" defaultValue={filters.area} aria-label={ui.areaLabel}>
            <option value="">{ui.anyArea}</option>
            {data.areas.map((a) => (
              <option key={a.slug} value={a.slug}>
                {a.name}
              </option>
            ))}
          </select>
        )}
        <Button type="submit" size="sm">
          {ui.search}
        </Button>
        {(filters.query || filters.area || filters.activity) && (
          <Button size="sm" variant="ghost" onClick={() => navigate(data.urls.action)}>
            {ui.clear}
          </Button>
        )}
      </form>

      {data.events.length === 0 ? (
        <p className="sa-empty">{ui.empty}</p>
      ) : (
        <div className="sa-card-grid">
          {data.events.map((e) => (
            <EventCard key={e.pk} e={e} />
          ))}
        </div>
      )}
    </div>
  );
}
