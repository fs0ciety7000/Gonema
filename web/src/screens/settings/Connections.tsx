import { type QueryKey, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { type ReactNode, useState } from 'react';
import {
  clientApi,
  clientsQuery,
  indexerApi,
  indexersQuery,
  lot3Keys,
  mediaServerApi,
  mediaServersQuery,
  notificationApi,
  notificationsQuery,
} from '@/api/lot3';
import type {
  DownloadClient,
  DownloadClientInput,
  Indexer,
  IndexerInput,
  MediaServer,
  MediaServerInput,
  Notification,
  NotificationInput,
  TestResult,
} from '@/api/schema';
import { StatusText } from '@/components/ui/Badges';
import { Button } from '@/components/ui/Button';
import { EmptyState, Skeleton } from '@/components/ui/Feedback';
import { toast } from '@/components/ui/Toast';
import { formatClock } from '@/lib/format';
import { ConnectionDialog, errorText, type FieldSpec, str, type Values } from './form';

type Row = { id: number; name: string; sub: string; status: ReactNode; error?: string };

/** List of one kind of connection: add, edit, test; removal applies at once with Undo
 *  (the DELETE is sent when the toast closes). */
function ConnectionList<T extends { id: number; name: string }>({
  noun,
  items,
  pending,
  queryKey,
  row,
  dialog,
  remove,
  testSaved,
  empty,
}: {
  noun: string;
  items: T[] | undefined;
  pending: boolean;
  queryKey: QueryKey;
  row: (x: T) => Row;
  dialog: (x: T | null, close: () => void) => ReactNode;
  remove: (id: number) => Promise<unknown>;
  testSaved?: (id: number) => Promise<TestResult>;
  empty: ReactNode;
}) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState<T | null | 'new'>(null);
  const [hidden, setHidden] = useState<ReadonlySet<number>>(new Set());
  const [testing, setTesting] = useState<number | null>(null);
  const list = (items ?? []).filter((x) => !hidden.has(x.id));
  const del = (x: T) => {
    setHidden((h) => new Set(h).add(x.id));
    const restore = () =>
      setHidden((h) => {
        const n = new Set(h);
        n.delete(x.id);
        return n;
      });
    toast.show({
      title: `Removed ${x.name}`,
      body: `Gonema stops using this ${noun}.`,
      undo: restore,
      onClose: (reason) => {
        if (reason === 'undo') return;
        remove(x.id)
          .then(() => qc.invalidateQueries({ queryKey }))
          .catch((e) => {
            restore();
            toast.error(`${x.name} was not removed`, errorText(e));
          });
      },
    });
  };
  const test = async (x: T) => {
    if (!testSaved) return;
    setTesting(x.id);
    try {
      const r = await testSaved(x.id);
      toast.show({
        tone: r.ok ? 'neutral' : 'error',
        title: r.ok ? `${x.name} answered.` : `${x.name} failed the test.`,
        body: r.message,
      });
      void qc.invalidateQueries({ queryKey });
    } catch (e) {
      toast.error(`${x.name} could not be tested`, errorText(e));
    } finally {
      setTesting(null);
    }
  };
  return (
    <>
      <div className="row conn-head">
        <Button
          variant="secondary"
          size="sm"
          icon={<Plus className="i" aria-hidden="true" />}
          onClick={() => setEditing('new')}
        >
          Add {noun}
        </Button>
      </div>
      {pending ? (
        <Skeleton className="sk-wanted" />
      ) : list.length ? (
        <ul className="conn-list">
          {list.map((x) => {
            const r = row(x);
            return (
              <li key={x.id}>
                <span className="nm">
                  {r.name}
                  <small>{r.sub}</small>
                  {r.error && <small className="err">{r.error}</small>}
                </span>
                {r.status}
                <span className="acts">
                  {testSaved && (
                    <Button
                      size="sm"
                      variant="ghost"
                      loading={testing === x.id}
                      onClick={() => void test(x)}
                      aria-label={`Test ${x.name}`}
                    >
                      Test
                    </Button>
                  )}
                  <Button size="sm" variant="ghost" onClick={() => setEditing(x)} aria-label={`Edit ${x.name}`}>
                    Edit
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => del(x)} aria-label={`Remove ${x.name}`}>
                    Remove
                  </Button>
                </span>
              </li>
            );
          })}
        </ul>
      ) : (
        empty
      )}
      {editing !== null && dialog(editing === 'new' ? null : editing, () => setEditing(null))}
    </>
  );
}

