"""Outline text from a font file into SVG path data (y-down), returns (d, width, ascent_top, baseline)."""
from fontTools.ttLib import TTFont
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.pens.boundsPen import BoundsPen

def outline(font_path, text, size, tracking=0.0, x0=0.0, baseline=0.0, kern_pairs=None):
    f = TTFont(font_path)
    upm = f['head'].unitsPerEm
    s = size / upm
    cmap = f.getBestCmap(); gs = f.getGlyphSet(); hmtx = f['hmtx']
    ds = []; x = x0; minx = miny = 1e9; maxx = maxy = -1e9
    prev = None
    for ch in text:
        gn = cmap[ord(ch)]
        if kern_pairs and prev and (prev+ch) in kern_pairs:
            x += kern_pairs[prev+ch] * size
        pen = SVGPathPen(gs, ntos=lambda v: ('%.2f' % v).rstrip('0').rstrip('.'))
        tp = TransformPen(pen, (s, 0, 0, -s, x, baseline))
        gs[gn].draw(tp)
        bp = BoundsPen(gs); gs[gn].draw(bp)
        if bp.bounds:
            a, b, c, d = bp.bounds
            minx = min(minx, x + a*s); maxx = max(maxx, x + c*s)
            miny = min(miny, baseline - d*s); maxy = max(maxy, baseline - b*s)
        ds.append(pen.getCommands())
        x += hmtx[gn][0]*s + tracking*size
        prev = ch
    return ' '.join(ds), (minx, miny, maxx, maxy)
