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
  // [name, file, viewport, fullPage]
  ['library-grid', 'mockups/library-grid.html', [1440, 900], false],
  ['library-grid-full', 'mockups/library-grid.html', [1440, 900], true],
  ['library-grid-tablet', 'mockups/library-grid.html', [1024, 768], false],
  ['library-grid-tablet-full', 'mockups/library-grid.html', [1024, 768], true],
  ['library-table', 'mockups/library-table.html', [1440, 900], false],
  ['movie-detail', 'mockups/movie-detail.html', [1440, 900], false],
  ['movie-detail-full', 'mockups/movie-detail.html', [1440, 900], true],
  ['interactive-search', 'mockups/interactive-search.html', [1440, 900], false],
  ['interactive-search-full', 'mockups/interactive-search.html', [1440, 900], true],
  ['design-system-full', 'system/index.html', [1440, 900], true],
  ['motion-full', 'motion/index.html', [1440, 900], true],
];

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch();
  const report = [];
  for (const theme of ['dark', 'light']) {
    for (const [name, file, [w, h], full] of jobs) {
      const page = await browser.newPage({ viewport: { width: w, height: h }, deviceScaleFactor: 2 });
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.goto('file://' + path.join(D, file) + '?freeze=1&theme=' + theme);
      await page.waitForLoadState('networkidle');
      await page.evaluate(() => document.fonts.ready);
      await page.waitForTimeout(500);
      await page.screenshot({ path: path.join(OUT, `${name}-${theme}.png`), fullPage: full });
      if (AXE && !full && !/-full$/.test(name) || (AXE && /design-system|motion/.test(name))) {
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
  await browser.close();
  if (AXE) fs.writeFileSync(path.join(OUT, 'a11y-report.json'), JSON.stringify(report, null, 2));
  for (const r of report) console.log(r.page, r.theme, 'violations:', r.violations.map(v => `${v.id}(${v.impact})x${v.n}`).join(', ') || 'none', '| passes', r.passes);
})();
