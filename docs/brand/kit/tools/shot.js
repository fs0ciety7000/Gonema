const { chromium } = require('playwright');
(async () => {
  const b = await chromium.launch(); const p = await b.newPage({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 2 });
  const hash = process.argv[3] || '';
  await p.goto('file:///home/user/Gonema/docs/brand/kit/index.html' + hash, { waitUntil: 'networkidle' });
  await p.evaluate(() => document.fonts.ready); await p.waitForTimeout(500);
  await p.screenshot({ path: process.argv[2], fullPage: true });
  await b.close();
})();
