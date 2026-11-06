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
