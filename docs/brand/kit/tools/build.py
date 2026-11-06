import json, os, subprocess
from marks import a_mark, b_mark, c_mark, svg, f
from outline import outline
from props import mascot_a, mascot_b, mascot_c
from palettes import P
from contrast import measure

ROOT = '/home/user/Gonema/docs/brand/directions'
FONTS = '/tmp/claude-0/-home-user-Gonema/e3b3031e-119a-5939-a129-16b173290428/scratchpad/fonts/'
D = {
 'a': dict(slug='a-salle', name='Salle', mark=a_mark, mascot=mascot_a, mascot_vb='0 0 760 330',
           font=FONTS+'instrserif.ttf', text='Gonema', size=176, tracking=-0.005,
           mark_colors=lambda t: (t['text-primary'], t['accent']), fav_bg='#12100E'),
 'b': dict(slug='b-sprocket', name='Sprocket', mark=b_mark, mascot=mascot_b, mascot_vb='0 0 680 330',
           font=FONTS+'archivo-exp.ttf', text='GONEMA', size=104, tracking=0.07,
           mark_colors=lambda t: (t['text-primary'], t['accent']), fav_bg='#0F0E0D'),
 'c': dict(slug='c-matinee', name='Matinée', mark=c_mark, mascot=mascot_c, mascot_vb='0 0 680 330',
           font=FONTS+'fraunces-soft.ttf', text='gonema', size=200, tracking=-0.012,
           mark_colors=lambda t: (t['accent'], t['text-primary']), fav_bg='#18110F'),
}

OFL = 'SIL Open Font License 1.1'
TYPO = {
 'a': {'display': {'family': 'Instrument Serif', 'use': 'film titles, page heads, wordmark', 'licence': OFL},
       'ui': {'family': 'Instrument Sans', 'use': 'interface, body; font-feature-settings tnum for figures', 'licence': OFL},
       'mono': {'family': 'IBM Plex Mono', 'use': 'release names, paths, sizes, logs', 'licence': OFL}},
 'b': {'display': {'family': 'Archivo (wdth 125, Expanded)', 'use': 'titles in caps, section heads, wordmark', 'licence': OFL},
       'ui': {'family': 'Geist', 'use': 'interface, body; tnum for figures', 'licence': OFL},
       'mono': {'family': 'Geist Mono', 'use': 'release names, scores, sizes, logs', 'licence': OFL}},
 'c': {'display': {'family': 'Fraunces (opsz 144, SOFT 100, WONK 1)', 'use': 'film titles, greetings, wordmark', 'licence': OFL},
       'ui': {'family': 'Figtree', 'use': 'interface, body; tnum for figures', 'licence': OFL},
       'mono': {'family': 'JetBrains Mono', 'use': 'release names, paths, logs', 'licence': OFL}},
}

def wordmark_path(k):
    d = D[k]
    path, (x0, y0, x1, y1) = outline(d['font'], d['text'], d['size'], tracking=d['tracking'])
    return path, (x0, y0, x1, y1)

def place_word(k, mark_h=256):
    """returns (transform-ready path group, total width) for lockup: mark 256 box then wordmark."""
    path, (x0, y0, x1, y1) = wordmark_path(k)
    gap = {'a': 30, 'b': 40, 'c': 26}[k]
    # vertical placement: baseline y in lockup coordinates
    base = {'a': 236, 'b': 128 + (0 - y0) / 2, 'c': 180}[k]
    tx = 256 + gap - x0
    W = 256 + gap + (x1 - x0) + 8
    return path, tx, base, W

def write(p, s):
    os.makedirs(os.path.dirname(p), exist_ok=True); open(p, 'w').write(s)

