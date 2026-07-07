import { useNavigate } from 'react-router-dom';
import { Button, Stack } from '@roedu/ui';
import type { ScreenProps } from './registry';
import type { ConnectionsData } from './types3';

function postAction(action: string, pk: number) {
  return action.includes('{pk}') ? action.replace('{pk}', String(pk)) : action;
}

function pageSearch(cq: string, page: number | null) {
  const qs = new URLSearchParams();
  if (cq) qs.set('cq', cq);
  if (page) qs.set('page', String(page));
  return qs;
}

export function ConnectionsScreen({ payload }: ScreenProps) {
  const data = payload.data as ConnectionsData;
  const { ui } = data;
  const navigate = useNavigate();

  const searchPeople = (q: string) => {
    const qs = new URLSearchParams();
    if (q) qs.set('q', q);
    navigate(qs.toString() ? data.actions.search + '?' + qs : data.actions.search);
  };

  const filterConnections = (cq: string, page: number | null = null) => {
    const qs = pageSearch(cq, page);
    navigate(qs.toString() ? data.actions.search + '?' + qs : data.actions.search);
  };

  return (
    <div className="sa-screen">
      <h1>{ui.title}</h1>

      <form
        className="sa-toolbar"
        role="search"
        method="get"
        action={data.actions.search}
        onSubmit={(e) => {
          e.preventDefault();
          searchPeople(String(new FormData(e.currentTarget).get('q') ?? ''));
        }}
      >
        <label>
          {ui.searchLabel} <input type="search" name="q" defaultValue={data.searchQuery} autoComplete="off" />
        </label>
        <Button type="submit" size="sm">
          {ui.search}
        </Button>
      </form>

      {data.searchQuery && (
        <section>
          <h2>{ui.resultsHead}</h2>
          {data.results.length > 0 && (
            <ul className="members">
              {data.results.map((user) => (
                <li key={user.publicId}>
                  {user.name}
                  <form method="post" action={data.actions.request} className="inline">
                    <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                    <input type="hidden" name="public_id" value={user.publicId} />
                    <Button type="submit" size="sm">
                      {ui.connect}
                    </Button>
                  </form>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {data.incoming.length > 0 && (
        <section>
          <h2>{ui.incoming}</h2>
          <ul className="members">
            {data.incoming.map((request) => (
              <li key={request.pk}>
                {request.user.name}
                <form method="post" action={postAction(data.actions.respond, request.pk)} className="inline">
                  <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                  <input type="hidden" name="pk" value={request.pk} />
                  <input type="hidden" name="accept" value="1" />
                  <Button type="submit" size="sm">
                    {ui.accept}
                  </Button>
                </form>
                <form method="post" action={postAction(data.actions.respond, request.pk)} className="inline">
                  <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                  <input type="hidden" name="pk" value={request.pk} />
                  <input type="hidden" name="accept" value="0" />
                  <Button type="submit" size="sm" variant="ghost">
                    {ui.decline}
                  </Button>
                </form>
              </li>
            ))}
          </ul>
        </section>
      )}

      {data.outgoing.length > 0 && (
        <section>
          <h2>{ui.outgoing}</h2>
          <ul className="members">
            {data.outgoing.map((request) => (
              <li key={request.pk} className="muted">
                {request.user.name} · {ui.pending}
                <form method="post" action={postAction(data.actions.withdraw, request.pk)} className="inline">
                  <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                  <input type="hidden" name="pk" value={request.pk} />
                  <Button type="submit" size="sm" variant="ghost">
                    {ui.withdraw}
                  </Button>
                </form>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section>
        <h2>
          {ui.yours} {data.total > 0 && <span className="muted u-text-sm">({data.total})</span>}
        </h2>
        {(data.total > 0 || data.filterQuery) && (
          <form
            className="sa-toolbar"
            role="search"
            method="get"
            action={data.actions.search}
            onSubmit={(e) => {
              e.preventDefault();
              filterConnections(String(new FormData(e.currentTarget).get('cq') ?? ''));
            }}
          >
            <label>
              {ui.filterLabel} <input type="search" name="cq" defaultValue={data.filterQuery} autoComplete="off" />
            </label>
            <Button type="submit" size="sm" variant="secondary">
              {ui.filter}
            </Button>
            {data.filterQuery && (
              <Button type="button" size="sm" variant="ghost" onClick={() => filterConnections('')}>
                {ui.clear}
              </Button>
            )}
          </form>
        )}

        {data.connections.length > 0 && (
          <ul className="members">
            {data.connections.map((user) => (
              <li key={user.publicId}>
                {user.name}
                <form method="post" action={data.actions.message} className="inline">
                  <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                  <input type="hidden" name="public_id" value={user.publicId} />
                  <Button type="submit" size="sm">
                    {ui.message}
                  </Button>
                </form>
                <form method="post" action={data.actions.remove} className="inline">
                  <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                  <input type="hidden" name="public_id" value={user.publicId} />
                  <Button type="submit" size="sm" variant="ghost">
                    {ui.remove}
                  </Button>
                </form>
              </li>
            ))}
          </ul>
        )}

        {data.page.numPages > 1 && (
          <Stack direction="row" gap="sm" align="center" wrap>
            {data.page.previous && (
              <Button
                type="button"
                size="sm"
                variant="secondary"
                onClick={() => filterConnections(data.filterQuery, data.page.previous)}
              >
                ← {ui.prev}
              </Button>
            )}
            <span className="muted u-text-sm">
              {data.page.number} / {data.page.numPages}
            </span>
            {data.page.next && (
              <Button
                type="button"
                size="sm"
                variant="secondary"
                onClick={() => filterConnections(data.filterQuery, data.page.next)}
              >
                {ui.next} →
              </Button>
            )}
          </Stack>
        )}
      </section>
    </div>
  );
}
