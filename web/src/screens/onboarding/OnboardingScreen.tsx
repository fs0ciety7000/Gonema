import '@/styles/lot3.css';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import {
  ArrowDownToLine,
  Check,
  ChevronRight,
  CircleCheck,
  ExternalLink,
  Film,
  Folder,
  Globe,
  HardDrive,
  Info,
  Lock,
  Magnet,
  Newspaper,
  RefreshCw,
  Search,
  Server,
  Sparkles,
} from 'lucide-react';
import { type FormEvent, type ReactNode, useEffect, useMemo, useState } from 'react';
import { clientApi, clientsQuery, indexerApi, indexersQuery, lot3Keys, addRootFolder } from '@/api/lot3';
import { onboardingQuery, setOnboardingDismissed } from '@/api/onboarding';
import { generalSettingsQuery, putUiPreferences } from '@/api/lot3';
import { keys, profilesQuery, rootFoldersQuery, useLibrary, useQueue } from '@/api/queries';
import type { DownloadClientInput, IndexerInput, LookupResult, TestResult } from '@/api/schema';
import { addMovie, lookupQuery, screenKeys, setTmdbKey, tmdbQuery } from '@/api/screens';
import { Mark, Wordmark } from '@/components/shell/Logo';
import { ThemeToggle } from '@/components/shell/Topbar';
import { StatusText } from '@/components/ui/Badges';
import { Button, buttonClass } from '@/components/ui/Button';
import { Notice } from '@/components/ui/Feedback';
import { Field, Input } from '@/components/ui/Field';
import { PosterArt } from '@/components/ui/PosterArt';
import { ProgressBar } from '@/components/ui/Progress';
import { formatBytes, formatEta, formatSpeed } from '@/lib/format';
import { qualityName } from '@/lib/quality';
import { CLIENT_KINDS, CLIENT_PORT, clientInitial, clientSpec } from '@/screens/settings/Connections';
import { errorText, FormFields, TestLine, toBody, validate, type Values } from '@/screens/settings/form';
import { readAddOptions, saveAddOptions } from '@/screens/discover/addOptions';
import mascotWelcome from '../../../../docs/brand/kit/mascot/kernel-welcome.svg?url';

const route = getRouteApi('/setup');

export type StepId = 'welcome' | 'tmdb' | 'folder' | 'quality' | 'indexer' | 'client' | 'film';

export const STEPS: ReadonlyArray<{ id: StepId; label: string; sub: string }> = [
  { id: 'welcome', label: 'Welcome', sub: '' },
  { id: 'tmdb', label: 'Metadata', sub: 'TMDB key' },
  { id: 'folder', label: 'Library folder', sub: 'Where films live' },
  { id: 'quality', label: 'Quality', sub: 'Starting profile' },
  { id: 'indexer', label: 'Indexer', sub: 'Newznab or Torznab' },
  { id: 'client', label: 'Download client', sub: 'Where releases go' },
  { id: 'film', label: 'First film', sub: 'Search and grab' },
];

/** The step to open: the one asked in the URL, else the first one not done (welcome when nothing is). */
export function currentStep(asked: string | undefined, done: Record<string, boolean>): StepId {
  if (asked && STEPS.some((s) => s.id === asked)) return asked as StepId;
  const anyDone = STEPS.some((s) => s.id !== 'welcome' && done[s.id]);
  if (!anyDone) return 'welcome';
  return STEPS.find((s) => s.id !== 'welcome' && !done[s.id])?.id ?? 'film';
}

function useDone() {
  const server = useQuery(onboardingQuery);
  const tmdb = useQuery(tmdbQuery);
  const roots = useQuery(rootFoldersQuery);
  const indexers = useQuery(indexersQuery);
  const clients = useQuery(clientsQuery);
  const lib = useLibrary();
  const ui = useQuery(generalSettingsQuery);
  const s = server.data?.done;
  // The server's answer wins; the lists fill the gap while it reloads after a step.
  const done: Record<string, boolean> = {
    tmdb: !!s?.tmdb || !!tmdb.data?.configured,
    folder: !!s?.folder || !!roots.data?.length,
    quality: !!s?.quality || !!ui.data?.ui.defaultQualityProfileId,
    indexer: !!s?.indexer || !!indexers.data?.length,
    client: !!s?.client || !!clients.data?.length,
    film: !!s?.film || !!lib.data?.length,
  };
  const ready = !server.isPending && !tmdb.isPending && !roots.isPending && !indexers.isPending && !clients.isPending;
  return { done, ready };
}

