import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  ArrowUpDown,
  Bookmark,
  ChevronsUpDown,
  CircleX,
  Download,
  Folder,
  Hand,
  Pencil,
  RefreshCw,
  Search,
  ShieldCheck,
  SlidersHorizontal,
} from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { ApiError } from '@/api/client';
import { qualitiesQuery } from '@/api/lot3';
import { keys, useLibrary, useProfiles } from '@/api/queries';
import {
  confirmImport,
  type LibraryFolder,
  type LibraryImport,
  type LibraryScan,
  startScan,
  thumbUrl,
  v11Keys,
} from '@/api/v11';
import { HdrFlag, QualityBadge } from '@/components/ui/Badges';
import { Button, buttonClass, IconButton } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { EmptyState } from '@/components/ui/Feedback';
import { Menu, MenuItem, MenuRadioGroup, MenuRadioItem } from '@/components/ui/Menu';
import { Popover } from '@/components/ui/Popover';
import { PosterArt } from '@/components/ui/PosterArt';
import { Segmented } from '@/components/ui/Segmented';
import { Select } from '@/components/ui/Select';
import { Switch } from '@/components/ui/Switch';
import { Tabs } from '@/components/ui/Tabs';
import { toast } from '@/components/ui/Toast';
import { formatBytes, formatNumber } from '@/lib/format';
import {
  belowCutoff,
  type Choices,
  importable,
  initialChoice,
  type Mode,
  type ReviewSort,
  type ReviewTab,
  type RowChoice,
  reviewRows,
  selectionSummary,
  tabCounts,
  toSelections,
} from './importModel';
import { Confidence, MatchPicker } from './MatchPicker';
import { FolderBadge } from './ScanStep';

const PAGE = 50;

/** Choices of the review (selection, match, profile, monitor, keep or move), kept while the tab is open. */
export function useChoices(folders: readonly LibraryFolder[]) {
  const profiles = useProfiles();
  const lib = useLibrary();
  // default profile: the one most films of the library use, else the first
  const defaultProfile = useMemo(() => {
    const n = new Map<number, number>();
    for (const m of lib.data ?? []) n.set(m.qualityProfileId, (n.get(m.qualityProfileId) ?? 0) + 1);
    const best = [...n.entries()].sort((a, b) => b[1] - a[1])[0]?.[0];
    return profiles.data?.some((p) => p.id === best) ? best! : (profiles.data?.[0]?.id ?? 0);
  }, [lib.data, profiles.data]);
  const [choices, setChoices] = useState<Choices>({});
  useEffect(() => {
    if (!defaultProfile) return;
    setChoices((prev) => {
      let next: Choices | null = null;
      for (const f of folders)
        if (!prev[f.folder]) {
          next ??= { ...prev };
          next[f.folder] = initialChoice(f, defaultProfile);
        }
      return next ?? prev;
    });
  }, [folders, defaultProfile]);
  const update = useCallback(
    (names: readonly string[], patch: Partial<RowChoice>) =>
      setChoices((prev) => {
        const next = { ...prev };
        for (const n of names) if (next[n]) next[n] = { ...next[n]!, ...patch };
        return next;
      }),
    [],
  );
  return { choices, update };
}

export type ChoicesState = ReturnType<typeof useChoices>;

