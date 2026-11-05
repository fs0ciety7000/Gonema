import json, os, subprocess, struct
import marks
marks.A_STROKES[0] = (36, 22)          # final: softened didone contrast (was 36/16) to sit with Archivo
from marks import a_mark, f
from outline import outline
from fav import fav16, fav32
import kit_mascot
SCR = '/tmp/claude-0/-home-user-Gonema/e3b3031e-119a-5939-a129-16b173290428/scratchpad/'
K = '/home/user/Gonema/docs/brand/kit'
FONT = SCR + 'fonts/ai-125-600.ttf'
WORD_SIZE, WORD_TRACK = 96, 0.08
INK_L, ACC_L, INK_D, ACC_D = '#1C1713', '#3B3FD6', '#F2E9DD', '#8F96FF'
BG_D, BG_L = '#12100E', '#F3EDE3'
VARIANTS = {'on-light': (INK_L, ACC_L), 'on-dark': (INK_D, ACC_D), 'black': ('#000000', '#000000'),
            'white': ('#FFFFFF', '#FFFFFF'), 'currentcolor': ('currentColor', 'currentColor')}

def svg(body, vb, title):
    x, y, W, H = vb.split()
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vb}" width="{W}" height="{H}" role="img" aria-labelledby="t">\n'
            f'  <title id="t">{title}</title>\n  {body}\n</svg>\n')
def write(p, s, mode='w'):
    os.makedirs(os.path.dirname(p), exist_ok=True); open(p, mode).write(s)

WP, (wx0, wy0, wx1, wy1) = outline(FONT, 'GONEMA', WORD_SIZE, tracking=WORD_TRACK)
CAP = -wy0                             # cap height in units
WW = wx1 - wx0
MARK_BOX = (44, 9, 212, 246)           # measured bbox of the mark in its 256 canvas

def word(ink, tx, base):
    return f'<g id="wordmark" transform="translate({f(tx)} {f(base)})"><path fill="{ink}" d="{WP}"/></g>'

def build_logos():
    out = {}
    for v, (ink, acc) in VARIANTS.items():
        sfx = f'-{v}'
        write(f'{K}/logo/mark{sfx}.svg', svg(f'<g id="symbol">{a_mark(ink, acc)}</g>', '0 0 256 256', f'Gonema mark ({v})'))
        pad = 8
        write(f'{K}/logo/wordmark{sfx}.svg', svg(word(ink, -wx0 + pad, CAP + pad), f'0 0 {f(WW + 2*pad)} {f(CAP + 2*pad)}', f'Gonema wordmark ({v})'))
        # horizontal: wordmark optically centred on the G ring (cy=166), gap = 0.5 x cap height... measured from mark bbox
        gap = 0.62 * CAP
        tx = MARK_BOX[2] + gap - wx0
        base = 166 + CAP / 2
        x0 = MARK_BOX[0] - 4; W = MARK_BOX[2] + gap + WW + 4 - x0
        write(f'{K}/logo/lockup-horizontal{sfx}.svg',
              svg(f'<g id="symbol">{a_mark(ink, acc)}</g>\n  {word(ink, tx, base)}', f'{f(x0)} 0 {f(W)} 256', f'Gonema horizontal lockup ({v})'))
        # stacked: mark centred above a smaller wordmark (0.7x) so the symbol leads
        k = 0.7; ww, cap = WW * k, CAP * k
        sgap = 0.7 * cap
        Wst = max(ww, 168) + 16
        mx = Wst / 2 - (MARK_BOX[0] + MARK_BOX[2]) / 2
        base_s = MARK_BOX[3] + sgap + cap
        Hst = base_s + 8
        wtx = (Wst - ww) / 2 - wx0 * k
        write(f'{K}/logo/lockup-stacked{sfx}.svg',
              svg(f'<g id="symbol" transform="translate({f(mx)} 0)">{a_mark(ink, acc)}</g>\n  '
                  f'<g id="wordmark" transform="translate({f(wtx)} {f(base_s)}) scale({k})"><path fill="{ink}" d="{WP}"/></g>',
                  f'0 0 {f(Wst)} {f(Hst)}', f'Gonema stacked lockup ({v})'))
    return CAP