// ----------------------------------------------------------------- layout

function Pane({
  id,
  overline,
  title,
  intro,
  children,
  nav,
}: {
  id: StepId;
  overline: string;
  title: ReactNode;
  intro?: ReactNode;
  children?: ReactNode;
  nav: ReactNode;
}) {
  return (
    <section className="pane" aria-labelledby={`h-${id}`}>
      <div>
        <span className="t-overline">{overline}</span>
        <h1 id={`h-${id}`} tabIndex={-1}>
          {title}
        </h1>
        {intro && <p className="intro">{intro}</p>}
      </div>
      {children}
      <div className="nav">{nav}</div>
    </section>
  );
}

function Nav({
  back,
  next,
  nextLabel = 'Continue',
  disabled,
  loading,
  hint,
  type = 'button',
}: {
  back?: () => void;
  next?: () => void;
  nextLabel?: string;
  disabled?: boolean;
  loading?: boolean;
  hint?: ReactNode;
  type?: 'button' | 'submit';
}) {
  return (
    <>
      {hint && <span className="hint">{hint}</span>}
      {back && (
        <Button variant="ghost" onClick={back}>
          Back
        </Button>
      )}
      <Button variant="primary" size="lg" type={type} onClick={next} disabled={disabled} loading={loading}>
        {nextLabel}
        <ChevronRight className="i" aria-hidden="true" />
      </Button>
    </>
  );
}

// ----------------------------------------------------------------- steps

function Welcome({ go }: { go: (s: StepId) => void }) {
  return (
    <Pane
      id="welcome"
      overline="Welcome"
      title="Your private cinema is almost open."
      nav={<Nav next={() => go('tmdb')} nextLabel="Let’s begin" />}
    >
      <div className="welcome">
        <p className="intro">
          Six short steps and Gonema will be finding films for you. Keep your download client and one indexer at hand.
        </p>
        <img src={mascotWelcome} alt="Kernel, the Gonema gopher, waving from the projection booth" />
      </div>
      <ul className="checklist">
        {[
          [Film, 'Connect film metadata', 'A free TMDB key for posters and dates', '1 min'],
          [HardDrive, 'Choose where films live', 'Your library folder', '30 s'],
          [Sparkles, 'Pick a quality', 'A starting profile you can fine-tune later', '30 s'],
          [ArrowDownToLine, 'Connect an indexer and a client', 'Newznab or Torznab, and SABnzbd, NZBGet, qBittorrent, Deluge or Transmission', '2 min'],
          [Search, 'Add a film', 'And watch the first grab happen', '1 min'],
        ].map(([Icon, t, d, time]) => {
          const I = Icon as typeof Film;
          return (
            <li key={t as string}>
              <span className="ic" aria-hidden="true">
                <I className="i" />
              </span>
              <span>
                <b>{t as string}</b>
                {d as string}
              </span>
              <span className="t">{time as string}</span>
            </li>
          );
        })}
      </ul>
    </Pane>
  );
}

