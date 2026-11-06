const nf = new Intl.NumberFormat('en-US');
const dateFmt = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
const dayMonth = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' });
const hm = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false });

export const formatNumber = (n: number) => nf.format(n);

/** Decimal units, one decimal from GB up: "21.7 GB", "9.7 TB", "640 MB". */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes <= 0) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  let v = bytes;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${i >= 3 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export const formatSpeed = (bps: number) => (bps > 0 ? `${formatBytes(bps)}/s` : '');

export function formatEta(seconds: number | null | undefined): string {
  if (seconds == null || seconds < 0) return '';
  if (seconds < 60) return '< 1 min';
  const m = Math.round(seconds / 60);
  if (m < 60) return `${m} min`;
  return `${Math.floor(m / 60)} h ${m % 60} min`;
}

export function formatRuntime(min: number): string {
  if (!min) return '';
  const h = Math.floor(min / 60);
  const m = min % 60;
  return h ? `${h} h ${m} min` : `${m} min`;
}

/** "3 Nov 2026" */
export function formatDate(d: Date | string | number | null | undefined): string {
  if (d == null || d === '') return '—';
  const date = d instanceof Date ? d : new Date(typeof d === 'string' && d.length === 10 ? `${d}T00:00:00` : d);
  return Number.isNaN(date.getTime()) ? '—' : dateFmt.format(date);
}

/** "3 Nov · 21:14" (year added when it differs from the current one). */
export function formatWhen(d: Date | string | number): string {
  const date = d instanceof Date ? d : new Date(d);
  if (Number.isNaN(date.getTime())) return '';
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return `${sameYear ? dayMonth.format(date) : dateFmt.format(date)} · ${hm.format(date)}`;
}

export const formatClock = (d: Date) => hm.format(d);

/** Scores use a true minus sign: "+70", "−15", "0". */
export function formatScore(n: number): string {
  if (n > 0) return `+${n}`;
  if (n < 0) return `−${Math.abs(n)}`;
  return '0';
}

export function formatVotes(n: number): string {
  if (n >= 10_000) return `${(n / 1000).toFixed(1).replace(/\.0$/, '')}k`;
  return nf.format(n);
}
