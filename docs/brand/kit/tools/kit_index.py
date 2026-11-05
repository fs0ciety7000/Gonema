import json, html
K = '/home/user/Gonema/docs/brand/kit'
T = json.load(open(f'{K}/tokens.json'))
ROLES = list(T['color']['dark'].keys())
def pal(th):
    c = T['contrast_wcag21'][th]['per_role_vs_bg-base/bg-raised/surface-glass']
    rows = ''
    for r in ROLES:
        v = T['color'][th][r]['value']
        if r in c:
            m = c[r]; ratio = ' / '.join(f'{m[k]:.2f}' for k in ('bg-base', 'bg-raised', 'surface-glass'))
        elif r == 'accent-contrast':
            ratio = f"{T['contrast_wcag21'][th]['accent-contrast on accent']:.2f} sur accent"
        elif r == 'border':
            ratio = 'décoratif'
        else:
            ratio = 'fond'
        rows += f'<tr><td><i class="sw" style="background:{v}"></i></td><td>{r}</td><td class="m">{v}</td><td class="m r">{ratio}</td></tr>'
    return f'<div class="pal" data-for="{th}"><h4>{"Sombre (défaut)" if th=="dark" else "Clair"} <span>ratio sur base / raised / verre</span></h4><table>{rows}</table></div>'
scale = ''
for n, s in T['typography']['scale'].items():
    fam = s['family']; style = f"font:{s['weight']} {s['size']}px/{s['lineHeight']}px var(--font-{fam});letter-spacing:{s['tracking']};"
    if fam == 'display': style += 'font-stretch:125%;'
    if s.get('transform'): style += 'text-transform:uppercase;'
    sample = {'display': 'Paris, Texas', 'ui': 'Wanted · 1 984 films · Cutoff 2160p', 'mono': 'Paris.Texas.1984.2160p.UHD.BluRay.REMUX-GRP'}[fam]
    if n == 'overline': sample = 'Now showing'
    scale += f'<div class="ts"><span class="tl">{n}<br><b>{s["size"]}/{s["lineHeight"]} · {s["weight"]}</b></span><span style="{style}">{sample}</span></div>'
poses = ['neutral', 'welcome', 'empty-library', '404', 'failed-download']
mascots = ''.join(f'<figure><img src="mascot/kernel-{p}.svg" alt="Kernel {p}"><figcaption>{p}</figcaption></figure>' for p in poses)
tone = [('Bibliothèque vide', "The house is dark. Add a film and we'll start the screening."),
        ('404', "This reel isn't in the booth. Back to the programme?"),
        ('Échec de téléchargement', 'The print arrived damaged. Blocklisted it, trying the next release.'),
        ('Upgrade réussi', 'New print: 1080p → 2160p Remux. Same seat, better picture.'),
        ('Décision rejetée', 'Skipped: 720p is below your cutoff (1080p).'),
        ('Health check', "Indexer NZBgeek hasn't answered for 10 minutes. Others keep searching.")]
