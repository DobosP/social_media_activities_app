import { Breadcrumbs } from '../components/Breadcrumbs';
import { EventCard } from '../components/EventCard';
import { SmartLink } from '../components/SmartLink';
import type { ScreenProps } from './registry';
import type { ThingsCityData, ThingsDetailData, ThingsIndexData } from './types';

export function ThingsIndexScreen({ payload }: ScreenProps) {
  const data = payload.data as ThingsIndexData;
  return (
    <div className="sa-screen">
      <h1>{data.ui.title}</h1>
      <p className="muted">{data.ui.lead}</p>
      {data.cities.length === 0 && <p className="sa-empty">{data.ui.empty}</p>}
      {data.cities.map((city) => (
        <section key={city.url}>
          <h2>
            <SmartLink href={city.url}>{city.name}</SmartLink>
          </h2>
          <ul className="link-list">
            {city.links.map((l) => (
              <li key={l.url}>
                <SmartLink href={l.url}>{l.label}</SmartLink>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

export function ThingsCityScreen({ payload }: ScreenProps) {
  const data = payload.data as ThingsCityData;
  return (
    <div className="sa-screen">
      <Breadcrumbs crumbs={data.breadcrumbs} />
      <h1>{data.ui.title}</h1>
      <ul className="link-list">
        {data.links.map((l) => (
          <li key={l.url}>
            <SmartLink href={l.url}>{l.label}</SmartLink>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ThingsDetailScreen({ payload }: ScreenProps) {
  const data = payload.data as ThingsDetailData;
  const { ui } = data;
  return (
    <div className="sa-screen">
      <Breadcrumbs crumbs={data.breadcrumbs} />
      <h1>{ui.title}</h1>
      {data.events.length > 0 && (
        <section>
          <h2>{ui.upcoming}</h2>
          <div className="sa-card-grid">
            {data.events.map((e) => (
              <EventCard key={e.pk} e={e} />
            ))}
          </div>
        </section>
      )}
      {data.places.length > 0 && (
        <section>
          <h2>{ui.places}</h2>
          <ul className="link-list">
            {data.places.map((p) => (
              <li key={p.url}>
                <a href={p.url}>{p.name}</a>
                {p.city && <span className="muted"> · {p.city}</span>}
              </li>
            ))}
          </ul>
        </section>
      )}
      <p className="muted">
        <SmartLink href={data.urls.exploreCity}>{ui.exploreCity}</SmartLink>
      </p>
      <p className="muted">
        <a href={data.urls.rss}>{ui.subscribe}</a>
      </p>
    </div>
  );
}
