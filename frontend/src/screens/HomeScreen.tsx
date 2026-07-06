import { useNavigate } from 'react-router-dom';
import { Badge, Button, Card, Stack } from '@roedu/ui';
import { ActivityCard, type ActivityCardData } from '../components/ActivityCard';
import { SmartLink } from '../components/SmartLink';
import type { ScreenProps } from './registry';
import type { HomeData } from './types';

function CardStrip({ heading, items, more }: { heading: string; items: ActivityCardData[]; more?: { href: string; label: string } }) {
  if (!items.length) return null;
  return (
    <section>
      <div className="sa-section-head">
        <h2>{heading}</h2>
        {more && (
          <SmartLink className="sa-more" href={more.href}>
            {more.label}
          </SmartLink>
        )}
      </div>
      <div className="sa-card-grid">
        {items.map((a) => (
          <ActivityCard key={a.pk} a={a} />
        ))}
      </div>
    </section>
  );
}

export function HomeScreen({ payload }: ScreenProps) {
  const data = payload.data as HomeData;
  const { ui, urls } = data;
  const navigate = useNavigate();

  return (
    <div className="sa-screen">
      <h1>{ui.greeting}</h1>

      {data.guardianInvites.length > 0 && (
        <Card>
          <h2>{ui.guardianRequests}</h2>
          {data.guardianInvites.map((gi) => (
            <Stack key={gi.acceptAction} direction="row" gap="sm" align="center" wrap>
              <span>
                {gi.name} · {gi.relationship}
              </span>
              {/* Classic POST round-trips: Django redirects + flashes, exactly like the legacy page. */}
              <form method="post" action={gi.acceptAction} className="inline">
                <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                <Button type="submit" size="sm">
                  {ui.accept}
                </Button>
              </form>
              <form method="post" action={gi.declineAction} className="inline">
                <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                <Button type="submit" size="sm" variant="secondary">
                  {ui.decline}
                </Button>
              </form>
            </Stack>
          ))}
        </Card>
      )}

      <Stack direction="row" gap="sm" wrap>
        <Button onClick={() => (window.location.href = urls.organizeNew)}>{ui.organise}</Button>
        <Button variant="secondary" onClick={() => (window.location.href = urls.places)}>
          {ui.findPlace}
        </Button>
        <Button variant="ghost" onClick={() => (window.location.href = urls.series)}>
          {ui.series}
        </Button>
      </Stack>

      <form
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          const q = new FormData(e.currentTarget).get('q');
          navigate(`${urls.browse}?${new URLSearchParams({ q: String(q ?? '') })}`);
        }}
      >
        <Stack direction="row" gap="sm">
          <input type="search" name="q" aria-label={ui.search} placeholder={ui.search} />
          <Button type="submit" variant="secondary">
            {ui.search}
          </Button>
        </Stack>
      </form>

      {data.starterTypes.length > 0 && (
        <Card>
          <h2>{ui.starterHead}</h2>
          <form method="post" action={urls.interestsAction}>
            <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
            <Stack direction="row" gap="sm" wrap>
              {data.starterTypes.map((t) => (
                <label key={t.slug} className="tag">
                  <input type="checkbox" name="interests" value={t.slug} /> {t.name}
                </label>
              ))}
            </Stack>
            <Button type="submit" size="sm">
              {ui.starterSave}
            </Button>
          </form>
        </Card>
      )}

      <CardStrip heading={ui.recommended} items={data.sections.recommended} />
      <CardStrip heading={ui.beginnersHead} items={data.sections.beginners} />
      <CardStrip heading={ui.mine} items={data.sections.mine} />
      <CardStrip
        heading={ui.upcoming}
        items={data.sections.upcoming}
        more={{ href: urls.browse, label: `${ui.search} →` }}
      />

      {data.events.length > 0 && (
        <section>
          <div className="sa-section-head">
            <h2>{ui.eventsHead}</h2>
          </div>
          <div className="sa-card-grid">
            {data.events.map((e) => (
              <Card key={e.pk}>
                <h3 className="sa-acard__title">
                  <a href={e.url}>{e.title}</a>
                </h3>
                {e.reason && <Badge tone="accent" size="sm">{e.reason}</Badge>}
                <p className="sa-acard__meta">{e.meta}</p>
              </Card>
            ))}
          </div>
        </section>
      )}

      {data.groupUpdates.length > 0 && (
        <section>
          <div className="sa-section-head">
            <h2>{ui.fromGroups}</h2>
          </div>
          <Stack gap="sm">
            {data.groupUpdates.map((g, i) => (
              <Card key={i}>
                <h3 className="sa-acard__title">
                  <a href={g.url}>{g.groupTitle}</a>
                </h3>
                <p className="sa-acard__meta">{g.when}</p>
                <p>{g.snippet}</p>
              </Card>
            ))}
          </Stack>
        </section>
      )}
    </div>
  );
}
