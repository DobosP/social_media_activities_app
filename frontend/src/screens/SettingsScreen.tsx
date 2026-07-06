import { Button, Card, Stack } from '@roedu/ui';
import { SmartLink } from '../components/SmartLink';
import { TabStrip } from '../components/TabStrip';
import type { ScreenProps } from './registry';
import type { SettingsData } from './types3';

export function SettingsScreen({ payload }: ScreenProps) {
  const data = payload.data as SettingsData;
  const { ui } = data;

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>
      <TabStrip tabs={data.tabs} />

      <div className="sa-card-grid">
        <Card>
          <h2>{ui.language}</h2>
          <p className="muted">{ui.languageHelp}</p>
          <form method="post" action={data.language.action} className="row">
            <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
            <input type="hidden" name="next" value={data.language.next} />
            <select name="language" aria-label={ui.language} defaultValue={data.language.current}>
              {data.language.options.map((option) => (
                <option key={option.code} value={option.code}>
                  {option.name}
                </option>
              ))}
            </select>
            <Button type="submit" size="sm">
              {ui.save}
            </Button>
          </form>
        </Card>

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

      <Card>
        <h2>{ui.apiAccess}</h2>
        {data.apiToken.created ? (
          <Stack gap="sm">
            <p className="muted">{data.apiToken.created}</p>
            <form method="post" action={data.apiToken.revokeAction} className="inline">
              <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
              <Button type="submit" size="sm" variant="secondary">
                {ui.revoke}
              </Button>
            </form>
          </Stack>
        ) : (
          <p className="muted">{ui.noToken}</p>
        )}
      </Card>

      <Card className="card-danger">
        <h2>{ui.yourAccount}</h2>
        <Stack direction="row" gap="sm" wrap>
          <SmartLink className="btn btn-secondary" href={data.account.export}>
            {ui.download}
          </SmartLink>
          <SmartLink className="btn btn-danger" href={data.account.delete}>
            {ui.delete}
          </SmartLink>
        </Stack>
      </Card>

      <form method="post" action={data.nav.logoutAction} className="inline">
        <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
        <Button type="submit" variant="secondary">
          {data.nav.logoutLabel}
        </Button>
      </form>
    </div>
  );
}
