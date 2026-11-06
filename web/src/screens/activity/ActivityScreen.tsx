import '@/styles/lot2.css';
import { useQuery } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { CircleCheck, CircleSlash, RefreshCw } from 'lucide-react';
import { useEffect } from 'react';
import { api, unwrap } from '@/api/client';
import { useQueue } from '@/api/queries';
import type { DownloadClient } from '@/api/schema';
import { queueSummaryQuery } from '@/api/screens';
import { SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { Button } from '@/components/ui/Button';
import { Tabs } from '@/components/ui/Tabs';
import { formatEta, formatSpeed } from '@/lib/format';
import { HistoryPanel, HistoryToolbar } from './HistoryPanel';
import { QueuePanel } from './QueuePanel';

type Tab = 'queue' | 'history';

const clientsQuery = {
  queryKey: ['download-clients'] as const,
  queryFn: async ({ signal }: { signal: AbortSignal }) => {
    try {
      return ((await unwrap(api.GET('/download-clients', { signal }))) ?? []) as DownloadClient[];
    } catch {
      return [] as DownloadClient[];
    }
  },
  staleTime: 60_000,
};

function Clients() {
  const clients = useQuery(clientsQuery);
  if (!clients.data?.length) return null;
  return (
    <div className="row clients">
      <span className="t-caption t-muted">Clients</span>
      {clients.data.map((c) => {
        const failing = c.enabled && (c.health?.failureCount ?? 0) > 0;
        const status = !c.enabled ? 'unmonitored' : failing ? 'failed' : 'available';
        return (
          <span
            key={c.id}
            className="status"
            data-status={status}
            title={failing ? c.health.lastError : c.enabled ? undefined : 'Disabled'}
          >
            {c.enabled && !failing ? (
              <CircleCheck className="i" aria-hidden="true" />
            ) : (
              <CircleSlash className="i" aria-hidden="true" />
            )}
            {c.name}
            {!c.enabled && <span className="sr-only">, disabled</span>}
            {failing && <span className="sr-only">, failing: {c.health.lastError}</span>}
          </span>
        );
      })}
    </div>
  );
}

function ActivityScreen({ tab }: { tab: Tab }) {
  const navigate = useNavigate();
  const queue = useQueue();
  const summary = useQuery(queueSummaryQuery);
  useEffect(() => {
    document.title = `${tab === 'queue' ? 'Queue' : 'History'} · Gonema`;
  }, [tab]);
  const items = queue.data ?? [];
  const s = summary.data;
  const downloading = s?.downloading ?? items.filter((q) => q.status === 'downloading').length;
  const speed = s?.totalSpeedBps ?? items.reduce((n, q) => n + (q.speedBps || 0), 0);
  const sub =
    tab === 'history'
      ? 'Every automatic action, with the reason it was taken.'
      : !items.length
        ? queue.isSuccess
          ? 'Nothing downloading.'
          : ' '
        : [
            `${downloading} downloading`,
            speed > 0 ? formatSpeed(speed) : '',
            s?.etaSeconds ? `about ${formatEta(s.etaSeconds)} for everything` : '',
            s?.paused ? `${s.paused} paused` : '',
          ]
            .filter(Boolean)
            .join(' · ');
  return (
    <>
      <Topbar>
        <SearchField onSearch={(v) => void navigate({ to: '/films', search: { q: v || undefined } })} />
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page activity-page" tabIndex={-1}>
        <div className="act-head">
          <div>
            <h1>Activity</h1>
            <p className="sub" aria-live="polite">
              {sub}
            </p>
          </div>
          {tab === 'queue' && <Clients />}
        </div>
        <div className="act-bar">
          <Tabs<Tab>
            label="Activity"
            panelId="activity-panel"
            className="tabs-plain"
            value={tab}
            onChange={(t) => void navigate({ to: t === 'queue' ? '/queue' : '/history' })}
            items={[
              { value: 'queue', label: 'Queue', count: items.length || undefined },
              { value: 'history', label: 'History' },
            ]}
          />
          <div className="toolbar">
            {tab === 'queue' ? (
              <Button
                variant="ghost"
                size="sm"
                icon={<RefreshCw className="i" aria-hidden="true" />}
                loading={queue.isFetching}
                onClick={() => void queue.refetch()}
              >
                Refresh
              </Button>
            ) : (
              <HistoryToolbar />
            )}
          </div>
        </div>
        <div id="activity-panel" role="tabpanel" aria-labelledby={`tab-activity-panel-${tab}`}>
          {tab === 'queue' ? <QueuePanel /> : <HistoryPanel />}
        </div>
      </main>
    </>
  );
}

export function QueueScreen() {
  return <ActivityScreen tab="queue" />;
}

export function HistoryScreen() {
  return <ActivityScreen tab="history" />;
}
