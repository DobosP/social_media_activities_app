import type { ComponentType } from 'react';
import { matchPath } from 'react-router-dom';
import type { SpaPayload } from '../lib/bootstrap';
import { HomeScreen } from './HomeScreen';
import { BrowseScreen } from './BrowseScreen';
import { OrganizeScreen } from './OrganizeScreen';
import { EventsScreen } from './EventsScreen';
import { PlacesScreen } from './PlacesScreen';
import { ThingsCityScreen, ThingsDetailScreen, ThingsIndexScreen } from './ThingsScreens';

export interface ScreenProps {
  payload: SpaPayload;
}

export interface ScreenEntry {
  /** Route id — must equal the `route` the Django view passes to spa_response(). */
  route: string;
  /** URL pattern matching the Django URL exactly (Django owns routing). */
  path: string;
  Component: ComponentType<ScreenProps>;
}

/**
 * Migrated screens. A URL absent here is a normal server-rendered page.
 * NOTE: /my-meetups/ deliberately stays server-rendered — it's the F38
 * offline-saved safety page; its delivery must not depend on the SPA.
 */
export const screens: ScreenEntry[] = [
  { route: 'home', path: '/', Component: HomeScreen },
  { route: 'browse', path: '/activities/', Component: BrowseScreen },
  { route: 'organize', path: '/organize/', Component: OrganizeScreen },
  { route: 'events', path: '/events/', Component: EventsScreen },
  { route: 'places', path: '/places/list/', Component: PlacesScreen },
  { route: 'things-index', path: '/things-to-do/', Component: ThingsIndexScreen },
  { route: 'things-city', path: '/things-to-do/:city/', Component: ThingsCityScreen },
  { route: 'things-detail', path: '/things-to-do/:city/:activity/', Component: ThingsDetailScreen },
];

/** True if the SPA owns this path (used by SmartLink to pick soft vs full nav). */
export function isSpaPath(path: string): boolean {
  return screens.some((s) => matchPath({ path: s.path, end: true }, path) !== null);
}