def build_icons():
    # dedicated small-size drawings
    for n, fn in (('favicon-16', fav16), ('favicon', fav32)):
        b, vb = fn(BG_D, INK_D, ACC_D)
        write(f'{K}/icons/{n}.svg', svg(b, vb, 'Gonema'))
    # app icon artwork (full mark on tile); maskable = full-bleed, mark inside the 80 % safe circle
    tile = f'<rect width="512" height="512" rx="112" fill="{BG_D}"/><g transform="translate(64 64.75) scale(1.5)">{a_mark(INK_D, ACC_D)}</g>'
    write(f'{K}/icons/icon.svg', svg(tile, '0 0 512 512', 'Gonema app icon'))
    mask = f'<rect width="512" height="512" fill="{BG_D}"/><g transform="translate(89.6 90.25) scale(1.3)">{a_mark(INK_D, ACC_D)}</g>'
    write(f'{K}/icons/icon-maskable.svg', svg(mask, '0 0 512 512', 'Gonema maskable icon'))
    apple = f'<rect width="512" height="512" fill="{BG_D}"/><g transform="translate(76.8 77.5) scale(1.4)">{a_mark(INK_D, ACC_D)}</g>'
    write(f'{K}/icons/apple-touch-icon.svg', svg(apple, '0 0 512 512', 'Gonema apple touch icon'))
    manifest = {"name": "Gonema", "short_name": "Gonema", "description": "Your private cinema: a self-hosted movie collection manager.",
                "start_url": "/", "display": "standalone", "background_color": BG_D, "theme_color": BG_D,
                "icons": [{"src": "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
                          {"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
                          {"src": "/icon-maskable-192.png", "sizes": "192x192", "type": "image/png", "purpose": "maskable"},
                          {"src": "/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"}]}
    write(f'{K}/icons/site.webmanifest', json.dumps(manifest, indent=2) + '\n')
    write(f'{K}/icons/head-snippet.html', '''<!-- Gonema icons: copy the files of docs/brand/kit/icons/ to the web root -->
<link rel="icon" href="/favicon.ico" sizes="16x16 32x32 48x48">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="/apple-touch-icon.png">
<link rel="manifest" href="/site.webmanifest">
<meta name="theme-color" content="#12100E" media="(prefers-color-scheme: dark)">
<meta name="theme-color" content="#F3EDE3" media="(prefers-color-scheme: light)">
''')
    jobs = [{'svg': f'{K}/icons/favicon-16.svg', 'out': f'{K}/icons/favicon-16.png', 'w': 16},
            {'svg': f'{K}/icons/favicon.svg', 'out': f'{K}/icons/favicon-32.png', 'w': 32},
            {'svg': f'{K}/icons/favicon.svg', 'out': f'{K}/icons/favicon-48.png', 'w': 48},
            {'svg': f'{K}/icons/apple-touch-icon.svg', 'out': f'{K}/icons/apple-touch-icon.png', 'w': 180},
            {'svg': f'{K}/icons/icon.svg', 'out': f'{K}/icons/icon-192.png', 'w': 192},
            {'svg': f'{K}/icons/icon.svg', 'out': f'{K}/icons/icon-512.png', 'w': 512},
            {'svg': f'{K}/icons/icon-maskable.svg', 'out': f'{K}/icons/icon-maskable-192.png', 'w': 192},
            {'svg': f'{K}/icons/icon-maskable.svg', 'out': f'{K}/icons/icon-maskable-512.png', 'w': 512}]
    return jobs

def pack_ico(pngs, out):
    data = [open(p, 'rb').read() for p in pngs]
    hdr = struct.pack('<HHH', 0, 1, len(data)); off = 6 + 16 * len(data); ent = b''
    for p, d in zip(pngs, data):
        w, h = struct.unpack('>II', d[16:24])
        ent += struct.pack('<BBBBHHII', w % 256, h % 256, 0, 0, 1, 32, len(d), off); off += len(d)
    open(out, 'wb').write(hdr + ent + b''.join(data))

def build_mascot():
    for n, fn in kit_mascot.POSES.items():
        write(f'{K}/mascot/kernel-{n}.svg', fn())

def render(jobs):
    json.dump(jobs, open(SCR + 'jobs.json', 'w'))
    subprocess.run(['node', os.path.join(os.path.dirname(os.path.abspath(__file__)), 'render.js'), SCR + 'jobs.json'], check=True)

if __name__ == '__main__':
    build_logos(); build_mascot(); jobs = build_icons()
    R = f'{K}/renders'; os.makedirs(R, exist_ok=True)
    for v, bg in (('on-dark', BG_D), ('on-light', BG_L)):
        jobs += [{'svg': f'{K}/logo/mark-{v}.svg', 'out': f'{R}/mark-{v}-512.png', 'w': 512, 'bg': bg},
                 {'svg': f'{K}/logo/lockup-horizontal-{v}.svg', 'out': f'{R}/lockup-horizontal-{v}-1600.png', 'w': 1600, 'h': 400, 'bg': bg},
                 {'svg': f'{K}/logo/lockup-stacked-{v}.svg', 'out': f'{R}/lockup-stacked-{v}-800.png', 'w': 800, 'h': 640, 'bg': bg}]
    for s in (16, 32, 64, 512):
        jobs.append({'svg': f'{K}/logo/mark-on-dark.svg', 'out': f'{R}/mark-on-dark-{s}.png', 'w': s})
        jobs.append({'svg': f'{K}/logo/mark-on-light.svg', 'out': f'{R}/mark-on-light-{s}.png', 'w': s})
    render(jobs)
    pack_ico([f'{K}/icons/favicon-16.png', f'{K}/icons/favicon-32.png', f'{K}/icons/favicon-48.png'], f'{K}/icons/favicon.ico')
    print('cap height', CAP, 'word width', WW)
