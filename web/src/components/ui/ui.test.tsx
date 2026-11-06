import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { QualityBadge, ScorePill, StatusBadge } from './Badges';
import { Button, IconButton } from './Button';
import { Checkbox } from './Checkbox';
import { ProgressBar } from './Progress';
import { Segmented } from './Segmented';
import { Switch } from './Switch';
import { Tabs } from './Tabs';
import { TOAST_DURATION, toast } from './Toast';
import { Toaster } from './Toaster';

function SwitchHarness() {
  const [on, setOn] = useState(false);
  return (
    <Switch checked={on} onCheckedChange={setOn}>
      Monitored
    </Switch>
  );
}

function SegmentedHarness() {
  const [v, setV] = useState<'a' | 'b' | 'c'>('a');
  return (
    <Segmented
      label="Density"
      value={v}
      onChange={setV}
      options={[
        { value: 'a', label: 'A' },
        { value: 'b', label: 'B' },
        { value: 'c', label: 'C' },
      ]}
    />
  );
}

function TabsHarness() {
  const [v, setV] = useState<'x' | 'y' | 'z'>('x');
  return (
    <Tabs
      label="Views"
      panelId="p"
      value={v}
      onChange={setV}
      items={[
        { value: 'x', label: 'X' },
        { value: 'y', label: 'Y' },
        { value: 'z', label: 'Z' },
      ]}
    />
  );
}

describe('Button', () => {
  it('keeps its label while loading (aria-busy, no spinner)', () => {
    render(<Button loading>Save</Button>);
    const b = screen.getByRole('button', { name: 'Save' });
    expect(b).toHaveAttribute('aria-busy', 'true');
    expect(b).toHaveClass('btn', 'btn-secondary');
  });
  it('icon buttons always have a name', () => {
    render(<IconButton label="Search">x</IconButton>);
    expect(screen.getByRole('button', { name: 'Search' })).toBeInTheDocument();
  });
});

describe('Status and data', () => {
  it('status badge carries an icon and a label (never colour only)', () => {
    const { container } = render(<StatusBadge status="failed" />);
    expect(screen.getByText('Failed')).toBeInTheDocument();
    expect(container.querySelector('svg[aria-hidden="true"]')).not.toBeNull();
    expect(container.firstChild).toHaveAttribute('data-status', 'failed');
  });
  it('quality badge splits resolution and source, inverts 2160p', () => {
    const { container } = render(<QualityBadge quality="webdl-2160p" />);
    expect(container.firstChild).toHaveClass('uhd');
    expect(screen.getByText('2160p')).toBeInTheDocument();
    expect(screen.getByText('WEB-DL')).toBeInTheDocument();
  });
  it('score pill uses a true minus sign', () => {
    render(<ScorePill score={-15} />);
    expect(screen.getByText('−15')).toHaveClass('score', 'neg');
  });
  it('progress exposes progressbar semantics', () => {
    render(<ProgressBar value={0.42} label="Download" live />);
    const p = screen.getByRole('progressbar', { name: 'Download' });
    expect(p).toHaveAttribute('aria-valuenow', '42');
    expect(p).toHaveClass('live');
  });
});

describe('Controls', () => {
  it('switch toggles with click and keyboard', async () => {
    const user = userEvent.setup();
    render(<SwitchHarness />);
    const sw = screen.getByRole('switch', { name: 'Monitored' });
    await user.click(sw);
    expect(sw).toHaveAttribute('aria-checked', 'true');
    await user.keyboard(' ');
    expect(sw).toHaveAttribute('aria-checked', 'false');
  });
  it('checkbox can be indeterminate', () => {
    render(<Checkbox checked={false} indeterminate aria-label="All" />);
    expect((screen.getByRole('checkbox', { name: 'All' }) as HTMLInputElement).indeterminate).toBe(true);
  });
  it('segmented: arrows move and select (roving tabindex)', async () => {
    const user = userEvent.setup();
    render(<SegmentedHarness />);
    const a = screen.getByRole('radio', { name: 'A' });
    expect(a).toHaveAttribute('tabindex', '0');
    a.focus();
    await user.keyboard('{ArrowRight}');
    const b = screen.getByRole('radio', { name: 'B' });
    expect(b).toHaveAttribute('aria-checked', 'true');
    expect(b).toHaveFocus();
    await user.keyboard('{ArrowLeft}{ArrowLeft}');
    expect(screen.getByRole('radio', { name: 'C' })).toHaveAttribute('aria-checked', 'true');
  });
  it('tabs: arrows, Home and End', async () => {
    const user = userEvent.setup();
    render(<TabsHarness />);
    screen.getByRole('tab', { name: 'X' }).focus();
    await user.keyboard('{End}');
    expect(screen.getByRole('tab', { name: 'Z' })).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{Home}{ArrowRight}');
    expect(screen.getByRole('tab', { name: 'Y' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: 'Y' })).toHaveAttribute('aria-controls', 'p');
  });
});

describe('Toast with Undo', () => {
  afterEach(() => {
    act(() => toast.clear());
    vi.useRealTimers();
  });

  it('runs Undo from the button and from Ctrl+Z', async () => {
    const undo = vi.fn();
    render(<Toaster />);
    act(() => void toast.show({ title: 'Stopped monitoring X', undo }));
    expect(screen.getByRole('status')).toHaveTextContent('Stopped monitoring X');
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    expect(undo).toHaveBeenCalledTimes(1);
    act(() => void toast.show({ title: 'Again', undo }));
    fireEvent.keyDown(window, { key: 'z', ctrlKey: true });
    expect(undo).toHaveBeenCalledTimes(2);
  });

  it('expires after 8 s, pauses while hovered, and commits deferred actions on close', () => {
    vi.useFakeTimers();
    const onClose = vi.fn();
    render(<Toaster />);
    act(() => void toast.show({ title: 'Removed', undo: () => {}, onClose }));
    const t = screen.getByRole('status');
    fireEvent.mouseEnter(t);
    act(() => void vi.advanceTimersByTime(TOAST_DURATION + 1000));
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.mouseLeave(t);
    act(() => void vi.advanceTimersByTime(TOAST_DURATION + 10));
    expect(onClose).toHaveBeenCalledWith('timeout');
  });

  it('errors are announced as alerts', () => {
    render(<Toaster />);
    act(() => void toast.error('Nothing was changed'));
    expect(screen.getByRole('alert')).toHaveTextContent('Nothing was changed');
  });
});
