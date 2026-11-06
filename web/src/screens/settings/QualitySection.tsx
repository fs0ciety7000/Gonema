import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Check,
  ChevronDown,
  ChevronUp,
  CircleCheck,
  CircleX,
  GripVertical,
  Layers,
  Minus,
  Plus,
  Sparkles,
  Trash2,
  WandSparkles,
  X,
} from 'lucide-react';
import { type KeyboardEvent, type PointerEvent as ReactPointerEvent, useEffect, useMemo, useRef, useState } from 'react';
import {
  conditionKindsQuery,
  previewRelease,
  profileApi,
  qualitiesQuery,
  ruleApi,
  scoringRulesQuery,
} from '@/api/lot3';
import { keys, profilesQuery, useLibrary } from '@/api/queries';
import type {
  Preview,
  ProfileEntry,
  QualityProfileDetail,
  ScoringCondition,
  ScoringRule,
} from '@/api/schema';
import { QualityBadge } from '@/components/ui/Badges';
import { Button, IconButton } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Chip } from '@/components/ui/Chip';
import { cx } from '@/components/ui/cx';
import { EmptyState, Skeleton } from '@/components/ui/Feedback';
import { Field, Input } from '@/components/ui/Field';
import { Select } from '@/components/ui/Select';
import { Switch } from '@/components/ui/Switch';
import { toast } from '@/components/ui/Toast';
import { formatNumber, formatScore } from '@/lib/format';
import { errorText } from './form';
import {
  conditionError,
  draftError,
  draftOf,
  entryKey,
  entryLabel,
  KIND_LABEL,
  moveEntry,
  newDraft,
  type ProfileDraft,
  plainWords,
  sizeRange,
  splitValues,
  toInput,
} from './qualityModel';

/** Integer field that accepts "+40", "−20" and "-20". */
function ScoreInput({
  value,
  onChange,
  label,
  id,
  className,
  invalid,
}: {
  value: number;
  onChange: (n: number) => void;
  label?: string;
  id?: string;
  className?: string;
  invalid?: boolean;
}) {
  const [text, setText] = useState(formatScore(value));
  const focused = useRef(false);
  useEffect(() => {
    if (!focused.current) setText(formatScore(value));
  }, [value]);
  return (
    <Input
      id={id}
      mono
      inputMode="numeric"
      aria-label={label}
      invalid={invalid}
      wrapperClassName={className}
      value={text}
      onFocus={() => {
        focused.current = true;
      }}
      onBlur={() => {
        focused.current = false;
        setText(formatScore(value));
      }}
      onChange={(ev) => {
        setText(ev.target.value);
        const n = Number(ev.target.value.replace('−', '-').replace('+', '').trim());
        if (Number.isInteger(n) && Math.abs(n) <= 100_000) onChange(n);
      }}
    />
  );
}

// ----------------------------------------------------------------- qualities list

