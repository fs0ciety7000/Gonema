import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, renderHook, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type LibraryItem, toLibraryItem } from '@/api/model';
import { resetProgress } from '@/api/progress';
import { keys, type QueueItem } from '@/api/queries';
import { useMovieActions } from '@/components/movie/useMovieActions';
import { toast } from '@/components/ui/Toast';
import { chainFlags } from './activity/HistoryPanel';
import { groupQueue, staticOf } from './activity/QueuePanel';
import { addBody, readAddOptions } from './discover/addOptions';
import { personIdOf } from './discover/DiscoverTab';

// Router hooks are not under test here: links render as anchors, navigation is recorded.
const navigate = vi.fn();
vi.mock('@tanstack/react-router', async (orig) => {
  const mod = await orig<typeof import('@tanstack/react-router')>();
  return {
    ...mod,
    useNavigate: () => navigate,
    getRouteApi: () => ({ useSearch: () => ({}), useParams: () => ({}) }),
    Link: ({ children, className }: { children: ReactNode; className?: string }) => (
      <a className={className} href="#x">
        {children}
      </a>
    ),
  };
});

type Call = { method: string; url: string; body?: unknown };
function server(routes: Record<string, (c: Call) => unknown>) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      const text = await req.text();
      const call: Call = {
        method: req.method,
        url: `${u.pathname.replace(/^.*\/api\/v1/, '')}${u.search}`,
        body: text ? JSON.parse(text) : undefined,
      };
      calls.push(call);
      const key = Object.keys(routes).find((k) => {
        const [m, p] = k.split(' ');
        return m === req.method && call.url.split('?')[0] === p;
      });
      if (!key)
        return new Response('{"detail":"no route"}', {
          status: 404,
          headers: { 'Content-Type': 'application/problem+json' },
        });
      const body = routes[key]!(call);
      if (body === 204) return new Response(null, { status: 204 });
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }),
  );
  return calls;
}

function client() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return { qc, wrapper };
}

afterEach(() => {
  act(() => toast.clear());
  vi.unstubAllGlobals();
  navigate.mockReset();
  resetProgress();
});

describe('add a film', () => {
  it('adds with the chosen options, offers Open and an Undo that removes it again', async () => {
    const { useAddFilm } = await import('./discover/useAddFilm');
    const calls = server({
      'POST /movies': () => ({ id: 42, title: 'Continental Drift', year: 1998, collection: null }),
      'DELETE /movies/42': () => 204,
    });
    const show = vi.spyOn(toast, 'show');
    const { qc, wrapper } = client();
    qc.setQueryData(['lookup', 'drift'], [{ tmdbId: 900, title: 'Continental Drift', inLibrary: false }]);
    const { result } = renderHook(() => useAddFilm(), { wrapper });
    const options = {
      rootFolderId: 2,
      qualityProfileId: 3,
      minimumAvailability: 'released' as const,
      monitor: 'film' as const,
      tags: ['drama'],
      searchOnAdd: true,
    };
    await act(async () => {
      await result.current.add({ tmdbId: 900, title: 'Continental Drift', year: 1998 }, options);
    });
    expect(calls[0]?.body).toEqual({
      tmdbId: 900,
      rootFolderId: 2,
      qualityProfileId: 3,
      minimumAvailability: 'released',
      monitored: true,
      searchOnAdd: true,
      tags: ['drama'],
    });
    // the result now says "in library" and links to the new film
    expect(qc.getQueryData<Array<{ inLibrary: boolean; movieId?: number }>>(['lookup', 'drift'])?.[0]).toMatchObject({
      inLibrary: true,
      movieId: 42,
    });
    const t = show.mock.calls.at(-1)?.[0];
    expect(t?.title).toBe('Added Continental Drift (1998)');
    t?.action?.onClick();
    expect(navigate).toHaveBeenCalledWith({ to: '/films/$movieId', params: { movieId: 42 } });
    act(() => t?.undo?.());
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url === '/movies/42')).toBe(true));
    await waitFor(() =>
      expect(qc.getQueryData<Array<{ inLibrary: boolean }>>(['lookup', 'drift'])?.[0]?.inLibrary).toBe(false),
    );
  });

  it('remembers the last options but drops folders and profiles that no longer exist', () => {
    const store = {
      getItem: () =>
        JSON.stringify({
          rootFolderId: 9,
          qualityProfileId: 2,
          minimumAvailability: 'bogus',
          monitor: 'none',
          tags: ['a', 3],
        }),
    };
    expect(readAddOptions([1, 2], [1, 2], store)).toEqual({
      rootFolderId: 1,
      qualityProfileId: 2,
      minimumAvailability: 'released',
      monitor: 'none',
      tags: ['a'],
      searchOnAdd: true,
    });
    // "Don't monitor" never starts a search
    expect(addBody(5, readAddOptions([1], [1], store))).toMatchObject({ monitored: false, searchOnAdd: false });
  });
});

