import { Button, Card, Stack } from '@roedu/ui';
import type { ScreenProps } from './registry';
import type { AccessData } from './types3';

export function AccessScreen({ payload }: ScreenProps) {
  const data = payload.data as AccessData;
  const { ui } = data;

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>

      <form method="post" action={data.action}>
        <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
        <Card>
          <Stack gap="sm">
            {data.fields.map((field) => (
              <label key={field.name} className="row">
                <input type="checkbox" name={field.name} defaultChecked={field.checked} />
                <span>
                  <strong>{field.label}</strong>
                </span>
              </label>
            ))}
            <Button type="submit">{ui.save}</Button>
          </Stack>
        </Card>
      </form>
    </div>
  );
}