function QualityList({
  draft,
  set,
  names,
  sizes,
}: {
  draft: ProfileDraft;
  set: (d: ProfileDraft) => void;
  names: ReadonlyMap<string, string>;
  sizes: ReadonlyMap<string, { minMbPerMin: number; maxMbPerMin: number }>;
}) {
  const [drag, setDrag] = useState<{ from: number; over: number } | null>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const [announce, setAnnounce] = useState('');
  const move = (from: number, to: number) => {
    const entries = moveEntry(draft.entries, from, to);
    set({ ...draft, entries });
    const e = entries[to];
    if (e) setAnnounce(`${entryLabel(e, names)} moved to position ${to + 1} of ${entries.length}.`);
  };
  const onKey = (ev: KeyboardEvent, i: number) => {
    if (ev.key !== 'ArrowUp' && ev.key !== 'ArrowDown') return;
    ev.preventDefault();
    const to = ev.key === 'ArrowUp' ? i - 1 : i + 1;
    if (to < 0 || to >= draft.entries.length) return;
    move(i, to);
    requestAnimationFrame(() => listRef.current?.querySelectorAll<HTMLButtonElement>('.grip')[to]?.focus());
  };
  const onPointerDown = (ev: ReactPointerEvent, i: number) => {
    if (ev.button !== 0) return;
    (ev.target as HTMLElement).setPointerCapture?.(ev.pointerId);
    setDrag({ from: i, over: i });
  };
  const onPointerMove = (ev: ReactPointerEvent) => {
    if (!drag || !listRef.current) return;
    const rows = [...listRef.current.querySelectorAll<HTMLElement>('[data-entry]')];
    let over = rows.length - 1;
    for (let k = 0; k < rows.length; k++) {
      const r = rows[k]!.getBoundingClientRect();
      if (ev.clientY < r.top + r.height / 2) {
        over = k;
        break;
      }
    }
    if (over !== drag.over) setDrag({ ...drag, over });
  };
  const onPointerUp = () => {
    if (drag && drag.over !== drag.from) move(drag.from, drag.over);
    setDrag(null);
  };
  const lastAllowed = draft.entries.map((e) => e.allowed).lastIndexOf(true);
  const floorAt = draft.entries.slice(lastAllowed + 1).length && draft.entries.slice(lastAllowed + 1).every((e) => !e.allowed) ? lastAllowed : -1;
  const toggle = (i: number, allowed: boolean) =>
    set({ ...draft, entries: draft.entries.map((e, k) => (k === i ? { ...e, allowed } : e)) });
  const row = (e: ProfileEntry, q: string, i: number, inGroup: boolean) => (
    <span className={cx('qr', !e.allowed && 'off')} key={q}>
      {!inGroup ? (
        <button
          type="button"
          className="grip"
          aria-label={`Move ${entryLabel(e, names)}, position ${i + 1} of ${draft.entries.length}. Use the up and down arrows.`}
          onKeyDown={(ev) => onKey(ev, i)}
          onPointerDown={(ev) => onPointerDown(ev, i)}
        >
          <GripVertical className="i" aria-hidden="true" />
        </button>
      ) : (
        <span />
      )}
      {!inGroup ? (
        <Checkbox
          checked={e.allowed}
          onCheckedChange={(c) => toggle(i, c)}
          aria-label={`Allow ${entryLabel(e, names)}`}
        />
      ) : (
        <span />
      )}
      <span className="qcell">
        <QualityBadge quality={q} dim={!e.allowed} />
        <span className="sr-only">{names.get(q) ?? q}</span>
      </span>
      <span className="sz">{e.allowed ? sizeRange(sizes.get(q)) : '—'}</span>
    </span>
  );
  return (
    <>
      <ul
        className="ql"
        aria-label="Qualities, best first"
        ref={listRef}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={() => setDrag(null)}
      >
        {draft.entries.map((e, i) => {
          const k = entryKey(e);
          const isCut = draft.upgradeAllowed && k === draft.cutoff;
          const dragging = drag?.from === i;
          return [
            drag && drag.over === i && drag.from !== i && drag.from > i ? <li key={`drop-${k}`} className="qr drop" aria-hidden="true" /> : null,
            <li key={k} data-entry={k} className={cx(e.qualities && e.qualities.length > 1 && 'qgroup', dragging && 'drag')}>
              {e.qualities && e.qualities.length > 1 ? (
                <>
                  <div className="gh">
                    <button
                      type="button"
                      className="grip"
                      aria-label={`Move group ${e.group}, position ${i + 1} of ${draft.entries.length}. Use the up and down arrows.`}
                      onKeyDown={(ev) => onKey(ev, i)}
                      onPointerDown={(ev) => onPointerDown(ev, i)}
                    >
                      <GripVertical className="i" aria-hidden="true" />
                    </button>
                    <Checkbox checked={e.allowed} onCheckedChange={(c) => toggle(i, c)} aria-label={`Allow group ${e.group}`} />
                    <Layers className="i i-xs" aria-hidden="true" />
                    {e.group} <span className="t-muted">· equal, best score wins</span>
                  </div>
                  {e.qualities.map((q) => row(e, q, i, true))}
                </>
              ) : (
                row(e, e.qualities?.[0] ?? '', i, false)
              )}
            </li>,
            drag && drag.over === i && drag.from !== i && drag.from < i ? <li key={`drop2-${k}`} className="qr drop" aria-hidden="true" /> : null,
            isCut ? (
              <li key="cut" className="marker cut">
                <span>
                  <Sparkles className="i" aria-hidden="true" />
                  Cutoff · stop upgrading here
                </span>
              </li>
            ) : null,
            i === floorAt ? (
              <li key="floor" className="marker floor">
                <span>
                  <Minus className="i" aria-hidden="true" />
                  Floor · nothing below is grabbed
                </span>
              </li>
            ) : null,
          ];
        })}
      </ul>
      <span className="sr-only" aria-live="polite">
        {announce}
      </span>
    </>
  );
}

// ----------------------------------------------------------------- sizes

