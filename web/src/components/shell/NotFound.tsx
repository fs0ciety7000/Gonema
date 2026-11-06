import { Link } from '@tanstack/react-router';
import { Film, Search } from 'lucide-react';
import { Button, buttonClass } from '@/components/ui/Button';
import { palette } from '@/lib/palette';
import mascot404 from '../../../../docs/brand/kit/mascot/kernel-404.svg?url';

const isApple = typeof navigator !== 'undefined' && /Mac|iP(hone|ad)/.test(navigator.platform);

/** 404 (mockups/404.html). A lazy chunk: the first load of a real screen does not carry it. */
export function NotFound({ path }: { path?: string }) {
  const p = path ?? (typeof location !== 'undefined' ? location.pathname : '');
  return (
    <main id="main" className="stage-404" tabIndex={-1}>
      <div className="curtain" aria-hidden="true" />
      <div className="card-404">
        <img src={mascot404} alt="Kernel peeking out from behind the curtain" />
        <span className="code">ERROR 404</span>
        <h1>This reel isn’t in the booth.</h1>
        <p>
          Nothing lives at <span className="path">{p}</span>. It may have been renamed, or removed from the library.
        </p>
        <div className="actions">
          <Link className={buttonClass('primary')} to="/films">
            <Film className="i" aria-hidden="true" />
            Back to the programme
          </Link>
          <Button icon={<Search className="i" aria-hidden="true" />} onClick={() => palette.open()}>
            Search{' '}
            <span className="kbd" aria-hidden="true">
              {isApple ? '⌘K' : 'Ctrl K'}
            </span>
          </Button>
        </div>
      </div>
    </main>
  );
}
