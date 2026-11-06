// Performance measurements on the demo library (10,000 films), real server.
//   node e2e/perf.mjs            -> prints a JSON report and writes e2e/perf.json
// Measures: initial JS/CSS (gzip) of shell + library route, time to the first poster,
// layout shift, and frame times while scrolling the grid and the table (rAF sampling),
// at CPU 1x and 4x slowdown (CDP throttling), with the pointer resting on a poster.
import { readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { BASE, ensureServer, launch, openPage } from './helpers.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const dist = join(here, '..', 'dist');

function bundleSizes(_page, urls) {
  const out = { files: {}, jsGzip: 0, cssGzip: 0 };
  for (const u of urls) {
    const f = u.split('/assets/')[1];
    if (!f) continue;
    const buf = readFileSync(join(dist, 'assets', f));
    const gz = gzipSync(buf, { level: 9 }).length;
    out.files[f] = { raw: buf.length, gzip: gz };
    if (f.endsWith('.js')) out.jsGzip += gz;
    if (f.endsWith('.css')) out.cssGzip += gz;
  }
  return out;
}

async function scrollFrames(page, seconds, step) {
  return page.evaluate(
    async ([seconds, step]) => {
      const times = [];
      let last = performance.now();
      const end = last + seconds * 1000;
      const maxY = document.documentElement.scrollHeight - innerHeight;
      await new Promise((resolve) => {
        const tick = (t) => {
          times.push(t - last);
          last = t;
          window.scrollBy(0, step);
          if (scrollY >= maxY - 10) window.scrollTo(0, 0);
          if (t < end) requestAnimationFrame(tick);
          else resolve();
        };
        requestAnimationFrame(tick);
      });
      times.shift();
      const sorted = [...times].sort((a, b) => a - b);
      const avg = times.reduce((a, b) => a + b, 0) / times.length;
      const p = (q) => sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))];
      return {
        frames: times.length,
        fps: Math.round((1000 / avg) * 10) / 10,
        avgMs: Math.round(avg * 100) / 100,
        p95Ms: Math.round(p(0.95) * 100) / 100,
        p99Ms: Math.round(p(0.99) * 100) / 100,
        maxMs: Math.round(sorted.at(-1) * 100) / 100,
        over20ms: times.filter((x) => x > 20).length,
      };
    },
    [seconds, step],
  );
}

async function run() {
  await ensureServer();
  const browser = await launch();
  const report = { base: BASE, date: new Date().toISOString(), runs: [] };

  // ---- first load (cold cache)
  const page = await openPage(browser);
  const assets = [];
  page.on('response', (r) => {
    if (r.url().includes('/assets/')) assets.push({ url: r.url(), t: Date.now() });
  });
  await page.addInitScript(() => {
    window.__cls = 0;
    new PerformanceObserver((l) => {
      for (const e of l.getEntries()) if (!e.hadRecentInput) window.__cls += e.value;
    }).observe({ type: 'layout-shift', buffered: true });
  });
  await page.goto(`${BASE}/films`);
  await page.locator('.pc-link').first().waitFor();
  const firstPoster = await page.evaluate(() => {
    const nav = performance.getEntriesByType('navigation')[0];
    const list = performance.getEntriesByType('resource').find((r) => r.name.includes('/movies/ids'));
    return {
      firstPosterMs: Math.round(performance.now()),
      domContentLoadedMs: Math.round(nav.domContentLoadedEventEnd),
      listRequestMs: list ? Math.round(list.responseEnd - list.requestStart) : null,
      listTransferBytes: list?.transferSize ?? null,
      listDecodedBytes: list?.decodedBodySize ?? null,
    };
  });
  // render time of the list itself: posters appear after the response; measure the gap
  const renderAfterList = await page.evaluate(() => {
    const list = performance.getEntriesByType('resource').find((r) => r.name.includes('/movies/ids'));
    const mark = performance.getEntriesByType('mark');
    return list ? { listResponseEndMs: Math.round(list.responseEnd), marks: mark.map((m) => m.name) } : null;
  });
  await page.waitForTimeout(1500);
  const cls = await page.evaluate(() => window.__cls);
  const initialUrls = [...new Set(assets.filter((a) => /\.(js|css)$/.test(a.url)).map((a) => a.url))];
  report.initial = {
    ...firstPoster,
    renderAfterListMs: renderAfterList ? firstPoster.firstPosterMs - renderAfterList.listResponseEndMs : null,
    cls: Math.round(cls * 1000) / 1000,
    loadedWithin1500ms: bundleSizes(page, initialUrls),
  };
  // the critical bundle = what index.html references (before any lazy chunk)
  const html = readFileSync(join(dist, 'index.html'), 'utf8');
  const critical = [...html.matchAll(/(?:src|href)="\.\/(assets\/[^"]+\.(?:js|css))"/g)].map((m) => `/${m[1]}`);
  report.initial.critical = bundleSizes(page, critical);
  report.allChunks = readdirSync(join(dist, 'assets')).filter((f) => f.endsWith('.js')).length;
  await page.close();

  // ---- scrolling
  for (const cpu of [1, 4]) {
    for (const layout of ['grid', 'table']) {
      const p = await openPage(browser);
      const cdp = await p.context().newCDPSession(p);
      await p.goto(`${BASE}/films${layout === 'table' ? '?layout=table' : ''}`);
      await p
        .locator(layout === 'table' ? 'tbody tr[data-movie-id]' : '.pc-link')
        .first()
        .waitFor();
      await p.waitForTimeout(1200);
      await cdp.send('Emulation.setCPUThrottlingRate', { rate: cpu });
      // pointer rests on a poster / row (hover active during the scroll, as motion.md asks)
      await p.mouse.move(700, 500);
      const result = await scrollFrames(p, 5, layout === 'table' ? 60 : 80);
      report.runs.push({ layout, cpuSlowdown: cpu, ...result });
      await p.close();
    }
  }
  await browser.close();
  writeFileSync(join(here, 'perf.json'), `${JSON.stringify(report, null, 2)}\n`);
  console.log(JSON.stringify(report, null, 2));
}

run().catch((e) => {
  console.error(e);
  process.exit(1);
});
