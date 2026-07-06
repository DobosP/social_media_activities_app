import { Badge, Card, Stack } from '@roedu/ui';
import type { ScreenProps } from './registry';
import type { OrganizeData } from './types';

/** Organizer console — read-only rows; every chip links into the screen that acts. */
export function OrganizeScreen({ payload }: ScreenProps) {
  const data = payload.data as OrganizeData;
  const { ui } = data;

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>
      <p className="muted">{ui.intro}</p>

      <section>
        <div className="sa-section-head">
          <h2>{ui.activities}</h2>
        </div>
        {data.activities.length === 0 ? (
          <p className="sa-empty">
            {ui.emptyLead} <a href={data.urls.organizeNew}>{ui.emptyCta}</a>
          </p>
        ) : (
          <Stack gap="sm">
            {data.activities.map((row) => (
              <Card key={row.pk}>
                <h3 className="sa-acard__title">
                  <a href={row.url}>{row.title}</a> <Badge size="sm">{row.type}</Badge>
                </h3>
                <p className="sa-acard__meta">
                  📅 {row.when}
                  {row.place && <> · {row.place}</>}
                </p>
                {row.allClear ? (
                  <p className="sa-acard__meta">✓ {ui.allClear}</p>
                ) : (
                  <Stack direction="row" gap="sm" wrap>
                    {row.badges.map((b) =>
                      b.url ? (
                        <a key={b.label} href={b.url}>
                          <Badge tone={b.tone === 'danger' ? 'danger' : 'primary'} size="sm">
                            {b.label}
                          </Badge>
                        </a>
                      ) : (
                        <Badge key={b.label} tone="neutral" size="sm">
                          {b.label}
                        </Badge>
                      ),
                    )}
                  </Stack>
                )}
                {row.supportNote && <p className="sa-acard__meta">{row.supportNote}</p>}
              </Card>
            ))}
          </Stack>
        )}
      </section>

      {data.series.length > 0 && (
        <section>
          <div className="sa-section-head">
            <h2>{ui.seriesHead}</h2>
          </div>
          <Stack gap="sm">
            {data.series.map((s) => (
              <Card key={s.pk}>
                <h3 className="sa-acard__title">
                  <a href={s.url}>{s.title}</a> <Badge size="sm">{s.cadence}</Badge>
                </h3>
                {s.next && <p className="sa-acard__meta">{s.next}</p>}
              </Card>
            ))}
          </Stack>
        </section>
      )}

      {data.groups.length > 0 && (
        <section>
          <div className="sa-section-head">
            <h2>{ui.groupsHead}</h2>
          </div>
          <Stack gap="sm">
            {data.groups.map((g) => (
              <Card key={g.pk}>
                <h3 className="sa-acard__title">
                  <a href={g.url}>{g.title}</a>
                </h3>
              </Card>
            ))}
          </Stack>
        </section>
      )}
    </div>
  );
}
