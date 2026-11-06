import {
  ArrowDownToLine,
  Ban,
  ChevronDown,
  CircleArrowUp,
  Disc3,
  FileVideo,
  FolderOpen,
  Info,
  MonitorPlay,
  Pencil,
  Plus,
  RefreshCw,
  Ticket,
  Trash2,
  TriangleAlert,
  X,
} from 'lucide-react';
import { type ReactNode, useEffect, useRef, useState } from 'react';
import type { HistoryEntry } from '@/api/schema';
import { HdrFlag, QualityBadge, ScorePill } from '@/components/ui/Badges';
import { Button } from '@/components/ui/Button';
import { Tag } from '@/components/ui/Chip';
import { Dialog } from '@/components/ui/Dialog';
import { PosterArt } from '@/components/ui/PosterArt';
import { Segmented } from '@/components/ui/Segmented';
import { formatBytes, formatDate, formatVotes, formatWhen } from '@/lib/format';
import { qualityName } from '@/lib/quality';
import type { DisplayStatus } from '@/lib/status';

// ----------------------------------------------------------------- ratings

type Rating = { value: number; votes: number } | null | undefined;

export function RatingRing({ source, rating }: { source: string; rating: Rating }) {
  if (!rating || !rating.votes) return null;
  const v = Math.max(0, Math.min(1, rating.value / 10));
  return (
    <div className="rating">
      <div className="ring" style={{ ['--v' as string]: v }} aria-hidden="true">
        <b>{Math.round(rating.value * 10)}</b>
      </div>
      <div className="src">
        <strong>
          {source} {rating.value.toFixed(1)}
        </strong>
        <span>
          <span className="sr-only">out of 10, </span>
          {formatVotes(rating.votes)} votes
        </span>
      </div>
    </div>
  );
}

// ----------------------------------------------------------------- hero poster

/** Shows the cached grid poster at once (same file as the grid: no flash during the
 *  shared-element transition), then swaps to the large one once it has loaded. */
export function HeroPoster({
  id,
  small,
  large,
  title,
  color,
  onReady,
}: {
  id: number;
  small: string | null;
  large: string | null;
  title: string;
  color: string | null;
  onReady?: (el: HTMLDivElement) => void;
}) {
  const [src, setSrc] = useState(small ?? large);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    setSrc(small ?? large);
    if (!large || large === small) return;
    const img = new Image();
    img.decoding = 'async';
    img.src = large;
    let alive = true;
    img
      .decode()
      .then(() => alive && setSrc(large))
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [small, large]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: once per film
  useEffect(() => {
    if (ref.current) onReady?.(ref.current);
  }, [id]);
  return (
    <div ref={ref} className="hero-poster" data-hero-poster={id} style={{ viewTransitionName: `poster-${id}` }}>
      <PosterArt src={src} title={title} color={color} eager />
    </div>
  );
}

// ----------------------------------------------------------------- trailer

/** YouTube trailer in a dialog, privacy-enhanced domain, loaded only when opened. */
export function TrailerDialog({
  open,
  onOpenChange,
  youtubeKey,
  title,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  youtubeKey: string;
  title: string;
}) {
  const safe = /^[\w-]{6,20}$/.test(youtubeKey) ? youtubeKey : null;
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={`${title} · trailer`} wide className="trailer-dialog">
      {safe ? (
        <div className="trailer-frame">
          <iframe
            src={`https://www.youtube-nocookie.com/embed/${safe}?autoplay=1&rel=0`}
            title={`${title} trailer`}
            allow="autoplay; encrypted-media; picture-in-picture; fullscreen"
            allowFullScreen
            referrerPolicy="strict-origin-when-cross-origin"
          />
        </div>
      ) : (
        <p>This trailer link looks malformed, so it was not loaded.</p>
      )}
      <div className="actions">
        <Button variant="secondary" onClick={() => onOpenChange(false)} icon={<X className="i" aria-hidden="true" />}>
          Close
        </Button>
      </div>
    </Dialog>
  );
}