jobs = []
for k, d in D.items():
    out = os.path.join(ROOT, d['slug']); pl = P[k]
    L, Dk = pl['light'], pl['dark']
    # mark (master, for light backgrounds) + dark variant
    c1, c2 = d['mark_colors'](L)
    write(f'{out}/mark.svg', svg(d['mark'](c1, c2), title=f'Gonema mark, direction {d["name"]}'))
    c1d, c2d = d['mark_colors'](Dk)
    write(f'{out}/mark-on-dark.svg', svg(d['mark'](c1d, c2d), title=f'Gonema mark for dark backgrounds, direction {d["name"]}'))
    write(f'{out}/mark-mono.svg', svg(d['mark']('#000000', '#000000').replace('fill="#000000"', 'fill="currentColor"'), title='Gonema mark, one colour (currentColor)'))
    # favicon: small-size cut on a warm-black tile
    tile = f'<rect width="256" height="256" rx="60" fill="{d["fav_bg"]}"/>\n  <g transform="translate(28 28) scale(0.78125)">\n  {d["mark"](c1d, c2d, small=True)}\n  </g>'
    write(f'{out}/favicon.svg', svg(tile, title='Gonema favicon'))
    # wordmark alone
    path, (x0, y0, x1, y1) = wordmark_path(k)
    pad = 8
    vb = f'{f(x0-pad)} {f(y0-pad)} {f(x1-x0+2*pad)} {f(y1-y0+2*pad)}'
    write(f'{out}/wordmark.svg', svg(f'<path fill="{L["text-primary"]}" d="{path}"/>', vb=vb, title='Gonema wordmark'))
    # lockups
    path, tx, base, W = place_word(k)
    for theme, T in (('dark', Dk), ('light', L)):
        m1, m2 = d['mark_colors'](T)
        body = (f'<g id="symbol">\n  {d["mark"](m1, m2)}\n  </g>\n  '
                f'<g id="wordmark" transform="translate({f(tx)} {f(base)})"><path fill="{T["text-primary"]}" d="{path}"/></g>')
        write(f'{out}/lockup-{theme}.svg', svg(body, vb=f'0 0 {f(W)} 256', title=f'Gonema lockup for {theme} backgrounds'))
    # mascot
    write(f'{out}/mascot.svg', svg(d['mascot'](pl), vb=d['mascot_vb'], title='Kernel, the Gonema mascot (sketch)'))
    # tokens
    def role(T, name):
        v = T[name]
        if isinstance(v, tuple):
            h = v[0].lstrip('#'); r, g, b = (int(h[i:i+2], 16) for i in (0, 2, 4))
            return f'rgba({r}, {g}, {b}, {v[1]})'
        return v
    tok = {'direction': d['slug'], 'name': d['name'],
           'color': {th: {r: role(pl[th], r) for r in pl[th]} for th in ('dark', 'light')},
           'typography': TYPO[k],
           'contrast_wcag21_note': 'Ratios measured with WCAG 2.1 relative luminance. surface-glass measured as its rgba composited on bg-base (over posters it varies: keep a scrim). Thresholds: body text 4.5, large text and UI 3.0.',
           'contrast_wcag21': {th: measure(pl[th]) for th in ('dark', 'light')}}
    write(f'{out}/tokens.json', json.dumps(tok, indent=2, ensure_ascii=False))
    # renders
    os.makedirs(f'{out}/renders', exist_ok=True)
    for s in (16, 32, 64, 512):
        jobs.append({'svg': f'{out}/favicon.svg', 'out': f'{out}/renders/favicon-{s}.png', 'w': s})
        jobs.append({'svg': f'{out}/mark.svg', 'out': f'{out}/renders/mark-{s}.png', 'w': s})
        jobs.append({'svg': f'{out}/mark-on-dark.svg', 'out': f'{out}/renders/mark-on-dark-{s}.png', 'w': s})
    jobs.append({'svg': f'{out}/lockup-dark.svg', 'out': f'{out}/renders/lockup-dark-1200.png', 'w': 1200, 'h': 300, 'bg': Dk['bg-base']})
    jobs.append({'svg': f'{out}/lockup-light.svg', 'out': f'{out}/renders/lockup-light-1200.png', 'w': 1200, 'h': 300, 'bg': L['bg-base']})
    jobs.append({'svg': f'{out}/mascot.svg', 'out': f'{out}/renders/mascot-1200.png', 'w': 1200, 'h': 540, 'bg': L['bg-raised']})
json.dump(jobs, open('/tmp/claude-0/-home-user-Gonema/e3b3031e-119a-5939-a129-16b173290428/scratchpad/jobs.json', 'w'))
subprocess.run(['node', os.path.join(os.path.dirname(os.path.abspath(__file__)), 'render.js'), '/tmp/claude-0/-home-user-Gonema/e3b3031e-119a-5939-a129-16b173290428/scratchpad/jobs.json'], check=True)
print('built')
