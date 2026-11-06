import { Construction } from 'lucide-react';
import { SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { ALL_SECTIONS, type Section } from '@/lib/nav';

/** Dev builds only: an honest stand-in for a section built in a later lot. Never shipped. */
export function Placeholder({ section }: { section: Section }) {
  const s = ALL_SECTIONS.find((x) => x.id === section);
  return (
    <>
      <Topbar>
        <SearchField />
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page" tabIndex={-1}>
        <div className="page-head">
          <div>
            <h1>{s?.label}</h1>
            <p className="sub">Development build only</p>
          </div>
        </div>
        <div className="empty">
          <span className="glyph" aria-hidden="true">
            <Construction className="i" />
          </span>
          <h2>Coming in this build.</h2>
          <p>
            {s?.label} is planned for lot {s?.lot}. This placeholder exists only in development builds; production hides
            the entry.
          </p>
        </div>
      </main>
    </>
  );
}
