import { Bookmark, BookmarkMinus, Ellipsis, Search } from 'lucide-react';
import { type CSSProperties, type MouseEvent, memo, type ReactNode } from 'react';
import type { LibraryItem } from '@/api/model';
import { HdrFlag, QualityBadge, StatusBadge, StatusText } from '@/components/ui/Badges';
import { IconButton } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { cx } from '@/components/ui/cx';
import { Skeleton } from '@/components/ui/Feedback';
import { Menu } from '@/components/ui/Menu';
import { PosterArt } from '@/components/ui/PosterArt';
import { ProgressBar } from '@/components/ui/Progress';
import { formatEta, formatSpeed } from '@/lib/format';
import { parseQuality } from '@/lib/quality';
import { type DisplayStatus, STATUS, statusText } from '@/lib/status';

export type LiveDownload = { progress: number; speedBps: number; etaSeconds: number | null; paused?: boolean };

type PosterCardProps = {
  movie: LibraryItem;
  status: DisplayStatus;
  href: string;
  selected: boolean;
  /** The roving tab stop of the grid: only this card's controls are tabbable. */
  active: boolean;
  live?: LiveDownload | undefined;
  onLinkClick: (ev: MouseEvent<HTMLAnchorElement>, movie: LibraryItem) => void;
  onSelectClick: (ev: MouseEvent<HTMLInputElement>, movie: LibraryItem) => void;
  onSearch: (movie: LibraryItem) => void;
  onToggleMonitor: (movie: LibraryItem) => void;
  onFocusCard: (movie: LibraryItem) => void;
  menu: (movie: LibraryItem) => ReactNode;
  style?: CSSProperties;
  className?: string;
  eager?: boolean;
};

/** The poster is a link; controls are its siblings, never nested in it (mockups/mock.js). */
export const PosterCard = memo(function PosterCard({
  movie: m,
  status,
  href,
  selected,
  active,
  live,
  onLinkClick,
  onSelectClick,
  onSearch,
  onToggleMonitor,
  onFocusCard,
  menu,
  style,
  className,
  eager,
}: PosterCardProps) {
  const label = m.year ? `${m.title} (${m.year})` : m.title;
  const tab = active ? 0 : -1;
  const showBadge = status === 'missing' || status === 'failed' || status === 'unmonitored';
  const progress = live?.progress ?? m.progress;
  const q = parseQuality(m.quality);
  const text = statusText(m, status);
  return (
    // biome-ignore lint/a11y/useFocusableInteractive: grid pattern, focus is on the poster link inside the cell (roving tabindex)
    // biome-ignore lint/a11y/useSemanticElements: a virtualised poster grid cannot be a <table>
    <div
      className={cx('pc', selected && 'is-selected', className)}
      data-state={status}
      data-movie-id={m.id}
      role="gridcell"
      aria-selected={selected}
      style={style}
    >
      <div className="pc-frame" data-poster-frame={m.id}>
        <a
          className="pc-link"
          href={href}
          aria-label={label}
          tabIndex={tab}
          data-card-link
          onClick={(ev) => onLinkClick(ev, m)}
          onFocus={() => onFocusCard(m)}
          draggable={false}
        >
          <PosterArt
            src={m.posterUrl}
            title={m.title}
            year={m.year}
            color={m.dominantColor}
            eager={eager}
            sizes="(max-width: 1100px) 160px, 240px"
          />
        </a>
        <div className="pc-shade" />
        <div className="pc-top">
          {showBadge && <StatusBadge status={status} onMedia />}
          <Checkbox
            onMedia
            labelClassName="pc-select"
            checked={selected}
            tabIndex={tab}
            aria-label={`Select ${label}`}
            onClick={(ev) => onSelectClick(ev, m)}
          />
        </div>
        {status === 'downloading' && (
          <div className="pc-progress">
            <div className="row">
              <span>{Math.round(progress * 100)}%</span>
              <span>{formatEta(live?.etaSeconds)}</span>
            </div>
            <ProgressBar
              value={progress}
              live={!live?.paused}
              state={live?.paused ? 'paused' : 'active'}
              size="thin"
              label={`Download progress for ${m.title}`}
            />
          </div>
        )}
        <div className="pc-bottom">
          <div className="pc-hoverinfo">
            <QualityBadge quality={m.quality} />
            {m.hdr.slice(0, 1).map((h) => (
              <HdrFlag key={h}>{h}</HdrFlag>
            ))}
          </div>
          <div className="pc-actions">
            <IconButton
              variant="on-media"
              label={`Search for ${m.title}`}
              title="Search  S"
              tabIndex={tab}
              onClick={() => onSearch(m)}
            >
              <Search className="i i-sm" aria-hidden="true" />
            </IconButton>
            <IconButton
              variant="on-media"
              label={m.monitored ? `Stop monitoring ${m.title}` : `Monitor ${m.title}`}
              aria-pressed={m.monitored}
              tabIndex={tab}
              onClick={() => onToggleMonitor(m)}
            >
              {m.monitored ? (
                <BookmarkMinus className="i i-sm" aria-hidden="true" />
              ) : (
                <Bookmark className="i i-sm" aria-hidden="true" />
              )}
            </IconButton>
            <Menu
              label={`Actions for ${m.title}`}
              heading={m.title}
              trigger={
                <IconButton variant="on-media" label={`More actions for ${m.title}`} tabIndex={tab}>
                  <Ellipsis className="i i-sm" aria-hidden="true" />
                </IconButton>
              }
            >
              {menu(m)}
            </Menu>
          </div>
        </div>
      </div>
      <div className="pc-meta">
        <div className="pc-title">{m.title}</div>
        <div className="pc-sub">
          {m.year > 0 && <span>{m.year}</span>}
          {m.year > 0 && <span className="sep" aria-hidden="true" />}
          {status === 'available' || status === 'upgraded' ? (
            <StatusText status={status} srPrefix={STATUS[status].label}>
              {q?.res ?? STATUS[status].label}
            </StatusText>
          ) : status === 'downloading' ? (
            <StatusText status="downloading">{formatSpeed(live?.speedBps ?? 0) || text}</StatusText>
          ) : (
            <span className="status" data-status={status}>
              {text}
            </span>
          )}
        </div>
      </div>
    </div>
  );
});

/** Skeleton card: same geometry as a real card (2:3 poster, two text lines), no layout shift. */
export function PosterSkeleton({ delay = 0, style }: { delay?: number; style?: CSSProperties }) {
  return (
    <div className="pc" data-state="skeleton" aria-hidden="true" style={style}>
      <Skeleton className="pc-frame" delay={delay} />
      <div className="pc-meta">
        <Skeleton className="sk-line" style={{ width: '78%' }} delay={delay} />
        <Skeleton className="sk-line" style={{ width: '42%', height: 10, marginTop: 6 }} delay={delay} />
      </div>
    </div>
  );
}
