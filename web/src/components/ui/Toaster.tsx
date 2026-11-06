import { X } from 'lucide-react';
import { AnimatePresence, motion } from 'motion/react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { isTypingTarget } from '@/lib/keys';
import { useReducedMotion } from '@/lib/motion';
import { Button, IconButton } from './Button';
import { cx } from './cx';
import {
  type CloseReason,
  currentToasts as currentItems,
  TOAST_DURATION,
  type ToastItem,
  toast,
  useToasts,
} from './toastStore';

/* The toast region (Motion springs). Loaded lazily after the first paint: the
   store (toastStore.ts) is tiny and usable before this view arrives. */

function ToastView({
  t,
  onDone,
  returnFocus,
}: {
  t: ToastItem;
  onDone: (reason: CloseReason) => void;
  returnFocus: () => void;
}) {
  const duration = t.duration ?? TOAST_DURATION;
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [hidden, setHidden] = useState(() => typeof document !== 'undefined' && document.hidden);
  const paused = hovered || focused || hidden;
  const remaining = useRef(duration);
  const startedAt = useRef(0);

  useEffect(() => {
    const onVis = () => setHidden(document.hidden);
    document.addEventListener('visibilitychange', onVis);
    return () => document.removeEventListener('visibilitychange', onVis);
  }, []);

  useEffect(() => {
    if (paused) return;
    startedAt.current = Date.now();
    const timer = setTimeout(() => onDone('timeout'), remaining.current);
    return () => {
      clearTimeout(timer);
      remaining.current = Math.max(0, remaining.current - (Date.now() - startedAt.current));
    };
  }, [paused, onDone]);

  const isError = t.tone === 'error';
  return (
    // biome-ignore lint/a11y/noNoninteractiveElementInteractions: hover and focus only pause the timer
    // biome-ignore lint/a11y/noStaticElementInteractions: same (role is status or alert)
    <div
      className={cx('toast', 'toast-live', isError && 'is-error')}
      role={isError ? 'alert' : 'status'}
      aria-live={isError ? 'assertive' : 'polite'}
      aria-atomic="true"
      data-paused={paused || undefined}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onFocus={() => setFocused(true)}
      onBlur={(ev) => {
        if (!ev.currentTarget.contains(ev.relatedTarget as Node | null)) setFocused(false);
      }}
    >
      <span className="ticon" aria-hidden="true">
        {t.icon}
      </span>
      <div className="grow">
        <div className="ttitle">{t.title}</div>
        {t.body && <div className="tbody">{t.body}</div>}
      </div>
      <div className="tactions">
        {t.undo && (
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              t.undo?.();
              returnFocus();
              onDone('undo');
            }}
          >
            Undo
          </Button>
        )}
        {t.action && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              t.action?.onClick();
              onDone('action');
            }}
          >
            {t.action.label}
          </Button>
        )}
        <IconButton
          label="Dismiss"
          size="sm"
          onClick={() => {
            returnFocus();
            onDone('dismiss');
          }}
        >
          <X className="i i-sm" aria-hidden="true" />
        </IconButton>
      </div>
      <span className="timer" aria-hidden="true" style={{ animationDuration: `${duration}ms` }} />
    </div>
  );
}

export function Toaster() {
  const list = useToasts();
  const reduce = useReducedMotion();
  const regionRef = useRef<HTMLElement>(null);
  const focusBefore = useRef<HTMLElement | null>(null);

  const returnFocus = useCallback(() => {
    const el = focusBefore.current;
    focusBefore.current = null;
    if (el?.isConnected) el.focus();
  }, []);

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      const typing = isTypingTarget(ev.target);
      if ((ev.metaKey || ev.ctrlKey) && !ev.shiftKey && ev.key.toLowerCase() === 'z' && !typing) {
        if (toast.undoLatest()) ev.preventDefault();
      } else if (ev.key === 'F6' && currentItems().length) {
        const buttons = regionRef.current?.querySelectorAll<HTMLButtonElement>('.toast:last-child button');
        if (buttons?.length) {
          ev.preventDefault();
          focusBefore.current = document.activeElement as HTMLElement | null;
          buttons[0]?.focus();
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const spring = reduce ? { duration: 0.15 } : { type: 'spring' as const, stiffness: 700, damping: 38, mass: 1 };

  return (
    <section
      ref={regionRef}
      className="toast-region"
      aria-label="Notifications"
      onFocusCapture={(ev) => {
        const from = ev.relatedTarget as HTMLElement | null;
        if (from && !regionRef.current?.contains(from)) focusBefore.current = from;
      }}
    >
      <ol>
        <AnimatePresence initial={false}>
          {list.map((t) => (
            <motion.li
              key={t.id}
              layout={!reduce}
              initial={{ opacity: 0, y: reduce ? 0 : 16 }}
              animate={{ opacity: 1, y: 0, transition: spring }}
              exit={{ opacity: 0, y: reduce ? 0 : 12, transition: { duration: 0.15, ease: [0.4, 0, 1, 1] } }}
            >
              <ToastView t={t} onDone={(r) => toast.dismiss(t.id, r)} returnFocus={returnFocus} />
            </motion.li>
          ))}
        </AnimatePresence>
      </ol>
    </section>
  );
}
