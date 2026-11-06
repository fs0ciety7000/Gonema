import {
  Activity,
  ArrowDownToLine,
  Ban,
  Calendar,
  CircleDashed,
  Compass,
  Film,
  Gauge,
  History,
  Layers,
  type LucideIcon,
  Settings,
} from 'lucide-react';
import { DEV_SCREENS } from './flags';

export type Section =
  | 'home'
  | 'films'
  | 'collections'
  | 'calendar'
  | 'discover'
  | 'queue'
  | 'history'
  | 'wanted'
  | 'blocklist'
  | 'health'
  | 'settings';

export type NavEntry = {
  id: Section;
  label: string;
  icon: LucideIcon;
  path: string;
  /** Built in this lot. Unbuilt entries are hidden from production builds. */
  built: boolean;
  /** Lot that builds the screen (for the dev placeholder). */
  lot?: number;
};

export const NAV: ReadonlyArray<{ heading: string; items: readonly NavEntry[] }> = [
  {
    heading: 'Library',
    items: [
      { id: 'home', label: 'Home', icon: Gauge, path: '/home', built: false, lot: 3 },
      { id: 'films', label: 'Films', icon: Film, path: '/films', built: true },
      { id: 'collections', label: 'Collections', icon: Layers, path: '/collections', built: false, lot: 3 },
      { id: 'calendar', label: 'Calendar', icon: Calendar, path: '/calendar', built: false, lot: 3 },
      { id: 'discover', label: 'Add & discover', icon: Compass, path: '/discover', built: true },
    ],
  },
  {
    heading: 'Activity',
    items: [
      { id: 'queue', label: 'Queue', icon: ArrowDownToLine, path: '/queue', built: true },
      { id: 'history', label: 'History', icon: History, path: '/history', built: true },
      { id: 'wanted', label: 'Wanted', icon: CircleDashed, path: '/wanted', built: false, lot: 3 },
      { id: 'blocklist', label: 'Blocklist', icon: Ban, path: '/blocklist', built: false, lot: 3 },
    ],
  },
  {
    heading: 'System',
    items: [
      { id: 'health', label: 'Health', icon: Activity, path: '/health', built: false, lot: 3 },
      { id: 'settings', label: 'Settings', icon: Settings, path: '/settings', built: false, lot: 3 },
    ],
  },
];

export const ALL_SECTIONS = NAV.flatMap((s) => s.items);

/** What the sidebar shows: every section in dev, only the built ones in production. */
export function visibleNav() {
  return NAV.map((s) => ({ ...s, items: s.items.filter((i) => i.built || DEV_SCREENS) })).filter((s) => s.items.length);
}

export const isBuilt = (id: Section) => ALL_SECTIONS.find((s) => s.id === id)?.built ?? false;
export const reachable = (id: Section) => isBuilt(id) || DEV_SCREENS;
