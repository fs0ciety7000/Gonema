import markSvg from '../../../../docs/brand/kit/logo/mark-currentcolor.svg?raw';
import wordmarkSvg from '../../../../docs/brand/kit/logo/wordmark-currentcolor.svg?raw';

/* The kit's currentColor mark and wordmark, used as is (never redrawn): their
   <path> elements are read from the kit files at build time and rendered as JSX. */

type Shape = { d: string; fillRule?: 'evenodd' | 'nonzero' };
type Parsed = { viewBox: string; transform?: string; paths: Shape[] };

function parse(svg: string): Parsed {
  const viewBox = /viewBox="([^"]+)"/.exec(svg)?.[1] ?? '0 0 256 256';
  const transform = /<g[^>]*transform="([^"]+)"/.exec(svg)?.[1];
  const paths = [...svg.matchAll(/<path([^>]*)\/>/g)].map((m) => {
    const attrs = m[1] ?? '';
    const d = /\sd="([^"]+)"/.exec(attrs)?.[1] ?? '';
    const rule = /fill-rule="(evenodd|nonzero)"/.exec(attrs)?.[1] as Shape['fillRule'];
    return rule ? { d, fillRule: rule } : { d };
  });
  return transform ? { viewBox, transform, paths } : { viewBox, paths };
}

const MARK = parse(markSvg);
const WORDMARK = parse(wordmarkSvg);

function Svg({ p, className }: { p: Parsed; className?: string | undefined }) {
  return (
    <svg viewBox={p.viewBox} className={className} aria-hidden="true">
      <g transform={p.transform}>
        {p.paths.map((s) => (
          <path key={s.d.slice(0, 24)} fill="currentColor" fillRule={s.fillRule} d={s.d} />
        ))}
      </g>
    </svg>
  );
}

export const Mark = ({ className }: { className?: string }) => <Svg p={MARK} className={className} />;
export const Wordmark = ({ className = 'wm' }: { className?: string }) => <Svg p={WORDMARK} className={className} />;
