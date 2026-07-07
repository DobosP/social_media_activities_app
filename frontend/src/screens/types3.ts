import type { ActivityCardData } from '../components/ActivityCard';

/** P3 payloads — mirror the P3 builders in apps/web/views_spa.py; change both together. */

export interface Tab {
  label: string;
  url: string;
  pill?: number;
}

export interface NavLink {
  label: string;
  url: string;
  pill?: number;
}

export interface AccountNav {
  groups: { title: string; links: NavLink[] }[];
  logoutAction: string;
  logoutLabel: string;
}

export interface YouData {
  name: string;
  username: string;
  isGuardian: boolean;
  tabs: Tab[];
  nav: AccountNav;
  ui: Record<string, string>;
}

export interface SettingsData {
  tabs: Tab[];
  nav: AccountNav;
  language: {
    action: string;
    next: string;
    current: string;
    options: { code: string; name: string }[];
  };
  apiToken: { created: string; revokeAction: string };
  account: { export: string; delete: string };
  ui: Record<string, string>;
}

export interface ProfileData {
  name: string;
  username: string;
  ageBand: string;
  identityVerified: boolean;
  canParticipate: boolean;
  avatarUrl: string;
  journeyAvatar: string;
  progression: { count: number; level: number; maxLevel: number } | null;
  provenance: {
    isCurrent: boolean;
    bandDisplay: string;
    provider: string;
    method: string;
    verifiedAt: string;
    expiresAt: string;
    status: string;
    expiresSoon: boolean;
    daysLeft: number | null;
  } | null;
  interests: string[];
  connections: { publicId: string; name: string }[];
  connectionsTotal: number;
  pendingIncomingCount: number;
  blocked: { pk: number; name: string }[];
  tabs: Tab[];
  actions: Record<string, string>;
  ui: Record<string, string>;
}

export interface InterestsData {
  groups: { category: string; types: { slug: string; name: string; checked: boolean }[] }[];
  starter: { slug: string; name: string; checked: boolean }[];
  chosenCount: number;
  action: string;
  ui: Record<string, string>;
}

export interface TopicsData {
  topics: { slug: string; name: string; description: string; checked: boolean }[];
  action: string;
  ui: Record<string, string>;
}

export interface AccessData {
  fields: { name: string; label: string; checked: boolean }[];
  action: string;
  ui: Record<string, string>;
}

export interface NotificationsData {
  items: { title: string; body: string; why: string; when: string; url: string; unread: boolean }[];
  actions: { readAll: string };
  urls: { preferences: string };
  ui: Record<string, string>;
}

export interface NotificationPreferencesData {
  rows: { value: string; label: string; reason: string; muted: boolean }[];
  action: string;
  ui: Record<string, string>;
}

export interface ConnectionsData {
  searchQuery: string;
  results: { publicId: string; name: string }[];
  incoming: { pk: number; user: { publicId: string; name: string } }[];
  outgoing: { pk: number; user: { publicId: string; name: string } }[];
  connections: { publicId: string; name: string }[];
  filterQuery: string;
  total: number;
  page: { number: number; numPages: number; previous: number | null; next: number | null };
  actions: Record<string, string>;
  ui: Record<string, string>;
}

export interface SavedSearchesData {
  items: { pk: number; what: string; extras: string[] }[];
  options: {
    activityTypes: { slug: string; name: string }[];
    categories: { slug: string; name: string }[];
    costBands: { value: string; label: string }[];
    coarseWindows: { value: string; label: string }[];
  };
  actions: { create: string; delete: string };
  next: string;
  ui: Record<string, string>;
}

export interface CommunitiesData {
  groups: {
    pk: number;
    url: string;
    title: string;
    type: string;
    category: string;
    area: string;
    description: string;
  }[];
  communities: {
    slug: string;
    url: string;
    name: string;
    tier: string;
    category: string;
    area: string;
  }[];
  pages: {
    communities: { number: number; numPages: number; previous: number | null; next: number | null };
    groups: { number: number; numPages: number; previous: number | null; next: number | null };
  };
  canCreate: boolean;
  urls: { graph: string; createGroup: string; action: string };
  ui: Record<string, string>;
}

export interface CommunityDetailData {
  name: string;
  lead: string;
  linkedGroup: { url: string; label: string } | null;
  cards: ActivityCardData[];
  urls: { communities: string; organizeNew: string };
  ui: Record<string, string>;
}
