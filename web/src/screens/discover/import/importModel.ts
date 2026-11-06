import type { ProfileEntry, QualityDefinition } from '@/api/schema';
import type { LibraryCandidate, LibraryFolder, LibraryImport, LibraryScan, LibrarySelection } from '@/api/v11';

/* Folder import (library-import.html): the review table's rows, its filters and counts, the body of
   POST /library-import/confirm and the summary of its answer. Pure: tested in import.test.ts. */

export type Mode = 'keep' | 'move';

/** What the user decided for one folder. */
export type RowChoice = {
  selected: boolean;
  /** The chosen TMDB match (null: no film, the folder is left out). */
  match: Pick<LibraryCandidate, 'tmdbId' | 'title' | 'year' | 'posterUrl' | 'confidence' | 'reason'> | null;
  picked: boolean;
  profileId: number;
  monitored: boolean;
  mode: Mode;
};

export type Choices = Record<string, RowChoice>;

export const importable = (f: Pick<LibraryFolder, 'status'>) => f.status !== 'in_library';

/** Default choice: new folders with a match are selected, kept in place and monitored. */
export function initialChoice(f: LibraryFolder, profileId: number): RowChoice {
  const best = f.candidates?.[0] ?? null;
  return {
    selected: f.status === 'new' && !!best && !f.rejections?.length,
    match: f.status === 'no_match' ? null : best,
    picked: false,
    profileId,
    monitored: true,
    mode: 'keep',
  };
}

/** Confidence bars (1 to 4) and their word, as in the mockup: ≥ 90 % high, ≥ 75 % medium, else low. */
export function confidenceLevel(c: number): { pct: number; level: 1 | 2 | 3 | 4; word: 'high' | 'medium' | 'low' } {
  const pct = Math.round(Math.max(0, Math.min(1, c)) * 100);
  const level = pct >= 90 ? 4 : pct >= 75 ? 3 : pct >= 50 ? 2 : 1;
  return { pct, level, word: level === 4 ? 'high' : level === 3 ? 'medium' : 'low' };
}

export type ReviewTab = 'all' | 'look' | 'new' | 'library';

export const needsALook = (f: Pick<LibraryFolder, 'status'>) => f.status === 'ambiguous' || f.status === 'no_match';

export function tabCounts(folders: readonly LibraryFolder[]): Record<ReviewTab, number> {
  let look = 0;
  let fresh = 0;
  let lib = 0;
  for (const f of folders) {
    if (needsALook(f)) look++;
    else if (f.status === 'new') fresh++;
    else lib++;
  }
  return { all: folders.length, look, new: fresh, library: lib };
}

export type ReviewSort = 'look' | 'name';

/** Folders of a tab; "Needs a look first" puts ambiguous and unmatched folders on top, then by name. */
export function reviewRows(folders: readonly LibraryFolder[], tab: ReviewTab, sort: ReviewSort): LibraryFolder[] {
  const rows = folders.filter((f) =>
    tab === 'all'
      ? true
      : tab === 'look'
        ? needsALook(f)
        : tab === 'new'
          ? f.status === 'new'
          : f.status === 'in_library',
  );
  const rank = (f: LibraryFolder) =>
    f.status === 'ambiguous' ? 0 : f.status === 'no_match' ? 1 : f.status === 'new' ? 2 : 3;
  return rows.sort(
    (a, b) =>
      (sort === 'look' ? rank(a) - rank(b) : 0) || a.folder.localeCompare(b.folder, 'en', { sensitivity: 'base' }),
  );
}

/** Selected folders that can be imported (a match, not in the library). */
export function selectedFolders(folders: readonly LibraryFolder[], choices: Choices): LibraryFolder[] {
  return folders.filter((f) => importable(f) && choices[f.folder]?.selected && choices[f.folder]?.match);
}

export function selectionSummary(folders: readonly LibraryFolder[], choices: Choices) {
  const sel = selectedFolders(folders, choices);
  let bytes = 0;
  let moved = 0;
  for (const f of sel) {
    bytes += f.size ?? 0;
    if (choices[f.folder]?.mode === 'move') moved++;
  }
  return { count: sel.length, bytes, kept: sel.length - moved, moved };
}

/** Body of POST /library-import/confirm. */
export function toSelections(folders: readonly LibraryFolder[], choices: Choices): LibrarySelection[] {
  return selectedFolders(folders, choices).map((f) => {
    const c = choices[f.folder]!;
    return {
      folder: f.folder,
      tmdbId: c.match!.tmdbId,
      qualityProfileId: c.profileId,
      monitored: c.monitored,
      mode: c.mode,
      searchOnAdd: false,
    };
  });
}

/** Rank at which a profile entry is reached (catalog ranks: higher is better). The qualities of a
 *  group are equal, so its lowest member already meets it. */
