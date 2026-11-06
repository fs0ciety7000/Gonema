import type {
  ProfileEntry,
  ProfileSize,
  QualityDefinition,
  QualityProfileDetail,
  QualityProfileInput,
  ScoringCondition,
} from '@/api/schema';
import { formatScore } from '@/lib/format';

export type SizeDraft = { minMbPerMin: number; preferredMbPerMin: number; maxMbPerMin: number };

export type ProfileDraft = {
  name: string;
  upgradeAllowed: boolean;
  entries: ProfileEntry[];
  /** Group name or quality id at which quality upgrades stop. */
  cutoff: string;
  minScore: number;
  cutoffScore: number;
  minUpgradeStep: number;
  /** Score of each rule for this profile (rules left out score 0). */
  scores: Record<number, number>;
  /** Size overrides by quality id; null leaves the stored ones untouched. */
  sizes: Record<string, SizeDraft> | null;
};

export const entryKey = (e: ProfileEntry) => e.group || e.qualities?.[0] || '';

export function draftOf(p: QualityProfileDetail): ProfileDraft {
  return {
    name: p.name,
    upgradeAllowed: p.upgradeAllowed,
    entries: (p.entries ?? []).map((e) => ({ ...e, qualities: [...(e.qualities ?? [])] })),
    cutoff: p.cutoffQuality,
    minScore: p.minScore,
    cutoffScore: p.cutoffScore,
    minUpgradeStep: p.minUpgradeStep,
    scores: Object.fromEntries((p.scores ?? []).map((s) => [s.ruleId, s.score])),
    sizes: null,
  };
}

/** A new profile: every quality of the catalog, best first, 1080p and up allowed. */
export function newDraft(catalog: readonly QualityDefinition[], name: string): ProfileDraft {
  const sorted = [...catalog].sort((a, b) => b.rank - a.rank);
  const entries = sorted.map((q) => ({ qualities: [q.id], allowed: /1080|2160/.test(q.resolution || q.id) }));
  const cutoff = entries.find((e) => e.allowed)?.qualities[0] ?? sorted[0]?.id ?? '';
  return {
    name,
    upgradeAllowed: true,
    entries,
    cutoff,
    minScore: 0,
    cutoffScore: 0,
    minUpgradeStep: 1,
    scores: {},
    sizes: null,
  };
}

export function moveEntry(entries: readonly ProfileEntry[], from: number, to: number): ProfileEntry[] {
  if (from === to || from < 0 || to < 0 || from >= entries.length || to >= entries.length) return [...entries];
  const out = [...entries];
  const [e] = out.splice(from, 1);
  out.splice(to, 0, e!);
  return out;
}

export function toInput(d: ProfileDraft): QualityProfileInput {
  return {
    name: d.name.trim(),
    upgradeAllowed: d.upgradeAllowed,
    entries: d.entries,
    cutoff: d.cutoff,
    minScore: d.minScore,
    cutoffScore: d.cutoffScore,
    minUpgradeStep: d.minUpgradeStep,
    scores: Object.entries(d.scores).map(([ruleId, score]) => ({ ruleId: Number(ruleId), score })),
    ...(d.sizes ? { sizes: Object.entries(d.sizes).map(([quality, s]) => ({ quality, ...s })) } : {}),
  };
}

/** What is wrong with the draft, in words that say how to fix it (null when it can be saved). */
export function draftError(d: ProfileDraft): string | null {
  if (!d.name.trim()) return 'Give the profile a name.';
  if (!d.entries.some((e) => e.allowed)) return 'Allow at least one quality, or nothing will ever be grabbed.';
  const cut = d.entries.find((e) => entryKey(e) === d.cutoff);
  if (d.upgradeAllowed && !cut?.allowed) return 'Pick an allowed quality as the cutoff.';
  if (d.cutoffScore < d.minScore) return 'The cutoff score cannot be lower than the minimum score.';
  return null;
}

/** "WEB-DL 1080p" style label of an entry (groups keep their name). */
export function entryLabel(e: ProfileEntry, names: ReadonlyMap<string, string>): string {
  return e.group || names.get(e.qualities?.[0] ?? '') || e.qualities?.[0] || '';
}

const resOf = (id: string) => /(\d{3,4}p)/.exec(id)?.[1];

