import { ExternalLink } from 'lucide-react';
import { useSystemStatus } from '@/api/queries';
import { buttonClass } from '@/components/ui/Button';
import { Skeleton } from '@/components/ui/Feedback';
import { formatWhen } from '@/lib/format';
import { Panel } from './Sections';

const REPO = 'https://github.com/fs0ciety7000/gonema';

function uptime(s: number) {
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  return d ? `${d} d ${h} h` : h ? `${h} h ${m} min` : `${m} min`;
}

export function AboutSection() {
  const s = useSystemStatus();
  const d = s.data;
  return (
    <>
      <Panel id="about-version" title="This server">
        {!d ? (
          <Skeleton className="sk-wanted" />
        ) : (
          <dl className="kv">
            <dt>Version</dt>
            <dd className="t-mono-sm">{d.version}</dd>
            <dt>Running since</dt>
            <dd>
              {formatWhen(d.startedAt)} ({uptime(d.uptimeSeconds)})
            </dd>
            <dt>Database schema</dt>
            <dd className="t-mono-sm">{d.schemaVersion}</dd>
            <dt>Data directory</dt>
            <dd className="t-mono-sm">{d.dataDir}</dd>
            <dt>Built with</dt>
            <dd className="t-mono-sm">
              {d.goVersion} · {d.os}/{d.arch}
            </dd>
          </dl>
        )}
      </Panel>
      <Panel id="about-license" title="License and credits">
        <p className="explain">
          Gonema is free software under the GNU General Public License, version 3 (GPL-3.0): you may use, study, share
          and change it. The source code comes with every copy.
        </p>
        <div className="about-links">
          <a className={buttonClass('secondary', 'sm')} href={REPO} target="_blank" rel="noreferrer noopener">
            Source code
            <ExternalLink className="i" aria-hidden="true" />
          </a>
          <a className={buttonClass('ghost', 'sm')} href={`${REPO}/issues`} target="_blank" rel="noreferrer noopener">
            Report a problem
            <ExternalLink className="i" aria-hidden="true" />
          </a>
          <a
            className={buttonClass('ghost', 'sm')}
            href="https://www.gnu.org/licenses/gpl-3.0.html"
            target="_blank"
            rel="noreferrer noopener"
          >
            GPL-3.0 text
            <ExternalLink className="i" aria-hidden="true" />
          </a>
        </div>
        <ul className="about-notes">
          <li>
            Film data and images: The Movie Database (TMDB). This product uses the TMDB API but is not endorsed or
            certified by TMDB.
          </li>
          <li>Icons: Lucide (ISC license).</li>
          <li>Typefaces: Archivo, Geist and Geist Mono (SIL Open Font License).</li>
        </ul>
      </Panel>
    </>
  );
}

export default AboutSection;
