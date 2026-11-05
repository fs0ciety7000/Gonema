// usage: node render.js jobs.json   jobs: [{svg, out, w, h, bg}]
const { chromium } = require('playwright');
const fs = require('fs');
(async () => {
  const jobs = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  const browser = await chromium.launch();
  const page = await browser.newPage({ deviceScaleFactor: 1 });
  for (const j of jobs) {
    const svg = fs.readFileSync(j.svg, 'utf8');
    const w = j.w, h = j.h || j.w;
    await page.setViewportSize({ width: w, height: h });
    const b64 = Buffer.from(svg).toString('base64');
    await page.setContent(`<html><body style="margin:0;background:${j.bg || 'transparent'}"><img src="data:image/svg+xml;base64,${b64}" style="display:block;width:${w}px;height:${h}px;object-fit:contain"></body></html>`);
    await page.waitForTimeout(30);
    await page.screenshot({ path: j.out, omitBackground: !j.bg, clip: { x: 0, y: 0, width: w, height: h } });
  }
  await browser.close();
})();
