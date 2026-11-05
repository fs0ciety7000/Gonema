// Renders the phase 2B design deliverables to docs/design/renders/ and runs axe-core.
// Usage: NODE_PATH=$(npm root -g) node docs/design/tools/render.js [path/to/axe.min.js]
// Needs Playwright (chromium) and network access to Google Fonts.
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');
const D = path.resolve(__dirname, '..');
const OUT = path.join(D, 'renders');
const AXE = process.argv[2];

const jobs = [
  // [name, file (+query), viewport, fullPage]
  ['library-grid', 'mockups/library-grid.html', [1440, 900], false],
  ['library-grid-full', 'mockups/library-grid.html', [1440, 900], true],
  ['library-grid-tablet', 'mockups/library-grid.html', [1024, 768], false],
  ['library-table', 'mockups/library-table.html', [1440, 900], false],
  ['movie-detail', 'mockups/movie-detail.html', [1440, 900], false],
  ['movie-detail-full', 'mockups/movie-detail.html', [1440, 900], true],
  ['movie-detail-tablet', 'mockups/movie-detail.html', [1024, 768], false],
  ['movie-detail-tablet-full', 'mockups/movie-detail.html', [1024, 768], true],
  ['interactive-search', 'mockups/interactive-search.html', [1440, 900], false],
  ['interactive-search-full', 'mockups/interactive-search.html', [1440, 900], true],
  ['onboarding-welcome', 'mockups/onboarding.html?step=welcome', [1440, 900], false],
  ['onboarding-tmdb', 'mockups/onboarding.html?step=tmdb', [1440, 900], false],
  ['onboarding-folder', 'mockups/onboarding.html?step=folder', [1440, 900], false],
  ['onboarding-client-error', 'mockups/onboarding.html?step=client', [1440, 900], false],
  ['onboarding-quality', 'mockups/onboarding.html?step=quality', [1440, 900], false],
  ['onboarding-grabbed', 'mockups/onboarding.html?step=film', [1440, 900], false],
  ['dashboard', 'mockups/dashboard.html', [1440, 900], false],
  ['dashboard-full', 'mockups/dashboard.html', [1440, 900], true],
  ['add-film', 'mockups/add-film.html', [1440, 900], false],
  ['add-film-discover', 'mockups/add-film.html?tab=discover', [1440, 900], false],
  ['add-film-discover-full', 'mockups/add-film.html?tab=discover', [1440, 900], true],
  ['queue', 'mockups/queue.html', [1440, 900], false],
  ['queue-history', 'mockups/queue.html?tab=history', [1440, 900], false],
  ['calendar', 'mockups/calendar.html', [1440, 900], false],
  ['calendar-agenda', 'mockups/calendar.html?view=agenda', [1440, 900], false],
  ['settings-quality', 'mockups/settings-quality.html', [1440, 900], false],
  ['settings-quality-full', 'mockups/settings-quality.html', [1440, 900], true],
  ['settings-index', 'mockups/settings-quality.html?section=index', [1440, 900], false],
  ['settings-index-full', 'mockups/settings-quality.html?section=index', [1440, 900], true],
  ['command-palette', 'mockups/command-palette.html?state=recent', [1440, 900], false],
  ['command-palette-query', 'mockups/command-palette.html?state=query', [1440, 900], false],
  ['command-palette-empty', 'mockups/command-palette.html?state=empty', [1440, 900], false],
  ['empty-states', 'mockups/empty-states.html', [1440, 900], false],
  ['404', 'mockups/404.html', [1440, 900], false],
  ['error-offline', 'mockups/error.html', [1440, 900], false],
  ['failed-download', 'mockups/failed-download.html', [1440, 900], false],
  ['design-system', 'system/index.html', [1440, 900], false],
  ['design-system-full', 'system/index.html', [1440, 900], true],
  ['motion', 'motion/index.html', [1440, 900], false],
  ['motion-full', 'motion/index.html', [1440, 900], true],
  ['gallery', 'index.html', [1440, 900], false],
];
const ONLY = process.env.ONLY ? new RegExp(process.env.ONLY) : null;

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch();
  const report = [];
  for (const theme of ['dark', 'light']) {
    for (const [name, file, [w, h], full] of jobs) {
      if (ONLY && !ONLY.test(name)) continue;
      if (name === 'gallery' && !fs.existsSync(path.join(D, 'index.html'))) continue;
      const page = await browser.newPage({ viewport: { width: w, height: h }, deviceScaleFactor: 2 });
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      const [f, q] = file.split('?');
      await page.goto('file://' + path.join(D, f) + '?freeze=1&theme=' + theme + (q ? '&' + q : ''));
      await page.waitForLoadState('networkidle');
      await page.evaluate(() => document.fonts.ready);
      await page.waitForTimeout(500);
      await page.screenshot({ path: path.join(OUT, `${name}-${theme}.jpg`), fullPage: full, type: 'jpeg', quality: 88 });
      if (AXE && !full) {
        await page.addScriptTag({ path: AXE });
        const r = await page.evaluate(async () => {
          const res = await axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice'] } });
          return { violations: res.violations.map(v => ({ id: v.id, impact: v.impact, n: v.nodes.length, help: v.help, targets: v.nodes.slice(0, 4).map(x => x.target.join(' ') + (x.any[0] ? ' :: ' + x.any[0].message.slice(0, 140) : '')) })), passes: res.passes.length, incomplete: res.incomplete.length };
        });
        report.push({ page: name, theme, errors, ...r });
      }
      await page.close();
      console.log('rendered', name, theme, errors.length ? errors : '');
    }
  }
  // Motion key frames: poster -> detail at fixed times, normal and reduced motion
  if (!ONLY || ONLY.test('motion-frames')) {
    for (const reduce of [false, true]) {
      for (const ms of [0, 75, 150, 225, 300]) {
        const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 });
        await page.goto('file://' + path.join(D, 'motion/index.html') + '?freeze=1' + (reduce ? '&motion=reduce' : ''));
        await page.waitForLoadState('networkidle');
        await page.evaluate(() => document.fonts.ready);
        await page.waitForTimeout(300);
        await page.evaluate((t) => {
          document.getAnimations().forEach(a => a.cancel());
          document.querySelector('#se-grid button[data-id="halcyon-drift"]').click();
          document.getAnimations().forEach(a => { a.pause(); a.currentTime = t; });
        }, ms);
        const box = await page.locator('#se').boundingBox();
        await page.screenshot({ path: path.join(OUT, `motion-${reduce ? 'reduced-' : ''}poster-detail-${String(ms).padStart(3, '0')}ms.jpg`), clip: box, type: 'jpeg', quality: 88 });
        await page.close();
      }
    }
    console.log('rendered motion frames');
  }
  await browser.close();
  if (AXE) fs.writeFileSync(path.join(OUT, 'a11y-report.json'), JSON.stringify(report, null, 2));
  for (const r of report) console.log(r.page, r.theme, 'violations:', r.violations.map(v => `${v.id}(${v.impact})x${v.n}`).join(', ') || 'none', '| passes', r.passes);
})();
