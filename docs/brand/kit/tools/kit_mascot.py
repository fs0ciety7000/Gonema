from mascot import gopher
INK = '#1C1713'; BONE = '#F3EDE3'; BONE2 = '#E4D8C8'; PAPER = '#FBF8F2'; BLUSH = '#EBC3B5'
ACC = '#3B3FD6'; ACC_D = '#8F96FF'
LW = 4      # outline weight (all silhouettes)
DW = 3      # inner details (mouth, brows, motion lines, sprockets)
C = dict(fur=BONE, fur2=BONE2, cream=PAPER, ink=INK, blush=BLUSH, white='#FFFFFF', line=INK, lw=LW, dw=DW, tw=2.5)
ST = f'stroke="{INK}" stroke-width="{LW}" stroke-linejoin="round" stroke-linecap="round"'
SD = f'stroke="{INK}" stroke-width="{DW}" stroke-linejoin="round" stroke-linecap="round"'
CAP = (f'<path d="M92 50 Q120 30 150 46 L146 26 Q118 16 94 30 Z" fill="{ACC}" {ST}/>'
       f'<path d="M90 50 Q120 40 152 48" fill="none" {ST}/>')
def paw(x, y, rx=14, ry=11):
    return f'<ellipse cx="{x}" cy="{y}" rx="{rx}" ry="{ry}" fill="{BONE2}" {ST}/>'
def wrap(body, vb, title):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vb}" role="img" aria-labelledby="t">\n'
            f'  <title id="t">{title}</title>\n  {body}\n</svg>\n')

def neutral():
    return wrap(f'<g transform="translate(20 10)">{gopher(C)}</g>', '0 0 290 310', 'Kernel, neutral')

def welcome():
    g = gopher(C, paws=False)
    limb = 'M168 170 Q204 146 212 108'
    arm = (f'<path d="{limb}" fill="none" stroke="{INK}" stroke-width="{16 + 2*LW}" stroke-linecap="round"/>'
           f'<path d="{limb}" fill="none" stroke="{BONE}" stroke-width="16" stroke-linecap="round"/>')  # raised arm: bone limb, ink outline
    wave = paw(214, 96, 13, 15)
    motion = f'<path d="M234 74 Q244 88 240 104 M248 66 Q262 86 256 110" fill="none" stroke="{ACC_D}" stroke-width="{DW}" stroke-linecap="round"/>'
    other = paw(98, 180)
    return wrap(f'<g transform="translate(20 20)">{arm}{g}{wave}{other}{motion}{CAP}</g>', '0 0 310 320', 'Kernel welcomes you (onboarding)')

def empty():
    beam = f'<path d="M190 168 L330 110 Q350 180 330 250 Z" fill="{ACC_D}" opacity="0.4"/>'
    torch = (f'<path d="M140 168 L182 160 L186 182 L144 190 Z" fill="{BONE2}" {ST}/>'
             f'<path d="M182 154 L192 152 L198 186 L188 188 Z" fill="{ACC}" {ST}/>')
    body = beam + gopher(C, paws=False) + torch + paw(132, 182) + paw(160, 182) + CAP
    return wrap(f'<g transform="translate(10 20)">{body}</g>', '0 0 370 320', 'Kernel lights an empty room (empty library)')

def notfound():
    g2 = gopher(C, mood='worried', paws=False)
    curtain = (f'<path d="M-90 -10 H70 C60 60 76 120 62 180 C52 230 70 266 64 296 Q20 306 -10 296 Q-50 306 -90 296 Z" fill="{ACC}" {ST}/>'
               f'<path d="M-40 4 C-50 90 -30 190 -44 292 M10 4 C0 100 20 200 6 292" fill="none" stroke="{ACC_D}" stroke-width="{DW}" stroke-linecap="round" opacity="0.8"/>')
    body = f'<g transform="translate(-20 0)">{g2}</g>' + curtain + paw(64, 170, 13, 16)
    return wrap(f'<g transform="translate(110 14)">{body}</g>', '0 0 330 330', 'Kernel peeks from behind the curtain (404)')

def failed():
    g = gopher(C, mood='worried', paws=False)
    # torn film strip held in both paws: two pieces with a jagged tear in the middle
    def strip(x0, x1, tear_right):
        y0, y1 = 186, 214
        tear = (f'L{x1} {y0} L{x1-6} {y0+7} L{x1+2} {y0+14} L{x1-6} {y0+21} L{x1} {y1}' if tear_right
                else f'L{x1} {y0}')
        if tear_right:
            d = f'M{x0} {y0} {tear} L{x0} {y1} Z'
        else:
            d = f'M{x0} {y0} L{x1} {y0} L{x1} {y1} L{x0} {y1} L{x0+6} {y1-7} L{x0-2} {y0+14} L{x0+6} {y0+7} Z'
        holes = ''.join(f'<rect x="{x}" y="{y0+5}" width="6" height="5" rx="1" fill="{INK}"/><rect x="{x}" y="{y1-10}" width="6" height="5" rx="1" fill="{INK}"/>'
                        for x in range(min(x0, x1) + 10, max(x0, x1) - 8, 14))
        return f'<path d="{d}" fill="{PAPER}" {ST}/>' + holes
    left = f'<g transform="rotate(-10 80 200)">{strip(34, 112, True)}</g>'
    right = f'<g transform="rotate(12 170 200)">{strip(130, 210, False)}</g>'
    sweat = f'<path d="M176 70 Q170 82 176 86 Q182 82 176 70 Z" fill="{ACC_D}" {SD}/>'
    body = g + left + right + paw(70, 196) + paw(178, 204) + sweat
    return wrap(f'<g transform="translate(30 16)">{body}</g>', '0 0 300 320', 'Kernel holds a torn film strip (failed download)')

POSES = {'neutral': neutral, 'welcome': welcome, 'empty-library': empty, '404': notfound, 'failed-download': failed}
