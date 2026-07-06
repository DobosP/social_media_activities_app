import type { ActivityCardData } from '../components/ActivityCard';

/** Mirrors apps/web/views_spa.py builders — change both together. */

export interface HomeData {
  sections: {
    recommended: ActivityCardData[];
    beginners: ActivityCardData[];
    upcoming: ActivityCardData[];
    mine: ActivityCardData[];
  };
  starterTypes: { slug: string; name: string }[];
  events: { pk: number; url: string; title: string; reason: string; meta: string }[];
  groupUpdates: { url: string; groupTitle: string; when: string; snippet: string }[];
  guardianInvites: {
    name: string;
    relationship: string;
    acceptAction: string;
    declineAction: string;
  }[];
  flags: { nearActive: boolean; beginnersOnly: boolean };
  urls: {
    browse: string;
    organizeNew: string;
    places: string;
    series: string;
    interestsAction: string;
  };
  ui: Record<string, string>;
}

export interface BrowseData {
  cards: ActivityCardData[];
  filters: {
    query: string;
    beginnersOnly: boolean;
    nearActive: boolean;
    didYouMean: string;
    didYouMeanQ: string;
  };
  viewMode: 'list' | 'cards';
  baseQs: string;
  page: {
    count: number;
    numPages: number;
    number: number;
    previous: number | null;
    next: number | null;
  };
  urls: { action: string; organizeNew: string };
  ui: Record<string, string>;
}

export interface OrganizeBadge {
  label: string;
  url: string | null;
  tone: 'info' | 'danger';
}

export interface OrganizeData {
  activities: {
    pk: number;
    url: string;
    title: string;
    type: string;
    when: string;
    place: string;
    badges: OrganizeBadge[];
    allClear: boolean;
    supportNote: string;
  }[];
  series: { pk: number; url: string; title: string; cadence: string; next: string }[];
  groups: { pk: number; url: string; title: string }[];
  urls: { organizeNew: string };
  ui: Record<string, string>;
}
