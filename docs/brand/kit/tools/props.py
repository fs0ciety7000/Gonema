from mascot import gopher
def tr(x, y, s=1, body=''):
    return f'<g transform="translate({x} {y}) scale({s})">\n    {body}\n  </g>'

def mascot_a(p):
    """Editorial ink line-art: the usher with a torch (empty states) + peeking from the velvet curtain (404)."""
    ink = '#1C1713'; acc = p['light']['accent']; accd = p['dark']['accent']
    c = dict(fur='#F3EDE3', fur2='#E4D8C8', cream='#FBF8F2', ink=ink, blush='#EBC3B5', white='#FFFFFF', line=ink, lw=4)
    st = f'stroke="{ink}" stroke-width="4" stroke-linejoin="round" stroke-linecap="round"'
    # pose 1: usher cap + torch with soft beam
    beam = f'<path d="M190 168 L330 110 Q350 180 330 250 Z" fill="{accd}" opacity="0.4"/>'
    torch = (f'<path d="M140 168 L182 160 L186 182 L144 190 Z" fill="#E4D8C8" {st}/>'
             f'<path d="M182 154 L192 152 L198 186 L188 188 Z" fill="{acc}" {st}/>')
    cap = (f'<path d="M92 50 Q120 30 150 46 L146 26 Q118 16 94 30 Z" fill="{acc}" {st}/>'
           f'<path d="M90 50 Q120 40 152 48" fill="none" {st}/>')
    paws = ''.join(f'<ellipse cx="{x}" cy="182" rx="14" ry="11" fill="#E4D8C8" {st}/>' for x in (132, 160))
    pose1 = beam + gopher(c, mood='happy', paws=False) + torch + paws + cap
    # pose 2: peeking from a curtain, worried
    g2 = gopher(c, mood='worried', paws=False)
    paw = f'<ellipse cx="64" cy="170" rx="13" ry="16" fill="#E4D8C8" {st}/>'
    curtain = (f'<path d="M-90 -10 H70 C60 60 76 120 62 180 C52 230 70 266 64 296 Q20 306 -10 296 Q-50 306 -90 296 Z" fill="{acc}" {st}/>'
               f'<path d="M-40 0 C-50 90 -30 190 -44 300 M10 0 C0 100 20 200 6 300" fill="none" stroke="{accd}" stroke-width="4" stroke-linecap="round" opacity="0.7"/>')
    pose2 = f'<g transform="translate(-20 0)">{g2}</g>' + curtain + paw
    return tr(20, 20, 1, pose1) + '\n  ' + tr(470, 20, 1, pose2)

def mascot_b(p):
    """Geometric flat: the clapper-loader (onboarding) + the searcher with a loupe (search/empty results)."""
    acc = p['dark']['accent']; acc2 = p['light']['accent']
    c = dict(fur='#B9A48E', fur2='#9C8670', cream='#EDE3D6', ink='#161412', blush='#D9A898', white='#FFFFFF')
    # slate
    slate = ('<g transform="translate(0 30)"><g transform="rotate(-6 120 200)">'
             '<rect x="62" y="168" width="116" height="78" rx="6" fill="#23201D"/>'
             '<rect x="74" y="190" width="92" height="4" rx="2" fill="#5C554D"/><rect x="74" y="206" width="60" height="4" rx="2" fill="#5C554D"/>'
             f'<rect x="74" y="222" width="76" height="4" rx="2" fill="{acc}"/>'
             '</g>'
             '<g transform="rotate(-22 62 162)">'
             '<rect x="62" y="146" width="116" height="20" rx="4" fill="#23201D"/>'
             f'<path d="M78 146 H96 L86 166 H68 Z M114 146 H132 L122 166 H104 Z M150 146 H168 L158 166 H140 Z" fill="{acc}"/>'
             '</g></g>')
    paws = ''.join(f'<ellipse cx="{x}" cy="236" rx="13" ry="11" fill="#9C8670"/>' for x in (66, 176))
    pose1 = gopher(c, paws=False) + slate + paws
    # loupe
    loupe = ('<g transform="translate(14 26)">' + f'<rect x="164" y="186" width="16" height="58" rx="8" fill="#23201D" transform="rotate(-40 172 215)"/>'
             f'<circle cx="150" cy="168" r="34" fill="{acc}" opacity="0.22"/>'
             f'<circle cx="150" cy="168" r="34" fill="none" stroke="#23201D" stroke-width="12"/>'
             f'<circle cx="138" cy="156" r="7" fill="#FFFFFF" opacity="0.7"/></g>')
    paws2 = '<ellipse cx="108" cy="206" rx="14" ry="11" fill="#9C8670"/><ellipse cx="198" cy="238" rx="13" ry="11" fill="#9C8670"/>'
    pose2 = gopher(c, paws=False) + loupe + paws2
    return tr(20, 20, 1, pose1) + '\n  ' + tr(400, 20, 1, pose2)