describe('bulk quality profile', () => {
  it('changes every film in one request and Undo sends back the previous settings from the response', async () => {
    const previous = [
      { id: 1, monitored: true, qualityProfileId: 4, minimumAvailability: 'released', rootFolderId: 1, tags: [] },
    ];
    const calls = server({
      'POST /movies/bulk': () => ({ changed: [1, 2], previous }),
      'POST /movies/bulk/restore': () => ({ changed: [1] }),
    });
    const { qc, wrapper } = client();
    qc.setQueryData<LibraryItem[]>(
      keys.library,
      [1, 2].map((id) => toLibraryItem({ id, title: `F${id}`, qualityProfileId: id === 1 ? 4 : 7 })),
    );
    const { result } = renderHook(() => useMovieActions(), { wrapper });
    await act(async () => {
      await result.current.setProfile(
        [
          { id: 1, title: 'F1' },
          { id: 2, title: 'F2' },
        ],
        7,
        'Cinema UHD',
        new Map([
          [1, 4],
          [2, 7],
        ]),
      );
    });
    expect(calls[0]).toMatchObject({ url: '/movies/bulk', body: { ids: [1, 2], set: { qualityProfileId: 7 } } });
    expect(qc.getQueryData<LibraryItem[]>(keys.library)!.map((m) => m.qualityProfileId)).toEqual([7, 7]);
    act(() => {
      expect(toast.undoLatest()).toBe(true);
    });
    expect(qc.getQueryData<LibraryItem[]>(keys.library)!.map((m) => m.qualityProfileId)).toEqual([4, 7]);
    await waitFor(() => expect(calls.find((c) => c.url === '/movies/bulk/restore')?.body).toEqual({ previous }));
  });
});

describe('queue', () => {
  const item = (id: number, status: QueueItem['status'], o: Partial<QueueItem> = {}) =>
    ({
      id,
      movieId: id * 10,
      title: `Film.${id}.2020.1080p.WEB-DL-GRP`,
      status,
      progress: 0.4,
      speedBps: 1e6,
      etaSeconds: 60,
      downloadedBytes: 1,
      size: 1e9,
      quality: 'webdl-1080p',
      client: 'SABnzbd',
      indexer: 'AltReel',
      movie: { id: id * 10, title: `Film ${id}`, year: 2020, posterUrl: null, dominantColor: null },
      retry: { attempt: 1, of: 6, previousDownloadId: 0, previousReason: '', previousRelease: '' },
      actions: {
        pause: status === 'downloading',
        resume: status === 'paused',
        remove: true,
        retryImport: status === 'warning',
      },
      messages: [],
      ...o,
    }) as QueueItem;

  it('strips the live fields so progress updates keep the list stable, and groups by state', () => {
    const a = staticOf([item(1, 'downloading')]);
    const b = staticOf([{ ...item(1, 'downloading'), progress: 0.9, speedBps: 5 }]);
    expect(a).toEqual(b);
    const g = groupQueue(staticOf([item(1, 'downloading'), item(2, 'failed'), item(3, 'paused'), item(4, 'queued')]));
    expect(g.map((x) => [x.id, x.items.map((i) => i.id)])).toEqual([
      ['downloading', [1, 4]],
      ['attention', [2]],
      ['waiting', [3]],
    ]);
  });

  it('remove hides the row at once; Undo brings it back and nothing is sent', async () => {
    const calls = server({
      'GET /queue': () => [item(1, 'downloading'), item(2, 'failed', { failedReason: 'CRC error in part 37.' })],
      'GET /settings/acquisition': () => ({ maxAttemptsPerDay: 6, rssIntervalMin: 15 }),
      'DELETE /queue/1': () => 204,
    });
    const { wrapper } = client();
    const { QueuePanel } = await import('./activity/QueuePanel');
    render(<QueuePanel />, { wrapper });
    const row = (await screen.findByRole('link', { name: 'Film 1' })).closest('li')!;
    expect(screen.getByText(/CRC error in part 37/)).toBeTruthy();
    await userEvent.click(within(row).getByRole('button', { name: 'Remove Film 1…' }));
    const pop = await screen.findByRole('dialog', { name: 'Remove Film 1' });
    expect(
      (within(pop).getByRole('checkbox', { name: /delete the downloaded data/i }) as HTMLInputElement).checked,
    ).toBe(false);
    await userEvent.click(within(pop).getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(screen.queryByRole('link', { name: 'Film 1' })).toBeNull());
    act(() => {
      expect(toast.undoLatest()).toBe(true);
    });
    await screen.findByRole('link', { name: 'Film 1' });
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });
});

describe('helpers', () => {
  it('reads a TMDB person id from a link, a prefix or a number', () => {
    expect(personIdOf('https://www.themoviedb.org/person/1245-ines-calloway')).toBe(1245);
    expect(personIdOf('tmdb:77')).toBe(77);
    expect(personIdOf('31')).toBe(31);
    expect(personIdOf('Ines Calloway')).toBeNull();
  });
  it('links history rows of the same download into a chain', () => {
    const rows = [
      { chainId: 3, downloadId: 3 },
      { chainId: 3, downloadId: 3 },
      { chainId: null, downloadId: null },
      { chainId: 8, downloadId: 8 },
    ];
    expect(chainFlags(rows)).toEqual([true, true, false, false]);
  });
});
