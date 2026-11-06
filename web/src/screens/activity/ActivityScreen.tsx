import '@/styles/lot2.css';
import '@/styles/lot3.css';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { CircleCheck, CircleSlash, Hand, Play, RefreshCw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { ApiError, api, unwrap } from '@/api/client';
import { keys, useQueue } from '@/api/queries';
import type { DownloadClient } from '@/api/schema';
import { blocklistQuery, queueControl } from '@/api/lot3';
import { queueSummaryQuery } from '@/api/screens';
import { SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { Button } from '@/components/ui/Button';
import { Tabs } from '@/components/ui/Tabs';
import { toast } from '@/components/ui/Toast';
import { formatEta, formatSpeed } from '@/lib/format';
import { BlocklistPanel } from './BlocklistPanel';
import { HistoryPanel, HistoryToolbar } from './HistoryPanel';
import { QueuePanel } from './QueuePanel';

type Tab = 'queue' | 'history' | 'blocklist';

const TITLE: Record<Tab, string> = { queue: 'Queue', history: 'History', blocklist: 'Blocklist' };
const PATH: Record<Tab, '/queue' | '/history' | '/blocklist'> = {
  queue: '/queue',
  history: '/history',
  blocklist: '/blocklist',
};

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

/** Pause all / Resume all: every Gonema download in its client (the clients' other downloads stay as they are). */
function PauseAll() {
  const qc = useQueryClient();
  const queue = useQueue();
  const [busy, setBusy] = useState(false);
  const items = queue.data ?? [];
  const active = items.filter((q) => q.status === 'downloading' || q.status === 'queued').length;
  const paused = items.filter((q) => q.status === 'paused').length;
  if (!active && !paused) return null;
  const what: 'pause' | 'resume' = active ? 'pause' : 'resume';
  const run = async () => {
    setBusy(true);
    try {
      const r = await queueControl(what);
      void qc.invalidateQueries({ queryKey: keys.queue });
      const unsupported = (r.clients ?? []).filter((c) => !c.supported).map((c) => c.name);
      toast.show({
        tone: r.failed ? 'error' : 'neutral',
        title: what === 'pause' ? `Paused ${r.changed} ${r.changed === 1 ? 'download' : 'downloads'}.` : `Resumed ${r.changed} ${r.changed === 1 ? 'download' : 'downloads'}.`,
        body: [
          r.failed ? `${r.failed} could not be changed in their client.` : '',
          unsupported.length ? `${unsupported.join(', ')} cannot pause from Gonema.` : '',
          what === 'pause' && !r.failed ? 'Nothing downloads until you resume.' : '',
        ]
          .filter(Boolean)
          .join(' '),
        action:
          what === 'pause' && r.changed
            ? { label: 'Resume', onClick: () => void queueControl('resume').then(() => qc.invalidateQueries({ queryKey: keys.queue })) }
            : undefined,
      });
    } catch (e) {
      toast.error(what === 'pause' ? 'Nothing was paused' : 'Nothing was resumed', e instanceof ApiError ? e.detail : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Button
      variant="ghost"
      size="sm"
      loading={busy}
      icon={what === 'pause' ? <Hand className="i" aria-hidden="true" /> : <Play className="i" aria-hidden="true" />}
      onClick={() => void run()}
    >
      {what === 'pause' ? 'Pause all' : 'Resume all'}
    </Button>
  );
}

function ActivityScreen({ tab }: { tab: Tab }) {
  const navigate = useNavigate();
  const queue = useQueue();
  const summary = useQuery(queueSummaryQuery);
  useEffect(() => {
    document.title = `${TITLE[tab]} · Gonema`;
  }, [tab]);
  const items = queue.data ?? [];
  const blocked = useQuery(blocklistQuery).data?.length;
  const s = summary.data;
  const downloading = s?.downloading ?? items.filter((q) => q.status === 'downloading').length;
  const speed = s?.totalSpeedBps ?? items.reduce((n, q) => n + (q.speedBps || 0), 0);
  const sub =
    tab === 'history'
      ? 'Every automatic action, with the reason it was taken.'
      : tab === 'blocklist'
        ? 'Releases and groups Gonema will never grab again.'
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
            onChange={(t) => void navigate({ to: PATH[t] })}
            items={[
              { value: 'queue', label: 'Queue', count: items.length || undefined },
              { value: 'history', label: 'History' },
              { value: 'blocklist', label: 'Blocklist', count: blocked || undefined },
            ]}
          />
          <div className="toolbar">
            {tab === 'queue' ? (
              <>
                <PauseAll />
                <Button
                variant="ghost"
                size="sm"
                icon={<RefreshCw className="i" aria-hidden="true" />}
                loading={queue.isFetching}
                onClick={() => void queue.refetch()}
              >
                Refresh
              </Button>
              </>
            ) : tab === 'history' ? (
              <HistoryToolbar />
            ) : null}
          </div>
        </div>
        <div id="activity-panel" role="tabpanel" aria-labelledby={`tab-activity-panel-${tab}`}>
          {tab === 'queue' ? <QueuePanel /> : tab === 'history' ? <HistoryPanel /> : <BlocklistPanel />}
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

export function BlocklistScreen() {
  return <ActivityScreen tab="blocklist" />;
}