def mascot_c(p):
    """Plush luxe: popcorn-stuffed cheeks + striped bucket (welcome) and a nap with a ticket stub (idle queue)."""
    acc = p['dark']['accent']; accl = p['light']['accent']
    c = dict(fur='#D19A63', fur2='#B07A45', cream='#F7E4CB', ink='#24160F', blush='#F2A0B5', white='#FFFDF8')
    corn = ''.join(f'<circle cx="{x}" cy="{y}" r="{r}" fill="#FFF6E2" stroke="#E8C98F" stroke-width="2"/>'
                   for x, y, r in [(88,168,13),(106,160,14),(124,158,13),(142,162,14),(158,170,12),(98,152,10),(134,148,11),(116,146,10),(150,152,9)])
    bucket = ('<path d="M76 176 H170 L158 262 H88 Z" fill="#FFF6E2"/>'
              f'<path d="M86 176 H104 L106 262 H95 Z M122 176 H140 L136 262 H124 Z M156 176 H170 L158 262 H152 Z" fill="{acc}"/>'
              f'<path d="M76 176 H170" stroke="{accl}" stroke-width="6" stroke-linecap="round"/>')
    paws = '<ellipse cx="78" cy="206" rx="14" ry="12" fill="#B07A45"/><ellipse cx="166" cy="206" rx="14" ry="12" fill="#B07A45"/>'
    stuffed = ''.join(f'<ellipse cx="{x}" cy="132" rx="26" ry="21" fill="#B07A45"/><circle cx="{x+(6 if x>120 else -6)}" cy="124" r="5" fill="#FFF6E2" stroke="#E8C98F" stroke-width="1.5"/>' for x in (72, 168))
    g1 = gopher(c, paws=False)
    # draw the stuffed cheeks over the regular pouches, then the muzzle again on top
    g1 = g1.replace('<ellipse cx="120" cy="130" rx="32"', stuffed + '<ellipse cx="120" cy="130" rx="32"', 1)
    pose1 = g1 + corn + bucket + paws
    ticket = ('<g transform="rotate(-12 120 200)">'
              f'<path d="M70 176 H170 V194 A10 10 0 0 0 170 214 V232 H70 V214 A10 10 0 0 0 70 194 Z" fill="{acc}"/>'
              f'<path d="M140 180 V228" stroke="#FFFDF8" stroke-width="3" stroke-dasharray="5 5"/>'
              f'<circle cx="104" cy="204" r="12" fill="none" stroke="#FFFDF8" stroke-width="4"/>'
              '</g>')
    paws2 = '<ellipse cx="80" cy="214" rx="14" ry="12" fill="#B07A45"/><ellipse cx="160" cy="196" rx="14" ry="12" fill="#B07A45"/>'
    zz = (f'<path d="M196 40 H216 L196 62 H216 M226 14 H240 L226 30 H240" fill="none" stroke="{acc}" stroke-width="5" stroke-linecap="round" stroke-linejoin="round"/>')
    pose2 = gopher(c, mood='sleepy', paws=False) + ticket + paws2 + zz
    return tr(20, 20, 1, pose1) + '\n  ' + tr(400, 20, 1, pose2)
