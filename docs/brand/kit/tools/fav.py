from marks import rot, poly, f
def g_ring(cx, cy, R, ts, tt, bar_t, bar_b, bar_x):
    rx, ry = R - ts, R - tt
    return (f'M{f(cx)} {f(cy-R)} A{f(R)} {f(R)} 0 1 0 {f(cx+R)} {f(cy)} V{f(bar_t)} H{f(bar_x)} V{f(bar_b)} H{f(cx+rx)} '
            f'A{f(rx)} {f(ry)} 0 1 1 {f(cx)} {f(cy-ry)} Z')
def arm(x0, yb, length, th, ang=-15, stripes=()):
    pts = [(x0, yb-th), (x0+length, yb-th), (x0+length, yb), (x0, yb)]
    d = poly([rot(p, (x0, yb), ang) for p in pts])
    for sx, w, inset in stripes:
        hp = [(x0+sx, yb-th+inset), (x0+sx+w, yb-th+inset), (x0+sx+w-(th-2*inset)*0.45, yb-inset), (x0+sx-(th-2*inset)*0.45, yb-inset)]
        d += ' ' + poly(list(reversed([rot(p, (x0, yb), ang) for p in hp])))
    return d
def fav16(bg, ink, acc, tile=True):
    t = f'<rect width="16" height="16" rx="3.5" fill="{bg}"/>' if tile else ''
    ring = g_ring(8.2, 10.4, 5.1, 2.4, 1.7, 9.6, 11.4, 9.0)
    a = arm(3.1, 4.6, 9.2, 2.0)
    return f'{t}<path fill="{ink}" d="{ring}"/><path fill="{acc}" d="{a}"/>', '0 0 16 16'
def fav32(bg, ink, acc, tile=True):
    t = f'<rect width="32" height="32" rx="7" fill="{bg}"/>' if tile else ''
    ring = g_ring(16.4, 20.2, 10, 4.4, 2.9, 18.6, 21.6, 17.4)
    a = arm(6.4, 8.6, 18, 4.0, stripes=((5.2, 2.4, 0.9), (10.6, 2.4, 0.9)))
    return f'{t}<path fill="{ink}" d="{ring}"/><path fill="{acc}" fill-rule="evenodd" d="{a}"/>', '0 0 32 32'
