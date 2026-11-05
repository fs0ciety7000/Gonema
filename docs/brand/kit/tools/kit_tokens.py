import json
from palettes import P
from contrast import hx, ratio, comp, tohex, measure
K = '/home/user/Gonema/docs/brand/kit'
A = P['a']
OFL = 'SIL Open Font License 1.1'
DESC = {
 'bg-base': 'App background (warm black / bone paper).', 'bg-raised': 'Cards, sidebars, menus.',
 'surface-glass': 'Translucent layer over posters/backdrops (+ backdrop-filter blur 16-24px). Add a scrim over bright images.',
 'border': 'Hairline borders and dividers. Decorative only, never the sole indicator of state or focus.',
 'text-primary': 'Titles and body text.', 'text-secondary': 'Supporting text, labels.', 'text-muted': 'Metadata, timestamps, placeholders (min 12px, weight >= 400).',
 'accent': 'Primary action, focus ring, selected state, links. Sparingly: max one accent-filled element per view.',
 'accent-contrast': 'Text/icons on an accent fill.',
 'status-downloading': 'In progress (queue, grabbing, importing).', 'status-missing': 'Monitored but not on disk.',
 'status-upgraded': 'Replaced by a better release.', 'status-failed': 'Failed download/import, errors.', 'status-available': 'On disk, cutoff met.'}
def val(v):
    if isinstance(v, tuple):
        h = v[0].lstrip('#'); r, g, b = (int(h[i:i+2], 16) for i in (0, 2, 4)); return f'rgba({r}, {g}, {b}, {v[1]})'
    return v
def badge_checks(T):
    """status text on its own 14% tint over bg-raised (badge pattern)"""
    out = {}
    raised = hx(T['bg-raised'])
    for s in ('downloading', 'missing', 'upgraded', 'failed', 'available'):
        c = hx(T[f'status-{s}']); tint = comp(c, 0.14, raised)
        out[f'status-{s} text on its 14% tint (badge)'] = round(ratio(c, tint), 2)
    return out
def focus_checks(T):
    a = hx(T['accent'])
    return {'focus ring (accent) vs bg-base': round(ratio(a, hx(T['bg-base'])), 2),
            'focus ring (accent) vs bg-raised': round(ratio(a, hx(T['bg-raised'])), 2)}

TYPE = {
 'families': {
  'display': {'family': 'Archivo', 'axes': {'wdth': 125, 'wght': '500-700'}, 'css': "'Archivo', 'Archivo Expanded', system-ui, sans-serif", 'licence': OFL, 'source': 'https://fonts.google.com/specimen/Archivo'},
  'ui': {'family': 'Geist', 'css': "'Geist', system-ui, -apple-system, 'Segoe UI', sans-serif", 'licence': OFL, 'source': 'https://fonts.google.com/specimen/Geist'},
  'mono': {'family': 'Geist Mono', 'css': "'Geist Mono', ui-monospace, 'SFMono-Regular', Menlo, monospace", 'licence': OFL, 'source': 'https://fonts.google.com/specimen/Geist+Mono'}},
 'scale': {
  'display-2xl': {'family': 'display', 'size': 64, 'lineHeight': 68, 'weight': 600, 'tracking': '-0.02em', 'stretch': '125%', 'use': 'Hero film title (detail page)'},
  'display-xl': {'family': 'display', 'size': 48, 'lineHeight': 52, 'weight': 600, 'tracking': '-0.015em', 'stretch': '125%', 'use': 'Page titles'},
  'display-lg': {'family': 'display', 'size': 36, 'lineHeight': 40, 'weight': 600, 'tracking': '-0.01em', 'stretch': '125%', 'use': 'Section titles, empty-state titles'},
  'display-md': {'family': 'display', 'size': 28, 'lineHeight': 34, 'weight': 600, 'tracking': '-0.005em', 'stretch': '125%', 'use': 'Card titles in hero rows, dialogs'},
  'overline': {'family': 'display', 'size': 11, 'lineHeight': 16, 'weight': 600, 'tracking': '0.12em', 'stretch': '125%', 'transform': 'uppercase', 'use': 'Kickers, section labels'},
  'title-lg': {'family': 'ui', 'size': 20, 'lineHeight': 28, 'weight': 600, 'tracking': '-0.01em', 'use': 'Panel headings'},
  'title-md': {'family': 'ui', 'size': 16, 'lineHeight': 24, 'weight': 600, 'tracking': '-0.005em', 'use': 'List headings, poster titles'},
  'body-lg': {'family': 'ui', 'size': 16, 'lineHeight': 24, 'weight': 400, 'tracking': '0', 'use': 'Overviews, onboarding'},
  'body': {'family': 'ui', 'size': 14, 'lineHeight': 20, 'weight': 400, 'tracking': '0', 'use': 'Default UI text'},
  'body-sm': {'family': 'ui', 'size': 13, 'lineHeight': 18, 'weight': 400, 'tracking': '0.003em', 'use': 'Dense tables, secondary info'},
  'caption': {'family': 'ui', 'size': 12, 'lineHeight': 16, 'weight': 500, 'tracking': '0.01em', 'use': 'Badges, metadata (minimum size for text-muted)'},
  'mono': {'family': 'mono', 'size': 13, 'lineHeight': 20, 'weight': 400, 'tracking': '0', 'use': 'Release names, paths, scores, logs'},
  'mono-sm': {'family': 'mono', 'size': 12, 'lineHeight': 16, 'weight': 400, 'tracking': '0', 'use': 'Table cells, sizes, timestamps'}},
 'numerals': 'All figures in UI text use font-variant-numeric: tabular-nums (Geist supports tnum); Geist Mono is tabular by construction. Use slashed-zero (zero) in mono for hashes and release names.',
 'rules': ['Archivo is always set at wdth 125 (expanded); never below 500 weight.', 'No weight below 400 under 16px (Geist).', 'Film titles in display styles use title case, overline uses caps.']}

