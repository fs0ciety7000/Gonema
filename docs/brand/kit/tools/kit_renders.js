const { chromium } = require('playwright'); const fs = require('fs');
const K = '/home/user/Gonema/docs/brand/kit';
const d = (f) => 'data:' + (f.endsWith('.png') ? 'image/png' : 'image/svg+xml') + ';base64,' + fs.readFileSync(K + '/' + f).toString('base64');
const font = `<link href="https://fonts.googleapis.com/css2?family=Archivo:wdth,wght@125,500..700&family=Geist:wght@400;500;600&family=Geist+Mono&family=Instrument+Serif&display=swap" rel="stylesheet">`;
const base = `body{margin:0;font:13px/1.4 Geist,system-ui,sans-serif}`;
function tabs(theme, icon) {
  const dark = theme === 'dark';
  const bar = dark ? '#202124' : '#DEE1E6', tab = dark ? '#35363A' : '#FFFFFF', fg = dark ? '#E8EAED' : '#202124', other = dark ? '#9AA0A6' : '#5F6368';
  const t = (ic, label, active) => `<div style="display:flex;align-items:center;gap:8px;height:34px;padding:0 12px;width:210px;border-radius:8px 8px 0 0;background:${active ? tab : 'transparent'};color:${active ? fg : other}">${ic}<span style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${label}</span></div>`;
  const dot = `<span style="width:16px;height:16px;border-radius:50%;background:${other};opacity:.5;display:inline-block"></span>`;
  return `<div style="background:${bar};padding:8px 8px 0;display:flex;gap:2px">${t(dot, 'Inbox (3)', false)}${t(`<img src="${icon}" width="16" height="16">`, 'Library · Gonema', true)}${t(dot, 'qBittorrent Web UI', false)}${t(dot, 'Plex', false)}</div><div style="height:30px;background:${tab}"></div>`;
}
(async () => {
  const b = await chromium.launch();
  for (const [dsf, name, icon] of [[1, 'tab-context-1x.png', 'icons/favicon-16.png'], [2, 'tab-context-2x.png', 'icons/favicon.svg']]) {
    const p = await b.newPage({ viewport: { width: 900, height: 160 }, deviceScaleFactor: dsf });
    await p.setContent(`<style>${base}</style>${tabs('dark', d(icon))}<div style="height:12px"></div>${tabs('light', d(icon))}`);
    await p.waitForTimeout(200); await p.screenshot({ path: `${K}/renders/${name}`, fullPage: true }); await p.close();
  }
  // clear space + minimum sizes
  let p = await b.newPage({ viewport: { width: 1200, height: 520 }, deviceScaleFactor: 2 });
  await p.setContent(`${font}<style>${base} body{background:#F3EDE3} .z{position:relative;display:inline-block;padding:var(--x);outline:2px dashed #8F96FF;outline-offset:-1px;background:repeating-linear-gradient(45deg,rgba(143,150,255,.10) 0 6px,transparent 6px 12px)} .z img{display:block;background:#12100E} .lbl{color:#C9BBAB;font-size:12px;margin-top:8px}</style>
  <div style="background:#12100E;padding:32px;display:flex;gap:48px;align-items:flex-start;color:#F2E9DD">
   <div><div class="z" style="--x:46px"><img src="${d('logo/lockup-horizontal-on-dark.svg')}" style="height:176px"></div><div class="lbl">Zone de protection = X sur chaque côté, X = hauteur de capitale du wordmark (≈ ¼ de la hauteur de la marque)</div></div>
   <div><div class="z" style="--x:44px"><img src="${d('logo/mark-on-dark.svg')}" style="height:176px"></div><div class="lbl">Marque seule : X = ¼ de sa hauteur</div></div>
  </div>
  <div style="background:#F3EDE3;padding:24px 32px;display:flex;gap:40px;align-items:flex-end;color:#1C1713">
   <div><img src="${d('logo/lockup-horizontal-on-light.svg')}" style="height:32px;display:block"><div class="lbl" style="color:#4E443B">Horizontal min. 32 px de haut</div></div>
   <div><img src="${d('logo/lockup-stacked-on-light.svg')}" style="height:64px;display:block"><div class="lbl" style="color:#4E443B">Empilé min. 64 px</div></div>
   <div><img src="${d('logo/mark-on-light.svg')}" style="height:24px;display:block"><div class="lbl" style="color:#4E443B">Marque min. 24 px</div></div>
   <div><img src="${d('icons/favicon-16.png')}" style="height:16px;display:block"><div class="lbl" style="color:#4E443B">16 px : dessin favicon dédié</div></div>
   <div><img src="${d('logo/wordmark-on-light.svg')}" style="height:12px;display:block"><div class="lbl" style="color:#4E443B">Wordmark seul min. 12 px de haut</div></div>
  </div>`);
  await p.waitForTimeout(800); await p.screenshot({ path: `${K}/renders/clear-space.png`, fullPage: true }); await p.close();
  // do / don't
  p = await b.newPage({ viewport: { width: 1200, height: 700 }, deviceScaleFactor: 2 });
  const cell = (ok, label, inner, bg) => `<figure style="margin:0"><div style="height:150px;border-radius:14px;display:grid;place-items:center;overflow:hidden;background:${bg}">${inner}</div><figcaption style="margin-top:8px;font-size:12.5px;color:#C9BBAB"><b style="color:${ok ? '#7FCC89' : '#F27268'}">${ok ? 'Oui' : 'Non'}</b> · ${label}</figcaption></figure>`;
  const L = (v, s = 'height:56px') => `<img src="${d('logo/lockup-horizontal-' + v + '.svg')}" style="${s}">`;
  const poster = 'linear-gradient(160deg,#3a5a6a 0%,#1d2b33 40%,#6b3d2a 100%)';
  await p.setContent(`${font}<style>${base} body{background:#12100E}</style><div style="background:#12100E;padding:28px;display:grid;grid-template-columns:repeat(4,1fr);gap:22px">
   ${cell(1, 'Couleur sur noir chaud', L('on-dark'), '#12100E')}
   ${cell(1, 'Couleur sur papier os', L('on-light'), '#F3EDE3')}
   ${cell(1, 'Blanc sur affiche (avec voile)', `<div style="background:rgba(0,0,0,.35);padding:16px 22px;border-radius:10px">${L('white', 'height:44px')}</div>`, poster)}
   ${cell(1, 'Une couleur (noir)', L('black'), '#FFFFFF')}
   ${cell(0, 'Déformer', L('on-dark', 'height:56px;width:340px;transform:scaleY(.65)'), '#12100E')}
   ${cell(0, 'Changer les couleurs', L('on-dark', 'height:56px;filter:hue-rotate(150deg) saturate(2)'), '#12100E')}
   ${cell(0, 'Pivoter ou incliner', L('on-dark', 'height:56px;transform:rotate(-8deg)'), '#12100E')}
   ${cell(0, 'Ombres, lueurs, effets', L('on-dark', 'height:56px;filter:drop-shadow(0 0 10px #8F96FF) drop-shadow(4px 6px 0 #000)'), '#12100E')}
   ${cell(0, 'Sur l’accent ou un fond peu contrasté', L('on-dark'), '#8F96FF')}
   ${cell(0, 'Recomposer le wordmark dans une autre police', `<div style="display:flex;align-items:center;gap:10px"><img src="${d('logo/mark-on-dark.svg')}" style="height:62px"><span style="font:400 46px 'Instrument Serif';color:#F2E9DD">Gonema</span></div>`, '#12100E')}
   ${cell(0, 'Inverser l’ordre marque / nom', `<div style="display:flex;align-items:center;gap:12px"><img src="${d('logo/wordmark-on-dark.svg')}" style="height:22px"><img src="${d('logo/mark-on-dark.svg')}" style="height:60px"></div>`, '#12100E')}
   ${cell(0, 'Sur une affiche sans voile', L('on-dark', 'height:44px'), 'linear-gradient(160deg,#d9c7a0,#8aa0a8 60%,#f0e2c8)')}
  </div>`);
  await p.waitForTimeout(800); await p.screenshot({ path: `${K}/renders/do-dont.png`, fullPage: true }); await p.close();
  // mascot set
  p = await b.newPage({ viewport: { width: 1500, height: 600 }, deviceScaleFactor: 2 });
  const ns = ['neutral', 'welcome', 'empty-library', '404', 'failed-download'];
  let h = '';
  for (const bg of ['#FBF8F2', '#12100E']) h += `<div style="display:flex;gap:10px;align-items:end;background:${bg};padding:16px">` + ns.map(n => `<figure style="margin:0;text-align:center"><img src="${d('mascot/kernel-' + n + '.svg')}" style="height:240px"><figcaption style="font:12px Geist,sans-serif;color:${bg === '#12100E' ? '#C9BBAB' : '#4E443B'}">${n}</figcaption></figure>`).join('') + '</div>';
  await p.setContent(`${font}<style>${base}</style>${h}`); await p.waitForTimeout(500);
  await p.screenshot({ path: `${K}/renders/mascot-set.png`, fullPage: true }); await p.close();
  await b.close();
})();
