import { Button, Card } from '@roedu/ui';
import { SmartLink } from '../components/SmartLink';
import { TabStrip } from '../components/TabStrip';
import type { ScreenProps } from './registry';
import type { YouData } from './types3';

/** Account overview hub — renders entirely from the shared account_nav config. */
export function YouScreen({ payload }: ScreenProps) {
  const data = payload.data as YouData;
  return (
    <div className="sa-screen">
      <h1>{data.ui.title}</h1>
      <p className="muted">
        {data.name} · {data.username}
      </p>
      <TabStrip tabs={data.tabs} />
      <div className="sa-card-grid">
        {data.nav.groups.map((group) => (
          <Card key={group.title}>
            <h2>{group.title}</h2>
            <ul className="link-list">
              {group.links.map((l) => (
                <li key={l.url}>
                  <SmartLink href={l.url}>{l.label}</SmartLink>
                  {l.pill ? <span className="pill">{l.pill}</span> : null}
                </li>
              ))}
            </ul>
          </Card>
        ))}
      </div>
      <form method="post" action={data.nav.logoutAction} className="inline">
        <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
        <Button type="submit" variant="secondary">
          {data.nav.logoutLabel}
        </Button>
      </form>
    </div>
  );
}
