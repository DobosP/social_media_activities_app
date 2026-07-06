import { Button, Card, Stack } from '@roedu/ui';
import type { ScreenProps } from './registry';
import type { TopicsData } from './types3';

export function TopicsScreen({ payload }: ScreenProps) {
  const data = payload.data as TopicsData;
  const { ui } = data;

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>

      <form method="post" action={data.action}>
        <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
        <Card>
          <fieldset>
            <legend className="muted">{ui.lean}</legend>
            {data.topics.length === 0 ? (
              <p className="muted">{ui.empty}</p>
            ) : (
              <Stack gap="sm">
                {data.topics.map((topic) => (
                  <label key={topic.slug} className="row">
                    <input type="checkbox" name="topics" value={topic.slug} defaultChecked={topic.checked} />
                    <span>
                      <strong>{topic.name}</strong>
                      {topic.description && (
                        <>
                          <br />
                          <span className="muted u-text-sm">{topic.description}</span>
                        </>
                      )}
                    </span>
                  </label>
                ))}
              </Stack>
            )}
          </fieldset>
          <Button type="submit">{ui.save}</Button>
        </Card>
      </form>
    </div>
  );
}
