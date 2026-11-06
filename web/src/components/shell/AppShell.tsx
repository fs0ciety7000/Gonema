import { useNavigate, useRouterState } from '@tanstack/react-router';
import { lazy, type ReactNode, Suspense, useEffect } from 'react';
import { useServerEvents } from '@/api/live';
import { useDialogsUsed } from '@/components/movie/dialogStore';
import { Toaster } from '@/components/ui/Toast';
import { isTypingTarget } from '@/lib/keys';
import { isPaletteShortcut, palette, usePalette } from '@/lib/palette';
import { Sidebar } from './Sidebar';
import { DemoBanner, ErrorBoundary, OfflineOverlay } from './Status';

const MovieDialogs = lazy(() => import('@/components/movie/dialogs/MovieDialogs'));
const CommandPalette = lazy(() => import('@/screens/palette/CommandPalette'));

/** Loads the command palette chunk (cmdk) on first ⌘K / Ctrl+K, then keeps it mounted. */
function PaletteHost() {
  const { used } = usePalette();
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (!isPaletteShortcut(ev)) return;
      ev.preventDefault();
      palette.toggle();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  if (!used) return null;
  return (
    <Suspense fallback={null}>
      <CommandPalette />
    </Suspense>
  );
}

/** Loads the library dialogs chunk once a dialog is first requested. */
function MovieDialogHost() {
  const used = useDialogsUsed();
  if (!used) return null;
  return (
    <Suspense fallback={null}>
      <MovieDialogs />
    </Suspense>
  );
}

/** "A" anywhere (not while typing, not in a menu or dialog) opens Add & discover. */
function useAddShortcut() {
  const navigate = useNavigate();
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.metaKey || ev.ctrlKey || ev.altKey || ev.shiftKey || ev.defaultPrevented) return;
      if (ev.key !== 'a' && ev.key !== 'A') return;
      if (isTypingTarget(ev.target, '[role="menu"], [role="listbox"], [role="dialog"]')) return;
      if (location.pathname.endsWith('/discover')) return;
      ev.preventDefault();
      void navigate({ to: '/discover' });
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [navigate]);
}

export function AppShell({ children }: { children: ReactNode }) {
  useServerEvents();
  useAddShortcut();
  // The guided setup is a full-screen page of its own (no sidebar).
  const bare = useRouterState({ select: (s) => s.location.pathname === '/setup' });
  if (bare)
    return (
      <>
        <ErrorBoundary>{children}</ErrorBoundary>
        <Toaster />
        <section id="gn-overlays" aria-label="Menus and dialogs" />
      </>
    );
  return (
    <>
      <OfflineOverlay>
        <a className="skip-link" href="#main">
          Skip to content
        </a>
        <div className="app">
          <Sidebar />
          <div className="main">
            <DemoBanner />
            <ErrorBoundary>{children}</ErrorBoundary>
          </div>
        </div>
      </OfflineOverlay>
      <Toaster />
      <MovieDialogHost />
      <PaletteHost />
      <section id="gn-overlays" aria-label="Menus and dialogs" />
    </>
  );
}
