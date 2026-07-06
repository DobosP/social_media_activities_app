import { useNavigate } from 'react-router-dom';
import { Button, Card, Stack } from '@roedu/ui';
import { SmartLink } from '../components/SmartLink';
import type { ScreenProps } from './registry';
import type { CommunitiesData } from './types3';

export function CommunitiesScreen({ payload }: ScreenProps) {
  const data = payload.data as CommunitiesData;
  const { ui } = data;
  const navigate = useNavigate();

  const pagedUrl = (target: 'groups' | 'communities', page: number) => {
    const qs = new URLSearchParams();
    if (target === 'groups') {
      qs.set('gpage', String(page));
      if (data.pages.communities.number > 1) qs.set('page', String(data.pages.communities.number));
    } else {
      qs.set('page', String(page));
      if (data.pages.groups.number > 1) qs.set('gpage', String(data.pages.groups.number));
    }
    return data.urls.action + '?' + qs;
  };

  const pager = (target: 'groups' | 'communities') => {
    const page = target === 'groups' ? data.pages.groups : data.pages.communities;
    if (page.numPages <= 1) return null;
    const previous = page.previous;
    const next = page.next;
    return (
      <Stack direction="row" gap="sm" align="center" wrap>
        {previous !== null && (
          <Button type="button" size="sm" variant="secondary" onClick={() => navigate(pagedUrl(target, previous))}>
            ← {ui.prev}
          </Button>
        )}
        <span className="muted u-text-sm">
          {page.number} / {page.numPages}
        </span>
        {next !== null && (
          <Button type="button" size="sm" variant="secondary" onClick={() => navigate(pagedUrl(target, next))}>
            {ui.next} →
          </Button>
        )}
      </Stack>
    );
  };

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>
      <Stack direction="row" gap="sm" wrap>
        <SmartLink className="btn btn-sm btn-secondary" href={data.urls.graph}>
          {ui.graph}
        </SmartLink>
        {data.canCreate && (
          <SmartLink className="btn btn-sm" href={data.urls.createGroup}>
            {ui.startGroup}
          </SmartLink>
        )}
      </Stack>

      <section>
        <h2>{ui.groupsHead}</h2>
        {data.groups.length === 0 ? (
          <p className="muted">
            {ui.groupsEmpty}
            {data.canCreate && (
              <>
                {' '}
                <SmartLink href={data.urls.createGroup}>{ui.startFirst}</SmartLink>
              </>
            )}
          </p>
        ) : (
          <Stack gap="sm">
            {data.groups.map((group) => (
              <Card key={group.pk}>
                <h3 className="sa-acard__title">
                  <SmartLink href={group.url}>{group.title}</SmartLink>
                </h3>
                <p className="muted">
                  {group.type || group.category}
                  {group.area && <> · {group.area}</>}
                </p>
                {group.description && <p>{group.description}</p>}
              </Card>
            ))}
          </Stack>
        )}
        {pager('groups')}
      </section>

      <section>
        <h2>{ui.communitiesHead}</h2>
        {data.communities.length > 0 && (
          <Stack gap="sm">
            {data.communities.map((community) => (
              <Card key={community.slug}>
                <h3 className="sa-acard__title">
                  <SmartLink href={community.url}>{community.name}</SmartLink>
                </h3>
                <p className="muted">
                  {community.category}
                  {community.area && <> · {community.area}</>}
                </p>
              </Card>
            ))}
          </Stack>
        )}
        {pager('communities')}
      </section>
    </div>
  );
}
