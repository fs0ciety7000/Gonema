import { Bookmark, Check, Copy, Film, Plus, Search, SlidersHorizontal, Trash2, TriangleAlert, X } from 'lucide-react';
import { type ReactNode, useState } from 'react';
import type { LibraryItem } from '@/api/model';
import { PosterCard, PosterSkeleton } from '@/components/movie/PosterCard';
import { ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { HdrFlag, QualityBadge, ScorePill, StatusBadge, StatusText } from '@/components/ui/Badges';
import { Button, IconButton, Kbd } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Chip, FilterChip, Tag } from '@/components/ui/Chip';
import { Dialog } from '@/components/ui/Dialog';
import { EmptyState, Notice, Skeleton } from '@/components/ui/Feedback';
import { Field, Input } from '@/components/ui/Field';
import { Menu, MenuCheckboxItem, MenuItem, MenuSeparator } from '@/components/ui/Menu';
import { ProgressBar } from '@/components/ui/Progress';
import { Segmented } from '@/components/ui/Segmented';
import { Select } from '@/components/ui/Select';
import { Switch } from '@/components/ui/Switch';
import { Tabs } from '@/components/ui/Tabs';
import { toast } from '@/components/ui/Toast';
import { Tooltip } from '@/components/ui/Tooltip';
import { type DisplayStatus, STATUS } from '@/lib/status';
import { setThemePreference, useTheme } from '@/lib/theme';

/* Dev-only sheet (route /__system, compiled out of production): every component in
   every state, for side-by-side comparison with docs/design/system/index.html.
   Forced states use the design system's .is-hover / .is-active / .is-focus classes. */

function Sec({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section className="ds-sec" aria-labelledby={`h-${id}`}>
      <h2 id={`h-${id}`} className="ds-h">
        {title}
      </h2>
      {children}
    </section>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="ds-row">
      <span className="ds-label">{label}</span>
      <div className="ds-items">{children}</div>
    </div>
  );
}

const STATES = ['', 'is-hover', 'is-active', 'is-focus'] as const;
const STATE_NAME = { '': 'Default', 'is-hover': 'Hover', 'is-active': 'Pressed', 'is-focus': 'Focus' } as const;

const base: LibraryItem = {
  id: 1,
  tmdbId: 1,
  title: 'Sample Title',
  sortTitle: 'sample title',
  year: 2024,
  status: 'released',
  state: 'available',
  monitored: true,
  qualityProfileId: 1,
  minimumAvailability: 'released',
  rootFolderId: 1,
  collectionId: null,
  path: 'Sample Title (2024)',
  addedAt: 0,
  genres: [],
  posterUrl: null,
  dominantColor: '#B8643C',
  quality: 'webdl-2160p',
  size: 21_700_000_000,
  score: 70,
  hdr: ['DV'],
  progress: 0,
  failed: false,
  releaseDigital: null,
  releasePhysical: null,
  releaseCinema: null,
  tags: [],
};

const CARDS: Array<{
  label: string;
  status: DisplayStatus;
  patch: Partial<LibraryItem>;
  cls?: string;
  selected?: boolean;
}> = [
  { label: 'Available', status: 'available', patch: { dominantColor: '#3C7AB8' } },
  { label: 'Hover / focus', status: 'available', patch: { dominantColor: '#2E8B7A' }, cls: 'is-hover' },
  { label: 'Selected', status: 'available', patch: { dominantColor: '#8A5A8A' }, selected: true },
  { label: 'Missing', status: 'missing', patch: { state: 'missing', quality: null, dominantColor: '#7A3A6A' } },
  {
    label: 'Downloading',
    status: 'downloading',
    patch: { state: 'downloading', progress: 0.42, quality: null, dominantColor: '#1E3A5A' },
  },
  { label: 'Failed', status: 'failed', patch: { failed: true, quality: null, dominantColor: '#B83C3C' } },
  { label: 'Upgraded', status: 'upgraded', patch: { dominantColor: '#B8643C' } },
  {
    label: 'Not monitored',
    status: 'unmonitored',
    patch: { monitored: false, quality: 'hdtv-720p', dominantColor: '#3A3A4A' },
  },
];

const noop = () => {};