export function ReviewStep({
  scan,
  folders,
  rootPath,
  choices: { choices, update },
  onBack,
  onScanAgain,
  onImported,
}: {
  scan: LibraryScan;
  folders: LibraryFolder[];
  rootPath: string;
  choices: ChoicesState;
  onBack: () => void;
  onScanAgain: (s: LibraryScan) => void;
  onImported: (r: LibraryImport, imported: string[], ms: number) => void;
}) {
  const qc = useQueryClient();
  const profiles = useProfiles();
  const catalog = useQuery(qualitiesQuery);
  const [tab, setTab] = useState<ReviewTab>('all');
  const [sort, setSort] = useState<ReviewSort>('look');
  const [shown, setShown] = useState(PAGE);
  const [busy, setBusy] = useState(false);
  const counts = tabCounts(folders);
  const rows = useMemo(() => reviewRows(folders, tab, sort), [folders, tab, sort]);
  const sum = selectionSummary(folders, choices);
  const selectable = rows.filter((f) => importable(f) && choices[f.folder]?.match);
  const allOn = selectable.length > 0 && selectable.every((f) => choices[f.folder]?.selected);
  const someOn = !allOn && selectable.some((f) => choices[f.folder]?.selected);
  const selectedNames = folders.filter((f) => importable(f) && choices[f.folder]?.selected).map((f) => f.folder);
  const profileName = (id: number) => profiles.data?.find((p) => p.id === id)?.name ?? '';
  const rootShown = rootPath.replace(/\/+$/, '') + (scan.path ? `/${scan.path}` : '');

  const scanAgain = async () => {
    try {
      onScanAgain(await startScan(scan.rootFolderId, scan.path));
    } catch (e) {
      toast.error('The scan did not start', e instanceof ApiError ? e.detail : undefined);
    }
  };
  const doImport = async () => {
    const selections = toSelections(folders, choices);
    if (!selections.length) return;
    setBusy(true);
    const t0 = performance.now();
    try {
      const r = await confirmImport(scan.rootFolderId, selections);
      const done = (r.results ?? [])
        .filter((x) => x.outcome !== 'failed' && x.outcome !== 'skipped')
        .map((x) => x.folder);
      void qc.invalidateQueries({ queryKey: keys.library });
      void qc.invalidateQueries({ queryKey: ['collections'] });
      void qc.invalidateQueries({ queryKey: ['collection'] });
      void qc.invalidateQueries({ queryKey: v11Keys.scans });
      onImported(r, done, performance.now() - t0);
    } catch (e) {
      toast.error('Nothing was imported', e instanceof ApiError ? e.detail : 'Gonema could not be reached.');
    } finally {
      setBusy(false);
    }
  };

  if (!folders.length)
    return (
      <EmptyState
        title={
          scan.status === 'failed'
            ? 'The scan stopped.'
            : scan.status === 'cancelled'
              ? 'The scan was stopped.'
              : 'Nothing to import here.'
        }
        action={
          <>
            <Button size="sm" icon={<RefreshCw className="i" aria-hidden="true" />} onClick={() => void scanAgain()}>
              Scan again
            </Button>
            <Button size="sm" variant="ghost" onClick={onBack}>
              Pick another folder
            </Button>
          </>
        }
      >
        {scan.status === 'failed'
          ? (scan.error ?? 'The scan failed.')
          : scan.status === 'cancelled'
            ? 'No folder was read before it stopped. Nothing was changed.'
            : `Every folder of ${rootShown} is already in your library, or holds no film.`}
      </EmptyState>
    );

  return (
    <div className="review">
      {scan.status === 'failed' && scan.error && (
        <p className="notice" data-tone="failed" role="alert">
          {scan.error} The folders read before it stopped are below.
        </p>
      )}
      <div className="rev-bar">
        <Tabs<ReviewTab>
          label="Show folders"
          panelId="rev-panel"
          className="tabs-plain"
          value={tab}
          onChange={(t) => {
            setTab(t);
            setShown(PAGE);
          }}
          items={[
            { value: 'all', label: 'All', count: formatNumber(counts.all) },
            { value: 'look', label: 'Needs a look', count: formatNumber(counts.look) },
            { value: 'new', label: 'New', count: formatNumber(counts.new) },
            { value: 'library', label: 'In library', count: formatNumber(counts.library) },
          ]}
        />
        <div className="right">
          <Menu
            label="Sort folders"
            align="end"
            trigger={
              <Button variant="ghost" size="sm" icon={<ArrowUpDown className="i" aria-hidden="true" />}>
                <span className="t-muted fw-500">Sort</span>{' '}
                <span className="fw-600">{sort === 'look' ? 'Needs a look first' : 'Folder name'}</span>
              </Button>
            }
          >
            <MenuRadioGroup<ReviewSort> value={sort} onValueChange={setSort}>
              <MenuRadioItem value="look" selected={sort === 'look'}>
                Needs a look first
              </MenuRadioItem>
              <MenuRadioItem value="name" selected={sort === 'name'}>
                Folder name
              </MenuRadioItem>
            </MenuRadioGroup>
          </Menu>
          <Button
            variant="ghost"
            size="sm"
            icon={<RefreshCw className="i" aria-hidden="true" />}
            onClick={() => void scanAgain()}
          >
            Scan again
          </Button>
        </div>
      </div>
      <div className="notice keep">
        <ShieldCheck className="i" aria-hidden="true" />
        <div className="nbody">
          <b>Nothing is deleted.</b> <i>Keep</i> leaves each file where it is. <i>Move</i> renames the file into a
          folder named by your template, inside <span className="t-mono-sm">{rootPath}</span>; the old folder is left in
          place.
        </div>
      </div>
      <div className="table-wrap" id="rev-panel" role="tabpanel" aria-labelledby={`tab-rev-panel-${tab}`}>
        <table className="tbl imp" aria-label={`Folders found in ${rootShown}`}>
          <colgroup>
            <col style={{ width: 44 }} />
            <col className="w-fold" />
            <col />
            <col style={{ width: 112 }} />
            <col className="w-profile" />
            <col className="w-mon" />
            <col className="w-files" />
            <col className="w-sum" />
          </colgroup>
          <thead>
            <tr>
              <th scope="col">
                <Checkbox
                  checked={allOn}
                  indeterminate={someOn}
                  disabled={!selectable.length}
                  aria-label="Select all importable folders"
                  onCheckedChange={(c) =>
                    update(
                      selectable.map((f) => f.folder),
                      { selected: c },
                    )
                  }
                />
              </th>
              <th scope="col">Folder on disk</th>
              <th scope="col">Match on TMDB</th>
              <th scope="col">Status</th>
              <th scope="col" className="c-profile">
                Profile
              </th>
              <th scope="col" className="c-mon">
                Monitor
              </th>
              <th scope="col" className="c-files">
                Files
              </th>
              <th scope="col" className="c-sum">
                On import
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.slice(0, shown).map((f) => (
              <Row
                key={f.folder}
                f={f}
                c={choices[f.folder]}
                rootFolderId={scan.rootFolderId}
                profiles={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))}
                low={belowCutoff(
                  f.quality,
                  profiles.data?.find((p) => p.id === choices[f.folder]?.profileId),
                  catalog.data ?? [],
                )}
                profileName={profileName}
                update={update}
              />
            ))}
          </tbody>
        </table>
        {rows.length > shown && (
          <div className="more-rows">
            <Button size="sm" variant="ghost" onClick={() => setShown((n) => n + PAGE)}>
              Show {formatNumber(Math.min(PAGE, rows.length - shown))} more of {formatNumber(rows.length - shown)}
            </Button>
          </div>
        )}
      </div>
      <div className="commit" role="toolbar" aria-label="Import the selected folders">
        <span className="cnt">
          {formatNumber(sum.count)} of {formatNumber(folders.length)} selected
          <small>
            {sum.bytes ? `${formatBytes(sum.bytes)} · ` : ''}
            {formatNumber(sum.kept)} kept in place · {formatNumber(sum.moved)} moved
          </small>
        </span>
        <span className="divider" aria-hidden="true" />
        <Menu
          label="Profile for the selected folders"
          side="top"
          trigger={
            <Button
              variant="ghost"
              size="sm"
              disabled={!selectedNames.length}
              icon={<SlidersHorizontal className="i" aria-hidden="true" />}
            >
              Profile
            </Button>
          }
        >
          {(profiles.data ?? []).map((p) => (
            <MenuItem key={p.id} onSelect={() => update(selectedNames, { profileId: p.id })}>
              {p.name}
            </MenuItem>
          ))}
        </Menu>
        <Menu
          label="Monitor the selected folders"
          side="top"
          trigger={
            <Button
              variant="ghost"
              size="sm"
              disabled={!selectedNames.length}
              icon={<Bookmark className="i" aria-hidden="true" />}
            >
              Monitor
            </Button>
          }
        >
          <MenuItem onSelect={() => update(selectedNames, { monitored: true })}>Monitor them</MenuItem>
          <MenuItem onSelect={() => update(selectedNames, { monitored: false })}>Don’t monitor them</MenuItem>
        </Menu>
        <Menu
          label="Keep or move the selected folders"
          side="top"
          trigger={
            <Button
              variant="ghost"
              size="sm"
              disabled={!selectedNames.length}
              icon={<Folder className="i" aria-hidden="true" />}
            >
              <span className="hide-tablet">Keep or move</span>
              <span className="only-tablet">Files</span>
            </Button>
          }
        >
          <MenuItem onSelect={() => update(selectedNames, { mode: 'keep' })}>Keep the files where they are</MenuItem>
          <MenuItem onSelect={() => update(selectedNames, { mode: 'move' })}>Move them into the naming</MenuItem>
        </Menu>
        <span className="say">
          <ShieldCheck className="i" aria-hidden="true" />
          Nothing is deleted
        </span>
        <Button
          variant="primary"
          icon={<Download className="i" aria-hidden="true" />}
          loading={busy}
          disabled={!sum.count || busy}
          onClick={() => void doImport()}
        >
          Import {formatNumber(sum.count)} {sum.count === 1 ? 'film' : 'films'}
        </Button>
      </div>
    </div>
  );
}

