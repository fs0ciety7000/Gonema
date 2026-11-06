const { chromium } = require('playwright');
(async () => {
  const b = await chromium.launch(); const p = await b.newPage({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 1 });
  await p.goto('file:///home/user/Gonema/docs/brand/kit/index.html' + (process.argv[2]||''), { waitUntil: 'networkidle' });
  await p.evaluate(() => document.fonts.ready); await p.waitForTimeout(400);
  const h = await p.evaluate(() => document.body.scrollHeight);
  for (let y = 0, i = 0; y < h; y += 1100, i++) await p.screenshot({ path: `/tmp/claude-0/-home-user-Gonema/e3b3031e-119a-5939-a129-16b173290428/scratchpad/look/k${process.argv[3]||''}${i}.png`, clip: { x: 0, y, width: 1600, height: Math.min(1100, h - y) }, fullPage: true });
  console.log(h);
  await b.close();
})();