export default function SystemSheet() {
  const { theme } = useTheme();
  const [sw, setSw] = useState(true);
  const [cb, setCb] = useState(true);
  const [seg, setSeg] = useState<'airy' | 'compact'>('airy');
  const [tab, setTab] = useState<'all' | 'uhd' | 'wanted'>('all');
  const [sel, setSel] = useState<number>(1);
  const [dialog, setDialog] = useState(false);
  const [menuCheck, setMenuCheck] = useState(true);
  return (
    <>
      <Topbar>
        <strong>Design system · implementation sheet</strong>
        <div className="spacer" />
        <Segmented
          label="Theme"
          value={theme}
          onChange={(t) => setThemePreference(t)}
          options={[
            { value: 'dark', label: 'Dark' },
            { value: 'light', label: 'Light' },
          ]}
        />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page ds-page" tabIndex={-1}>
        <div className="page-head">
          <div>
            <h1>System</h1>
            <p className="sub">Development build only · compare with docs/design/system/index.html</p>
          </div>
        </div>

        <Sec id="buttons" title="Buttons">
          {(['primary', 'secondary', 'ghost', 'danger'] as const).map((v) => (
            <Row key={v} label={v}>
              {STATES.map((s) => (
                <Button key={s} variant={v} className={s || undefined} icon={<Plus className="i" aria-hidden="true" />}>
                  {STATE_NAME[s]}
                </Button>
              ))}
              <Button variant={v} disabled>
                Disabled
              </Button>
              <Button variant={v} loading>
                Loading
              </Button>
            </Row>
          ))}
          <Row label="tint (film page)">
            <div className="movie-scope ds-inline" style={{ ['--movie-dominant' as string]: '#B8643C' }}>
              <Button variant="tint" size="lg" icon={<Search className="i" aria-hidden="true" />}>
                Search now
              </Button>
              <Button variant="tint" className="is-hover">
                Hover
              </Button>
              <Button variant="danger-solid">Delete files</Button>
            </div>
          </Row>
          <Row label="sizes">
            <Button size="sm">Small 28</Button>
            <Button>Medium 36</Button>
            <Button size="lg">Large 44</Button>
            <Button variant="ghost" size="sm">
              Filters <Kbd>F</Kbd>
            </Button>
          </Row>
        </Sec>

        <Sec id="icon-buttons" title="Icon buttons">
          <Row label="ghost / outlined">
            {STATES.map((s) => (
              <IconButton key={s} label={`Search, ${STATE_NAME[s]}`} className={s || undefined}>
                <Search className="i" aria-hidden="true" />
              </IconButton>
            ))}
            <IconButton label="Pressed toggle" aria-pressed="true">
              <Bookmark className="i" aria-hidden="true" />
            </IconButton>
            <IconButton label="Disabled" disabled>
              <Copy className="i" aria-hidden="true" />
            </IconButton>
            <IconButton label="Outlined" variant="outlined">
              <SlidersHorizontal className="i" aria-hidden="true" />
            </IconButton>
            <IconButton label="With dot" dot>
              <TriangleAlert className="i" aria-hidden="true" />
            </IconButton>
            <div className="ds-media">
              <IconButton label="On media" variant="on-media">
                <Search className="i i-sm" aria-hidden="true" />
              </IconButton>
            </div>
          </Row>
        </Sec>

        <Sec id="inputs" title="Inputs and select">
          <div className="ds-grid">
            <Field label="Search">
              {({ id }) => (
                <Input id={id} icon={<Search className="i" aria-hidden="true" />} placeholder="Search films…" />
              )}
            </Field>
            <Field label="Port" hint="The port the client listens on">
              {({ id, describedBy }) => (
                <Input id={id} aria-describedby={describedBy} defaultValue="8080" suffix="TCP" mono />
              )}
            </Field>
            <Field label="Host" error="This host did not answer: check the address and that the client runs.">
              {({ id, describedBy, invalid }) => (
                <Input id={id} aria-describedby={describedBy} invalid={invalid} defaultValue="nas.locl" />
              )}
            </Field>
            <Field label="Disabled">{({ id }) => <Input id={id} disabled defaultValue="Read only" />}</Field>
            <div className="field">
              <span className="field-label" id="ds-sel">
                Quality profile
              </span>
              <Select
                labelledBy="ds-sel"
                value={sel}
                onValueChange={setSel}
                options={[
                  { value: 1, label: 'Cinema UHD' },
                  { value: 2, label: 'HD 1080p' },
                  { value: 3, label: 'Any' },
                ]}
              />
            </div>
          </div>
        </Sec>

        <Sec id="toggles" title="Toggle, checkbox, segmented, tabs">
          <Row label="switch">
            <Switch checked={sw} onCheckedChange={setSw}>
              Monitored
            </Switch>
            <Switch checked={false} onCheckedChange={noop} className="is-hover">
              Hover
            </Switch>
            <Switch checked onCheckedChange={noop} className="is-focus">
              Focus
            </Switch>
            <Switch checked={false} onCheckedChange={noop} disabled>
              Disabled
            </Switch>
          </Row>
          <Row label="checkbox">
            <Checkbox checked={cb} onCheckedChange={setCb} label="Checked" />
            <Checkbox checked={false} label="Unchecked" onCheckedChange={noop} />
            <Checkbox checked={false} indeterminate label="Indeterminate" onCheckedChange={noop} />
            <Checkbox checked={false} label="Hover" labelClassName="is-hover" onCheckedChange={noop} />
            <Checkbox checked disabled label="Disabled" onCheckedChange={noop} />
          </Row>
          <Row label="segmented">
            <Segmented
              label="Density"
              value={seg}
              onChange={setSeg}
              options={[
                { value: 'airy', label: 'Airy' },
                { value: 'compact', label: 'Compact' },
              ]}
            />
          </Row>
          <Row label="tabs">
            <Tabs
              label="Example views"
              panelId="ds-panel"
              value={tab}
              onChange={setTab}
              items={[
                { value: 'all', label: 'All films', count: '1,284' },
                { value: 'uhd', label: '4K', count: '312' },
                { value: 'wanted', label: 'Wanted', count: '23' },
              ]}
            />
            <span id="ds-panel" className="sr-only">
              Panel
            </span>
          </Row>
        </Sec>

        <Sec id="chips" title="Chips and tags">
          <Row label="chips">
            <Chip>Default</Chip>
            <Chip className="is-hover">Hover</Chip>
            <Chip pressed>Pressed</Chip>
            <Chip disabled>Disabled</Chip>
            <FilterChip k="Year" value="2012–2026" onRemove={noop} />
            <FilterChip k="Monitored" value="Yes" onRemove={noop} />
          </Row>
          <Row label="tags">
            <Tag>sci-fi</Tag>
            <Tag>4k-hdr</Tag>
            <Tag plain>Usenet</Tag>
          </Row>
        </Sec>

        <Sec id="data" title="Data display">
          <Row label="status badge">
            {(Object.keys(STATUS) as DisplayStatus[]).map((s) => (
              <StatusBadge key={s} status={s} />
            ))}
          </Row>
          <Row label="on artwork">
            <div className="ds-media ds-media-wide">
              {(['missing', 'failed', 'unmonitored', 'downloading'] as DisplayStatus[]).map((s) => (
                <StatusBadge key={s} status={s} onMedia />
              ))}
            </div>
          </Row>
          <Row label="inline status">
            {(Object.keys(STATUS) as DisplayStatus[]).map((s) => (
              <StatusText key={s} status={s} />
            ))}
          </Row>
          <Row label="quality / HDR / score">
            <QualityBadge quality="remux-2160p" />
            <QualityBadge quality="bluray-1080p" />
            <QualityBadge quality="webdl-720p" />
            <HdrFlag>DV</HdrFlag>
            <HdrFlag>HDR10+</HdrFlag>
            <ScorePill score={70} />
            <ScorePill score={-15} />
            <ScorePill score={0} />
            <ScorePill score={40} onClick={noop} />
          </Row>
          <Row label="progress">
            <div className="ds-progress">
              <ProgressBar value={0.42} live label="Downloading 42%" />
              <ProgressBar value={0.61} state="paused" label="Paused 61%" />
              <ProgressBar value={0.18} state="failed" label="Failed at 18%" />
            </div>
          </Row>
          <Row label="tooltip">
            <span className="tooltip static">
              Search now <span className="kbd">S</span>
            </span>
            <Tooltip content="Real tooltip (hover me)" shortcut="S">
              <Button size="sm">Hover for tooltip</Button>
            </Tooltip>
          </Row>
          <Row label="skeleton">
            <div className="ds-skeleton">
              <Skeleton className="sk-line" style={{ width: 220 }} />
              <Skeleton className="sk-line" style={{ width: 140, marginTop: 8 }} delay={60} />
            </div>
          </Row>
        </Sec>

        <Sec id="overlays" title="Menus, dialogs, toasts">
          <Row label="menu (static)">
            <div className="menu ds-static" role="menu" aria-label="Example menu">
              <div className="menu-heading">Orbit of Small Things</div>
              <button type="button" role="menuitem" tabIndex={-1}>
                <Film className="i" aria-hidden="true" />
                <span className="label">Open</span>
                <span className="kbd-hint">↵</span>
              </button>
              <button type="button" role="menuitem" className="is-hover" tabIndex={-1}>
                <Search className="i" aria-hidden="true" />
                <span className="label">Search now</span>
                <span className="kbd-hint">S</span>
              </button>
              <button type="button" role="menuitemcheckbox" aria-checked="true" tabIndex={-1}>
                <Bookmark className="i" aria-hidden="true" />
                <span className="label">Monitored</span>
                <span className="check-mark">
                  <Check className="i i-sm" aria-hidden="true" />
                </span>
              </button>
              <button type="button" role="menuitem" aria-disabled="true" tabIndex={-1}>
                <Copy className="i" aria-hidden="true" />
                <span className="label">Refresh metadata</span>
                <span className="kbd-hint">Running</span>
              </button>
              <hr className="ds-sep" />
              <button type="button" role="menuitem" className="danger" tabIndex={-1}>
                <Trash2 className="i" aria-hidden="true" />
                <span className="label">Remove from library</span>
                <span className="kbd-hint">⌫</span>
              </button>
            </div>
            <Menu label="Live menu" heading="Live menu" trigger={<Button>Open a real menu</Button>}>
              <MenuItem icon={<Film className="i" aria-hidden="true" />} hint="↵">
                Open
              </MenuItem>
              <MenuCheckboxItem
                icon={<Bookmark className="i" aria-hidden="true" />}
                checked={menuCheck}
                onCheckedChange={setMenuCheck}
              >
                Monitored
              </MenuCheckboxItem>
              <MenuItem disabled icon={<Copy className="i" aria-hidden="true" />}>
                Disabled
              </MenuItem>
              <MenuSeparator />
              <MenuItem danger icon={<Trash2 className="i" aria-hidden="true" />}>
                Remove
              </MenuItem>
            </Menu>
          </Row>
          <Row label="dialog / toast">
            <Button variant="danger" onClick={() => setDialog(true)} icon={<Trash2 className="i" aria-hidden="true" />}>
              Delete files…
            </Button>
            <Button
              onClick={() =>
                toast.show({
                  title: 'Stopped monitoring Halcyon Drift',
                  body: 'Gonema will not search for it until you monitor it again.',
                  icon: <Bookmark className="i" />,
                  undo: noop,
                })
              }
            >
              Show an Undo toast
            </Button>
            <Button onClick={() => toast.error('Nothing was changed', 'Gonema could not be reached.')}>
              Show an error toast
            </Button>
            <Dialog
              open={dialog}
              onOpenChange={setDialog}
              icon={<Trash2 className="i i-lg" aria-hidden="true" />}
              title="Delete 2 films and their files?"
              description="The files go to the recycle bin for 7 days, then they are gone. The films leave the library."
              actions={
                <>
                  <Button variant="ghost" onClick={() => setDialog(false)}>
                    Cancel
                  </Button>
                  <Button variant="danger-solid" onClick={() => setDialog(false)}>
                    Delete 2 films
                  </Button>
                </>
              }
            />
          </Row>
        </Sec>

        <Sec id="states" title="Empty state and inline notice">
          <div className="ds-grid ds-grid-2">
            <EmptyState title="The house is dark." action={<Button variant="primary">Add a film</Button>}>
              Add a film and we’ll start the screening.
            </EmptyState>
            <div className="stack">
              <Notice
                tone="failed"
                icon={<TriangleAlert className="i" aria-hidden="true" />}
                title="The print arrived damaged."
                action={<Button size="sm">Retry</Button>}
              >
                Blocklisted it, trying the next release.
              </Notice>
              <Notice
                tone="missing"
                icon={<TriangleAlert className="i" aria-hidden="true" />}
                title="Disk almost full."
              >
                12 GB left on /data/movies. Imports pause below 5 GB.
              </Notice>
              <Notice tone="info" icon={<X className="i" aria-hidden="true" />} title="Informational notice.">
                Calm, one sentence, one action.
              </Notice>
            </div>
          </div>
        </Sec>

        <Sec id="posters" title="Poster card (9 states)">
          <div className="poster-grid ds-posters">
            {CARDS.map((c, i) => {
              const m = { ...base, ...c.patch, id: 100 + i, title: c.label };
              return (
                <PosterCard
                  key={c.label}
                  movie={m}
                  status={c.status}
                  href="#posters"
                  selected={!!c.selected}
                  active={false}
                  live={
                    c.status === 'downloading' ? { progress: 0.42, speedBps: 18_400_000, etaSeconds: 720 } : undefined
                  }
                  className={c.cls}
                  onLinkClick={(ev) => ev.preventDefault()}
                  onSelectClick={noop}
                  onSearch={noop}
                  onToggleMonitor={noop}
                  onFocusCard={noop}
                />
              );
            })}
            <PosterSkeleton />
          </div>
        </Sec>
      </main>
    </>
  );
}
