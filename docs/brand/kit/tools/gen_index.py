import json, html
from palettes import P
from contrast import measure
ROOT = '/home/user/Gonema/docs/brand/directions'
DIRS = [
 ('a', 'a-salle', 'A — Salle', 'Éditoriale, art et essai',
  'Le G-ardoise à contraste didone, et le bras du clap qui s’ouvre à 15°.',
  ("'Instrument Serif', serif", "'Instrument Sans', sans-serif", "'IBM Plex Mono', monospace"),
  ('Instrument Serif', 'Instrument Sans', 'IBM Plex Mono'),
  ["The house is dark. Add a film and we'll start the screening.",
   "This reel isn't in the booth. Back to the programme?",
   "The print arrived damaged. Blocklisted it, trying the next release.",
   "New print: 1080p → 2160p Remux. Same seat, better picture."], 'normal', 0),
 ('b', 'b-sprocket', 'B — Sprocket', 'Moderne, technique, précise',
  'Un G carré sur grille de 8, fût perforé comme une pellicule ; la barre en accent est la fenêtre de décision.',
  ("'Archivo', sans-serif", "'Geist', sans-serif", "'Geist Mono', monospace"),
  ('Archivo Expanded', 'Geist', 'Geist Mono'),
  ["0 films. Add one and Gonema starts watching the indexers.",
   "Frame 404 not found. The rest of the reel is fine.",
   "Download failed (CRC error). Release blocklisted, next candidate scored 87.",
   "Upgraded to WEB-DL 2160p, +40 points. Cutoff reached."], 'uppercase', 125),
 ('c', 'c-matinee', 'C — Matinée', 'Chaleureuse, ludique, luxe doux',
  'Un « g » dont la panse est une bobine et la queue, la pellicule qui se déroule.',
  ("'Fraunces', serif", "'Figtree', sans-serif", "'JetBrains Mono', monospace"),
  ('Fraunces', 'Figtree', 'JetBrains Mono'),
  ["Nothing on tonight's bill yet. Pick a film, we'll handle the popcorn.",
   "Wrong screen. Kernel ate the page, sorry.",
   "That copy didn't make it. We've moved on to the next one.",
   "Swapped in the 4K print. Enjoy the upgrade."], 'normal', 0),
]
TONE_LABELS = ['Bibliothèque vide', '404', 'Échec de téléchargement', 'Upgrade réussi']
ROLES = ['bg-base', 'bg-raised', 'surface-glass', 'border', 'text-primary', 'text-secondary', 'text-muted', 'accent', 'accent-contrast',
         'status-downloading', 'status-missing', 'status-upgraded', 'status-failed', 'status-available']

def css_val(v):
    if isinstance(v, tuple):
        h = v[0].lstrip('#'); r, g, b = (int(h[i:i+2], 16) for i in (0, 2, 4)); return f'rgba({r},{g},{b},{v[1]})'
    return v

css_vars = ''
for k, *_ in DIRS:
    for th in ('dark', 'light'):
        sel = f'html[data-theme="{th}"] .dir-{k}'
        css_vars += sel + '{' + ';'.join(f'--{r}:{css_val(P[k][th][r])}' for r in ROLES) + '}\n'

def fmt(x, thr):
    cls = 'ok' if x >= thr else 'bad'
    return f'<span class="{cls}">{x:.2f}</span>'

def palette_html(k):
    out = ''
    for th in ('dark', 'light'):
        m = measure(P[k][th]); T = P[k][th]
        rows = ''
        for r in ROLES:
            v = css_val(T[r]); disp = v if not isinstance(T[r], tuple) else f'{T[r][0]} @ {int(T[r][1]*100)} %'
            if r in m and isinstance(m[r], dict):
                mm = m[r]
                thr = 4.5
                ratio = f'{fmt(mm["bg-base"], thr)} / {fmt(mm["bg-raised"], thr)} / {fmt(mm["surface-glass"], thr)}'
            elif r == 'accent-contrast':
                ratio = f'on accent {fmt(m["accent-contrast-on-accent"], 4.5)}'
            elif r == 'border':
                ratio = f'<span class="dim">{m["border-on-bg-raised (decorative)"]:.2f} (décoratif)</span>'
            else:
                ratio = '<span class="dim">fond</span>'
            rows += f'<tr><td><i class="sw" style="background:{v}"></i></td><td class="role">{r}</td><td class="mono">{disp}</td><td class="mono r">{ratio}</td></tr>'
        out += f'<div class="pal pal-{th}"><h5>{"Sombre" if th=="dark" else "Clair"} <span class="dim">· ratio sur base / raised / glass</span></h5><table>{rows}</table></div>'
    return out