def main():
    tok = {'$schema-note': 'Single source of truth for Gonema brand tokens (direction A Salle, typography from B). Ratios: WCAG 2.1 relative luminance, measured by script.',
           'version': '1.0.0', 'color': {}, 'focus': {}, 'typography': TYPE, 'contrast_wcag21': {}}
    for th in ('dark', 'light'):
        T = A[th]
        tok['color'][th] = {r: {'value': val(v), 'usage': DESC[r]} for r, v in T.items()}
        m = measure(T)
        tok['contrast_wcag21'][th] = {'per_role_vs_bg-base/bg-raised/surface-glass': {k: v for k, v in m.items() if isinstance(v, dict)},
                                      'surface-glass composited on bg-base': m['_glass_composited_on_base'],
                                      'accent-contrast on accent': m['accent-contrast-on-accent'],
                                      'border on bg-raised (decorative, no requirement)': m['border-on-bg-raised (decorative)'],
                                      **focus_checks(T), **badge_checks(T)}
        tok['focus'][th] = {'ring': f"0 0 0 2px {T['bg-base']}, 0 0 0 4px {T['accent']}", 'color': T['accent'], 'width': '2px', 'offset': '2px'}
    tok['contrast_wcag21']['thresholds'] = {'body text': 4.5, 'large text (>=24px or >=18.66px bold) and UI components / focus': 3.0}
    tok['contrast_wcag21']['font_note'] = ('Ratios do not depend on the typeface, but Geist at 12-13px is light-stroked: keep text-muted at >= 12px and weight >= 400, '
                                           'Geist Mono release names in text-secondary or brighter. Accent passes 4.5:1 for text in both themes, but use it for links/actions only.')
    tok['radius'] = {'sm': '6px', 'md': '10px', 'lg': '16px', 'xl': '24px', 'pill': '999px'}
    tok['shadow'] = {'dark': {'raised': '0 1px 0 rgba(242,233,221,0.04) inset, 0 12px 32px -12px rgba(0,0,0,0.6)'},
                     'light': {'raised': '0 1px 2px rgba(42,32,24,0.06), 0 12px 32px -16px rgba(42,32,24,0.18)'}}
    tok['glass'] = {'blur': '20px', 'saturate': '1.2'}
    json.dump(tok, open(f'{K}/tokens.json', 'w'), indent=2, ensure_ascii=False)

    def block(T):
        lines = [f'  --{r}: {val(v)};' for r, v in T.items()]
        lines.append(f"  --focus-ring: 0 0 0 2px {T['bg-base']}, 0 0 0 4px {T['accent']};")
        return '\n'.join(lines)
    fam = TYPE['families']
    tl = []
    for n, s in TYPE['scale'].items():
        tl.append(f"  --type-{n}: {s['weight']} {s['size']}px/{s['lineHeight']}px var(--font-{s['family']});")
        tl.append(f"  --type-{n}-tracking: {s['tracking']};")
    css = f'''/* Gonema brand tokens, generated from tokens.json (direction A "Salle" + B typography).
   Dark is the default; [data-theme="light"] or the OS preference switches to light.
   Fonts (SIL OFL 1.1, Google Fonts): Archivo (wdth 125), Geist, Geist Mono. */

:root,
[data-theme="dark"] {{
  color-scheme: dark;
{block(A['dark'])}
  --shadow-raised: {tok['shadow']['dark']['raised']};
}}

[data-theme="light"] {{
  color-scheme: light;
{block(A['light'])}
  --shadow-raised: {tok['shadow']['light']['raised']};
}}

@media (prefers-color-scheme: light) {{
  :root:not([data-theme]) {{
    color-scheme: light;
{block(A['light']).replace(chr(10)+'  ', chr(10)+'    ').replace('  --', '    --', 1)}
    --shadow-raised: {tok['shadow']['light']['raised']};
  }}
}}

:root {{
  --font-display: {fam['display']['css']};
  --font-ui: {fam['ui']['css']};
  --font-mono: {fam['mono']['css']};
  --display-stretch: 125%;
{chr(10).join(tl)}
  --radius-sm: 6px; --radius-md: 10px; --radius-lg: 16px; --radius-xl: 24px; --radius-pill: 999px;
  --glass-blur: 20px;
}}

/* Usage helpers */
.tnum {{ font-variant-numeric: tabular-nums; }}
.display {{ font-family: var(--font-display); font-stretch: var(--display-stretch); font-variation-settings: "wdth" 125; }}
'''
    open(f'{K}/tokens.css', 'w').write(css)
    # summary
    for th in ('dark', 'light'):
        c = tok['contrast_wcag21'][th]
        print(th, {k: min(v.values()) for k, v in c['per_role_vs_bg-base/bg-raised/surface-glass'].items()})
        print('  ', {k: v for k, v in c.items() if 'badge' in k or 'focus' in k or 'accent-contrast' in k})
main()
