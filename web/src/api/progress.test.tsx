import { QueryClient } from '@tanstack/react-query';
import { act, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { handleServerEvent } from './live';
import { getProgress, type Progress, resetProgress, seedProgress, setProgress, useProgress } from './progress';
import { keys, type QueueItem } from './queries';

const FALLBACK: Progress = { progress: 0, speedBps: 0, etaSeconds: null, status: 'queued' };

function Row({ id, onRender }: { id: number; onRender: (id: number, p: Progress) => void }) {
  const p = useProgress(id, FALLBACK);
  onRender(id, p);
  return <span data-testid={`row-${id}`}>{Math.round(p.progress * 100)}%</span>;
}

describe('queue progress store', () => {
  afterEach(() => resetProgress());

  it('re-renders only the row whose download moved', () => {
    seedProgress([
      { id: 1, progress: 0.1, speedBps: 1, etaSeconds: 60, status: 'downloading' },
      { id: 2, progress: 0.5, speedBps: 1, etaSeconds: 60, status: 'downloading' },
    ]);
    const renders: number[] = [];
    const view = render(
      <>
        <Row id={1} onRender={(id) => renders.push(id)} />
        <Row id={2} onRender={(id) => renders.push(id)} />
      </>,
    );
    renders.length = 0;
    act(() => setProgress(2, { progress: 0.6 }));
    expect(renders).toEqual([2]);
    expect(view.getByTestId('row-2').textContent).toBe('60%');
    expect(view.getByTestId('row-1').textContent).toBe('10%');
  });

  it('does not notify when nothing changed, and merges partial updates', () => {
    setProgress(3, { progress: 0.2, speedBps: 5, etaSeconds: 10, status: 'downloading' });
    const renders: number[] = [];
    render(<Row id={3} onRender={(id) => renders.push(id)} />);
    renders.length = 0;
    act(() => setProgress(3, { progress: 0.2 }));
    expect(renders).toEqual([]);
    act(() => setProgress(3, { status: 'paused' }));
    expect(getProgress(3)).toEqual({ progress: 0.2, speedBps: 5, etaSeconds: 10, status: 'paused' });
  });
});

describe('download.progress events', () => {
  afterEach(() => resetProgress());
  const item = (id: number, o: Partial<QueueItem> = {}) =>
    ({
      id,
      movieId: id * 10,
      title: `R${id}`,
      status: 'downloading',
      progress: 0,
      speedBps: 0,
      etaSeconds: null,
      ...o,
    }) as QueueItem;

  it('merges the compact payload into the cached item and the store', () => {
    const qc = new QueryClient();
    qc.setQueryData(keys.queue, [item(1), item(2)]);
    handleServerEvent(qc, { id: 1, type: 'download.progress', data: { id: 2, progress: 0.42, speedBps: 9 } }, () => {});
    const q = qc.getQueryData<QueueItem[]>(keys.queue)!;
    expect(q[1]).toMatchObject({ id: 2, title: 'R2', progress: 0.42, speedBps: 9 });
    expect(q[0]).toBe(qc.getQueryData<QueueItem[]>(keys.queue)![0]);
    expect(getProgress(2)?.progress).toBe(0.42);
  });

  it('refetches the queue for a download it does not know, and the library when a status changes', () => {
    const qc = new QueryClient();
    qc.setQueryData(keys.queue, [item(1)]);
    const inv = vi.spyOn(qc, 'invalidateQueries');
    const lib = vi.fn();
    handleServerEvent(qc, { id: 1, type: 'download.progress', data: { id: 99, progress: 0.1 } }, lib);
    expect(inv).toHaveBeenCalledWith({ queryKey: keys.queue });
    expect(lib).not.toHaveBeenCalled();
    handleServerEvent(qc, { id: 2, type: 'download.progress', data: { id: 1, status: 'importing' } }, lib);
    expect(lib).toHaveBeenCalledTimes(1);
  });
});
