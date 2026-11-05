// node sheet.js out.png file1.svg file2.svg ... : rows per file: 200, 64, 32, 16 on light & dark, plus 16px zoomed x6
const { chromium } = require('playwright'); const fs = require('fs');
(async () => {
  const out = process.argv[2]; const files = process.argv.slice(3);
  const b = await chromium.launch(); const p = await b.newPage({ deviceScaleFactor: 1 });
  let rows = '';
  for (const f of files) {
    const src = 'data:image/svg+xml;base64,' + Buffer.from(fs.readFileSync(f)).toString('base64');
    const cell = (bg) => `<div style="background:${bg};display:flex;gap:18px;align-items:center;padding:12px">
      <img src="${src}" style="width:200px;height:200px;object-fit:contain">
      <img src="${src}" style="width:64px;height:64px;object-fit:contain">
      <img src="${src}" style="width:32px;height:32px;object-fit:contain">
      <img src="${src}" style="width:16px;height:16px;object-fit:contain">
      <canvas data-src="${src}" width="16" height="16" style="width:96px;height:96px;image-rendering:pixelated;background:${bg}"></canvas></div>`;
    rows += `<div style="display:flex;font:12px sans-serif">${cell('#ffffff')}${cell('#14100E')}<div style="padding:8px;color:#888">${f.split('/').pop()}</div></div>`;
  }
  await p.setContent(`<body style="margin:0;background:#888">${rows}<script>
   document.querySelectorAll('canvas').forEach(c=>{const i=new Image();i.onload=()=>{const x=c.getContext('2d');x.drawImage(i,0,0,16,16)};i.src=c.dataset.src;});</script></body>`);
  await p.waitForTimeout(300);
  await p.screenshot({ path: out, fullPage: true });
  await b.close();
})();
