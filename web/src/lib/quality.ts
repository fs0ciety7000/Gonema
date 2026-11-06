/* Quality ids come from internal/quality ("webdl-2160p"). The badge shows the
   resolution and the source as in the design ("2160p" + "WEB-DL"). */

const SOURCE: Record<string, string> = {
  webdl: 'WEB-DL',
  webrip: 'WEBRip',
  bluray: 'Bluray',
  remux: 'Remux',
  brdisk: 'BR-Disk',
  hdtv: 'HDTV',
  dvd: 'DVD',
  sdtv: 'SDTV',
  cam: 'CAM',
  telesync: 'TS',
  telecine: 'TC',
  workprint: 'Workprint',
  dvdscr: 'DVDSCR',
  regional: 'R5',
};

export type QualityParts = { res: string; source: string; uhd: boolean };

export function parseQuality(id: string | null | undefined): QualityParts | null {
  if (!id) return null;
  const m = /^([a-z0-9]+)-(\d{3,4}p)$/i.exec(id.trim());
  if (!m) {
    const res = /(\d{3,4}p)/.exec(id)?.[1] ?? id;
    return { res, source: '', uhd: res === '2160p' };
  }
  const src = m[1]!.toLowerCase();
  const res = m[2]!.toLowerCase();
  return { res, source: SOURCE[src] ?? m[1]!, uhd: res === '2160p' };
}

/** Rank used by the "Quality" sort: resolution first, then source. */
export function qualityRank(id: string | null | undefined): number {
  const q = parseQuality(id);
  if (!q) return -1;
  const res = Number.parseInt(q.res, 10) || 0;
  const order = ['SDTV', 'DVD', 'HDTV', 'WEBRip', 'WEB-DL', 'Bluray', 'Remux', 'BR-Disk'];
  return res * 10 + Math.max(0, order.indexOf(q.source));
}

/** Display name used in sentences: "WEBDL-2160p". */
export function qualityName(id: string | null | undefined): string {
  const q = parseQuality(id);
  if (!q) return '';
  return `${q.source.replace('-', '')}-${q.res}`;
}
