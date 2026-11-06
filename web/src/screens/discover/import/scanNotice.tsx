import { Link } from '@tanstack/react-router';
import { ScanSearch } from 'lucide-react';
import { toast } from '@/components/ui/Toast';
import { formatNumber } from '@/lib/format';

/** A scan ended while its page was not open: say so, with the way back (the scan step's promise). */
export function notifyScanFinished(data: unknown) {
  const s = data as { id?: unknown; status?: unknown; done?: unknown };
  if (typeof s?.id !== 'number' || s.status !== 'done') return;
  const here = new URLSearchParams(location.search);
  if (location.pathname.endsWith('/discover') && here.get('tab') === 'import' && here.get('scan') === String(s.id))
    return;
  const n = typeof s.done === 'number' ? s.done : 0;
  toast.show({
    title: 'Folders ready to review',
    body: (
      <>
        Gonema read {formatNumber(n)} {n === 1 ? 'folder' : 'folders'}; nothing was changed.{' '}
        <Link to="/discover" search={{ tab: 'import', scan: s.id }}>
          Review them
        </Link>
      </>
    ),
    icon: <ScanSearch className="i" />,
    duration: 12_000,
  });
}