function useSaver<B>(
  queryKey: QueryKey,
  create: (b: B) => Promise<unknown>,
  update: (id: number, b: B) => Promise<unknown>,
) {
  const qc = useQueryClient();
  return (id: number | null, name: string) => async (body: Values) => {
    if (id == null) await create(body as B);
    else await update(id, body as B);
    await qc.invalidateQueries({ queryKey });
    toast.show({ title: `${id == null ? 'Added' : 'Saved'} ${name || str(body.name)}` });
  };
}

const ok = <S extends 'available' | 'failed' | 'missing' | 'unmonitored'>(s: S, label: string) => (
  <StatusText status={s}>{label}</StatusText>
);

// ----------------------------------------------------------------- indexers

export const INDEXER_KINDS = [
  { value: 'newznab', label: 'Newznab (Usenet)' },
  { value: 'torznab', label: 'Torznab (torrent)' },
] as const;

export function indexerSpec(v: Values, stored = false): FieldSpec[] {
  const torrent = v.kind === 'torznab';
  return [
    { kind: 'select', name: 'kind', label: 'Type', options: INDEXER_KINDS },
    { kind: 'text', name: 'name', label: 'Name', required: true },
    {
      kind: 'url',
      name: 'baseUrl',
      label: 'URL',
      required: true,
      hint: 'The indexer’s address, for example https://indexer.example',
    },
    { kind: 'mono', name: 'apiPath', label: 'API path', hint: 'Usually /api' },
    { kind: 'secret', name: 'apiKey', label: 'API key', stored },
    {
      kind: 'list',
      name: 'categories',
      label: 'Categories',
      numbers: true,
      hint: 'Newznab category ids, comma separated (2000 = films)',
    },
    { kind: 'number', name: 'priority', label: 'Priority', min: 1, max: 50, hint: '1 is tried first' },
    ...(torrent
      ? ([
          { kind: 'number', name: 'minSeeders', label: 'Minimum seeders', min: 0 },
          { kind: 'number', name: 'seedRatio', label: 'Seed ratio', min: 0, step: 0.1 },
          { kind: 'number', name: 'seedTimeMin', label: 'Seed time (minutes)', min: 0 },
        ] as FieldSpec[])
      : []),
    { kind: 'switch', name: 'enableRss', label: 'RSS sync', hint: 'New releases every few minutes' },
    { kind: 'switch', name: 'enableAutoSearch', label: 'Automatic search' },
    { kind: 'switch', name: 'enableInteractive', label: 'Interactive search' },
  ];
}

export const indexerInitial = (x: Indexer | null): Values =>
  x
    ? { ...x, apiKey: '' }
    : {
        kind: 'newznab',
        name: '',
        baseUrl: '',
        apiPath: '/api',
        apiKey: '',
        categories: [2000],
        priority: 25,
        enableRss: true,
        enableAutoSearch: true,
        enableInteractive: true,
      };