function Row({
  f,
  c,
  rootFolderId,
  profiles,
  low,
  profileName,
  update,
}: {
  f: LibraryFolder;
  c: RowChoice | undefined;
  rootFolderId: number;
  profiles: { value: number; label: string }[];
  low: boolean;
  profileName: (id: number) => string;
  update: ChoicesState['update'];
}) {
  const [open, setOpen] = useState(false);
  const canImport = importable(f);
  const m = c?.match ?? null;
  const on = !!c?.selected && !!m && canImport;
  const set = (patch: Partial<RowChoice>) => update([f.folder], patch);
  const title = m?.title ?? f.name;
  const why = !m
    ? (f.rejections?.[0] ?? 'No film title in these names')
    : f.status === 'in_library'
      ? f.libraryPath
        ? `Already at ${f.libraryPath}`
        : m.reason
      : m.reason;
  const picker = (
    <MatchPicker
      folder={f}
      rootFolderId={rootFolderId}
      current={m?.tmdbId ?? null}
      onPick={(cand) => {
        set({
          match: cand,
          picked: cand.tmdbId !== f.candidates?.[0]?.tmdbId || f.status !== 'new',
          selected: !cand.movieId,
        });
        setOpen(false);
      }}
      onSkip={() => {
        set({ match: null, selected: false, picked: false });
        setOpen(false);
      }}
    />
  );
  const trigger =
    f.status === 'in_library' ? null : !m ? (
      <Button size="sm" className="chg" icon={<Search className="i i-sm" aria-hidden="true" />} aria-haspopup="dialog">
        Search
      </Button>
    ) : f.status === 'ambiguous' && !c?.picked ? (
      <Button size="sm" className="chg" aria-haspopup="dialog" aria-label={`Change match for ${f.name}`}>
        Change
        <ChevronsUpDown className="i i-sm" aria-hidden="true" />
      </Button>
    ) : (
      <IconButton
        size="sm"
        className="chg icon"
        label={`Change match for ${f.name}`}
        title="Change match"
        aria-haspopup="dialog"
      >
        <ChevronsUpDown className="i i-sm" aria-hidden="true" />
      </IconButton>
    );
  return (
    <tr className={on ? undefined : 'off'}>
      <td>
        <Checkbox
          checked={on}
          disabled={!canImport || !m}
          aria-label={canImport ? `Import ${f.name}` : `Already in library: ${f.name}`}
          onCheckedChange={(v) => set({ selected: v })}
        />
      </td>
      <td>
        <div className="fold">
          <b title={f.folder}>{f.name}</b>
          <span className="ql" title={f.file ?? undefined}>
            {f.quality ? <QualityBadge quality={f.quality} /> : <span>No video found</span>}
            {(f.hdr ?? []).map((h) => (
              <HdrFlag key={h}>{h.toUpperCase()}</HdrFlag>
            ))}
            {f.size ? <span>{formatBytes(f.size)}</span> : null}
          </span>
          {on && c?.mode === 'move' && <span className="to">→ renamed with your naming templates</span>}
        </div>
      </td>
      <td className="c-match">
        <div className="mt">
          {m ? (
            <span className="th">
              <PosterArt src={thumbUrl(m.posterUrl)} title={m.title} />
            </span>
          ) : (
            <span className="th none" aria-hidden="true">
              <CircleX className="i" />
            </span>
          )}
          <span className="nm">
            <b>
              {m ? (
                <>
                  {m.title} {m.year ? <em>{m.year}</em> : null}
                </>
              ) : (
                'No film found'
              )}
            </b>
            <span className="why">
              {m && c?.picked ? (
                <span className="status picked">
                  <Hand className="i" aria-hidden="true" />
                  Picked by you
                </span>
              ) : m && f.status !== 'in_library' ? (
                <>
                  <Confidence value={m.confidence} />
                  <span>{why}</span>
                </>
              ) : (
                <span>{why}</span>
              )}
            </span>
          </span>
          {f.status === 'in_library' ? (
            f.movieId ? (
              <Link className={buttonClass('ghost', 'sm', 'chg')} to="/films/$movieId" params={{ movieId: f.movieId }}>
                Open
              </Link>
            ) : (
              <span />
            )
          ) : (
            <Popover
              label={`Change match for ${f.name}`}
              align="start"
              open={open}
              onOpenChange={setOpen}
              trigger={trigger!}
            >
              {open ? picker : null}
            </Popover>
          )}
        </div>
      </td>
      <td>
        <FolderBadge status={f.status} />
        {on && low && (
          <div className="low" title="Below the profile cutoff: Gonema will look for a better copy">
            Below profile
          </div>
        )}
      </td>
      <td className="c-profile">
        {on && c ? (
          <Select<number>
            ariaLabel={`Quality profile for ${title}`}
            value={c.profileId}
            options={profiles}
            onValueChange={(v) => set({ profileId: v })}
          />
        ) : (
          <span className="na">—</span>
        )}
      </td>
      <td className="c-mon">
        {on && c ? (
          <Switch
            checked={c.monitored}
            aria-label={`Monitor ${title}`}
            onCheckedChange={(v) => set({ monitored: v })}
          />
        ) : (
          <span className="na">—</span>
        )}
      </td>
      <td className="c-files">
        {on && c ? (
          <Segmented<Mode>
            label={`Files of ${title}`}
            value={c.mode}
            onChange={(v) => set({ mode: v })}
            options={[
              { value: 'keep', label: 'Keep' },
              { value: 'move', label: 'Move' },
            ]}
          />
        ) : (
          <span className="na">—</span>
        )}
      </td>
      <td className="c-sum">
        {on && c ? (
          <div className="sumrow">
            <div className="sum">
              <b>{profileName(c.profileId)}</b>
              <span>
                {c.monitored ? 'Monitored' : 'Not monitored'} · {c.mode === 'move' ? 'Move' : 'Keep'}
              </span>
            </div>
            <Popover
              label={`Import options for ${title}`}
              align="end"
              trigger={
                <IconButton size="sm" label={`Change import options for ${title}`}>
                  <Pencil className="i i-sm" aria-hidden="true" />
                </IconButton>
              }
            >
              <div className="sum-pop">
                <Select<number>
                  ariaLabel={`Quality profile for ${title}`}
                  value={c.profileId}
                  options={profiles}
                  onValueChange={(v) => set({ profileId: v })}
                />
                <Switch checked={c.monitored} onCheckedChange={(v) => set({ monitored: v })}>
                  Monitor
                </Switch>
                <Segmented<Mode>
                  label={`Files of ${title}`}
                  value={c.mode}
                  onChange={(v) => set({ mode: v })}
                  options={[
                    { value: 'keep', label: 'Keep' },
                    { value: 'move', label: 'Move' },
                  ]}
                />
              </div>
            </Popover>
          </div>
        ) : (
          <span className="na">—</span>
        )}
      </td>
    </tr>
  );
}
