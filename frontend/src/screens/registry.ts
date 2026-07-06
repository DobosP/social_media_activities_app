import type { ComponentType } from 'react';
import { matchPath } from 'react-router-dom';
import type { SpaPayload } from '../lib/bootstrap';
import { HomeScreen } from './HomeScreen';
import { BrowseScreen } from './BrowseScreen';
import { OrganizeScreen } from './OrganizeScreen';
import { EventsScreen } from './EventsScreen';
import { PlacesScreen } from './PlacesScreen';
import { ThingsCityScreen, ThingsDetailScreen, ThingsIndexScreen } from './ThingsScreens';
import { YouScreen } from './YouScreen';
import { SettingsScreen } from './SettingsScreen';
import { ProfileScreen } from './ProfileScreen';
import { InterestsScreen } from './InterestsScreen';
import { TopicsScreen } from './TopicsScreen';
import { AccessScreen } from './AccessScreen';
import { NotificationsScreen } from './NotificationsScreen';
import { NotificationPreferencesScreen } from './NotificationPreferencesScreen';
import { ConnectionsScreen } from './ConnectionsScreen';
import { SavedSearchesScreen } from './SavedSearchesScreen';
import { CommunitiesScreen } from './CommunitiesScreen';
import { CommunityDetailScreen } from './CommunityDetailScreen';

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