export function IndexersSection() {
  const q = useQuery(indexersQuery);
  const save = useSaver<IndexerInput>(lot3Keys.indexers, indexerApi.create, indexerApi.update);
  return (
    <ConnectionList<Indexer>
      noun="indexer"
      items={q.data}
      pending={q.isPending}
      queryKey={lot3Keys.indexers}
      remove={indexerApi.remove}
      testSaved={indexerApi.testSaved}
      row={(x) => {
        const h = x.health;
        return {
          id: x.id,
          name: x.name,
          sub: [
            x.kind === 'torznab' ? 'Torznab' : 'Newznab',
            `priority ${x.priority}`,
            [x.enableRss && 'RSS', x.enableAutoSearch && 'auto', x.enableInteractive && 'interactive']
              .filter(Boolean)
              .join(', '),
            h?.state === 'disabled' && h.disabledUntil ? `paused until ${formatClock(new Date(h.disabledUntil))}` : '',
          ]
            .filter(Boolean)
            .join(' · '),
          error: h?.state !== 'ok' ? h?.lastError : undefined,
          status:
            h?.state === 'disabled'
              ? ok('missing', 'Paused')
              : h?.state === 'failing'
                ? ok('failed', 'Failing')
                : ok('available', 'OK'),
        };
      }}
      dialog={(x, close) => (
        <ConnectionDialog
          open
          onOpenChange={(o) => !o && close()}
          title={x ? `Edit ${x.name}` : 'Add an indexer'}
          description="Any Newznab (Usenet) or Torznab (torrent) indexer: paste its API URL and key."
          spec={(v) => indexerSpec(v, !!x?.apiKeySet)}
          initial={indexerInitial(x)}
          onTest={(b) => indexerApi.test(b as IndexerInput, x?.id)}
          onSave={save(x?.id ?? null, x?.name ?? '')}
        />
      )}
      empty={
        <EmptyState title="No indexer yet." headingLevel={3}>
          Gonema needs at least one indexer to look for releases.
        </EmptyState>
      }
    />
  );
}

// ----------------------------------------------------------------- download clients

export const CLIENT_KINDS = [
  { value: 'sabnzbd', label: 'SABnzbd' },
  { value: 'nzbget', label: 'NZBGet' },
  { value: 'qbittorrent', label: 'qBittorrent' },
  { value: 'deluge', label: 'Deluge' },
  { value: 'transmission', label: 'Transmission' },
] as const;

export const CLIENT_PORT: Record<string, number> = {
  sabnzbd: 8080,
  nzbget: 6789,
  qbittorrent: 8080,
  deluge: 8112,
  transmission: 9091,
};

export function clientSpec(
  v: Values,
  x: { apiKeySet?: boolean; passwordSet?: boolean } | null,
  withKind = true,
): FieldSpec[] {
  const kind = str(v.kind);
  const usesKey = kind === 'sabnzbd';
  const usesUser = kind !== 'sabnzbd' && kind !== 'deluge';
  return [
    ...(withKind ? ([{ kind: 'select', name: 'kind', label: 'Type', options: CLIENT_KINDS }] as FieldSpec[]) : []),
    { kind: 'text', name: 'name', label: 'Name', required: true },
    { kind: 'mono', name: 'host', label: 'Host', required: true, hint: 'Host name or IP, as Gonema reaches it' },
    { kind: 'number', name: 'port', label: 'Port', min: 1, max: 65535 },
    { kind: 'mono', name: 'urlBase', label: 'URL base', hint: 'Only behind a reverse proxy path' },
    { kind: 'switch', name: 'useTls', label: 'Use HTTPS' },
    ...(usesKey ? ([{ kind: 'secret', name: 'apiKey', label: 'API key', stored: !!x?.apiKeySet }] as FieldSpec[]) : []),
    ...(usesUser ? ([{ kind: 'text', name: 'username', label: 'Username' }] as FieldSpec[]) : []),
    { kind: 'secret', name: 'password', label: 'Password', stored: !!x?.passwordSet },
    { kind: 'mono', name: 'category', label: 'Category', hint: 'Created for you if it does not exist' },
    { kind: 'number', name: 'priority', label: 'Priority', min: 1, max: 50, hint: 'Same priority: Gonema alternates' },
    { kind: 'switch', name: 'enabled', label: 'Enabled' },
    { kind: 'switch', name: 'removeCompleted', label: 'Remove completed downloads after import' },
    { kind: 'switch', name: 'removeFailed', label: 'Remove failed downloads after blocklisting' },
  ];
}

export const clientInitial = (x: DownloadClient | null, kind = 'qbittorrent'): Values =>
  x
    ? { ...x, apiKey: '', password: '' }
    : {
        kind,
        name: CLIENT_KINDS.find((k) => k.value === kind)?.label ?? '',
        host: '',
        port: CLIENT_PORT[kind],
        urlBase: '',
        useTls: false,
        username: '',
        password: '',
        apiKey: '',
        category: 'movies',
        priority: 1,
        enabled: true,
        removeCompleted: true,
        removeFailed: true,
      };

