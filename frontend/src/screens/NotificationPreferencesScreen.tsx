import { Button, Card, Stack } from '@roedu/ui';
import type { ScreenProps } from './registry';
import type { NotificationPreferencesData } from './types3';

export function NotificationPreferencesScreen({ payload }: ScreenProps) {
  const data = payload.data as NotificationPreferencesData;
  const { ui } = data;

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>
      <form method="post" action={data.action}>
        <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
        <Card>
          <Stack gap="sm">
            {data.rows.map((row) => (
              <label key={row.value} className="row">
                <input type="checkbox" name="muted" value={row.value} defaultChecked={row.muted} />
                <span>
                  <strong>{row.label}</strong>
                  <br />
                  <span className="muted u-text-sm">{row.reason}</span>
                </span>
              </label>
            ))}
            <div>
              <Button type="submit">{ui.save}</Button>
            </div>
          </Stack>
        </Card>
      </form>
    </div>
  );
}
