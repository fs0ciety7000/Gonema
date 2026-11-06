import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type LibraryItem, toLibraryItem } from '@/api/model';
import { keys } from '@/api/queries';
import { toast } from '@/components/ui/Toast';
import { useMovieActions } from './useMovieActions';

function setup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const lib: LibraryItem[] = [1, 2].map((id) =>
    toLibraryItem({ id, title: `Film ${id}`, monitored: true, state: 'available' }),
  );
  qc.setQueryData(keys.library, lib);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return { qc, wrapper };
}

const monitored = (qc: QueryClient) => qc.getQueryData<LibraryItem[]>(keys.library)!.map((m) => m.monitored);
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json' },
  });

describe('useMovieActions', () => {
  afterEach(() => {
    act(() => toast.clear());
    vi.unstubAllGlobals();
  });

  it('applies optimistically, then offers Undo that restores the previous value', async () => {
    const calls: unknown[] = [];
    let release!: () => void;
    const gate = new Promise<void>((r) => {
      release = r;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (req: Request) => {
        const body = await req.json();
        calls.push(body);
        if (calls.length === 1) await gate;
        return json({ id: Number(req.url.split('/').pop()), monitored: body.monitored });
      }),
    );
    const { qc, wrapper } = setup();
    const { result } = renderHook(() => useMovieActions(), { wrapper });
    let done!: Promise<void>;
    act(() => {
      done = result.current.setMonitored([{ id: 1, title: 'Film 1' }], false);
    });
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    expect(monitored(qc)).toEqual([false, true]); // applied before the server answered
    release();
    await act(async () => {
      await done;
    });
    act(() => {
      expect(toast.undoLatest()).toBe(true);
    });
    await vi.waitFor(() => expect(calls).toEqual([{ monitored: false }, { monitored: true }]));
    expect(monitored(qc)).toEqual([true, true]);
  });

  it('rolls back the cache and reports an error when the server refuses', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ title: 'Unprocessable', detail: 'Quality profile 9 does not exist.' }, 422)),
    );
    const show = vi.spyOn(toast, 'show');
    const { qc, wrapper } = setup();
    const { result } = renderHook(() => useMovieActions(), { wrapper });
    let ok: boolean | undefined;
    await act(async () => {
      ok = await result.current.patch([1, 2], { monitored: false });
    });
    expect(ok).toBe(false);
    expect(monitored(qc)).toEqual([true, true]);
    expect(show).toHaveBeenCalledWith(
      expect.objectContaining({ tone: 'error', body: 'Quality profile 9 does not exist.' }),
    );
  });
});