export function ClientsSection() {
  const q = useQuery(clientsQuery);
  const save = useSaver<DownloadClientInput>(lot3Keys.clients, clientApi.create, clientApi.update);
  return (
    <ConnectionList<DownloadClient>
      noun="download client"
      items={q.data}
      pending={q.isPending}
      queryKey={lot3Keys.clients}
      remove={clientApi.remove}
      testSaved={clientApi.testSaved}
      row={(c) => {
        const failing = c.enabled && (c.health?.failureCount ?? 0) > 0;
        return {
          id: c.id,
          name: c.name,
          sub: [
            CLIENT_KINDS.find((k) => k.value === c.kind)?.label ?? c.kind,
            c.protocol === 'usenet' ? 'Usenet' : 'Torrent',
            c.category ? `category ${c.category}` : '',
            `priority ${c.priority}`,
          ]
            .filter(Boolean)
            .join(' · '),
          error: failing ? c.health.lastError : undefined,
          status: !c.enabled ? ok('unmonitored', 'Off') : failing ? ok('failed', 'Failing') : ok('available', 'OK'),
        };
      }}
      dialog={(x, close) => (
        <ConnectionDialog
          open
          onOpenChange={(o) => !o && close()}
          title={x ? `Edit ${x.name}` : 'Add a download client'}
          description="Gonema sends releases here and watches them until they are ready to import."
          spec={(v) => clientSpec(v, x)}
          initial={clientInitial(x)}
          onTest={(b) => clientApi.test(b as DownloadClientInput, x?.id)}
          onSave={save(x?.id ?? null, x?.name ?? '')}
        />
      )}
      empty={
        <EmptyState title="No download client yet." headingLevel={3}>
          Connect SABnzbd, NZBGet, qBittorrent, Deluge or Transmission.
        </EmptyState>
      }
    />
  );
}

// ----------------------------------------------------------------- media servers

const SERVER_KINDS = [
  { value: 'plex', label: 'Plex' },
  { value: 'jellyfin', label: 'Jellyfin' },
  { value: 'kodi', label: 'Kodi' },
] as const;

const REFRESH = [
  { value: 'add', label: 'Import' },
  { value: 'upgrade', label: 'Upgrade' },
  { value: 'rename', label: 'Rename' },
  { value: 'delete', label: 'Delete' },
] as const;

function serverSpec(v: Values, x: MediaServer | null): FieldSpec[] {
  const kind = str(v.kind);
  return [
    { kind: 'select', name: 'kind', label: 'Type', options: SERVER_KINDS },
    { kind: 'text', name: 'name', label: 'Name', required: true },
    { kind: 'url', name: 'baseUrl', label: 'URL', required: true },
    ...(kind === 'kodi'
      ? ([
          { kind: 'text', name: 'username', label: 'Username' },
          { kind: 'secret', name: 'password', label: 'Password', stored: !!x?.passwordSet },
          { kind: 'switch', name: 'cleanOnDelete', label: 'Clean the library after a file is removed' },
        ] as FieldSpec[])
      : ([
          { kind: 'secret', name: 'token', label: kind === 'plex' ? 'Plex token' : 'API key', stored: !!x?.tokenSet },
        ] as FieldSpec[])),
    { kind: 'multi', name: 'refreshOn', label: 'Refresh the library on', options: REFRESH },
    { kind: 'switch', name: 'enabled', label: 'Enabled' },
  ];
}