function Tmdb({ go, done }: { go: (s: StepId) => void; done: boolean }) {
  const qc = useQueryClient();
  const [key, setKey] = useState('');
  const [error, setError] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: (k: string) => setTmdbKey(k),
    onSuccess: () => {
      setKey('');
      void qc.invalidateQueries({ queryKey: screenKeys.tmdb });
      void qc.invalidateQueries({ queryKey: keys.status });
      void qc.invalidateQueries({ queryKey: onboardingQuery.queryKey });
    },
    onError: (e) => setError(errorText(e)),
  });
  const ok = done || save.isSuccess;
  const submit = (ev: FormEvent) => {
    ev.preventDefault();
    if (ok && !key.trim()) return go('folder');
    if (key.trim().length < 16) {
      setError('That is too short for a TMDB key. Paste the API Read Access Token (the long one).');
      return;
    }
    setError(null);
    save.mutate(key.trim());
  };
  return (
    <form onSubmit={submit} noValidate className="contents">
      <Pane
        id="tmdb"
        overline="Step 1 · Metadata"
        title="Connect film metadata"
        intro="Gonema uses The Movie Database for posters, cast, collections and release dates. The key is yours, free, and stays encrypted on this server."
        nav={<Nav type="submit" back={() => go('welcome')} loading={save.isPending} nextLabel={ok && !key ? 'Continue' : 'Check and continue'} />}
      >
        <div className="helper">
          <b>
            <Info className="i i-sm" aria-hidden="true" />
            How to get a key (2 minutes)
          </b>
          <ol>
            <li>Create a free account on themoviedb.org.</li>
            <li>Open Settings › API and request a “Developer” key. Any short description is fine.</li>
            <li>
              Copy the <b className="inline">API Read Access Token</b> (the long one) and paste it below.
            </li>
          </ol>
          <a
            className={buttonClass('secondary', 'sm', 'self-start')}
            href="https://www.themoviedb.org/settings/api"
            target="_blank"
            rel="noreferrer noopener"
          >
            Open TMDB API settings
            <ExternalLink className="i" aria-hidden="true" />
          </a>
        </div>
        <div className="form">
          <Field label="API Read Access Token" error={error}>
            {({ id, describedBy, invalid }) => (
              <Input
                id={id}
                mono
                type="password"
                autoComplete="off"
                icon={<Lock className="i" aria-hidden="true" />}
                aria-describedby={describedBy}
                invalid={invalid}
                placeholder={ok ? 'Saved · paste a new key to replace it' : 'eyJhbGciOi…'}
                value={key}
                onChange={(ev) => setKey(ev.target.value)}
              />
            )}
          </Field>
          {ok && (
            <span className="ok-line" role="status">
              <CircleCheck className="i" aria-hidden="true" />
              Key works. Posters and release dates now come from TMDB.
            </span>
          )}
        </div>
      </Pane>
    </form>
  );
}

function FolderStep({ go }: { go: (s: StepId) => void }) {
  const qc = useQueryClient();
  const roots = useQuery(rootFoldersQuery);
  const [path, setPath] = useState('');
  const [error, setError] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: (p: string) => addRootFolder(p),
    onSuccess: () => {
      setPath('');
      void qc.invalidateQueries({ queryKey: keys.rootFolders });
      void qc.invalidateQueries({ queryKey: onboardingQuery.queryKey });
    },
    onError: (e) => setError(errorText(e)),
  });
  const list = roots.data ?? [];
  const submit = (ev: FormEvent) => {
    ev.preventDefault();
    const p = path.trim();
    if (!p) {
      if (list.length) go('quality');
      else setError('Type the folder where films should live, as Gonema sees it (for example /data/movies).');
      return;
    }
    if (!p.startsWith('/') && !/^[A-Za-z]:\\/.test(p)) {
      setError('Use an absolute path as Gonema sees it, for example /data/movies.');
      return;
    }
    setError(null);
    add.mutate(p);
  };
  return (
    <form onSubmit={submit} noValidate className="contents">
      <Pane
        id="folder"
        overline="Step 2 · Library"
        title="Choose where films live"
        intro="Each film gets its own folder here. Gonema hardlinks finished torrents into it when it can, so nothing is stored twice."
        nav={<Nav type="submit" back={() => go('tmdb')} loading={add.isPending} nextLabel={path.trim() ? 'Add and continue' : 'Continue'} />}
      >
        <div className="form">
          {list.map((r) => (
            <div key={r.id} className="root-ok">
              <span className="t-mono-sm">
                <Folder className="i i-sm" aria-hidden="true" /> {r.path}
              </span>
              {r.totalSpace ? (
                <>
                  <ProgressBar
                    value={(r.totalSpace - (r.freeSpace ?? 0)) / r.totalSpace}
                    label={`${r.path} used`}
                    size="thin"
                    className="disk-bar"
                  />
                  <span className="t-caption t-muted">
                    {formatBytes(r.freeSpace)} free of {formatBytes(r.totalSpace)}
                  </span>
                </>
              ) : null}
              {r.accessible ? (
                <span className="ok-line">
                  <CircleCheck className="i" aria-hidden="true" />
                  Writable.
                </span>
              ) : (
                <span className="field-error">{r.error || 'Gonema cannot write in this folder.'}</span>
              )}
            </div>
          ))}
          <Field
            label={list.length ? 'Add another root folder' : 'Root folder'}
            aside={<span className="t-muted fw-400">as seen by Gonema</span>}
            error={error}
          >
            {({ id, describedBy, invalid }) => (
              <Input
                id={id}
                mono
                icon={<Folder className="i" aria-hidden="true" />}
                aria-describedby={describedBy}
                invalid={invalid}
                placeholder="/data/movies"
                value={path}
                onChange={(ev) => setPath(ev.target.value)}
              />
            )}
          </Field>
          <Notice tone="info" icon={<Server className="i" aria-hidden="true" />} title="Running in Docker?">
            Mount one parent folder (for example <code>/data</code>) into both Gonema and your download client, at the
            same path. If your client sees <code>/downloads</code> while Gonema sees <code>/data/downloads</code>, add a
            remote path mapping later in Settings.
          </Notice>
        </div>
      </Pane>
    </form>
  );
}