def img(src, s, alt=''):
    return f'<img src="{src}" width="{s}" height="{s}" alt="{alt}">'

cols = ''
for k, slug, title, sub, idea, stacks, names, tone, tt, wdth in DIRS:
    import re
    def inline(path, cls=''):
        t = open(path).read()
        t = re.sub(r'<title id="t">', '<title>', t).replace(' aria-labelledby="t"', f' class="{cls}"')
        return re.sub(r' width="[\d.]+" height="[\d.]+"', '', t, count=1)
    mascot = inline(f'{ROOT}/{slug}/mascot.svg')
    mono = inline(f'{ROOT}/{slug}/mark-mono.svg', 'monoi')
    tones = ''.join(f'<li><b>{TONE_LABELS[i]}</b><span>{html.escape(t)}</span></li>' for i, t in enumerate(tone))
    disp_style = f"font-family:{stacks[0]};text-transform:{tt};" + (f"font-stretch:125%;font-weight:700;letter-spacing:.04em;" if wdth else '') + ("font-variation-settings:'SOFT' 100,'WONK' 1;font-weight:600;" if k == 'c' else '')
    cols += f'''
<section class="dir dir-{k}">
  <header><p class="kicker">{sub}</p><h2>{title}</h2><p class="idea">{idea}</p></header>
  <div class="card hero">
    <img class="only-dark" src="{slug}/mark-on-dark.svg" width="200" height="200" alt="{title} mark">
    <img class="only-light" src="{slug}/mark.svg" width="200" height="200" alt="{title} mark">
  </div>
  <div class="card sizes">
    <div>{img(slug+'/favicon.svg',16)}{img(slug+'/favicon.svg',32)}{img(slug+'/favicon.svg',64)}<small>favicon 16 / 32 / 64</small></div>
    <div><span class="only-dark">{img(slug+'/mark-on-dark.svg',16)}{img(slug+'/mark-on-dark.svg',32)}</span><span class="only-light">{img(slug+'/mark.svg',16)}{img(slug+'/mark.svg',32)}</span>{mono}<small>marque 16 / 32, mono</small></div>
  </div>
  <div class="lock dark-bg" style="background:{P[k]['dark']['bg-base']}"><img src="{slug}/lockup-dark.svg" alt="lockup dark"></div>
  <div class="lock light-bg" style="background:{P[k]['light']['bg-base']}"><img src="{slug}/lockup-light.svg" alt="lockup light"></div>
  <div class="card type">
    <p class="disp" style="{disp_style}">Paris, Texas</p>
    <p class="ui" style="font-family:{stacks[1]}">Wanted · 1 984 films · Cutoff 2160p</p>
    <p class="mono-s" style="font-family:{stacks[2]}">Paris.Texas.1984.2160p.UHD.BluRay.REMUX-GRP</p>
    <p class="tnum" style="font-family:{stacks[1]}"><span>12.4 GB</span><span>87 pts</span><span>00:42:10</span></p>
    <small>{names[0]} · {names[1]} · {names[2]} — SIL OFL 1.1</small>
  </div>
  <div class="card statuses">
    <span class="st" style="--c:var(--status-downloading)">Downloading 42 %</span>
    <span class="st" style="--c:var(--status-missing)">Missing</span>
    <span class="st" style="--c:var(--status-upgraded)">Upgraded</span>
    <span class="st" style="--c:var(--status-failed)">Failed</span>
    <span class="st" style="--c:var(--status-available)">Available</span>
    <button class="btn">Search now</button>
  </div>
  <div class="palettes">{palette_html(k)}</div>
  <div class="card mascot">{mascot}</div>
  <div class="card tone"><h5>Ton de voix</h5><ul>{tones}</ul></div>
  <p class="more"><a href="{slug}/README.md">README</a> · <a href="{slug}/tokens.json">tokens.json</a></p>
</section>'''

