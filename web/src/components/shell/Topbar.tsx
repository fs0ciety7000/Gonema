import { Link } from '@tanstack/react-router';
import { Moon, Plus, Search, Sun } from 'lucide-react';
import { type ReactNode, useEffect, useRef } from 'react';
import { useQueue } from '@/api/queries';
import { StatusText } from '@/components/ui/Badges';
import { buttonClass, IconButton } from '@/components/ui/Button';
import { cx } from '@/components/ui/cx';
import { formatSpeed } from '@/lib/format';
import { isTypingTarget } from '@/lib/keys';
import { reachable } from '@/lib/nav';
import { palette } from '@/lib/palette';
import { setThemePreference, useTheme } from '@/lib/theme';

/** Search field of the top bar. ⌘K / Ctrl+K opens the command palette (AppShell); "/" focuses
 *  the field. With onSearch the field filters the current list; without it, it opens the palette. */
export function SearchField({
  value,
  onSearch,
  compact,
  placeholder = 'Search films, people, releases…',
}: {
  value?: string;
  onSearch?: (q: string) => void;
  compact?: boolean;
  placeholder?: string;
}) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (!isTypingTarget(ev.target) && ev.key === '/' && !ev.metaKey && !ev.ctrlKey && !ev.altKey) {
        ev.preventDefault();
        ref.current?.focus();
        ref.current?.select();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  const isMac = typeof navigator !== 'undefined' && /Mac|iP(hone|ad)/.test(navigator.platform);
  return (
    <label className={cx('input search', compact && 'search-compact')}>
      <Search className="i" aria-hidden="true" />
      <input
        ref={ref}
        type="search"
        placeholder={placeholder}
        aria-label="Search films"
        value={value ?? ''}
        onChange={(ev) => onSearch?.(ev.target.value)}
        readOnly={!onSearch}
        onClick={onSearch ? undefined : () => palette.open()}
        onKeyDownCapture={(ev) => {
          // without a list to filter, typing starts a palette search
          if (onSearch || ev.metaKey || ev.ctrlKey || ev.altKey) return;
          if (ev.key === 'Enter' || ev.key.length === 1) {
            ev.preventDefault();
            palette.open(ev.key.length === 1 && ev.key !== ' ' ? ev.key : '');
          }
        }}
        onKeyDown={(ev) => {
          if (ev.key === 'Escape') {
            if (value) onSearch?.('');
            else ref.current?.blur();
          }
        }}
      />
      <span className="kbd" aria-hidden="true">
        {isMac ? '⌘K' : 'Ctrl K'}
      </span>
    </label>
  );
}

export function ThemeToggle() {
  const { theme } = useTheme();
  const light = theme === 'light';
  return (
    <IconButton
      label={light ? 'Switch to dark theme' : 'Switch to light theme'}
      onClick={() => setThemePreference(light ? 'dark' : 'light')}
    >
      {light ? <Moon className="i" aria-hidden="true" /> : <Sun className="i" aria-hidden="true" />}
    </IconButton>
  );
}

/** Live queue indicator: only when something downloads (calm when all is well). */
export function QueuePill() {
  const queue = useQueue();
  const active = (queue.data ?? []).filter((q) => q.status === 'downloading' || q.status === 'queued');
  if (!active.length) return null;
  const speed = active.reduce((s, q) => s + (q.speedBps || 0), 0);
  const text = `${active.length} downloading`;
  const content = (
    <>
      <StatusText status="downloading">{text}</StatusText>
      {speed > 0 && <span>{formatSpeed(speed)}</span>}
    </>
  );
  const linkContent = (
    <>
      <span className="sr-only">Queue: </span>
      <StatusText status="downloading">{text}</StatusText>
      {speed > 0 && (
        <span>
          <span className="sr-only"> at </span>
          {formatSpeed(speed)}
        </span>
      )}
    </>
  );
  const label = `Queue: ${text}${speed > 0 ? ` at ${formatSpeed(speed)}` : ''}`;
  return reachable('queue') ? (
    <Link className="live-pill hide-tablet" to={'/queue' as never}>
      {linkContent}
    </Link>
  ) : (
    <span className="live-pill hide-tablet" role="status" aria-label={label}>
      {content}
    </span>
  );
}

export function AddFilmButton() {
  if (!reachable('discover')) return null;
  return (
    <Link className={buttonClass('primary')} to={'/discover' as never}>
      <Plus className="i" aria-hidden="true" />
      Add film
    </Link>
  );
}

export function Topbar({ children, overMedia }: { children: ReactNode; overMedia?: boolean }) {
  return <header className={cx('topbar', overMedia && 'over-media')}>{children}</header>;
}