function QualityStep({ go }: { go: (s: StepId) => void }) {
  const qc = useQueryClient();
  const profiles = useQuery(profilesQuery);
  const roots = useQuery(rootFoldersQuery);
  const ui = useQuery(generalSettingsQuery);
  const list = profiles.data ?? [];
  const [chosen, setChosen] = useState<number | null>(null);
  const current = chosen ?? ui.data?.ui.defaultQualityProfileId ?? list[0]?.id ?? null;
  const save = useMutation({
    mutationFn: async (id: number) => {
      const cur = ui.data?.ui ?? (await qc.fetchQuery(generalSettingsQuery)).ui;
      return putUiPreferences({ ...cur, defaultQualityProfileId: id });
    },
    onSuccess: (_r, id) => {
      // the add dialogs start from the same profile
      const o = readAddOptions((roots.data ?? []).map((r) => r.id), list.map((p) => p.id));
      saveAddOptions({ ...o, qualityProfileId: id });
      void qc.invalidateQueries({ queryKey: generalSettingsQuery.queryKey });
      void qc.invalidateQueries({ queryKey: onboardingQuery.queryKey });
      go('indexer');
    },
  });
  return (
    <Pane
      id="quality"
      overline="Step 3 · Quality"
      title="How should your films look?"
      intro="Pick a starting point. Gonema grabs the first good copy, then keeps upgrading until it reaches the target. You can fine-tune everything in Settings."
      nav={
        <Nav
          back={() => go('folder')}
          next={() => current && save.mutate(current)}
          disabled={!current}
          loading={save.isPending}
        />
      }
    >
      <fieldset className="choice">
        <legend className="sr-only">Quality profile</legend>
        {list.map((p) => {
          const allowed = (p.entries ?? []).filter((e) => e.allowed);
          const floor = allowed.at(-1);
          const floorLabel = floor ? (floor.group || qualityName(floor.qualities?.at(-1))) : '—';
          return (
            <label key={p.id}>
              <input type="radio" name="profile" checked={current === p.id} onChange={() => setChosen(p.id)} />
              <b>
                {p.name} <span className="radio" aria-hidden="true" />
              </b>
              {`${allowed.length} ${allowed.length === 1 ? 'quality' : 'qualities'} accepted${p.upgradeAllowed ? ', upgrades until the cutoff' : ', no upgrades'}.`}
              <span className="qs">
                {floorLabel} → {p.upgradeAllowed ? p.cutoffLabel || 'stop' : 'stop'}
              </span>
            </label>
          );
        })}
      </fieldset>
      {save.isError && <span className="field-error">{errorText(save.error)}</span>}
      {!profiles.isPending && !list.length && (
        <Notice tone="missing" title="No quality profile yet.">
          Create one in Settings › Quality & scoring, then come back.
        </Notice>
      )}
      <Notice tone="info" icon={<Info className="i" aria-hidden="true" />} title="What this means in practice">
        Nothing below the profile’s lowest quality is accepted. If a better copy appears later, Gonema replaces the file
        and tells you why in the film’s history.
      </Notice>
    </Pane>
  );
}

function detectKind(url: string): 'newznab' | 'torznab' {
  return /torznab|jackett|prowlarr.*\/download|\/torznab/i.test(url) ? 'torznab' : 'newznab';
}

