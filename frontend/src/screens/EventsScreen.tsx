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
        key={JSON.stringify([filters, data.localPOC])}
        className="sa-toolbar"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          const fd = new FormData(e.currentTarget);
          search({
            q: String(fd.get('q') ?? ''),
            area: String(fd.get('area') ?? ''),
            activity: filters.activity,
            city: String(fd.get('city') ?? ''),
            ...(data.localPOC?.pastOnly ? { past_only: '1' } : data.localPOC?.includePast ? { include_past: '1' } : {}),
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
        {filters.city !== undefined && <input type="text" name="city" defaultValue={filters.city} placeholder={ui.cityLabel} aria-label={ui.cityLabel} />}
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
        {(filters.query || filters.area || filters.activity || filters.city) && (
          <Button size="sm" variant="ghost" onClick={() => navigate(data.urls.clear ?? data.urls.action)}>
            {ui.clear}
          </Button>
        )}
      </form>

      {data.localPOC && <p className="muted">
        {(data.localPOC.includePast || data.localPOC.pastOnly) && <><SmartLink href={data.localPOC.upcomingURL}>{ui.upcomingOnly}</SmartLink>{' · '}</>}
        {(!data.localPOC.includePast || data.localPOC.pastOnly) && <><SmartLink href={data.localPOC.includePastURL}>{ui.includePast}</SmartLink>{' · '}</>}
        {data.localPOC.pastOnly ? <strong>{ui.pastOnly}</strong> : <SmartLink href={data.localPOC.pastOnlyURL}>{ui.pastOnly}</SmartLink>}
      </p>}

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
