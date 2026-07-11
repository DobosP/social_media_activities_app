import { lazy, type ComponentType } from 'react';
import { matchPath } from 'react-router-dom';
import type { SpaPayload } from '../lib/bootstrap';
import { HomeScreen } from './HomeScreen';

// The server supplies data for exactly one route at a time. Keep the landing
// screen eager and fetch every other screen only on first navigation so a
// low-end phone does not parse the whole product during its first visit.
const BrowseScreen = lazy(() =>
  import('./BrowseScreen').then((module) => ({ default: module.BrowseScreen })),
);
const OrganizeScreen = lazy(() =>
  import('./OrganizeScreen').then((module) => ({ default: module.OrganizeScreen })),
);
const EventsScreen = lazy(() =>
  import('./EventsScreen').then((module) => ({ default: module.EventsScreen })),
);
const PlacesScreen = lazy(() =>
  import('./PlacesScreen').then((module) => ({ default: module.PlacesScreen })),
);
const ThingsIndexScreen = lazy(() =>
  import('./ThingsScreens').then((module) => ({ default: module.ThingsIndexScreen })),
);
const ThingsCityScreen = lazy(() =>
  import('./ThingsScreens').then((module) => ({ default: module.ThingsCityScreen })),
);
const ThingsDetailScreen = lazy(() =>
  import('./ThingsScreens').then((module) => ({ default: module.ThingsDetailScreen })),
);
const YouScreen = lazy(() =>
  import('./YouScreen').then((module) => ({ default: module.YouScreen })),
);
const SettingsScreen = lazy(() =>
  import('./SettingsScreen').then((module) => ({ default: module.SettingsScreen })),
);
const ProfileScreen = lazy(() =>
  import('./ProfileScreen').then((module) => ({ default: module.ProfileScreen })),
);
const InterestsScreen = lazy(() =>
  import('./InterestsScreen').then((module) => ({ default: module.InterestsScreen })),
);
const TopicsScreen = lazy(() =>
  import('./TopicsScreen').then((module) => ({ default: module.TopicsScreen })),
);
const AccessScreen = lazy(() =>
  import('./AccessScreen').then((module) => ({ default: module.AccessScreen })),
);
const NotificationsScreen = lazy(() =>
  import('./NotificationsScreen').then((module) => ({ default: module.NotificationsScreen })),
);
const NotificationPreferencesScreen = lazy(() =>
  import('./NotificationPreferencesScreen').then((module) => ({
    default: module.NotificationPreferencesScreen,
  })),
);
const ConnectionsScreen = lazy(() =>
  import('./ConnectionsScreen').then((module) => ({ default: module.ConnectionsScreen })),
);
const SavedSearchesScreen = lazy(() =>
  import('./SavedSearchesScreen').then((module) => ({ default: module.SavedSearchesScreen })),
);
const CommunitiesScreen = lazy(() =>
  import('./CommunitiesScreen').then((module) => ({ default: module.CommunitiesScreen })),
);
const CommunityDetailScreen = lazy(() =>
  import('./CommunityDetailScreen').then((module) => ({ default: module.CommunityDetailScreen })),
);

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
 * Deliberately NOT here: /my-meetups/ (F38 offline safety page), activity detail
 * (embeds the thread + pre-send nudge — sensitive track), messaging, maps, graph,
 * donations, and all safety/legal surfaces (ADR-0016 P4: restyle-in-place only).
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
  { route: 'you', path: '/you/', Component: YouScreen },
  { route: 'settings', path: '/settings/', Component: SettingsScreen },
  { route: 'profile', path: '/profile/', Component: ProfileScreen },
  { route: 'interests', path: '/interests/', Component: InterestsScreen },
  { route: 'topics', path: '/topics/', Component: TopicsScreen },
  { route: 'access', path: '/access/', Component: AccessScreen },
  { route: 'notifications', path: '/notifications/', Component: NotificationsScreen },
  {
    route: 'notification-preferences',
    path: '/notifications/preferences/',
    Component: NotificationPreferencesScreen,
  },
  { route: 'connections', path: '/connections/', Component: ConnectionsScreen },
  { route: 'saved-searches', path: '/saved-searches/', Component: SavedSearchesScreen },
  { route: 'communities', path: '/communities/', Component: CommunitiesScreen },
  { route: 'community-detail', path: '/communities/:slug/', Component: CommunityDetailScreen },
];

/** Legacy pages whose URLs would otherwise match a dynamic pattern above. */
const NOT_SPA = ['/communities/graph/'];

/** True if the SPA owns this path (used by SmartLink to pick soft vs full nav). */
export function isSpaPath(path: string): boolean {
  if (NOT_SPA.includes(path)) return false;
  return screens.some((s) => matchPath({ path: s.path, end: true }, path) !== null);
}
