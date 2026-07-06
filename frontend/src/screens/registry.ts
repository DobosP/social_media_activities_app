import type { ComponentType } from 'react';
import type { SpaPayload } from '../lib/bootstrap';
import { HomeScreen } from './HomeScreen';
import { BrowseScreen } from './BrowseScreen';
import { OrganizeScreen } from './OrganizeScreen';

export interface ScreenProps {
  payload: SpaPayload;
}

export interface ScreenEntry {
  /** Route id — must equal the `route` the Django view passes to spa_response(). */
  route: string;
  /** URL path, matching the Django URL exactly (Django owns routing). */
  path: string;
  Component: ComponentType<ScreenProps>;
}

/**
 * Migrated screens. A URL absent here is a normal server-rendered page.
 * NOTE: /my-meetups/ deliberately stays server-rendered — it's the F38
 * offline-saved safety page; its delivery mechanism must not depend on the SPA.
 */
export const screens: ScreenEntry[] = [
  { route: 'home', path: '/', Component: HomeScreen },
  { route: 'browse', path: '/activities/', Component: BrowseScreen },
  { route: 'organize', path: '/organize/', Component: OrganizeScreen },
];

/** True if the SPA owns this path (used by SmartLink to pick soft vs full nav). */
export function isSpaPath(path: string): boolean {
  return screens.some((s) => s.path === path);
}
