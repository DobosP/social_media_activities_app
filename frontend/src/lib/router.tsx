import { useEffect } from 'react';
import {
  createBrowserRouter,
  useLoaderData,
  type LoaderFunctionArgs,
} from 'react-router-dom';
import { fetchPayload, readIsland, syncChrome, type SpaPayload } from './bootstrap';
import { screens } from '../screens/registry';

// The island feeds the FIRST matched loader; everything after is a fetch.
let island: SpaPayload | null = readIsland();

function makeLoader(route: string) {
  return async ({ request }: LoaderFunctionArgs): Promise<SpaPayload> => {
    if (island && island.route === route) {
      const first = island;
      island = null;
      return first;
    }
    try {
      return await fetchPayload(request.url, request.signal);
    } catch {
      // Session expiry, server error, non-migrated redirect target… fall back
      // to a classic full navigation instead of a broken in-app state.
      window.location.assign(request.url);
      return new Promise<never>(() => {});
    }
  };
}

function Screen({ route }: { route: string }) {
  const payload = useLoaderData() as SpaPayload;
  const entry = screens.find((s) => s.route === route);
  useEffect(() => {
    syncChrome(payload.title);
  }, [payload]);
  if (!entry) return null;
  const Component = entry.Component;
  return <Component payload={payload} />;
}

export function makeRouter() {
  return createBrowserRouter(
    screens.map((s) => ({
      path: s.path,
      loader: makeLoader(s.route),
      element: <Screen route={s.route} />,
    })),
  );
}