function IndexerStep({ go }: { go: (s: StepId) => void }) {
  const qc = useQueryClient();
  const existing = useQuery(indexersQuery);
  const [url, setUrl] = useState('');
  const [key, setKey] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [test, setTest] = useState<TestResult | null>(null);
  const [busy, setBusy] = useState(false);
  const kind = detectKind(url);
  const body = (): IndexerInput => {
    let base = url.trim();
    let apiPath = '/api';
    const m = /^(https?:\/\/.+?)(\/api\/?)$/i.exec(base);
    if (m) {
      base = m[1]!;
      apiPath = '/api';
    }
    let name = 'Indexer';
    try {
      name = new URL(base).hostname.replace(/^(www|api)\./, '');
    } catch {}
    return {
      kind,
      name,
      baseUrl: base,
      apiPath,
      apiKey: key.trim() || undefined,
      categories: [2000],
      enableRss: true,
      enableAutoSearch: true,
      enableInteractive: true,
    };
  };
  const submit = async (ev: FormEvent) => {
    ev.preventDefault();
    if (!url.trim() && existing.data?.length) return go('client');
    if (!/^https?:\/\/\S+$/i.test(url.trim())) {
      setError('Paste the indexer’s API URL, starting with http:// or https://.');
      return;
    }
    setError(null);
    setBusy(true);
    setTest(null);
    try {
      const r = await indexerApi.test(body());
      setTest(r);
      if (r.ok) {
        await indexerApi.create(body());
        await qc.invalidateQueries({ queryKey: lot3Keys.indexers });
        void qc.invalidateQueries({ queryKey: onboardingQuery.queryKey });
        setUrl('');
        setKey('');
      }
    } catch (e) {
      setTest({ ok: false, message: errorText(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <form onSubmit={(ev) => void submit(ev)} noValidate className="contents">
      <Pane
        id="indexer"
        overline="Step 4 · Indexer"
        title="Add an indexer"
        intro="Where Gonema looks for releases: any Newznab (Usenet) or Torznab (torrent) indexer. Paste its API URL and key."
        nav={
          <Nav
            type="submit"
            back={() => go('quality')}
            loading={busy}
            nextLabel={busy ? 'Testing…' : url.trim() ? 'Test and add' : 'Continue'}
            disabled={!url.trim() && !existing.data?.length}
            hint={
              <>
                <Info className="i i-sm" aria-hidden="true" />
                You can add more indexers later.
              </>
            }
          />
        }
      >
        <div className="form">
          {(existing.data ?? []).map((x) => (
            <span key={x.id} className="ok-line">
              <CircleCheck className="i" aria-hidden="true" />
              {x.name} is connected ({x.kind === 'torznab' ? 'Torznab' : 'Newznab'}).
            </span>
          ))}
          <Field label="API URL" hint={url.trim() ? `Detected: ${kind === 'torznab' ? 'Torznab · torrent' : 'Newznab · Usenet'}` : undefined} error={error}>
            {({ id, describedBy, invalid }) => (
              <Input
                id={id}
                mono
                type="url"
                icon={<Globe className="i" aria-hidden="true" />}
                aria-describedby={describedBy}
                invalid={invalid}
                placeholder="https://indexer.example/api"
                value={url}
                onChange={(ev) => {
                  setUrl(ev.target.value);
                  setTest(null);
                }}
              />
            )}
          </Field>
          <Field label="API key">
            {({ id, describedBy }) => (
              <Input
                id={id}
                mono
                type="password"
                autoComplete="off"
                icon={<Lock className="i" aria-hidden="true" />}
                aria-describedby={describedBy}
                value={key}
                onChange={(ev) => setKey(ev.target.value)}
              />
            )}
          </Field>
          <div aria-live="polite">
            {busy ? (
              <div className="input testing" role="status" aria-busy="true">
                <span className="t-body-sm t-secondary grow">Testing… asking for capabilities and a sample search</span>
              </div>
            ) : (
              <TestLine result={test} />
            )}
          </div>
        </div>
      </Pane>
    </form>
  );
}

function ClientStep({ go }: { go: (s: StepId) => void }) {
  const qc = useQueryClient();
  const existing = useQuery(clientsQuery);
  const [v, setV] = useState<Values>(clientInitial(null, 'qbittorrent'));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [test, setTest] = useState<TestResult | null>(null);
  const [busy, setBusy] = useState(false);
  const spec = clientSpec(v, null, false).filter((f) => !['removeCompleted', 'removeFailed', 'enabled', 'priority', 'urlBase', 'category'].includes(f.name));
  const pick = (kind: string) => {
    const label = CLIENT_KINDS.find((k) => k.value === kind)?.label ?? kind;
    setV({ ...v, kind, name: label, port: CLIENT_PORT[kind] });
    setTest(null);
  };
  const submit = async (ev: FormEvent) => {
    ev.preventDefault();
    if (!String(v.host ?? '').trim() && existing.data?.length) return go('film');
    const e = validate(spec, v);
    setErrors(e);
    if (Object.keys(e).length) return;
    const body = { ...toBody(spec, v), kind: v.kind, category: 'movies', enabled: true } as DownloadClientInput;
    setBusy(true);
    setTest(null);
    try {
      const r = await clientApi.test(body);
      setTest(r);
      if (r.ok) {
        await clientApi.create(body);
        await qc.invalidateQueries({ queryKey: lot3Keys.clients });
        void qc.invalidateQueries({ queryKey: onboardingQuery.queryKey });
        go('film');
      }
    } catch (err) {
      setTest({ ok: false, message: errorText(err) });
    } finally {
      setBusy(false);
    }
  };
  const label = CLIENT_KINDS.find((k) => k.value === v.kind)?.label ?? 'the client';
  return (
    <form onSubmit={(ev) => void submit(ev)} noValidate className="contents">
      <Pane
        id="client"
        overline="Step 5 · Download client"
        title="Connect your download client"
        intro="Gonema sends releases here and watches them until they’re ready to import."
        nav={
          <Nav
            type="submit"
            back={() => go('indexer')}
            loading={busy}
            nextLabel={busy ? 'Testing…' : String(v.host ?? '').trim() ? 'Test and continue' : 'Continue'}
            disabled={!String(v.host ?? '').trim() && !existing.data?.length}
            hint={
              <>
                <Info className="i i-sm" aria-hidden="true" />
                You can add more clients later.
              </>
            }
          />
        }
      >
        <div className="form wide">
          {(existing.data ?? []).map((c) => (
            <span key={c.id} className="ok-line">
              <CircleCheck className="i" aria-hidden="true" />
              {c.name} is connected.
            </span>
          ))}
          <div className="conn" role="group" aria-label="Client type">
            {CLIENT_KINDS.map((k) => {
              const Icon = k.value === 'sabnzbd' || k.value === 'nzbget' ? Newspaper : Magnet;
              return (
                <button key={k.value} type="button" aria-pressed={v.kind === k.value} onClick={() => pick(k.value)}>
                  <Icon className="i" aria-hidden="true" />
                  {k.label}
                </button>
              );
            })}
          </div>
          <FormFields spec={spec} values={v} onChange={(x) => { setV(x); setTest(null); }} errors={errors} />
          <div aria-live="polite">
            <TestLine result={test} />
            {test && !test.ok && v.kind === 'qbittorrent' && (
              <div className="fix">
                <b>Most likely fix</b>
                Recent qBittorrent versions generate a random password on first start. Find it in the container log (
                <code>docker logs qbittorrent</code>), or set one in qBittorrent › Options › Web UI, then test again.
              </div>
            )}
          </div>
          <span className="t-caption t-muted">
            Category <span className="t-mono-sm">movies</span> will be created in {label} for you.
          </span>
        </div>
      </Pane>
    </form>
  );
}

function FilmStep({ go, finish }: { go: (s: StepId) => void; finish: () => void }) {
  const qc = useQueryClient();
  const roots = useQuery(rootFoldersQuery);
  const profiles = useQuery(profilesQuery);
  const tmdb = useQuery(tmdbQuery);
  const queue = useQueue();
  const [q, setQ] = useState('');
  const [term, setTerm] = useState('');
  const lookup = useQuery({ ...lookupQuery(term), enabled: !!term && !!tmdb.data?.configured });
  const [added, setAdded] = useState<{ id: number; title: string; year: number; posterUrl: string | null } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: async (r: LookupResult) => {
      const o = readAddOptions((roots.data ?? []).map((x) => x.id), (profiles.data ?? []).map((x) => x.id));
      return addMovie({
        tmdbId: r.tmdbId,
        rootFolderId: o.rootFolderId,
        qualityProfileId: o.qualityProfileId,
        minimumAvailability: o.minimumAvailability,
        monitored: true,
        searchOnAdd: true,
        tags: [],
      });
    },
    onSuccess: (m) => {
      setAdded({ id: m.id, title: m.title, year: m.year, posterUrl: m.posterUrl });
      void qc.invalidateQueries({ queryKey: keys.library });
      void qc.invalidateQueries({ queryKey: keys.queue });
      void qc.invalidateQueries({ queryKey: onboardingQuery.queryKey });
    },
    onError: (e) => setError(errorText(e)),
  });
  const dl = added ? (queue.data ?? []).find((x) => x.movieId === added.id) : undefined;
  if (added) {
    return (
      <Pane
        id="film"
        overline="Step 6 · First screening"
        title={dl ? 'The first print is on its way.' : 'Your first film is in the programme.'}
        intro={
          dl
            ? `You added ${added.title}. Gonema picked the best release and sent it to ${dl.client}.`
            : `You added ${added.title}. Gonema is searching your indexers; it keeps trying on its own if nothing fits yet.`
        }
        nav={
          <>
            <span className="hint">You’ll see it on the home screen when it is ready to watch.</span>
            <Link className={buttonClass('secondary', 'lg')} to="/discover">
              Add more films
            </Link>
            <Button variant="primary" size="lg" onClick={finish}>
              Open my cinema
              <ChevronRight className="i" aria-hidden="true" />
            </Button>
          </>
        }
      >
        <div className="grabbed">
          <PosterArt className="big" src={added.posterUrl} title={added.title} year={added.year} />
          <div className="stack">
            <ol className="flow">
              <li>
                <span className="d" aria-hidden="true">
                  <Check className="i" />
                </span>
                <span>
                  <b>Added</b> to your library
                </span>
              </li>
              <li className={dl ? undefined : 'live'}>
                <span className="d" aria-hidden="true">
                  {dl ? <Check className="i" /> : <RefreshCw className="i" />}
                </span>
                <span>
                  <b>{dl ? 'Searched' : 'Searching'}</b> your indexers
                </span>
              </li>
              {dl && (
                <li>
                  <span className="d" aria-hidden="true">
                    <Check className="i" />
                  </span>
                  <span>
                    <b>Picked</b> <span className="t-mono-sm">{dl.title}</span>
                  </span>
                </li>
              )}
              {dl && (
                <li className="live">
                  <span className="d" aria-hidden="true">
                    <ArrowDownToLine className="i" />
                  </span>
                  <span>
                    <b>Downloading</b> {formatBytes(dl.size)}
                    {dl.speedBps ? ` · ${formatSpeed(dl.speedBps)}` : ''}
                  </span>
                  <span className="t">{formatEta(dl.etaSeconds)}</span>
                </li>
              )}
            </ol>
            {dl && (
              <ProgressBar value={dl.progress} label={`${added.title} download`} live />
            )}
          </div>
        </div>
      </Pane>
    );
  }
  const results = (lookup.data ?? []).slice(0, 5);
  return (
    <Pane
      id="film"
      overline="Step 6 · First screening"
      title="Add your first film"
      intro="Search TMDB for a film you want. Gonema adds it with the quality you picked and starts looking right away."
      nav={<Nav back={() => go('client')} next={finish} nextLabel="Skip and open my cinema" />}
    >
      {!tmdb.data?.configured ? (
        <Notice tone="missing" title="TMDB isn’t connected yet.">
          Searching for films needs the TMDB key from step 1. <button type="button" className="link" onClick={() => go('tmdb')}>Add it</button>
        </Notice>
      ) : (
        <div className="form">
          <form
            className="row add-root"
            role="search"
            onSubmit={(ev) => {
              ev.preventDefault();
              setTerm(q.trim());
            }}
          >
            <Field label="Film title">
              {({ id, describedBy }) => (
                <Input
                  id={id}
                  aria-describedby={describedBy}
                  icon={<Search className="i" aria-hidden="true" />}
                  value={q}
                  onChange={(ev) => setQ(ev.target.value)}
                  placeholder="The Third Man"
                />
              )}
            </Field>
            <Button type="submit" loading={lookup.isFetching}>
              Search
            </Button>
          </form>
          {error && <span className="field-error">{error}</span>}
          <ul className="hits" aria-live="polite">
            {results.map((r) => (
              <li key={r.tmdbId} className="search-hit">
                <PosterArt src={r.posterUrl ?? null} title={r.title} />
                <div>
                  <b>{r.title}</b>
                  <div className="t-caption t-muted">{r.year || 'Year unknown'}</div>
                </div>
                {r.inLibrary ? (
                  <StatusText status="available">In your library</StatusText>
                ) : (
                  <Button size="sm" variant="secondary" loading={add.isPending && add.variables?.tmdbId === r.tmdbId} onClick={() => add.mutate(r)}>
                    Add and search
                  </Button>
                )}
              </li>
            ))}
            {term && lookup.isSuccess && !results.length && <li className="t-secondary">Nothing on TMDB called “{term}”.</li>}
          </ul>
        </div>
      )}
    </Pane>
  );
}

// ----------------------------------------------------------------- screen

export function OnboardingScreen() {
  const navigate = useNavigate();
  const asked = route.useSearch().step;
  const { done, ready } = useDone();
  const step = currentStep(asked, done);
  const idx = STEPS.findIndex((s) => s.id === step);
  useEffect(() => {
    document.title = 'Setup · Gonema';
  }, []);
  useEffect(() => {
    // focus the new step's heading (keyboard and screen reader users land on it)
    if (asked) document.getElementById(`h-${step}`)?.focus({ preventScroll: true });
  }, [step, asked]);
  const go = (s: StepId) => void navigate({ to: '/setup', search: { step: s } });
  const qc = useQueryClient();
  const finish = () => {
    // "Skip for now" is remembered by the server (every browser); finishing needs nothing more
    setOnboardingDismissed(qc, true)
      .catch(() => {})
      .finally(() => void navigate({ to: '/' }));
  };
  const doneCount = STEPS.filter((s) => s.id !== 'welcome' && done[s.id]).length;
  const posters = useLibrary().data?.slice(0, 30) ?? [];
  const wall = useMemo(() => posters.filter((m) => m.posterUrl), [posters]);
  return (
    <div className="ob-page">
      {wall.length >= 10 && (
        <div className="atmos" aria-hidden="true">
          <div className="wall">
            {wall.map((m) => (
              <PosterArt key={m.id} src={m.posterUrl} title={m.title} />
            ))}
          </div>
        </div>
      )}
      <header className="ob-top">
        <span className="brand" role="img" aria-label="Gonema">
          <Mark />
          <Wordmark />
        </span>
        <span className="time">Setup · about 5 minutes</span>
        <div className="spacer" />
        <ThemeToggle />
        <Button variant="ghost" size="sm" onClick={finish}>
          Skip for now
        </Button>
      </header>
      <main className="ob glass-2" id="main" aria-label="Setup">
        <nav className="steps" aria-label="Setup steps">
          <ol>
            {STEPS.map((s, i) => {
              const d = s.id !== 'welcome' && done[s.id];
              return (
                <li key={s.id} className={d ? 'done' : undefined}>
                  <Link to="/setup" search={{ step: s.id }} aria-current={s.id === step ? 'step' : undefined}>
                    <span className="n" aria-hidden="true">
                      {d ? <Check className="i" /> : i + 1}
                    </span>
                    <span>
                      {s.label}
                      {d && <span className="sr-only">, done</span>}
                      {s.sub && <small>{s.sub}</small>}
                    </span>
                  </Link>
                </li>
              );
            })}
          </ol>
          <div className="foot">
            <ProgressBar value={doneCount / (STEPS.length - 1)} label="Setup progress" size="thin" className="ob-prog" />
            <span>
              {doneCount} of {STEPS.length - 1} done
            </span>{' '}
            · progress is saved, you can leave anytime.
          </div>
        </nav>
        {!ready && step !== 'welcome' ? (
          <section className="pane" aria-busy="true" />
        ) : step === 'welcome' ? (
          <Welcome go={go} />
        ) : step === 'tmdb' ? (
          <Tmdb go={go} done={done.tmdb!} />
        ) : step === 'folder' ? (
          <FolderStep go={go} />
        ) : step === 'quality' ? (
          <QualityStep go={go} />
        ) : step === 'indexer' ? (
          <IndexerStep go={go} />
        ) : step === 'client' ? (
          <ClientStep go={go} />
        ) : (
          <FilmStep go={go} finish={finish} />
        )}
        <span className="sr-only" aria-live="polite">
          Step {idx + 1} of {STEPS.length}: {STEPS[idx]?.label}
        </span>
      </main>
    </div>
  );
}