page = f'''<!doctype html>
<html lang="fr" data-theme="dark">
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Gonema — directions de marque (phase 2A)</title>
<link rel="icon" href="a-salle/favicon.svg">
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Instrument+Serif&family=Instrument+Sans:wght@400;500;600&family=IBM+Plex+Mono:wght@400&family=Archivo:wdth,wght@125,700&family=Geist:wght@400;500;600&family=Geist+Mono&family=Fraunces:opsz,wght,SOFT,WONK@9..144,400..700,0..100,0..1&family=Figtree:wght@400;500;600&family=JetBrains+Mono&display=swap" rel="stylesheet">
<style>
{css_vars}
html[data-theme="dark"]{{--page:#110F0D;--ink:#EFE6DA;--ink2:#B9AC9D;--line:rgba(239,230,218,.12)}}
html[data-theme="light"]{{--page:#F4EFE7;--ink:#1D1814;--ink2:#5A4F45;--line:rgba(29,24,20,.12)}}
*{{box-sizing:border-box}}
body{{margin:0;background:var(--page);color:var(--ink);font:15px/1.5 'Instrument Sans',system-ui,sans-serif}}
.top{{display:flex;justify-content:space-between;align-items:end;padding:40px 48px 8px;gap:24px}}
.top h1{{font:400 44px/1.05 'Instrument Serif',serif;margin:0}}
.top p{{margin:6px 0 0;color:var(--ink2);max-width:760px}}
#toggle{{font:500 14px 'Instrument Sans';padding:10px 16px;border-radius:999px;border:1px solid var(--line);background:transparent;color:var(--ink);cursor:pointer}}
#toggle:focus-visible{{outline:2px solid #8F96FF;outline-offset:2px}}
.grid{{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:24px;padding:24px 48px}}
.dir{{background:var(--bg-base);color:var(--text-primary);border:1px solid var(--border);border-radius:24px;padding:24px;display:flex;flex-direction:column;gap:14px;box-shadow:0 24px 60px -30px rgba(0,0,0,.45)}}
.dir header h2{{margin:2px 0 4px;font:600 26px/1.1 'Instrument Sans'}}
.kicker{{margin:0;color:var(--text-muted);font-size:12px;letter-spacing:.08em;text-transform:uppercase}}
.idea{{margin:0;color:var(--text-secondary)}}
.card{{background:var(--surface-glass);border:1px solid var(--border);border-radius:16px;padding:16px;backdrop-filter:blur(12px)}}
.hero{{display:grid;place-items:center;padding:28px;background:var(--bg-raised)}}
.sizes{{display:flex;gap:18px;justify-content:space-between;flex-wrap:wrap}}
.sizes div{{display:flex;gap:10px;align-items:end;flex-wrap:wrap}}
.sizes small,.type small{{width:100%;color:var(--text-muted);font-size:12px}}
.lock{{border-radius:14px;padding:20px 24px;border:1px solid var(--border)}}
.lock img{{display:block;height:56px;width:auto;max-width:100%}}
html[data-theme="dark"] .only-light, html[data-theme="light"] .only-dark{{display:none}}
.type p{{margin:0 0 6px}}
.disp{{font-size:40px;line-height:1.05}}
.ui{{font-size:15px;color:var(--text-secondary)}}
.mono-s{{font-size:12.5px;color:var(--text-secondary);overflow-wrap:anywhere}}
.tnum{{font-variant-numeric:tabular-nums;display:flex;gap:16px;font-size:15px}}
.statuses{{display:flex;flex-wrap:wrap;gap:8px;align-items:center}}
.st{{font-size:13px;font-weight:500;color:var(--c);border:1px solid color-mix(in srgb,var(--c) 45%,transparent);background:color-mix(in srgb,var(--c) 12%,transparent);padding:3px 10px;border-radius:999px}}
.st::before{{content:"";display:inline-block;width:7px;height:7px;border-radius:50%;background:var(--c);margin-right:6px;vertical-align:1px}}
.btn{{margin-left:auto;background:var(--accent);color:var(--accent-contrast);border:0;border-radius:10px;padding:8px 14px;font:600 13px 'Instrument Sans'}}
.palettes{{display:grid;gap:10px}}
.pal{{border-radius:14px;padding:12px;border:1px solid var(--border);background:var(--bg-raised)}}
.pal h5,.tone h5{{margin:0 0 8px;font-size:13px}}
.pal table{{width:100%;border-collapse:collapse;font-size:11.5px}}
.pal td{{padding:2px 4px;vertical-align:middle}}
.sw{{display:inline-block;width:18px;height:18px;border-radius:5px;border:1px solid rgba(128,128,128,.4)}}
.role{{color:var(--text-secondary)}}
.mono{{font-family:'IBM Plex Mono',monospace}}
.r{{text-align:right;white-space:nowrap}}
.ok{{color:var(--text-primary)}} .bad{{color:#fff;background:#B42A22;padding:0 3px;border-radius:3px}}
.dim{{color:var(--text-muted)}}
.monoi{{width:32px;height:32px;color:var(--text-primary)}}
.mascot{{background:var(--bg-raised)}} .mascot svg{{width:100%;height:auto;display:block}}
.tone ul{{margin:0;padding:0;list-style:none;display:grid;gap:8px}}
.tone li{{display:grid;gap:1px}} .tone b{{font-size:11px;color:var(--text-muted);font-weight:500;text-transform:uppercase;letter-spacing:.06em}}
.more{{margin:0;font-size:13px}} .more a{{color:var(--accent)}}
.reco{{margin:8px 48px 56px;padding:32px 36px;border:1px solid var(--line);border-radius:24px}}
.reco h2{{font:400 38px/1.1 'Instrument Serif',serif;margin:0 0 12px}}
.reco p,.reco li{{max-width:980px;color:var(--ink2)}} .reco strong{{color:var(--ink)}}
.foot{{padding:0 48px 40px;color:var(--ink2);font-size:13px}}
@media (max-width:1100px){{.grid{{grid-template-columns:1fr}}}}
</style></head>
<body>
<div class="top"><div><h1>Gonema — trois directions</h1>
<p>Phase 2A. Chaque colonne utilise sa propre palette (sombre par défaut, bascule claire en haut à droite). Contrastes WCAG 2.1 mesurés par script ; texte courant ≥ 4,5:1.</p></div>
<button id="toggle" type="button" aria-pressed="false">Thème clair</button></div>
<div class="grid">{cols}</div>
<section class="reco">
<h2>Recommandation : A — Salle</h2>
<p><strong>Pourquoi.</strong> C’est la seule direction qui dit « salle de cinéma privée » avant même de lire le nom, et la plus éloignée de toute la catégorie (*arr, Plex, Jellyfin : jaunes, oranges, violets, chevrons, radars). Le G-ardoise est une idée en une phrase, un geste plutôt qu’un pictogramme ; l’outremer unique sur noirs chauds est rare, élégant et ne concurrence aucune couleur de statut. Instrument Serif donne aux titres de films la place d’une affiche, l’UI en Instrument Sans reste neutre.</p>
<p><strong>À emprunter aux autres.</strong></p>
<ul>
<li><strong>De B :</strong> la discipline petite taille (favicon dessiné à part, grille de 8), Geist Mono ou IBM Plex Mono pour les releases et scores, et son ton factuel chiffré pour le moteur de décision, l’historique et les journaux.</li>
<li><strong>De C :</strong> la mascotte en volume (peluche aux bajoues de pop-corn), plus chaleureuse que le trait d’encre, recolorée dans la palette A ; et une dose de son humour pour l’onboarding et les états vides.</li>
</ul>
<p><strong>Risques à traiter en phase 2B.</strong> Le bras du clap devient une barre à 16 px (tester un favicon sans rayures, déjà fourni) ; Instrument Serif réservé aux tailles ≥ 24 px ; vérifier les verres dépolis au-dessus d’affiches claires (prévoir un voile).</p>
</section>
<p class="foot">Polices : toutes sous SIL Open Font License 1.1 (Google Fonts). Wordmarks vectorisés depuis les fichiers OFL ; aucune balise &lt;text&gt; dans les marques. Recherche d’antériorité du nom « Gonema » : rien trouvé sur le web et GitHub (recherche sommaire, ce n’est pas une vérification juridique).</p>
<script>
const t=document.getElementById('toggle');
t.addEventListener('click',()=>{{const h=document.documentElement;const d=h.dataset.theme==='dark';h.dataset.theme=d?'light':'dark';t.textContent=d?'Thème sombre':'Thème clair';t.setAttribute('aria-pressed',String(d));}});
if(location.hash==='#light'){{t.click();}}
</script>
</body></html>'''
open(f'{ROOT}/index.html', 'w').write(page)
print('index written')