export function MediaServersSection() {
  const q = useQuery(mediaServersQuery);
  const save = useSaver<MediaServerInput>(lot3Keys.mediaServers, mediaServerApi.create, mediaServerApi.update);
  return (
    <ConnectionList<MediaServer>
      noun="media server"
      items={q.data}
      pending={q.isPending}
      queryKey={lot3Keys.mediaServers}
      remove={mediaServerApi.remove}
      row={(s) => ({
        id: s.id,
        name: s.name,
        sub: [
          SERVER_KINDS.find((k) => k.value === s.kind)?.label ?? s.kind,
          s.refreshOn?.length
            ? `refresh on ${s.refreshOn.map((r) => REFRESH.find((x) => x.value === r)?.label.toLowerCase() ?? r).join(', ')}`
            : '',
        ]
          .filter(Boolean)
          .join(' · '),
        status: !s.enabled ? ok('unmonitored', 'Off') : ok('available', 'On'),
      })}
      dialog={(x, close) => (
        <ConnectionDialog
          open
          onOpenChange={(o) => !o && close()}
          title={x ? `Edit ${x.name}` : 'Add a media server'}
          description="Gonema asks it to refresh its library after imports, upgrades, renames and deletions."
          spec={(v) => serverSpec(v, x)}
          initial={
            x
              ? { ...x, token: '', password: '' }
              : {
                  kind: 'jellyfin',
                  name: 'Jellyfin',
                  baseUrl: '',
                  token: '',
                  enabled: true,
                  refreshOn: REFRESH.map((r) => r.value),
                }
          }
          onTest={(b) => mediaServerApi.test(b as MediaServerInput, x?.id)}
          onSave={save(x?.id ?? null, x?.name ?? '')}
        />
      )}
      empty={
        <EmptyState title="No media server yet." headingLevel={3}>
          Connect Plex, Jellyfin or Kodi to refresh it when a film arrives.
        </EmptyState>
      }
    />
  );
}

// ----------------------------------------------------------------- notifications

const NOTIF_KINDS = [
  { value: 'webhook', label: 'Webhook' },
  { value: 'discord', label: 'Discord' },
  { value: 'slack', label: 'Slack' },
  { value: 'telegram', label: 'Telegram' },
  { value: 'pushover', label: 'Pushover' },
  { value: 'ntfy', label: 'ntfy' },
  { value: 'gotify', label: 'Gotify' },
  { value: 'email', label: 'Email' },
] as const;

export const EVENTS = [
  { value: 'grab', label: 'Grab' },
  { value: 'import', label: 'Import' },
  { value: 'upgrade', label: 'Upgrade' },
  { value: 'failure', label: 'Failure' },
  { value: 'health', label: 'Health' },
  { value: 'rename', label: 'Rename' },
  { value: 'delete', label: 'Delete' },
] as const;

/* Settings and secrets of each service (ServiceSettings / NotificationSecretsInput). Secrets
   are flattened as "secret.<name>" in the form and nested back under `secrets` on save. */
function notifSpec(v: Values, n: Notification | null): FieldSpec[] {
  const set = n?.secretsSet;
  const S = (name: string, label: string, stored?: boolean, hint?: string): FieldSpec => ({
    kind: 'secret',
    name: `secret.${name}`,
    label,
    stored,
    hint,
  });
  const T = (name: string, label: string, hint?: string, kind: 'text' | 'url' | 'mono' = 'text'): FieldSpec => ({
    kind,
    name: `settings.${name}`,
    label,
    hint,
  });
  const by: Record<string, FieldSpec[]> = {
    webhook: [
      T('url', 'URL', 'The JSON is POSTed here', 'url'),
      S('signingSecret', 'Signing secret', set?.signingSecret, 'HMAC-SHA256 key of the X-Gonema-Signature header'),
    ],
    discord: [S('webhookUrl', 'Webhook URL', set?.webhookUrl), T('username', 'Display name')],
    slack: [S('webhookUrl', 'Webhook URL', set?.webhookUrl)],
    telegram: [S('token', 'Bot token', set?.token), T('chatId', 'Chat id', 'Chat id or @channel', 'mono')],
    pushover: [
      S('token', 'Application token', set?.token),
      S('userKey', 'User key', set?.userKey),
      T('device', 'Device', 'Empty for all devices'),
    ],
    ntfy: [
      T('serverUrl', 'Server', 'Default https://ntfy.sh', 'url'),
      T('topic', 'Topic', undefined, 'mono'),
      S('token', 'Access token', set?.token),
    ],
    gotify: [T('serverUrl', 'Server', undefined, 'url'), S('token', 'Application token', set?.token)],
    email: [
      T('host', 'SMTP host', undefined, 'mono'),
      {
        kind: 'number',
        name: 'settings.port',
        label: 'SMTP port',
        min: 0,
        max: 65535,
        hint: '0 for the default of the encryption',
      },
      {
        kind: 'select',
        name: 'settings.encryption',
        label: 'Encryption',
        options: [
          { value: 'starttls', label: 'STARTTLS (587)' },
          { value: 'tls', label: 'TLS (465)' },
          { value: 'none', label: 'None (25)' },
        ],
      },
      T('username', 'SMTP user'),
      S('password', 'SMTP password', set?.password),
      T('from', 'From'),
      { kind: 'list', name: 'settings.to', label: 'To', hint: 'Recipients, comma separated' },
    ],
  };
  return [
    { kind: 'select', name: 'kind', label: 'Service', options: NOTIF_KINDS },
    { kind: 'text', name: 'name', label: 'Name', required: true },
    ...(by[str(v.kind)] ?? []),
    { kind: 'multi', name: 'events', label: 'Send on', options: EVENTS },
    { kind: 'switch', name: 'enabled', label: 'Enabled' },
  ];
}

