import math
def f(v): return ('%.2f' % v).rstrip('0').rstrip('.')
def pt(cx, cy, r, deg):
    a = math.radians(deg); return cx + r*math.cos(a), cy + r*math.sin(a)
def rot(p, c, deg):
    a = math.radians(deg); x, y = p[0]-c[0], p[1]-c[1]
    return (c[0] + x*math.cos(a) - y*math.sin(a), c[1] + x*math.sin(a) + y*math.cos(a))
def poly(pts): return 'M' + ' L'.join(f'{f(x)} {f(y)}' for x, y in pts) + ' Z'
def circle(cx, cy, r, ccw=False):
    s = 0 if ccw else 1
    return f'M{f(cx-r)} {f(cy)} A{f(r)} {f(r)} 0 1 {s} {f(cx+r)} {f(cy)} A{f(r)} {f(r)} 0 1 {s} {f(cx-r)} {f(cy)} Z'

# ---------- A : Salle — Didone-stressed G (the slate) + lifted clapper arm ----------
A_STROKES = [(36, 16), (40, 26)]
def a_parts(small=False):
    cx, cy, R = 132, 166, 80
    ts, tt = A_STROKES[1] if small else A_STROKES[0]
    rx, ry = R - ts, R - tt
    bar_h = tt + 4
    ring = (f'M{f(cx)} {f(cy-R)} A{R} {R} 0 1 0 {f(cx+R)} {f(cy)} '
            f'H{f(cx+R)} V{f(cy - bar_h/2 - 6)} H{f(cx+6)} V{f(cy + bar_h/2 - 6)} H{f(cx+rx)} '
            f'A{f(rx)} {f(ry)} 0 1 1 {f(cx)} {f(cy-ry)} Z')
    # clapper arm, hinged at left, opened 12 degrees
    x0, x1 = cx - R, cx + R - 12
    gap = 8 if not small else 12
    th = 32 if not small else 40
    yb = cy - R - gap; yt = yb - th
    piv = (x0, yb)
    corners = [(x0, yt), (x1, yt), (x1, yb), (x0, yb)]
    arm = poly([rot(p, piv, -15) for p in corners])
    holes = ''
    if not small:
        for sx in (40, 80, 120):
            w = 18
            hp = [(x0+sx, yt+7), (x0+sx+w, yt+7), (x0+sx+w-14, yb-7), (x0+sx-14, yb-7)]
            holes += ' ' + poly(list(reversed([rot(p, piv, -15) for p in hp])))
    return ring, arm + holes

def a_mark(c1, c2, small=False):
    ring, arm = a_parts(small)
    return (f'<path fill="{c1}" d="{ring}"/>\n  <path fill="{c2}" fill-rule="evenodd" d="{arm}"/>')

# ---------- B : Sprocket — squared G cut from a film strip ----------
def b_parts(small=False):
    t = 44
    g = ('M212 24 H64 A40 40 0 0 0 24 64 V192 A40 40 0 0 0 64 232 H192 A40 40 0 0 0 232 192 '
         'V120 H188 V188 H68 V68 H212 Z')
    if small:
        holes = [(36, 96, 20, 24), (36, 148, 20, 24)]
    else:
        holes = [(37, 84, 18, 20), (37, 118, 18, 20), (37, 152, 18, 20)]
    hd = ''
    for x, y, w, h in holes:
        rr = 4
        hd += (f' M{x+rr} {y} H{x+w-rr} A{rr} {rr} 0 0 1 {x+w} {y+rr} V{y+h-rr} A{rr} {rr} 0 0 1 {x+w-rr} {y+h} '
               f'H{x+rr} A{rr} {rr} 0 0 1 {x} {y+h-rr} V{y+rr} A{rr} {rr} 0 0 1 {x+rr} {y} Z')
    return g + hd

def b_mark(c1, c2, small=False):
    # crossbar in accent: split it out of the G
    d = b_parts(small)
    bar = 'M132 120 H188 V160 H132 Z'
    return f'<path fill="{c1}" fill-rule="evenodd" d="{d}"/>\n  <path fill="{c2}" d="{bar}"/>'

# ---------- C : Matinee — reel bowl + film-strip stem = lowercase "g" ----------
def c_parts(small=False):
    cx, cy, R = 125, 100, 80
    disc = circle(cx, cy, R)
    hub = circle(cx, cy, 13 if not small else 15, ccw=True)
    holes = ''
    hr, hd = (21, 46) if not small else (24, 46)
    for ang in (-90, 30, 150):
        hx, hy = pt(cx, cy, hd, ang); holes += ' ' + circle(hx, hy, hr, ccw=True)
    w = 34 if not small else 40
    xr = cx + R + 6 if not small else cx + R + 8
    xl = xr - w
    ytop = cy - R + 8
    yturn = 186
    ro = 50; ri = ro - w
    end_x = 77
    tail = (f'M{f(xl)} {f(ytop)} H{f(xr)} V{f(yturn)} A{ro} {ro} 0 0 1 {f(xr-ro)} {f(yturn+ro)} '
            f'H{f(end_x)} A{w/2} {w/2} 0 0 1 {f(end_x)} {f(yturn+ro-w)} H{f(xr-ro)} '
            f'A{ri} {ri} 0 0 0 {f(xl)} {f(yturn)} Z')
    return disc + ' ' + hub + holes, tail

def c_mark(c1, c2, small=False):
    reel, tail = c_parts(small)
    return f'<path fill="{c2}" d="{tail}"/>\n  <path fill="{c1}" fill-rule="evenodd" d="{reel}"/>'

def svg(body, vb='0 0 256 256', title='Gonema', w=None, h=None):
    x, y, W, H = vb.split()
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vb}" width="{w or W}" height="{h or H}" role="img" aria-labelledby="t">\n'
            f'  <title id="t">{title}</title>\n  {body}\n</svg>\n')
