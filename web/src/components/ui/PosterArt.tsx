import { memo, useState } from 'react';
import { cx } from './cx';

type PosterArtProps = {
  /** Image URL from the server (the local image cache), or null. */
  src: string | null;
  title: string;
  year?: number;
  /** movies.dominant_color: tints the placeholder while there is no image. */
  color?: string | null;
  /** Rendered width, for srcset selection (w342 / w780). */
  sizes?: string;
  eager?: boolean;
  className?: string;
  kind?: 'poster' | 'backdrop';
};

function hueOf(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

/** Poster image from the local cache; when there is none (no TMDB key, demo data) a typographic
 *  placeholder built from the film's own title and colour, never invented artwork. */
export const PosterArt = memo(function PosterArt({
  src: url,
  title,
  year,
  color,
  sizes,
  eager,
  className,
  kind = 'poster',
}: PosterArtProps) {
  const [failed, setFailed] = useState(false);
  const src = failed ? null : url;
  if (src) {
    const large = src.includes('/w342/') ? src.replace('/w342/', '/w780/') : null;
    return (
      <div className={cx('poster-art', className)}>
        {/* biome-ignore lint/a11y/noNoninteractiveElementInteractions: onError swaps to the placeholder, not an interaction */}
        <img
          src={src}
          srcSet={large && sizes ? `${src} 342w, ${large} 780w` : undefined}
          sizes={large && sizes ? sizes : undefined}
          alt=""
          loading={eager ? 'eager' : 'lazy'}
          decoding="async"
          draggable={false}
          onError={() => setFailed(true)}
        />
      </div>
    );
  }
  const base = color ?? `hsl(${hueOf(title)} 32% 34%)`;
  return (
    <div
      className={cx('poster-art poster-fallback', className)}
      style={{ ['--pf' as string]: base }}
      aria-hidden="true"
    >
      {kind === 'poster' && (
        <>
          <span className="pf-title">{title}</span>
          {year ? <span className="pf-year">{year}</span> : null}
        </>
      )}
    </div>
  );
});
