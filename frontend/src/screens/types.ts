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

export interface EventRow {
  pk: number;
  url: string;
  title: string;
  type: string;
  when: string;
  place: { name: string; url: string } | null;
  description: string;
}

export interface EventsData {
  events: EventRow[];
  filters: { query: string; activity: string; area: string; areaName: string };
  areas: { slug: string; name: string }[];
  urls: { action: string; rss: string; thingsIndex: string };
  ui: Record<string, string>;
}

export interface PlaceRow {
  pk: number;
  url: string;
  name: string;
  street: string;
  city: string;
  distance: string;
  activities: string[];
  accessMatch: boolean;
  accessTags: { label: string; state: string }[];
}

export interface PlacesData {
  places: PlaceRow[];
  filters: { activity: string; city: string; source: string };
  flags: { nearActive: boolean; truncated: boolean };
  urls: { action: string; map: string };
  ui: Record<string, string>;
}

export interface Crumb {
  name: string;
  url: string | null;
}

export interface ThingsIndexData {
  cities: { name: string; url: string; links: { url: string; label: string }[] }[];
  ui: Record<string, string>;
}

export interface ThingsCityData {
  city: string;
  links: { url: string; label: string }[];
  breadcrumbs: Crumb[];
  ui: Record<string, string>;
}

export interface ThingsDetailData {
  city: string;
  activity: string;
  events: EventRow[];
  places: { url: string; name: string; city: string }[];
  breadcrumbs: Crumb[];
  urls: { exploreCity: string; rss: string };
  ui: Record<string, string>;
}
