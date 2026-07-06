import { Button, Card, Stack } from '@roedu/ui';
import type { ScreenProps } from './registry';
import type { InterestsData } from './types3';

export function InterestsScreen({ payload }: ScreenProps) {
  const data = payload.data as InterestsData;
  const { ui } = data;

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>
      {data.chosenCount > 0 && <p className="muted">{data.chosenCount}</p>}

      <form method="post" action={data.action}>
        <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />

        <Stack gap="md">
          {data.starter.length > 0 && (
            <Card className="card-accent">
              <h2>{ui.starterHead}</h2>
              <div className="pick-row">
                {data.starter.map((type) => (
                  <label key={type.slug} className="pick">
                    <input type="checkbox" name="interests" value={type.slug} defaultChecked={type.checked} />
                    <span>{type.name}</span>
                  </label>
                ))}
              </div>
            </Card>
          )}

          {data.groups.map((group, index) => (
            <details key={group.category} className="pick-group" open={index < 3}>
              <summary>
                {group.category} <span className="muted">({group.types.length})</span>
              </summary>
              <div className="pick-row">
                {group.types.map((type) => (
                  <label key={type.slug} className="pick">
                    <input type="checkbox" name="interests" value={type.slug} defaultChecked={type.checked} />
                    <span>{type.name}</span>
                  </label>
                ))}
              </div>
            </details>
          ))}

          <Button type="submit">{ui.save}</Button>
        </Stack>
      </form>
    </div>
  );
}