function entryRank(e: ProfileEntry | undefined, ranks: ReadonlyMap<string, number>): number {
  const r = (e?.qualities ?? []).map((q) => ranks.get(q)).filter((x): x is number => x !== undefined);
  return r.length ? Math.min(...r) : -1;
}

/** The file is below the profile's cutoff (Gonema looks for a better copy later). Unknown: false. */
export function belowCutoff(
  quality: string | undefined,
  profile: { cutoffQuality: string; upgradeAllowed: boolean; entries?: ProfileEntry[] | null } | undefined,
  catalog: readonly Pick<QualityDefinition, 'id' | 'rank'>[],
): boolean {
  if (!quality || !profile?.upgradeAllowed) return false;
  const ranks = new Map(catalog.map((q) => [q.id, q.rank]));
  const q = ranks.get(quality);
  if (q === undefined) return false;
  const cut = (profile.entries ?? []).find(
    (e) => e.group === profile.cutoffQuality || e.qualities?.includes(profile.cutoffQuality),
  );
  const c = cut ? entryRank(cut, ranks) : (ranks.get(profile.cutoffQuality) ?? -1);
  return c >= 0 && q < c;
}

/** "Folders found" feed of the scan step: newest first (event order), else the reverse of the folder order. */
export function feedOf(scan: Pick<LibraryScan, 'folders'>, arrived: readonly string[], max = 8): LibraryFolder[] {
  const byName = new Map((scan.folders ?? []).map((f) => [f.folder, f]));
  const seen = new Set<string>();
  const out: LibraryFolder[] = [];
  for (let i = arrived.length - 1; i >= 0 && out.length < max; i--) {
    const f = byName.get(arrived[i]!);
    if (f && !seen.has(f.folder)) {
      seen.add(f.folder);
      out.push(f);
    }
  }
  const rest = [...(scan.folders ?? [])].reverse();
  for (const f of rest) {
    if (out.length >= max) break;
    if (!seen.has(f.folder)) out.push(f);
  }
  return out;
}

/** Adds a folder streamed by a libraryimport.folder event to the scan (replacing an older copy). */
export function withFolder(scan: LibraryScan, ev: { done: number; total: number; folder: LibraryFolder }): LibraryScan {
  const folders = (scan.folders ?? []).filter((f) => f.folder !== ev.folder.folder);
  folders.push(ev.folder);
  folders.sort((a, b) => a.folder.localeCompare(b.folder));
  const counts = { new: 0, ambiguous: 0, noMatch: 0, inLibrary: 0 };
  for (const f of folders) {
    if (f.status === 'new') counts.new++;
    else if (f.status === 'ambiguous') counts.ambiguous++;
    else if (f.status === 'no_match') counts.noMatch++;
    else counts.inLibrary++;
  }
  return {
    ...scan,
    status: scan.status === 'pending' ? 'running' : scan.status,
    done: Math.max(scan.done, ev.done),
    total: Math.max(scan.total, ev.total),
    folders,
    counts,
  };
}

export const isRunning = (s: Pick<LibraryScan, 'status'> | undefined) =>
  s?.status === 'pending' || s?.status === 'running';

/** Seconds left from the pace so far (null until it can be told). */
export function secondsLeft(s: Pick<LibraryScan, 'done' | 'total' | 'createdAt'>, now = Date.now()): number | null {
  const elapsed = (now - Date.parse(s.createdAt)) / 1000;
  if (!s.total || s.done < 3 || !(elapsed > 0)) return null;
  return Math.max(0, Math.round(((s.total - s.done) * elapsed) / s.done));
}

export function formatSeconds(sec: number): string {
  if (sec < 60) return `${Math.max(1, Math.round(sec))} s`;
  const m = Math.floor(sec / 60);
  const s = Math.round(sec % 60);
  return s ? `${m} min ${s} s` : `${m} min`;
}

export type DoneSummary = {
  imported: number;
  kept: number;
  moved: number;
  added: number;
  failed: number;
  skipped: number;
  belowCutoff: number;
  failures: { folder: string; error: string }[];
  collections: string[];
};

export function doneSummary(r: LibraryImport): DoneSummary {
  const s = r.summary;
  const failures = (r.results ?? [])
    .filter((x) => x.outcome === 'failed')
    .map((x) => ({ folder: x.folder, error: x.error || x.reason || 'The import failed.' }));
  const collections = [...(s.collectionsCompleted ?? []), ...(s.collectionsGrown ?? [])]
    .map((c) => c.name)
    .filter((n, i, a) => a.indexOf(n) === i);
  return {
    imported: s.imported,
    kept: s.kept,
    moved: s.moved,
    added: s.added,
    failed: s.failed,
    skipped: s.skipped,
    belowCutoff: s.belowCutoff,
    failures,
    collections,
  };
}

/** "A, B and C" */
export function listWords(items: readonly string[]): string {
  if (items.length <= 1) return items[0] ?? '';
  return `${items.slice(0, -1).join(', ')} and ${items.at(-1)}`;
}