tones = ''.join(f'<li><b>{a}</b><span>{html.escape(b)}</span></li>' for a, b in tone)
page = f'''<!doctype html>
<html lang="fr" data-theme="dark">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Gonema — charte de marque</title>
<link rel="icon" href="icons/favicon.ico" sizes="16x16 32x32 48x48"><link rel="icon" href="icons/favicon.svg" type="image/svg+xml">
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Archivo:wdth,wght@125,500..700&family=Geist:wght@400;500;600&family=Geist+Mono:wght@400;500&display=swap" rel="stylesheet">
<link rel="stylesheet" href="tokens.css">
<style>
*{{box-sizing:border-box}}
body{{margin:0;background:var(--bg-base);color:var(--text-primary);font:var(--type-body);font-variant-numeric:tabular-nums}}
.wrap{{max-width:1360px;margin:0 auto;padding:40px 48px 64px}}
.top{{display:flex;justify-content:space-between;align-items:center;gap:24px;margin-bottom:28px}}
.top img{{height:56px}}
.over{{font:var(--type-overline);letter-spacing:.12em;font-stretch:125%;text-transform:uppercase;color:var(--text-muted);margin:0 0 6px}}
h1{{font:var(--type-display-xl);font-stretch:125%;letter-spacing:-.015em;margin:0 0 8px}}
h2{{font:var(--type-display-md);font-stretch:125%;margin:0 0 16px}}
h4{{font:var(--type-title-md);margin:0 0 10px}} h4 span{{font:var(--type-caption);color:var(--text-muted);margin-left:6px}}
p.lead{{font:var(--type-body-lg);color:var(--text-secondary);max-width:820px;margin:0}}
section{{margin-top:44px}}
.grid{{display:grid;gap:16px}} .g2{{grid-template-columns:1fr 1fr}} .g3{{grid-template-columns:repeat(3,1fr)}} .g5{{grid-template-columns:repeat(5,1fr)}}
.card{{background:var(--bg-raised);border:1px solid var(--border);border-radius:var(--radius-lg);padding:20px;box-shadow:var(--shadow-raised)}}
.tile{{border-radius:var(--radius-lg);display:grid;place-items:center;padding:28px;border:1px solid var(--border);min-height:170px}}
.tile img{{max-width:100%;max-height:120px}}
.dk{{background:#12100E}} .lt{{background:#F3EDE3}} .wh{{background:#fff}} .gr{{background:linear-gradient(160deg,#3a5a6a,#1d2b33 45%,#6b3d2a)}}
.cap{{font:var(--type-caption);color:var(--text-muted);margin-top:8px}}
.cc{{color:var(--accent)}} .cc svg{{height:64px;width:auto}}
#toggle{{font:500 14px var(--font-ui);padding:10px 16px;border-radius:var(--radius-pill);border:1px solid var(--border);background:var(--surface-glass);color:var(--text-primary);cursor:pointer}}
#toggle:focus-visible,.btn:focus-visible{{outline:none;box-shadow:var(--focus-ring)}}
.sizes{{display:flex;gap:22px;align-items:flex-end;flex-wrap:wrap}}
.full{{width:100%;border-radius:var(--radius-lg);border:1px solid var(--border);display:block}}
.pal table{{width:100%;border-collapse:collapse;font:var(--type-body-sm)}}
.pal td{{padding:4px 6px;border-top:1px solid var(--border)}}
.sw{{display:inline-block;width:20px;height:20px;border-radius:6px;border:1px solid rgba(128,128,128,.35);vertical-align:middle}}
.m{{font:var(--type-mono-sm)}} .r{{text-align:right;white-space:nowrap}}
.ts{{display:grid;grid-template-columns:150px 1fr;gap:16px;align-items:baseline;padding:10px 0;border-top:1px solid var(--border);overflow:hidden}}
.tl{{font:var(--type-mono-sm);color:var(--text-muted)}} .tl b{{font-weight:400;color:var(--text-secondary)}}
.ui-row{{display:flex;gap:10px;flex-wrap:wrap;align-items:center}}
.st{{font:var(--type-caption);color:var(--c);background:color-mix(in srgb,var(--c) 14%,var(--bg-raised));padding:4px 10px;border-radius:var(--radius-pill)}}
.st::before{{content:"";display:inline-block;width:7px;height:7px;border-radius:50%;background:var(--c);margin-right:6px}}
.btn{{font:600 14px var(--font-ui);border-radius:var(--radius-md);padding:10px 16px;border:1px solid var(--border);background:transparent;color:var(--text-primary)}}
.btn.p{{background:var(--accent);color:var(--accent-contrast);border-color:transparent}}
.btn.f{{box-shadow:var(--focus-ring)}}
.glass{{position:relative;border-radius:var(--radius-lg);overflow:hidden;min-height:180px;background:linear-gradient(140deg,#5b6f7a,#2c2420 55%,#8a5236)}}
.glass .pane{{position:absolute;left:16px;right:16px;bottom:16px;background:var(--surface-glass);backdrop-filter:blur(20px) saturate(1.2);border:1px solid var(--border);border-radius:var(--radius-md);padding:14px 16px}}
.glass .t{{font:var(--type-display-md);font-stretch:125%}} .glass .s{{font:var(--type-body-sm);color:var(--text-secondary)}}
figure{{margin:0;text-align:center}} figure img{{width:100%;height:190px;object-fit:contain}} figcaption{{font:var(--type-caption);color:var(--text-muted)}}
.tone ul{{list-style:none;margin:0;padding:0;display:grid;grid-template-columns:1fr 1fr;gap:12px 28px}}
.tone li{{display:grid;gap:2px}} .tone b{{font:var(--type-overline);font-stretch:125%;letter-spacing:.12em;text-transform:uppercase;color:var(--text-muted)}}
.files{{font:var(--type-mono-sm);color:var(--text-secondary)}} a{{color:var(--accent)}}
html[data-theme="dark"] .only-light,html[data-theme="light"] .only-dark{{display:none}}
@media (max-width:1000px){{.g2,.g3,.g5,.tone ul{{grid-template-columns:1fr}}}}
</style></head>
<body><div class="wrap">
<div class="top"><img class="only-dark" src="logo/lockup-horizontal-on-dark.svg" alt="Gonema"><img class="only-light" src="logo/lockup-horizontal-on-light.svg" alt="Gonema">
<button id="toggle" type="button" aria-pressed="false">Thème clair</button></div>
<p class="over">Charte de marque · v1.0</p>
<h1>A private cinema, not a spreadsheet.</h1>
<p class="lead">Direction A « Salle » (marque, palette outremer, ton, mascotte) avec la typographie de B (Archivo Expanded, Geist, Geist Mono). Toutes les valeurs viennent de <code>tokens.json</code> ; cette page utilise directement <code>tokens.css</code>.</p>

<section><h2>Logo</h2>
<div class="grid g3">
 <div><div class="tile dk"><img src="logo/lockup-horizontal-on-dark.svg" alt=""></div><div class="cap">Horizontal · sur noir chaud</div></div>
 <div><div class="tile lt"><img src="logo/lockup-horizontal-on-light.svg" alt=""></div><div class="cap">Horizontal · sur papier os</div></div>
 <div><div class="tile gr"><img src="logo/lockup-horizontal-white.svg" alt="" style="max-height:70px"></div><div class="cap">Blanc · sur affiche avec voile</div></div>
 <div><div class="tile dk"><img src="logo/lockup-stacked-on-dark.svg" alt="" style="max-height:150px"></div><div class="cap">Empilé · sombre</div></div>
 <div><div class="tile lt"><img src="logo/lockup-stacked-on-light.svg" alt="" style="max-height:150px"></div><div class="cap">Empilé · clair</div></div>
 <div><div class="tile wh"><img src="logo/lockup-stacked-black.svg" alt="" style="max-height:150px"></div><div class="cap">Une couleur · noir (aussi blanc et currentColor)</div></div>
</div>
<p class="cap">Wordmark en capitales Archivo Expanded 600 : les capitales évitent deux G concurrents et lisent comme un carton de générique. Déliés du G passés de 16 à 22/256 pour s'accorder à la grotesque.</p>
<img class="full" src="renders/clear-space.png" alt="Zone de protection et tailles minimales" style="margin-top:12px">
</section>

<section><h2>Petites tailles</h2>
<div class="grid g2">
 <div class="card"><h4>Favicon dédié et icônes d'app</h4><div class="sizes">
  <img src="icons/favicon-16.png" width="16" height="16" alt=""><img src="icons/favicon.svg" width="32" height="32" alt=""><img src="icons/favicon.svg" width="48" height="48" alt="">
  <img src="icons/icon-192.png" width="96" height="96" alt=""><img src="icons/icon-maskable-192.png" width="96" height="96" alt="" style="border-radius:50%"><img src="icons/apple-touch-icon.png" width="90" height="90" alt="" style="border-radius:20px"></div>
  <div class="cap">16 px (dessin dédié) · 32 · 48 · PWA · maskable (masque rond) · apple-touch</div></div>
 <div class="card"><h4>En onglet de navigateur (16 px réels)</h4><img class="full" src="renders/tab-context-1x.png" alt="Favicon dans un onglet" style="image-rendering:auto"></div>
</div></section>

<section><h2>Couleur</h2>
<div class="grid g2">{pal('dark')}{pal('light')}</div>
<div class="grid g2" style="margin-top:16px">
 <div class="card"><h4>Statuts, action, focus</h4><div class="ui-row">
  <span class="st" style="--c:var(--status-downloading)">Downloading 42 %</span><span class="st" style="--c:var(--status-missing)">Missing</span>
  <span class="st" style="--c:var(--status-upgraded)">Upgraded</span><span class="st" style="--c:var(--status-failed)">Failed</span><span class="st" style="--c:var(--status-available)">Available</span></div>
  <div class="ui-row" style="margin-top:14px"><button class="btn p">Search now</button><button class="btn">Edit profile</button><button class="btn f">Focus visible</button></div>
  <p class="cap">Accent : une seule action pleine par vue. Statuts toujours avec pastille + libellé. Badges = texte sur sa teinte à 14 % (≥ 4.57:1).</p></div>
 <div class="glass"><div class="pane"><div class="t">Paris, Texas</div><div class="s">1984 · Wim Wenders · 2160p Remux · 61.2 GB</div></div></div>
</div></section>

<section><h2>Typographie</h2>
<div class="card">{scale}
<p class="cap">Archivo (wdth 125), Geist, Geist Mono — SIL Open Font License 1.1, Google Fonts. Chiffres tabulaires partout : 12.4 GB · 87 pts · 00:42:10.</p></div></section>

<section><h2>Mascotte · Kernel</h2>
<div class="card"><div class="grid g5">{mascots}</div>
<p class="cap">États vides, onboarding, 404, échec en vue détaillée. Jamais à côté du logo, dans les tableaux ou les confirmations destructives. Original : ni bleu, ni proportions du gopher Go.</p></div></section>

<section class="tone"><h2>Voix</h2><div class="card"><ul>{tones}</ul>
<p class="cap">Calme, transparent, court. Dire ce qui s'est passé et ce que Gonema fait ensuite. Une plaisanterie par écran au plus.</p></div></section>

<section><h2>À faire / à éviter</h2><img class="full" src="renders/do-dont.png" alt="Exemples d'usage corrects et incorrects"></section>

<section><h2>Fichiers</h2><div class="card files">
logo/ — mark, wordmark, lockup-horizontal, lockup-stacked × on-dark · on-light · black · white · currentcolor<br>
icons/ — favicon.ico · favicon.svg · favicon-16.svg · apple-touch-icon.png · icon-192/512 · icon-maskable-192/512 · site.webmanifest · head-snippet.html<br>
mascot/ — kernel-neutral · welcome · empty-library · 404 · failed-download<br>
<a href="tokens.json">tokens.json</a> · <a href="tokens.css">tokens.css</a> · <a href="guidelines.md">guidelines.md</a></div></section>
</div>
<script>
const t=document.getElementById('toggle');
t.addEventListener('click',()=>{{const h=document.documentElement;const d=h.dataset.theme==='dark';h.dataset.theme=d?'light':'dark';t.textContent=d?'Thème sombre':'Thème clair';t.setAttribute('aria-pressed',String(d));}});
if(location.hash==='#light')t.click();
</script></body></html>'''
open(f'{K}/index.html', 'w').write(page)
