import { Badge, Button, Card, Stack } from '@roedu/ui';
import type { ScreenProps } from './registry';
import type { SavedSearchesData } from './types3';

export function SavedSearchesScreen({ payload }: ScreenProps) {
  const data = payload.data as SavedSearchesData;
  const { ui } = data;

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>

      <Card>
        <h2>{ui.createHead}</h2>
        <form method="post" action={data.actions.create}>
          <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
          <input type="hidden" name="next" value={data.next} />
          <Stack gap="sm">
            <label>
              {ui.activityType}
              <select name="activity_type" defaultValue="">
                <option value="" />
                {data.options.activityTypes.map((t) => (
                  <option key={t.slug} value={t.slug}>
                    {t.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              {ui.orCategory}
              <select name="category" defaultValue="">
                <option value="" />
                {data.options.categories.map((c) => (
                  <option key={c.slug} value={c.slug}>
                    {c.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              {ui.city}
              <input type="text" name="city" maxLength={128} />
            </label>
            <label>
              {ui.cost}
              <select name="cost_band" defaultValue="">
                <option value="" />
                {data.options.costBands
                  .filter((c) => c.value !== 'unspecified')
                  .map((c) => (
                    <option key={c.value} value={c.value}>
                      {c.label}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              {ui.when}
              <select name="coarse_window" defaultValue="">
                <option value="" />
                {data.options.coarseWindows.map((w) => (
                  <option key={w.value} value={w.value}>
                    {w.label}
                  </option>
                ))}
              </select>
            </label>
            <label className="row">
              <input type="checkbox" name="beginners" /> {ui.beginners}
            </label>
            <div>
              <Button type="submit">{ui.save}</Button>
            </div>
          </Stack>
        </form>
      </Card>

      <section>
        <h2>{ui.yours}</h2>
        <Stack gap="sm">
          {data.items.map((item) => (
            <Card key={item.pk}>
              <Stack direction="row" gap="sm" align="center" wrap>
                {item.what && <Badge tone="neutral">{item.what}</Badge>}
                {item.extras.map((extra) => (
                  <Badge key={extra} tone="neutral">
                    {extra}
                  </Badge>
                ))}
                <form method="post" action={data.actions.delete.replace('{pk}', String(item.pk))} className="inline">
                  <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                  <input type="hidden" name="next" value={data.next} />
                  <Button type="submit" size="sm" variant="ghost">
                    {ui.remove}
                  </Button>
                </form>
              </Stack>
            </Card>
          ))}
        </Stack>
      </section>
    </div>
  );
}
