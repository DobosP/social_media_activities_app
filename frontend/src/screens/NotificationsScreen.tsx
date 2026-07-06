import { Badge, Button, Card, Stack } from '@roedu/ui';
import { SmartLink } from '../components/SmartLink';
import { TabStrip } from '../components/TabStrip';
import type { ScreenProps } from './registry';
import type { NotificationsData } from './types3';

export function NotificationsScreen({ payload }: ScreenProps) {
  const data = payload.data as NotificationsData;
  const { ui } = data;
  return (
    <div className="sa-screen">
      <div className="sa-section-head">
        <h1>{ui.title}</h1>
        <SmartLink className="sa-more" href={data.urls.preferences}>
          {ui.settings}
        </SmartLink>
      </div>
      <TabStrip tabs={data.tabs} />
      {data.items.some((n) => n.unread) && (
        <form method="post" action={data.actions.readAll} className="inline">
          <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
          <Button type="submit" size="sm" variant="secondary">
            {ui.markAllRead}
          </Button>
        </form>
      )}
      {data.items.length === 0 ? (
        <p className="sa-empty">{ui.empty}</p>
      ) : (
        <Stack gap="sm">
          {data.items.map((n, i) => (
            <Card key={i}>
              <Stack direction="row" gap="sm" align="center" wrap>
                <strong>{n.title}</strong>
                {n.unread && (
                  <Badge tone="primary" size="sm">
                    {ui.new}
                  </Badge>
                )}
                <span className="muted u-text-sm">{n.when}</span>
              </Stack>
              {n.body && <p>{n.body}</p>}
              {n.why && <p className="muted u-text-sm">{n.why}</p>}
              {n.url && <a href={n.url}>{ui.view} →</a>}
            </Card>
          ))}
        </Stack>
      )}
    </div>
  );
}
