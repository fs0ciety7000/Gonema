import type { ReactNode } from 'react';
import { useServerEvents } from '@/api/live';
import { Toaster } from '@/components/ui/Toast';
import { Sidebar } from './Sidebar';
import { DemoBanner, ErrorBoundary, OfflineOverlay } from './Status';

export function AppShell({ children }: { children: ReactNode }) {
  useServerEvents();
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
      <section id="gn-overlays" aria-label="Menus and dialogs" />
    </>
  );
}