/** The profile in one sentence (mockup "In plain words"). */
export function plainWords(d: ProfileDraft, names: ReadonlyMap<string, string>): string {
  const allowed = d.entries.filter((e) => e.allowed);
  if (!allowed.length) return 'Nothing is allowed: Gonema will not grab any release with this profile.';
  const res = [
    ...new Set(
      allowed
        .flatMap((e) => e.qualities ?? [])
        .map(resOf)
        .filter(Boolean),
    ),
  ] as string[];
  res.sort((a, b) => Number.parseInt(a, 10) - Number.parseInt(b, 10));
  const resText = res.length ? `${res.join(res.length > 2 ? ', ' : ' or ')} ` : '';
  const grab = `Grab any allowed ${resText}copy scoring ${formatScore(d.minScore)} or more.`;
  if (!d.upgradeAllowed) return `${grab} Keep the first good copy: no upgrades.`;
  const cut = d.entries.find((e) => entryKey(e) === d.cutoff);
  const cutLabel = cut ? entryLabel(cut, names) : d.cutoff;
  return `${grab} Keep upgrading when a copy is better by at least ${d.minUpgradeStep} ${d.minUpgradeStep === 1 ? 'point' : 'points'} or a higher quality, until the file is ${cutLabel} scoring ${formatScore(d.cutoffScore)}.`;
}

/** Size range of a two-hour film for MB-per-minute limits: "6–25 GB", "from 6 GB". */
export function sizeRange(s: Pick<ProfileSize, 'minMbPerMin' | 'maxMbPerMin'> | undefined, minutes = 120): string {
  if (!s) return '—';
  const gb = (mb: number) => {
    const v = (mb * minutes) / 1000;
    return v >= 10 ? Math.round(v) : Math.round(v * 10) / 10;
  };
  if (!s.minMbPerMin && !s.maxMbPerMin) return 'any size';
  if (!s.maxMbPerMin) return `from ${gb(s.minMbPerMin)} GB`;
  return `${gb(s.minMbPerMin)}–${gb(s.maxMbPerMin)} GB`;
}

// ----------------------------------------------------------------- scoring rules

export const KIND_LABEL: Record<ScoringCondition['kind'], string> = {
  title_regex: 'Release title',
  release_group: 'Release group',
  source: 'Source',
  resolution: 'Resolution',
  codec: 'Video codec',
  hdr: 'HDR format',
  audio: 'Audio',
  edition: 'Edition',
  language: 'Language',
  indexer_flag: 'Indexer flag',
  indexer: 'Indexer',
  size_range: 'Size',
  year_range: 'Year',
};

/** Comma-separated value of a condition ↔ chips. */
export const splitValues = (v: string | undefined) =>
  (v ?? '')
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean);

export function conditionError(c: ScoringCondition): string | null {
  if (c.kind === 'size_range' || c.kind === 'year_range') {
    if ((c.max ?? 0) > 0 && (c.min ?? 0) > (c.max ?? 0)) return 'The minimum is above the maximum.';
    return null;
  }
  if (!c.value?.trim()) return 'Add at least one value.';
  if (c.kind === 'title_regex') {
    try {
      new RegExp(c.value);
    } catch {
      return 'This is not a valid regular expression.';
    }
  }
  return null;
}

// ----------------------------------------------------------------- quality groups

/* Groups: qualities that count as equal (one rank); among them the best score wins. The API
   takes the new entries as they are (rename, merge and split are a PUT of the profile); the
   cutoff follows the group it pointed at. */

export const isGroup = (e: ProfileEntry) => !!e.group && (e.qualities?.length ?? 0) > 1;

/** Why a group name cannot be used (null when it can): empty, too long, taken by another group
 *  (case-insensitive) or by a quality. `self` is the group being renamed. */
export function groupNameError(
  entries: readonly ProfileEntry[],
  name: string,
  names: ReadonlyMap<string, string>,
  self?: string,
): string | null {
  const n = name.trim();
  if (!n) return 'Give the group a name.';
  if (n.length > 100) return 'Keep the name under 100 characters.';
  const low = n.toLowerCase();
  if (entries.some((e) => e.group && e.group !== self && e.group.toLowerCase() === low))
    return 'Another group has this name.';
  for (const [id, label] of names)
    if (id.toLowerCase() === low || label.toLowerCase() === low) return 'A quality has this name: pick another.';
  return null;
}