/** Flat form values → NotificationInput (settings and secrets nested). */
export function notifBody(b: Values): NotificationInput {
  const settings: Record<string, unknown> = {};
  const secrets: Record<string, unknown> = {};
  const out: Record<string, unknown> = {};
  for (const [k, val] of Object.entries(b)) {
    if (k.startsWith('settings.')) settings[k.slice(9)] = val;
    else if (k.startsWith('secret.')) secrets[k.slice(7)] = val;
    else out[k] = val;
  }
  return { ...(out as NotificationInput), settings, secrets };
}

function notifInitial(n: Notification | null): Values {
  if (!n) return { kind: 'ntfy', name: 'ntfy', enabled: true, events: ['import', 'upgrade', 'failure', 'health'] };
  const v: Values = { kind: n.kind, name: n.name, enabled: n.enabled, events: n.events ?? [] };
  for (const [k, val] of Object.entries(n.settings ?? {})) v[`settings.${k}`] = val;
  return v;
}

export function NotificationsSection() {
  const q = useQuery(notificationsQuery);
  const save = useSaver<NotificationInput>(
    lot3Keys.notifications,
    (b) => notificationApi.create(notifBody(b as never)),
    (id, b) => notificationApi.update(id, notifBody(b as never)),
  );
  return (
    <>
      <ConnectionList<Notification>
        noun="channel"
        items={q.data}
        pending={q.isPending}
        queryKey={lot3Keys.notifications}
        remove={notificationApi.remove}
        row={(n) => ({
          id: n.id,
          name: n.name,
          sub: [NOTIF_KINDS.find((k) => k.value === n.kind)?.label ?? n.kind, (n.events ?? []).join(', ')]
            .filter(Boolean)
            .join(' · '),
          status: !n.enabled ? ok('unmonitored', 'Off') : ok('available', 'On'),
        })}
        dialog={(x, close) => (
          <ConnectionDialog
            open
            onOpenChange={(o) => !o && close()}
            title={x ? `Edit ${x.name}` : 'Add a notification channel'}
            description="Gonema sends a short note for the events you pick, with the reason behind each one."
            spec={(v) => notifSpec(v, x)}
            initial={notifInitial(x)}
            onTest={(b) => notificationApi.test(notifBody(b), x?.id)}
            onSave={save(x?.id ?? null, x?.name ?? '')}
          />
        )}
        empty={
          <EmptyState title="No channel yet." headingLevel={3}>
            Get a note on your phone or in a chat when a film arrives or something needs a look.
          </EmptyState>
        }
      />
      {!!q.data?.length && (
        <table className="matrix" aria-label="Notification events per channel">
          <thead>
            <tr>
              <th scope="col">Channel</th>
              {EVENTS.map((e) => (
                <th scope="col" key={e.value}>
                  {e.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {q.data.map((n) => (
              <tr key={n.id}>
                <th scope="row">{n.name}</th>
                {EVENTS.map((e) => {
                  const on = (n.events ?? []).includes(e.value);
                  return (
                    <td key={e.value}>
                      {on ? (
                        <span className="on" aria-hidden="true">
                          ✓
                        </span>
                      ) : (
                        <span className="off" aria-hidden="true">
                          ·
                        </span>
                      )}
                      <span className="sr-only">{on ? 'on' : 'off'}</span>
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
