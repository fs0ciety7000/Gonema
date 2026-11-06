/* Options used to add a film, remembered between visits (last choices win). */

export type Availability = 'announced' | 'in_cinemas' | 'released';
export type MonitorChoice = 'film' | 'collection' | 'none';

export type AddOptions = {
  rootFolderId: number;
  qualityProfileId: number;
  minimumAvailability: Availability;
  monitor: MonitorChoice;
  tags: string[];
  searchOnAdd: boolean;
};

const KEY = 'gn-add-options';
const AVAIL = new Set<Availability>(['announced', 'in_cinemas', 'released']);
const MONITOR = new Set<MonitorChoice>(['film', 'collection', 'none']);

export const AVAILABILITY_OPTIONS: ReadonlyArray<{ value: Availability; label: string }> = [
  { value: 'announced', label: 'Announced' },
  { value: 'in_cinemas', label: 'In cinemas' },
  { value: 'released', label: 'Released' },
];

/** Reads the remembered options, validated against what exists now (folders and profiles may be gone). */
export function readAddOptions(
  rootIds: readonly number[],
  profileIds: readonly number[],
  storage: Pick<Storage, 'getItem'> | undefined = globalThis.localStorage,
): AddOptions {
  let raw: Partial<AddOptions> = {};
  try {
    const v = JSON.parse(storage?.getItem(KEY) ?? 'null') as unknown;
    if (v && typeof v === 'object') raw = v as Partial<AddOptions>;
  } catch {
    /* defaults */
  }
  return {
    rootFolderId:
      typeof raw.rootFolderId === 'number' && rootIds.includes(raw.rootFolderId) ? raw.rootFolderId : (rootIds[0] ?? 0),
    qualityProfileId:
      typeof raw.qualityProfileId === 'number' && profileIds.includes(raw.qualityProfileId)
        ? raw.qualityProfileId
        : (profileIds[0] ?? 0),
    minimumAvailability: AVAIL.has(raw.minimumAvailability as Availability)
      ? (raw.minimumAvailability as Availability)
      : 'released',
    monitor: MONITOR.has(raw.monitor as MonitorChoice) ? (raw.monitor as MonitorChoice) : 'film',
    tags: Array.isArray(raw.tags) ? raw.tags.filter((t): t is string => typeof t === 'string').slice(0, 20) : [],
    searchOnAdd: typeof raw.searchOnAdd === 'boolean' ? raw.searchOnAdd : true,
  };
}

export function saveAddOptions(o: AddOptions, storage: Pick<Storage, 'setItem'> | undefined = globalThis.localStorage) {
  try {
    storage?.setItem(KEY, JSON.stringify(o));
  } catch {
    /* kept for this visit only */
  }
}

/** Body of POST /movies for these options. */
export function addBody(tmdbId: number, o: AddOptions) {
  return {
    tmdbId,
    rootFolderId: o.rootFolderId,
    qualityProfileId: o.qualityProfileId,
    minimumAvailability: o.minimumAvailability,
    monitored: o.monitor !== 'none',
    searchOnAdd: o.monitor !== 'none' && o.searchOnAdd,
    tags: o.tags,
  };
}