/** "WEB 2160p" for WEBDL-2160p + WEBRip-2160p; members joined with "+" otherwise. Unique in `entries`. */
export function defaultGroupName(
  qualities: readonly string[],
  names: ReadonlyMap<string, string>,
  entries: readonly ProfileEntry[] = [],
): string {
  const labels = qualities.map((q) => names.get(q) ?? q);
  const res = labels.map((l) => /(\d{3,4}p)/.exec(l)?.[1] ?? '');
  let base: string;
  if (labels.length && res[0] && res.every((r) => r === res[0])) {
    const sources = labels.map((l) => l.replace(res[0]!, '').replace(/[-_\s]+$|^[-_\s]+/g, ''));
    let p = sources[0] ?? '';
    for (const s of sources) while (p && !s.toLowerCase().startsWith(p.toLowerCase())) p = p.slice(0, -1);
    p = p.replace(/[^A-Za-z0-9]+$/, '');
    base = p.length >= 2 ? `${p} ${res[0]}` : `${sources.join(' + ')} ${res[0]}`;
  } else base = labels.join(' + ');
  base = base.slice(0, 60).trim() || 'Group';
  let name = base;
  for (let i = 2; groupNameError(entries, name, names) === 'Another group has this name.'; i++) name = `${base} ${i}`;
  return name;
}

const keyCutoff = (d: ProfileDraft, gone: readonly ProfileEntry[], to: string) =>
  gone.some((e) => entryKey(e) === d.cutoff || (e.qualities ?? []).includes(d.cutoff)) ? to : d.cutoff;

/** Makes one group of the single qualities `qualities`, where the best of them was. */
export function groupQualities(d: ProfileDraft, qualities: readonly string[], name: string): ProfileDraft {
  const set = new Set(qualities);
  const picked = d.entries.filter((e) => !isGroup(e) && (e.qualities ?? []).some((q) => set.has(q)));
  if (picked.length < 2) return d;
  const at = d.entries.indexOf(picked[0]!);
  const group: ProfileEntry = {
    group: name.trim(),
    qualities: picked.flatMap((e) => e.qualities ?? []),
    allowed: picked.some((e) => e.allowed),
  };
  const entries = d.entries.filter((e) => !picked.includes(e));
  entries.splice(at, 0, group);
  return { ...d, entries, cutoff: keyCutoff(d, picked, group.group!) };
}

export function renameGroup(d: ProfileDraft, from: string, to: string): ProfileDraft {
  const name = to.trim();
  return {
    ...d,
    entries: d.entries.map((e) => (e.group === from ? { ...e, group: name } : e)),
    cutoff: d.cutoff === from ? name : d.cutoff,
  };
}

/** Splits a group into single qualities, in its place; a cutoff on the group moves to its best member. */
export function splitGroup(d: ProfileDraft, name: string): ProfileDraft {
  const i = d.entries.findIndex((e) => e.group === name);
  const g = d.entries[i];
  if (!g) return d;
  const singles = (g.qualities ?? []).map((q) => ({ qualities: [q], allowed: g.allowed }));
  const entries = [...d.entries];
  entries.splice(i, 1, ...singles);
  return { ...d, entries, cutoff: d.cutoff === name ? (g.qualities?.[0] ?? d.cutoff) : d.cutoff };
}

/** Takes one quality out of its group (placed right after it); a group left with one quality becomes a single. */
export function ungroupQuality(d: ProfileDraft, name: string, quality: string): ProfileDraft {
  const i = d.entries.findIndex((e) => e.group === name);
  const g = d.entries[i];
  if (!g || !(g.qualities ?? []).includes(quality)) return d;
  const rest = (g.qualities ?? []).filter((q) => q !== quality);
  if (rest.length < 2) return splitGroup(d, name);
  const entries = [...d.entries];
  entries.splice(i, 1, { ...g, qualities: rest }, { qualities: [quality], allowed: g.allowed });
  return { ...d, entries };
}

/** Merges the entries at i and i+1 into one group (a group keeps its name; two singles get one). */
export function mergeWithNext(d: ProfileDraft, i: number, names: ReadonlyMap<string, string>): ProfileDraft {
  const a = d.entries[i];
  const b = d.entries[i + 1];
  if (!a || !b) return d;
  const qualities = [...(a.qualities ?? []), ...(b.qualities ?? [])];
  const name = (isGroup(a) && a.group) || (isGroup(b) && b.group) || defaultGroupName(qualities, names, d.entries);
  const merged: ProfileEntry = { group: name, qualities, allowed: a.allowed || b.allowed };
  const entries = [...d.entries];
  entries.splice(i, 2, merged);
  return { ...d, entries, cutoff: keyCutoff(d, [a, b], name) };
}
