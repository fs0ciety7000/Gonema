import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type LibraryItem, toLibraryItem } from '@/api/model';
import { keys } from '@/api/queries';
import { toast } from '@/components/ui/Toast';
import { closeMovieDialog, openMovieDialog } from '../dialogStore';
import MovieDialogs, { tagStates } from './MovieDialogs';

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
      const body = key ? routes[key]!(call) : { title: 'Not Found', detail: 'no route' };
      if (body === 204) return new Response(null, { status: 204 });
      return new Response(JSON.stringify(body), {
        status: key ? 200 : 404,
        headers: { 'Content-Type': key ? 'application/json' : 'application/problem+json' },
      });
    }),
  );
  return calls;
}

function setup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const lib: LibraryItem[] = [
    toLibraryItem({ id: 5, title: 'Halcyon Drift', monitored: true, state: 'available', tagIds: [1] }),
    toLibraryItem({ id: 6, title: 'Ninefold', monitored: true, state: 'missing', tagIds: [] }),
  ];
  qc.setQueryData(keys.library, lib);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  render(<MovieDialogs />, { wrapper });
  return { qc };
}

afterEach(() => {
  act(() => {
    closeMovieDialog();
    toast.clear();
  });
  vi.unstubAllGlobals();
});

describe('delete file (confirmation dialog)', () => {
  it('explains the recycle bin and sends nothing until confirmed', async () => {
    const calls = server({
      'GET /import/settings': () => ({ recycleRetentionDays: 30 }),
      'DELETE /movies/5/file': () => ({
        movieId: 5,
        bin: '/data/.gonema-recycle',
        recycled: ['Halcyon Drift (2024)'],
        missing: [],
        notes: [],
      }),
    });
    const show = vi.spyOn(toast, 'show');
    setup();
    act(() => openMovieDialog({ kind: 'deleteFile', target: { id: 5, title: 'Halcyon Drift' } }));
    const dialog = await screen.findByRole('dialog', { name: /Delete the file of Halcyon Drift/ });
    await waitFor(() => expect(dialog.textContent).toMatch(/recycle bin.*30 days.*can be restored/));
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);

    act(() => openMovieDialog({ kind: 'deleteFile', target: { id: 5, title: 'Halcyon Drift' } }));
    await userEvent.click(await screen.findByRole('button', { name: 'Move file to recycle bin' }));
    await waitFor(() => expect(calls.find((c) => c.method === 'DELETE')?.url).toBe('/movies/5/file?confirm=true'));
    await waitFor(() =>
      expect(show).toHaveBeenCalledWith(
        expect.objectContaining({
          title: 'Halcyon Drift: file moved to the recycle bin',
          action: expect.objectContaining({ label: 'Restore' }),
        }),
      ),
    );
  });
});

describe('remove from library', () => {
  it('keeping the files applies at once with Undo, and Undo sends nothing', async () => {
    const calls = server({
      'GET /import/settings': () => ({ recycleRetentionDays: 30 }),
      'DELETE /movies/5': () => 204,
    });
    const { qc } = setup();
    act(() => openMovieDialog({ kind: 'remove', targets: [{ id: 5, title: 'Halcyon Drift' }] }));
    await userEvent.click(await screen.findByRole('button', { name: 'Remove from library' }));
    expect(qc.getQueryData<LibraryItem[]>(keys.library)!.map((m) => m.id)).toEqual([6]);
    act(() => {
      expect(toast.undoLatest()).toBe(true);
    });
    expect(
      qc
        .getQueryData<LibraryItem[]>(keys.library)!
        .map((m) => m.id)
        .sort(),
    ).toEqual([5, 6]);
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });

  it('moving the files to the recycle bin needs the explicit choice and says so on the button', async () => {
    const calls = server({
      'GET /import/settings': () => ({ recycleRetentionDays: 14 }),
      'DELETE /movies/5': () => 204,
    });
    setup();
    act(() => openMovieDialog({ kind: 'remove', targets: [{ id: 5, title: 'Halcyon Drift' }] }));
    await userEvent.click(await screen.findByRole('radio', { name: 'Also move files to recycle bin' }));
    const btn = await screen.findByRole('button', { name: 'Remove and move files to recycle bin' });
    expect(screen.getByRole('dialog').textContent).toMatch(/14 days/);
    await userEvent.click(btn);
    await waitFor(() =>
      expect(calls.find((c) => c.method === 'DELETE')?.url).toBe('/movies/5?deleteFiles=true&confirm=true'),
    );
  });
});

describe('tags', () => {
  it('computes all / some / none for the selection', () => {
    const lib = [
      { id: 1, tagIds: [1, 2] },
      { id: 2, tagIds: [1] },
    ];
    const s = tagStates([1, 2], lib, [
      { id: 1, label: 'a', movies: 2 },
      { id: 2, label: 'b', movies: 1 },
      { id: 3, label: 'c', movies: 0 },
    ]);
    expect([...s.values()]).toEqual(['all', 'some', 'none']);
  });

  it('assigns in one bulk request; Undo restores the previous settings the server returned', async () => {
    const previous = [
      {
        id: 5,
        monitored: true,
        qualityProfileId: 1,
        minimumAvailability: 'released',
        rootFolderId: 1,
        tags: ['drama'],
      },
      { id: 6, monitored: true, qualityProfileId: 1, minimumAvailability: 'released', rootFolderId: 1, tags: [] },
    ];
    const calls = server({
      'GET /tags': () => [
        { id: 1, label: 'drama', movies: 1 },
        { id: 2, label: 'kids', movies: 0 },
      ],
      'POST /movies/bulk': () => ({ changed: [5, 6], previous }),
      'POST /movies/bulk/restore': () => ({ changed: [5, 6] }),
    });
    setup();
    act(() =>
      openMovieDialog({
        kind: 'tags',
        targets: [
          { id: 5, title: 'Halcyon Drift' },
          { id: 6, title: 'Ninefold' },
        ],
      }),
    );
    const kids = await screen.findByRole('checkbox', { name: /kids/ });
    const drama = screen.getByRole('checkbox', { name: /drama/ }) as HTMLInputElement;
    expect(drama.indeterminate).toBe(true);
    await userEvent.click(kids);
    await userEvent.click(screen.getByRole('button', { name: 'Apply 1 change' }));
    await waitFor(() =>
      expect(calls.find((c) => c.url === '/movies/bulk')?.body).toEqual({
        ids: [5, 6],
        set: { addTags: ['kids'], removeTags: [] },
      }),
    );
    await waitFor(() => expect(toast.undoLatest()).toBe(true));
    await waitFor(() => expect(calls.find((c) => c.url === '/movies/bulk/restore')?.body).toEqual({ previous }));
  });
});