// ----------------------------------------------------------------- cast

export type PersonView = {
  tmdbId: number;
  name: string;
  character?: string;
  job?: string;
  department?: string;
  profileUrl: string | null;
};

export function Portrait({ p }: { p: PersonView }) {
  const initials = p.name
    .split(/\s+/)
    .map((w) => w[0])
    .slice(0, 2)
    .join('');
  return (
    <div className="ph">
      {p.profileUrl ? (
        <img src={p.profileUrl} alt="" loading="lazy" decoding="async" />
      ) : (
        <span className="ph-initials">{initials}</span>
      )}
    </div>
  );
}

export function CastDialog({
  open,
  onOpenChange,
  cast,
  crew,
  title,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  cast: PersonView[];
  crew: PersonView[];
  title: string;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={`${title} · cast & crew`} wide>
      <div className="cast-list">
        <section aria-labelledby="cast-all-h">
          <h3 id="cast-all-h" className="t-overline">
            Cast
          </h3>
          <ul>
            {cast.map((p) => (
              <li key={`${p.tmdbId}-${p.character}`}>
                <b>{p.name}</b>
                <span>{p.character}</span>
              </li>
            ))}
          </ul>
        </section>
        {crew.length > 0 && (
          <section aria-labelledby="crew-all-h">
            <h3 id="crew-all-h" className="t-overline">
              Crew
            </h3>
            <ul>
              {crew.map((p) => (
                <li key={`${p.tmdbId}-${p.job}`}>
                  <b>{p.name}</b>
                  <span>{p.job}</span>
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
      <div className="actions">
        <Button variant="secondary" onClick={() => onOpenChange(false)}>
          Close
        </Button>
      </div>
    </Dialog>
  );
}

// ----------------------------------------------------------------- history

const EVENT: Record<
  string,
  { label: string; status?: DisplayStatus; icon: typeof Info; kind: 'grab' | 'import' | 'failure' | 'other' }
> = {
  grabbed: { label: 'Grabbed', status: 'downloading', icon: ArrowDownToLine, kind: 'grab' },
  grab_overridden: { label: 'Grabbed on request', status: 'downloading', icon: ArrowDownToLine, kind: 'grab' },
  grab_failed: { label: 'Grab failed', status: 'failed', icon: TriangleAlert, kind: 'failure' },
  download_failed: { label: 'Download failed', status: 'failed', icon: TriangleAlert, kind: 'failure' },
  imported: { label: 'Imported', status: 'available', icon: FolderOpen, kind: 'import' },
  upgraded: { label: 'Upgraded', status: 'upgraded', icon: CircleArrowUp, kind: 'import' },
  import_failed: { label: 'Import failed', status: 'failed', icon: TriangleAlert, kind: 'failure' },
  file_deleted: { label: 'File deleted', icon: Trash2, kind: 'other' },
  file_renamed: { label: 'Renamed', icon: Pencil, kind: 'other' },
  blocklisted: { label: 'Blocklisted', status: 'failed', icon: Ban, kind: 'failure' },
  ignored: { label: 'Ignored', icon: Ban, kind: 'other' },
  metadata_changed: { label: 'Details updated', icon: RefreshCw, kind: 'other' },
  movie_added: { label: 'Added to the library', icon: Plus, kind: 'other' },
  movie_deleted: { label: 'Removed from the library', icon: Trash2, kind: 'other' },
};

type HistoryData = {
  reason?: unknown;
  upgradeReason?: unknown;
  client?: unknown;
  method?: unknown;
  size?: unknown;
  path?: unknown;
  previous?: { quality?: unknown } | null;
  indexer?: unknown;
};

const str = (v: unknown) => (typeof v === 'string' && v ? v : null);

type Filter = 'all' | 'grab' | 'import' | 'failure';

export function HistoryTimeline({
  entries,
  canLoadMore,
  onLoadMore,
  loadingMore,
}: {
  entries: readonly HistoryEntry[];
  canLoadMore: boolean;
  onLoadMore: () => void;
  loadingMore: boolean;
}) {
  const [filter, setFilter] = useState<Filter>('all');
  const shown = entries.filter((e) => filter === 'all' || (EVENT[e.event]?.kind ?? 'other') === filter);
  return (
    <section aria-labelledby="hist-h">
      <div className="sec-hd">
        <h2 id="hist-h">History</h2>
        <Segmented
          label="History filter"
          value={filter}
          onChange={setFilter}
          options={[
            { value: 'all', label: 'All' },
            { value: 'grab', label: 'Grabs' },
            { value: 'import', label: 'Imports' },
            { value: 'failure', label: 'Failures' },
          ]}
        />
      </div>
      {shown.length === 0 ? (
        <p className="t-secondary history-empty">
          {entries.length === 0
            ? 'No reels logged yet. Every grab, import, upgrade and failure is written here, with the reason behind it.'
            : 'Nothing of this kind yet.'}
        </p>
      ) : (
        <ol className="timeline">
          {shown.map((e) => {
            const meta = EVENT[e.event] ?? { label: e.event.replace(/_/g, ' '), icon: Info, kind: 'other' as const };
            const Icon = meta.icon;
            const d = (e.data ?? {}) as HistoryData;
            const reason = str(d.reason);
            const prevQ = str(d.previous?.quality);
            const q = e.quality ? qualityName(e.quality) : null;
            const size = typeof d.size === 'number' ? formatBytes(d.size) : null;
            const facts: ReactNode[] = [];
            if (e.customScore !== null && e.customScore !== undefined)
              facts.push(<ScorePill key="s" score={e.customScore} />);
            if (str(d.client)) facts.push(<span key="c">{str(d.client)}</span>);
            if (str(d.method))
              facts.push(
                <Tag key="m" plain>
                  {str(d.method)}
                </Tag>,
              );
            return (
              <li key={e.id} className="ev" data-status={meta.status}>
                <span className="dot" aria-hidden="true">
                  <Icon className="i" />
                </span>
                <div className="what">
                  <div className="head">
                    <strong>{meta.label}</strong>{' '}
                    {e.event === 'upgraded' && prevQ ? (
                      <>
                        <span className="t-muted">{qualityName(prevQ)}</span>{' '}
                        <span className="arrow" aria-hidden="true">
                          →
                        </span>
                        <span className="sr-only">to</span>
                        {q}
                      </>
                    ) : (
                      [q, size].filter(Boolean).join(', ')
                    )}
                  </div>
                  {reason && (
                    <div className="reason">
                      <Info className="i" aria-hidden="true" />
                      <span>
                        {meta.status === 'failed' ? '' : 'Why: '}
                        {reason}
                      </span>
                    </div>
                  )}
                  {(e.sourceTitle || facts.length > 0) && (
                    <div className="facts">
                      {facts}
                      {e.sourceTitle && e.sourceTitle !== q && (
                        <span className="rel" title={e.sourceTitle}>
                          {e.sourceTitle}
                        </span>
                      )}
                    </div>
                  )}
                </div>
                <time className="when" dateTime={e.createdAt}>
                  {formatWhen(e.createdAt)}
                </time>
              </li>
            );
          })}
        </ol>
      )}
      {canLoadMore && (
        <Button
          variant="ghost"
          size="sm"
          className="more-ev"
          loading={loadingMore}
          onClick={onLoadMore}
          icon={<ChevronDown className="i" aria-hidden="true" />}
        >
          Earlier events
        </Button>
      )}
    </section>
  );
}

// ----------------------------------------------------------------- file

type FileView = {
  relativePath: string;
  fullPath: string;
  size: number;
  quality: string;
  revision: number;
  edition: string;
  languages: string[] | null;
  releaseGroup: string;
  customScore: number;
  importedAt: string;
  media?: {
    videoCodec?: string;
    bitDepth?: number;
    hdr?: string[] | null;
    dvProfile?: number;
    audio?: string[] | null;
    atmos?: boolean;
    channels?: string;
  } | null;
};

export function FilePanel({ file, runtimeMin }: { file: FileView; runtimeMin: number }) {
  const m = file.media ?? {};
  const name = file.relativePath.split('/').pop() ?? file.relativePath;
  const perMin = runtimeMin > 0 ? formatBytes(file.size / runtimeMin) : null;
  const video = [m.videoCodec, m.bitDepth ? `${m.bitDepth}-bit` : null].filter(Boolean).join(' · ');
  const hdr = [...(m.hdr ?? [])];
  const audio = [...(m.audio ?? []), m.atmos ? 'Atmos' : null, m.channels].filter(Boolean).join(' ');
  return (
    <section className="panel" aria-labelledby="file-h">
      <div className="panel-hd">
        <h2 id="file-h">
          <FileVideo className="i" aria-hidden="true" />
          File
        </h2>
        <span className="saved">Imported {formatDate(file.importedAt)}</span>
      </div>
      <div className="panel-bd">
        <div className="file-name" title={file.fullPath}>
          {name}
        </div>
        <dl className="spec">
          <div className="spec-row">
            <dt>Quality</dt>
            <dd>
              <QualityBadge quality={file.quality} />
              {file.revision > 1 && <span className="t-muted">rev {file.revision}</span>}
            </dd>
          </div>
          <div className="spec-row">
            <dt>Size</dt>
            <dd>
              {formatBytes(file.size)}
              {perMin && <span className="t-muted">· {perMin}/min</span>}
            </dd>
          </div>
          {file.edition && (
            <div className="spec-row">
              <dt>Edition</dt>
              <dd>{file.edition}</dd>
            </div>
          )}
          {video && (
            <div className="spec-row">
              <dt>Video</dt>
              <dd>{video}</dd>
            </div>
          )}
          {hdr.length > 0 && (
            <div className="spec-row">
              <dt>HDR</dt>
              <dd>
                {hdr.map((h) => (
                  <HdrFlag key={h}>{h === 'DV' && m.dvProfile ? `DV P${m.dvProfile}` : h}</HdrFlag>
                ))}
              </dd>
            </div>
          )}
          {audio && (
            <div className="spec-row">
              <dt>Audio</dt>
              <dd>{audio}</dd>
            </div>
          )}
          {file.languages && file.languages.length > 0 && (
            <div className="spec-row">
              <dt>Languages</dt>
              <dd>{file.languages.map((l) => l.toUpperCase()).join(', ')}</dd>
            </div>
          )}
          {file.releaseGroup && (
            <div className="spec-row">
              <dt>Group</dt>
              <dd>{file.releaseGroup}</dd>
            </div>
          )}
          <div className="spec-row">
            <dt>Score</dt>
            <dd>
              <ScorePill score={file.customScore} />
            </dd>
          </div>
        </dl>
      </div>
    </section>
  );
}

// ----------------------------------------------------------------- release dates

export function ReleaseDates({
  cinema,
  digital,
  physical,
}: {
  cinema: string | null;
  digital: string | null;
  physical: string | null;
}) {
  const rows = [
    { icon: Ticket, label: 'In cinemas', kind: 'Theatrical', d: cinema },
    { icon: MonitorPlay, label: 'Digital', kind: 'Streaming & rental', d: digital },
    { icon: Disc3, label: 'Physical', kind: 'Disc release', d: physical },
  ].filter((r) => r.d);
  if (!rows.length) return null;
  return (
    <section className="panel" aria-labelledby="dates-h">
      <div className="panel-hd">
        <h2 id="dates-h">Release dates</h2>
      </div>
      <div className="panel-bd">
        <ul className="dates">
          {rows.map((r) => {
            const Icon = r.icon;
            return (
              <li key={r.label}>
                <span className="ico" aria-hidden="true">
                  <Icon className="i" />
                </span>
                <div>
                  <b>{r.label}</b>
                  <span>{r.kind}</span>
                </div>
                <span className="d">{formatDate(r.d)}</span>
              </li>
            );
          })}
        </ul>
      </div>
    </section>
  );
}