function Sizes({
  draft,
  set,
  profile,
  names,
}: {
  draft: ProfileDraft;
  set: (d: ProfileDraft) => void;
  profile: QualityProfileDetail | null;
  names: ReadonlyMap<string, string>;
}) {
  const stored = new Map((profile?.sizes ?? []).map((s) => [s.quality, s]));
  const qualities = draft.entries.filter((e) => e.allowed).flatMap((e) => e.qualities ?? []);
  const cur = (q: string) =>
    draft.sizes?.[q] ?? stored.get(q) ?? { minMbPerMin: 0, preferredMbPerMin: 0, maxMbPerMin: 0 };
  const edit = (q: string, k: 'minMbPerMin' | 'preferredMbPerMin' | 'maxMbPerMin', v: number) => {
    const base: ProfileDraft['sizes'] =
      draft.sizes ??
      Object.fromEntries(
        (profile?.sizes ?? [])
          .filter((s) => s.custom)
          .map((s) => [s.quality, { minMbPerMin: s.minMbPerMin, preferredMbPerMin: s.preferredMbPerMin, maxMbPerMin: s.maxMbPerMin }]),
      );
    set({ ...draft, sizes: { ...base, [q]: { ...cur(q), [k]: v } } });
  };
  return (
    <details className="sizes">
      <summary>Size limits per quality (MB per minute)</summary>
      <table className="size-tbl">
        <caption className="sr-only">Size limits, in megabytes per minute of film</caption>
        <thead>
          <tr>
            <th scope="col">Quality</th>
            <th scope="col">Min</th>
            <th scope="col">Preferred</th>
            <th scope="col">Max</th>
            <th scope="col">2 h film</th>
          </tr>
        </thead>
        <tbody>
          {qualities.map((q) => {
            const s = cur(q);
            const name = names.get(q) ?? q;
            return (
              <tr key={q}>
                <th scope="row">
                  {name}
                  {(draft.sizes?.[q] || stored.get(q)?.custom) && <span className="t-caption t-muted"> · custom</span>}
                </th>
                {(['minMbPerMin', 'preferredMbPerMin', 'maxMbPerMin'] as const).map((k) => (
                  <td key={k}>
                    <Input
                      mono
                      type="number"
                      min={0}
                      step={1}
                      aria-label={`${name}: ${k === 'minMbPerMin' ? 'minimum' : k === 'maxMbPerMin' ? 'maximum (0 = no limit)' : 'preferred'} MB per minute`}
                      value={String(s[k])}
                      onChange={(ev) => {
                        const n = Number(ev.target.value);
                        if (Number.isFinite(n) && n >= 0) edit(q, k, n);
                      }}
                    />
                  </td>
                ))}
                <td className="sz">{sizeRange(s)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </details>
  );
}

// ----------------------------------------------------------------- rule editor

const NUM_KINDS = new Set(['size_range', 'year_range']);

function ConditionRow({
  c,
  onChange,
  onRemove,
  kinds,
}: {
  c: ScoringCondition;
  onChange: (c: ScoringCondition) => void;
  onRemove: () => void;
  kinds: ReadonlyArray<{ kind: string; label: string; input: string; values: string[] | null; unit?: string }>;
}) {
  const info = kinds.find((k) => k.kind === c.kind);
  const values = splitValues(c.value);
  const [adding, setAdding] = useState('');
  const err = conditionError(c);
  const label = info?.label ?? KIND_LABEL[c.kind];
  return (
    <div className="cond" role="group" aria-label={`Condition: ${label}`}>
      <Select
        ariaLabel={`Condition kind: ${label}`}
        value={c.kind}
        options={(kinds.length ? kinds : Object.keys(KIND_LABEL).map((k) => ({ kind: k, label: KIND_LABEL[k as ScoringCondition['kind']] }))).map((k) => ({ value: k.kind, label: k.label }))}
        onValueChange={(kind) => onChange({ kind: kind as ScoringCondition['kind'], required: c.required, negate: c.negate })}
      />
      {NUM_KINDS.has(c.kind) ? (
        <div className="range">
          <Input
            mono
            type="number"
            min={0}
            aria-label={`${label} from${info?.unit ? ` (${info.unit})` : ''}`}
            value={String(c.min ?? 0)}
            onChange={(ev) => onChange({ ...c, min: Math.max(0, Number(ev.target.value) || 0) })}
          />
          <span className="t-muted">to</span>
          <Input
            mono
            type="number"
            min={0}
            aria-label={`${label} up to${info?.unit ? ` (${info.unit})` : ''}, 0 for no limit`}
            value={String(c.max ?? 0)}
            onChange={(ev) => onChange({ ...c, max: Math.max(0, Number(ev.target.value) || 0) })}
          />
          {info?.unit && <span className="t-caption t-muted">{info.unit}</span>}
        </div>
      ) : info?.input === 'choice' && info.values?.length ? (
        <div className="valchips" role="group" aria-label={`${label} values`}>
          {info.values.map((v) => (
            <Chip
              key={v}
              pressed={values.includes(v)}
              onClick={() =>
                onChange({
                  ...c,
                  value: (values.includes(v) ? values.filter((x) => x !== v) : [...values, v]).join(','),
                })
              }
            >
              {v}
            </Chip>
          ))}
        </div>
      ) : info?.input === 'regex' || c.kind === 'title_regex' ? (
        <Input
          mono
          aria-label={`${label}, regular expression`}
          invalid={!!err}
          value={c.value ?? ''}
          placeholder="\\b(DV|DoVi)\\b"
          onChange={(ev) => onChange({ ...c, value: ev.target.value })}
        />
      ) : (
        <div className="input valchips">
          {values.map((v) => (
            <span key={v} className="tag plain">
              {v}
              <button
                type="button"
                className="remove"
                aria-label={`Remove ${v}`}
                onClick={() => onChange({ ...c, value: values.filter((x) => x !== v).join(',') })}
              >
                <X className="i i-xs" aria-hidden="true" />
              </button>
            </span>
          ))}
          <input
            aria-label={`Add a ${label.toLowerCase()} value, then press Enter`}
            value={adding}
            placeholder={values.length ? '' : 'Type and press Enter'}
            onChange={(ev) => setAdding(ev.target.value)}
            onKeyDown={(ev) => {
              if ((ev.key === 'Enter' || ev.key === ',') && adding.trim()) {
                ev.preventDefault();
                onChange({ ...c, value: [...values, adding.trim()].join(',') });
                setAdding('');
              } else if (ev.key === 'Backspace' && !adding && values.length) {
                onChange({ ...c, value: values.slice(0, -1).join(',') });
              }
            }}
          />
        </div>
      )}
      <IconButton label={`Remove the ${label.toLowerCase()} condition`} size="sm" onClick={onRemove}>
        <X className="i i-sm" aria-hidden="true" />
      </IconButton>
      <div className="flags">
        <Switch checked={!!c.required} onCheckedChange={(v) => onChange({ ...c, required: v })}>
          Required
        </Switch>
        <Switch checked={!!c.negate} onCheckedChange={(v) => onChange({ ...c, negate: v })}>
          Not
        </Switch>
        {err && <span className="field-error">{err}</span>}
      </div>
    </div>
  );
}

type RuleDraft = {
  name: string;
  description: string;
  includeInRename: boolean;
  conditions: ScoringCondition[];
  scores: Record<number, number>;
};

function RuleEditor({
  rule,
  profiles,
  onClose,
  focusProfile,
}: {
  rule: ScoringRule | null;
  profiles: QualityProfileDetail[];
  onClose: () => void;
  focusProfile: number | null;
}) {
  const qc = useQueryClient();
  const kinds = useQuery(conditionKindsQuery);
  const [d, setD] = useState<RuleDraft>(() =>
    rule
      ? {
          name: rule.name,
          description: rule.description,
          includeInRename: rule.includeInRename,
          conditions: (rule.conditions ?? []).map((c) => ({ ...c })),
          scores: Object.fromEntries((rule.scores ?? []).map((s) => [s.profileId, s.score])),
        }
      : { name: '', description: '', includeInRename: false, conditions: [{ kind: 'hdr', value: '' }], scores: {} },
  );
  const valid = d.conditions.length > 0 && d.conditions.every((c) => !conditionError(c));
  const [matches, setMatches] = useState<{ count: number } | null>(null);
  const condKey = JSON.stringify(d.conditions);
  useEffect(() => {
    if (!valid) {
      setMatches(null);
      return;
    }
    const ctl = new AbortController();
    const t = setTimeout(() => {
      ruleApi
        .matches(d.conditions, ctl.signal)
        .then((r) => setMatches({ count: r.count }))
        .catch(() => {});
    }, 300);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [condKey, valid]);
  const save = useMutation({
    mutationFn: () => {
      const body = {
        name: d.name.trim(),
        description: d.description.trim() || undefined,
        includeInRename: d.includeInRename,
        conditions: d.conditions,
        scores: profiles.map((p) => ({ profileId: p.id, score: d.scores[p.id] ?? 0 })),
      };
      return rule ? ruleApi.update(rule.id, body) : ruleApi.create(body);
    },
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: scoringRulesQuery.queryKey });
      void qc.invalidateQueries({ queryKey: keys.profiles });
      toast.show({
        title: `${rule ? 'Saved' : 'Created'} ${r.rule.name}`,
        body: r.rescoredFiles ? `${formatNumber(r.rescoredFiles)} files of the library were rescored.` : 'No file of the library changed score.',
      });
      onClose();
    },
    onError: (e) => toast.error('The rule was not saved', errorText(e)),
  });
  const nameId = `rule-name-${rule?.id ?? 'new'}`;
  const headId = `rule-h-${rule?.id ?? 'new'}`;
  return (
    <div className="editor" role="group" aria-labelledby={headId}>
      <div className="eh">
        <b id={headId}>{rule ? rule.name : 'New rule'}</b>
        {matches && (
          <span className="t-caption t-muted" aria-live="polite">
            matches {formatNumber(matches.count)} {matches.count === 1 ? 'file' : 'files'} in your library
          </span>
        )}
        <IconButton label="Close the rule editor" size="sm" className="ml-auto" onClick={onClose}>
          <ChevronUp className="i i-sm" aria-hidden="true" />
        </IconButton>
      </div>
      <Field label="Name">
        {({ id, describedBy }) => (
          <Input id={id ?? nameId} aria-describedby={describedBy} value={d.name} onChange={(ev) => setD({ ...d, name: ev.target.value })} />
        )}
      </Field>
      {d.conditions.map((c, i) => (
        <ConditionRow
          // biome-ignore lint/suspicious/noArrayIndexKey: conditions have no id; order is their identity
          key={i}
          c={c}
          kinds={kinds.data ?? []}
          onChange={(nc) => setD({ ...d, conditions: d.conditions.map((x, k) => (k === i ? nc : x)) })}
          onRemove={() => setD({ ...d, conditions: d.conditions.filter((_, k) => k !== i) })}
        />
      ))}
      {rule?.readsAs && (
        <span className="t-caption t-muted">
          Reads as: <b className="t-secondary fw-500">{rule.readsAs}</b>
          {JSON.stringify(rule.conditions) !== condKey && ' (before your changes)'}.
        </span>
      )}
      <div className="per-profile">
        {profiles.map((p) => (
          <div className="field" key={p.id}>
            <label className="field-label" htmlFor={`pp-${rule?.id ?? 'n'}-${p.id}`}>
              {p.name}
            </label>
            <ScoreInput
              id={`pp-${rule?.id ?? 'n'}-${p.id}`}
              value={d.scores[p.id] ?? 0}
              className={p.id === focusProfile ? 'is-current' : undefined}
              onChange={(n) => setD({ ...d, scores: { ...d.scores, [p.id]: n } })}
            />
          </div>
        ))}
      </div>
      <div className="row">
        <Button
          variant="ghost"
          size="sm"
          icon={<Plus className="i" aria-hidden="true" />}
          onClick={() => setD({ ...d, conditions: [...d.conditions, { kind: 'release_group', value: '' }] })}
        >
          Condition
        </Button>
        <Checkbox
          checked={d.includeInRename}
          onCheckedChange={(c) => setD({ ...d, includeInRename: c })}
          label="Use in file names"
          labelClassName="ml-auto t-body-sm"
        />
        <Button size="sm" variant="secondary" disabled={!valid || !d.name.trim()} loading={save.isPending} onClick={() => save.mutate()}>
          {rule ? 'Save rule' : 'Create rule'}
        </Button>
      </div>
    </div>
  );
}

// ----------------------------------------------------------------- tester

function Tester({ profile }: { profile: QualityProfileDetail }) {
  const lib = useLibrary();
  const sample = useMemo(() => (lib.data ?? []).find((m) => m.quality && m.qualityProfileId === profile.id), [lib.data, profile.id]);
  const [title, setTitle] = useState('');
  const [res, setRes] = useState<Preview | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    const t0 = title.trim();
    if (!t0) {
      setRes(null);
      setErr(null);
      return;
    }
    const ctl = new AbortController();
    const t = setTimeout(() => {
      previewRelease({ title: t0, profileId: profile.id, movieId: sample?.id, protocol: 'usenet' }, ctl.signal)
        .then((r) => {
          setRes(r);
          setErr(null);
        })
        .catch((e) => {
          if ((e as Error).name !== 'AbortError') setErr(errorText(e));
        });
    }, 250);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [title, profile.id, sample?.id]);
  const p = res?.parsed;
  const parsed: Array<[string, string | number | undefined]> = p
    ? [
        ['title', p.title],
        ['year', p.year || undefined],
        ['quality', res.qualityName],
        ['hdr', p.hdr],
        ['audio', [p.audio, p.channels].filter(Boolean).join(' ') || undefined],
        ['codec', p.videoCodec],
        ['edition', p.edition],
        ['group', p.releaseGroup],
        ['parsed in', `${Math.round(res.parseMicros)} µs`],
      ]
    : [];
  const rules = res?.score.rules ?? [];
  const dec = res?.decision;
  const accepted = dec ? dec.accepted : !!res && res.qualityAllowed && res.score.total >= res.minScore;
  return (
    <section className="panel" aria-labelledby="th">
      <div className="panel-hd">
        <h2 id="th">
          <WandSparkles className="i" aria-hidden="true" />
          Test a release name
        </h2>
        <span className="t-caption t-muted">
          against {profile.name}
          {sample ? `, for ${sample.title}` : ''}
        </span>
      </div>
      <div className="panel-bd tester">
        <Input
          mono
          aria-label="Release name to test"
          placeholder="Film.Title.2024.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos-GROUP"
          value={title}
          onChange={(ev) => setTitle(ev.target.value)}
        />
        <div aria-live="polite" className="tester-out">
          {err && <span className="field-error">{err}</span>}
          {res && (
            <>
              <div className="parsed">
                {parsed
                  .filter(([, v]) => v !== undefined && v !== '')
                  .map(([k, v]) => (
                    <span key={k} className="tag plain">
                      <b>{k}</b>
                      {v}
                    </span>
                  ))}
              </div>
              <div className="tester-grid">
                <ul className="bd" aria-label="Score breakdown">
                  {rules.map((r) => (
                    <li key={r.rule}>
                      <span>{r.rule}</span>
                      <b className={r.score > 0 ? 'pos' : r.score < 0 ? 'neg' : 'zero'}>{formatScore(r.score)}</b>
                    </li>
                  ))}
                  {!rules.length && <li className="t-muted">No rule matched.</li>}
                  <li className="total">
                    <span>Total</span>
                    <b className={res.score.total > 0 ? 'pos' : res.score.total < 0 ? 'neg' : 'zero'}>{formatScore(res.score.total)}</b>
                  </li>
                </ul>
                <div className={cx('verdict', !accepted && 'no')}>
                  {accepted ? <CircleCheck className="i" aria-hidden="true" /> : <CircleX className="i" aria-hidden="true" />}
                  <span>
                    <b className="fw-600">
                      {accepted ? (dec?.upgrade ? 'Accepted, as an upgrade.' : 'Accepted.') : 'Rejected.'}
                    </b>{' '}
                    {dec?.upgrade?.reason ? `${dec.upgrade.reason} ` : ''}
                    {!res.qualityAllowed && `${res.qualityName || 'This quality'} is not allowed by ${res.profile}. `}
                    {res.score.total < res.minScore && `It scores below the minimum (${formatScore(res.minScore)}). `}
                    {(dec?.rejections ?? []).map((r) => r.message).join(' ')}
                    {accepted &&
                      (res.meetsCutoff
                        ? ' A file like this reaches the cutoff: upgrades would stop.'
                        : ` Below the cutoff (${res.cutoffLabel}, ${formatScore(res.cutoffScore)}), so upgrades continue.`)}
                  </span>
                </div>
              </div>
            </>
          )}
        </div>
      </div>
    </section>
  );
}

// ----------------------------------------------------------------- section

export default function QualitySection() {
  const qc = useQueryClient();
  const profiles = useQuery(profilesQuery);
  const catalog = useQuery(qualitiesQuery);
  const rules = useQuery(scoringRulesQuery);
  const [selected, setSelected] = useState<number | 'new' | null>(null);
  const [draft, setDraft] = useState<ProfileDraft | null>(null);
  const [openRule, setOpenRule] = useState<number | 'new' | null>(null);
  const [hiddenRules, setHiddenRules] = useState<ReadonlySet<number>>(new Set());
  const list = profiles.data ?? [];
  const names = useMemo(() => new Map((catalog.data ?? []).map((q) => [q.id, q.name])), [catalog.data]);
  const currentId = selected ?? list[0]?.id ?? null;
  const profile = currentId === 'new' ? null : (list.find((p) => p.id === currentId) ?? null);
  useEffect(() => {
    if (currentId === 'new') return;
    if (profile) setDraft(draftOf(profile));
  }, [profile, currentId]);
  const sizes = useMemo(() => {
    const m = new Map<string, { minMbPerMin: number; maxMbPerMin: number }>();
    for (const q of catalog.data ?? []) m.set(q.id, q.sizes);
    for (const s of profile?.sizes ?? []) m.set(s.quality, s);
    for (const [q, s] of Object.entries(draft?.sizes ?? {})) m.set(q, s);
    return m;
  }, [catalog.data, profile, draft?.sizes]);
  const dirty = !!draft && (currentId === 'new' || (profile && JSON.stringify(toInput(draft)) !== JSON.stringify(toInput(draftOf(profile)))));
  const problem = draft ? draftError(draft) : null;
  const save = useMutation({
    mutationFn: async () => {
      if (!draft) throw new Error('Nothing to save');
      return currentId === 'new' || !profile ? profileApi.create(toInput(draft)) : profileApi.update(profile.id, toInput(draft));
    },
    onSuccess: (r) => {
      qc.setQueryData(keys.profiles, (old: QualityProfileDetail[] | undefined) => {
        const rest = (old ?? []).filter((p) => p.id !== r.profile.id);
        return [...rest, r.profile].sort((a, b) => a.id - b.id);
      });
      void qc.invalidateQueries({ queryKey: scoringRulesQuery.queryKey });
      setSelected(r.profile.id);
      toast.show({
        title: `Saved ${r.profile.name}`,
        body: `${r.rescoredFiles ? `${formatNumber(r.rescoredFiles)} files rescored. ` : ''}Applies to ${formatNumber(r.profile.usage.movies)} ${r.profile.usage.movies === 1 ? 'film' : 'films'}.`,
      });
    },
    onError: (e) => toast.error('The profile was not saved', errorText(e)),
  });
  const removeProfile = () => {
    if (!profile) return;
    const p = profile;
    const others = list.filter((x) => x.id !== p.id);
    setSelected(others[0]?.id ?? null);
    qc.setQueryData(keys.profiles, others);
    toast.show({
      title: `Deleted ${p.name}`,
      body: 'No film used it.',
      undo: () => {
        qc.setQueryData(keys.profiles, list);
        setSelected(p.id);
      },
      onClose: (reason) => {
        if (reason === 'undo') return;
        profileApi
          .remove(p.id)
          .catch((e) => {
            qc.setQueryData(keys.profiles, list);
            toast.error(`${p.name} was not deleted`, errorText(e));
          })
          .finally(() => void qc.invalidateQueries({ queryKey: keys.profiles }));
      },
    });
  };
  const removeRule = (r: ScoringRule) => {
    setHiddenRules((h) => new Set(h).add(r.id));
    setOpenRule(null);
    const restore = () =>
      setHiddenRules((h) => {
        const n = new Set(h);
        n.delete(r.id);
        return n;
      });
    toast.show({
      title: `Deleted the rule ${r.name}`,
      body: 'Its scores go with it; files are rescored when the toast closes.',
      undo: restore,
      onClose: (reason) => {
        if (reason === 'undo') return;
        ruleApi
          .remove(r.id)
          .then(() => {
            void qc.invalidateQueries({ queryKey: scoringRulesQuery.queryKey });
            void qc.invalidateQueries({ queryKey: keys.profiles });
          })
          .catch((e) => {
            restore();
            toast.error(`${r.name} was not deleted`, errorText(e));
          });
      },
    });
  };
  if (profiles.isPending || catalog.isPending) return <Skeleton className="sk-wanted" />;
  if (profiles.isError)
    return (
      <EmptyState title="Profiles could not be loaded." headingLevel={2}>
        {errorText(profiles.error)}
      </EmptyState>
    );
  const shownRules = (rules.data ?? []).filter((r) => !hiddenRules.has(r.id));
  const name = draft?.name || 'New profile';
  return (
    <>
      <div className="profile-tabs" role="group" aria-label="Profiles">
        {list.map((p) => (
          <Chip
            key={p.id}
            pressed={p.id === currentId}
            onClick={() => {
              setSelected(p.id);
              setOpenRule(null);
            }}
          >
            {p.name} <span className="k">{formatNumber(p.usage.movies)}</span>
          </Chip>
        ))}
        {currentId === 'new' && <Chip pressed>New profile</Chip>}
        <Button
          variant="ghost"
          size="sm"
          icon={<Plus className="i" aria-hidden="true" />}
          onClick={() => {
            setSelected('new');
            setDraft(newDraft(catalog.data ?? [], 'New profile'));
          }}
        >
          New profile
        </Button>
        <span className="saved t-caption t-muted" aria-live="polite">
          {dirty ? (
            'Unsaved changes'
          ) : profile ? (
            <>
              <Check className="i i-xs" aria-hidden="true" /> Saved · applies to {formatNumber(profile.usage.movies)}{' '}
              {profile.usage.movies === 1 ? 'film' : 'films'}
            </>
          ) : null}
        </span>
      </div>
      {draft && (
        <div className="qgrid">
          <section className="panel" aria-labelledby="ph">
            <div className="panel-hd">
              <h2 id="ph">{name}</h2>
              <Switch checked={draft.upgradeAllowed} onCheckedChange={(v) => setDraft({ ...draft, upgradeAllowed: v })}>
                Upgrades
              </Switch>
            </div>
            <div className="panel-bd qbody">
              <Field label="Name">
                {({ id }) => <Input id={id} value={draft.name} onChange={(ev) => setDraft({ ...draft, name: ev.target.value })} />}
              </Field>
              <div>
                <div className="row ql-head">
                  <span className="field-label">
                    Allowed qualities <span className="t-muted fw-400">best first · drag or use the arrows to reorder</span>
                  </span>
                </div>
                <QualityList draft={draft} set={setDraft} names={names} sizes={sizes} />
              </div>
              {draft.upgradeAllowed && (
                <div className="field">
                  <span className="field-label" id="cut-l">
                    Cutoff · stop upgrading at
                  </span>
                  <Select
                    labelledBy="cut-l"
                    value={draft.cutoff}
                    options={draft.entries.filter((e) => e.allowed).map((e) => ({ value: entryKey(e), label: entryLabel(e, names) }))}
                    onValueChange={(v) => setDraft({ ...draft, cutoff: v })}
                  />
                </div>
              )}
              <div className="scores3">
                <div className="field">
                  <label className="field-label" htmlFor="s-min">
                    Minimum score
                  </label>
                  <ScoreInput id="s-min" value={draft.minScore} onChange={(n) => setDraft({ ...draft, minScore: n })} />
                </div>
                <div className="field">
                  <label className="field-label" htmlFor="s-cut">
                    Cutoff score
                  </label>
                  <ScoreInput id="s-cut" value={draft.cutoffScore} onChange={(n) => setDraft({ ...draft, cutoffScore: n })} />
                </div>
                <div className="field">
                  <label className="field-label" htmlFor="s-step">
                    Min. upgrade step
                  </label>
                  <ScoreInput
                    id="s-step"
                    value={draft.minUpgradeStep}
                    onChange={(n) => setDraft({ ...draft, minUpgradeStep: Math.max(0, n) })}
                  />
                </div>
              </div>
              <Sizes draft={draft} set={setDraft} profile={profile} names={names} />
              <p className="plain">
                <b>In plain words:</b> {plainWords(draft, names)}
              </p>
              {problem && dirty && (
                <span className="field-error" role="status">
                  {problem}
                </span>
              )}
              <div className="row">
                {profile && (
                  <Button
                    variant="ghost"
                    size="sm"
                    icon={<Trash2 className="i" aria-hidden="true" />}
                    disabled={profile.usage.movies > 0 || list.length < 2}
                    title={
                      profile.usage.movies > 0
                        ? 'Films use this profile: move them to another one first'
                        : list.length < 2
                          ? 'The last profile cannot be deleted'
                          : undefined
                    }
                    onClick={removeProfile}
                  >
                    Delete profile
                  </Button>
                )}
                <span className="ml-auto" />
                {dirty && (
                  <Button
                    variant="ghost"
                    onClick={() => {
                      if (profile) setDraft(draftOf(profile));
                      else setSelected(null);
                    }}
                  >
                    Discard
                  </Button>
                )}
                <Button variant="primary" disabled={!dirty || !!problem} loading={save.isPending} onClick={() => save.mutate()}>
                  Save profile
                </Button>
              </div>
            </div>
          </section>

          <div className="qside">
            <section className="panel" aria-labelledby="rh">
              <div className="panel-hd">
                <h2 id="rh">
                  Scoring rules <span className="t-muted t-caption fw-400">scores for {name}</span>
                </h2>
                <Button
                  variant="secondary"
                  size="sm"
                  icon={<Plus className="i" aria-hidden="true" />}
                  onClick={() => setOpenRule('new')}
                >
                  New rule
                </Button>
              </div>
              <div className="panel-bd rules-bd">
                <ul className="rules">
                  {openRule === 'new' && (
                    <li className="open">
                      <RuleEditor rule={null} profiles={list} onClose={() => setOpenRule(null)} focusProfile={profile?.id ?? null} />
                    </li>
                  )}
                  {shownRules.map((r) =>
                    openRule === r.id ? (
                      <li className="open" key={r.id}>
                        <RuleEditor rule={r} profiles={list} onClose={() => setOpenRule(null)} focusProfile={profile?.id ?? null} />
                        <Button
                          variant="ghost"
                          size="sm"
                          icon={<Trash2 className="i" aria-hidden="true" />}
                          onClick={() => removeRule(r)}
                          className="rule-del"
                        >
                          Delete rule
                        </Button>
                      </li>
                    ) : (
                      <li key={r.id}>
                        <span className="nm">
                          {r.name}
                          <small>{r.readsAs || r.description}</small>
                        </span>
                        <ScoreInput
                          label={`Score for ${r.name} in ${name}`}
                          className={cx('num', (draft.scores[r.id] ?? 0) <= -1000 && 'very-neg')}
                          value={draft.scores[r.id] ?? 0}
                          onChange={(n) => setDraft({ ...draft, scores: { ...draft.scores, [r.id]: n } })}
                        />
                        <IconButton label={`Edit ${r.name}`} size="sm" onClick={() => setOpenRule(r.id)}>
                          <ChevronDown className="i i-sm" aria-hidden="true" />
                        </IconButton>
                      </li>
                    ),
                  )}
                </ul>
                {!shownRules.length && openRule !== 'new' && (
                  <p className="t-secondary t-body-sm">
                    No rule yet. Rules add or remove points for what you care about: HDR, audio, groups, editions…
                  </p>
                )}
                {!!shownRules.length && dirty && (
                  <span className="t-caption t-muted">Scores are saved with the profile.</span>
                )}
              </div>
            </section>
            {profile && <Tester profile={profile} />}
          </div>
        </div>
      )}
    </>
  );
}
