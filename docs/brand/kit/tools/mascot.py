# Original gopher-inspired mascot ("Kernel"): pear-shaped seated body, small close-set dot eyes,
# big cheek pouches, two small teeth, caramel colour. Deliberately unlike the Go gopher
# (cyan, tall bean body, huge wide-set eyes).
BODY = ('M120 38 C162 38 186 68 186 104 C186 124 182 136 188 150 C202 180 208 205 208 228 '
        'C208 262 172 282 120 282 C68 282 32 262 32 228 C32 205 38 180 52 150 '
        'C58 136 54 124 54 104 C54 68 78 38 120 38 Z')

def gopher(c, style='flat', mood='happy', paws=True, belly=True):
    """c: dict fur, fur2 (shade), cream, ink, blush, white, line (stroke colour or None), lw"""
    line = c.get('line'); lw = c.get('lw', 0)
    st = f' stroke="{line}" stroke-width="{lw}" stroke-linejoin="round" stroke-linecap="round"' if line else ''
    o = []
    # tail
    o.append(f'<path d="M196 262 C224 262 236 244 230 230 C224 240 212 246 196 244 Z" fill="{c["fur2"]}"{st}/>')
    # ears
    for x in (70, 170):
        o.append(f'<circle cx="{x}" cy="60" r="18" fill="{c["fur"]}"{st}/>')
        o.append(f'<circle cx="{x}" cy="60" r="8" fill="{c["blush"]}"/>')
    o.append(f'<path d="{BODY}" fill="{c["fur"]}"{st}/>')
    if belly:
        o.append(f'<ellipse cx="120" cy="222" rx="54" ry="50" fill="{c["cream"]}"/>')
    # cheek pouches (the gag: they get stuffed)
    for x in (74, 166):
        o.append(f'<ellipse cx="{x}" cy="130" rx="22" ry="18" fill="{c["fur2"]}"/>')
        o.append(f'<ellipse cx="{x}" cy="132" rx="9" ry="6" fill="{c["blush"]}" opacity="0.9"/>')
    # muzzle
    o.append(f'<ellipse cx="120" cy="130" rx="32" ry="23" fill="{c["cream"]}"{st}/>')
    # eyes: small, close-set
    if mood == 'sleepy':
        for x in (101, 139):
            o.append(f'<path d="M{x-8} 100 Q{x} 108 {x+8} 100" fill="none" stroke="{c["ink"]}" stroke-width="{c.get('dw',4)}" stroke-linecap="round"/>')
    else:
        for x in (101, 139):
            o.append(f'<ellipse cx="{x}" cy="99" rx="7" ry="9" fill="{c["ink"]}"/>')
            o.append(f'<circle cx="{x+2.5}" cy="95.5" r="2.6" fill="{c["white"]}"/>')
    # brows for 'worried'
    if mood == 'worried':
        o.append(f'<path d="M92 86 L108 80 M148 86 L132 80" stroke="{c["ink"]}" stroke-width="{c.get('dw',4)}" stroke-linecap="round"/>')
    # nose
    o.append(f'<path d="M111 117 Q120 112 129 117 Q126 125 120 126 Q114 125 111 117 Z" fill="{c["ink"]}"/>')
    # mouth + two small teeth
    o.append(f'<path d="M120 126 V133 M108 133 Q114 139 120 133 Q126 139 132 133" fill="none" stroke="{c["ink"]}" stroke-width="{c.get('dw',3)}" stroke-linecap="round"/>')
    o.append(f'<path d="M113.5 136 H119 V146 Q116 148 113.5 146 Z M121 136 H126.5 V146 Q124 148 121 146 Z" fill="{c["white"]}" stroke="{c["ink"]}" stroke-width="{c.get('tw',2)}" stroke-linejoin="round"/>')
    # feet
    for x in (88, 152):
        o.append(f'<ellipse cx="{x}" cy="278" rx="26" ry="11" fill="{c["fur2"]}"{st}/>')
    if paws:
        for x in (98, 142):
            o.append(f'<ellipse cx="{x}" cy="178" rx="14" ry="11" fill="{c["fur2"]}"{st}/>')
    return '\n    '.join(o)
