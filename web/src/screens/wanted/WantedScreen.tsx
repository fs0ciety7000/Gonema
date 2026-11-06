import '@/styles/lot2.css';
import '@/styles/lot3.css';
import { useInfiniteQuery } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { Calendar, CircleCheck, Compass, Search } from 'lucide-react';
import { useEffect, useMemo } from 'react';
import { fetchWanted, type WantedKind } from '@/api/lot3';
import { toLibraryItem } from '@/api/model';
import { useProfiles } from '@/api/queries';
import { useMovieActions } from '@/components/movie/useMovieActions';
import { SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { QualityBadge, StatusText } from '@/components/ui/Badges';
import { Button, buttonClass } from '@/components/ui/Button';
import { EmptyState, Notice, Skeleton } from '@/components/ui/Feedback';
import { PosterArt } from '@/components/ui/PosterArt';
import { Tabs } from '@/components/ui/Tabs';
import { formatDate, formatNumber } from '@/lib/format';
import { missingNote } from '@/lib/status';
import mascotNeutral from '../../../../docs/brand/kit/mascot/kernel-neutral.svg?url';

const route = getRouteApi('/wanted');
type Tab = 'missing' | 'cutoff';
const KIND: Record<Tab, WantedKind> = { missing: 'missing', cutoff: 'cutoff_unmet' };

export function WantedScreen() {
  const navigate = useNavigate();
  const tab: Tab = route.useSearch().kind === 'cutoff' ? 'cutoff' : 'missing';
  const actions = useMovieActions();
  const profiles = useProfiles();
  const q = useInfiniteQuery({
    queryKey: ['wanted', KIND[tab]],
    queryFn: ({ pageParam, signal }) => fetchWanted(KIND[tab], pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (p) => p.nextCursor || undefined,
    staleTime: 30_000,
  });
  useEffect(() => {
    document.title = `${tab === 'cutoff' ? 'Cutoff unmet' : 'Wanted'} · Gonema`;
  }, [tab]);
  const rows = useMemo(
    () => (q.data?.pages ?? []).flatMap((p) => p.movies ?? []).map((m) => toLibraryItem(m as never)),
    [q.data],
  );
  const total = q.data?.pages[0]?.total;
  const profileName = (id: number) => profiles.data?.find((p) => p.id === id)?.name ?? '';
  const sub =
    total == null
      ? ' '
      : tab === 'missing'
        ? total
          ? `${formatNumber(total)} monitored ${total === 1 ? 'film has' : 'films have'} no file yet. Gonema searches for them on its own.`
          : 'Every monitored film is on disk.'
        : total
          ? `${formatNumber(total)} ${total === 1 ? 'film is' : 'films are'} below the cutoff of ${total === 1 ? 'its' : 'their'} profile. Gonema keeps looking for upgrades.`
          : 'Every file meets its cutoff.';
  return (
    <>
      <Topbar>
        <SearchField />
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page activity-page" tabIndex={-1}>
        <div className="act-head">
          <div>
            <h1>Wanted</h1>
            <p className="sub" aria-live="polite">
              {sub}
            </p>
          </div>
        </div>
        <div className="act-bar">
          <Tabs<Tab>
            label="Wanted"
            panelId="wanted-panel"
            className="tabs-plain"
            value={tab}
            onChange={(t) => void navigate({ to: '/wanted', search: t === 'cutoff' ? { kind: 'cutoff' } : {} })}
            items={[
              { value: 'missing', label: 'Missing', count: tab === 'missing' && total ? formatNumber(total) : undefined },
              {
                value: 'cutoff',
                label: 'Cutoff unmet',
                count: tab === 'cutoff' && total ? formatNumber(total) : undefined,
              },
            ]}
          />
        </div>
        <div id="wanted-panel" role="tabpanel" aria-labelledby={`tab-wanted-panel-${tab}`}>
          {q.isError ? (
            <Notice tone="failed" role="alert" title="The list could not be loaded." action={
              <Button size="sm" onClick={() => void q.refetch()}>
                Try again
              </Button>
            }>
              {(q.error as Error).message}
            </Notice>
          ) : q.isPending ? (
            <div className="hist" aria-hidden="true">
              {[0, 1, 2, 3].map((i) => (
                <Skeleton key={i} className="sk-wanted" delay={i * 24} />
              ))}
            </div>
          ) : !rows.length ? (
            tab === 'missing' ? (
              <EmptyState
                title="All clear in the booth."
                mascot={mascotNeutral}
                action={
                  <>
                    <Link className={buttonClass('secondary', 'sm')} to="/calendar">
                      <Calendar className="i" aria-hidden="true" />
                      See what’s coming
                    </Link>
                    <Link className={buttonClass('ghost', 'sm')} to="/discover" search={{ tab: 'discover' }}>
                      <Compass className="i" aria-hidden="true" />
                      Discover
                    </Link>
                  </>
                }
              >
                Every monitored film is on disk and at its cutoff. Gonema will tell you when something new comes out.
              </EmptyState>
            ) : (
              <EmptyState title="Every file meets its cutoff.">
                No upgrade to chase. When a profile’s cutoff goes up, the films below it show here.
              </EmptyState>
            )
          ) : (
            <div className="hist wanted">
              <table className="tbl">
                <caption className="sr-only">{tab === 'missing' ? 'Missing films' : 'Films below their cutoff'}</caption>
                <thead>
                  <tr>
                    <th scope="col" style={{ width: '38%' }}>
                      Film
                    </th>
                    <th scope="col">{tab === 'missing' ? 'Status' : 'On disk'}</th>
                    <th scope="col">Profile</th>
                    <th scope="col">Added</th>
                    <th scope="col" style={{ width: 150 }}>
                      <span className="sr-only">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((m) => (
                    <tr key={m.id}>
                      <td>
                        <Link className="film" to="/films/$movieId" params={{ movieId: m.id }}>
                          <PosterArt className="thumb" src={m.posterUrl} title={m.title} color={m.dominantColor} />
                          <span>
                            {m.title}
                            {m.year ? <span className="t-muted"> ({m.year})</span> : null}
                          </span>
                        </Link>
                      </td>
                      <td>
                        {tab === 'missing' ? (
                          <StatusText status="missing">{missingNote(m)}</StatusText>
                        ) : m.quality ? (
                          <QualityBadge quality={m.quality} />
                        ) : (
                          '—'
                        )}
                      </td>
                      <td className="t-secondary">{profileName(m.qualityProfileId)}</td>
                      <td className="t-secondary tnum">{formatDate(m.addedAt)}</td>
                      <td className="ta-r">
                        <Button
                          size="sm"
                          variant="ghost"
                          icon={<Search className="i" aria-hidden="true" />}
                          onClick={() => void actions.search([{ id: m.id, title: m.title }])}
                          aria-label={`Search for ${m.title} now`}
                        >
                          Search now
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {q.hasNextPage && (
                <div className="more-row">
                  <Button size="sm" loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
                    Show {Math.min(100, (total ?? 0) - rows.length) || 100} more
                  </Button>
                </div>
              )}
              {!q.hasNextPage && rows.length > 20 && (
                <p className="more-row t-caption t-muted">
                  <CircleCheck className="i i-xs" aria-hidden="true" /> That’s all {formatNumber(rows.length)}.
                </p>
              )}
            </div>
          )}
        </div>
      </main>
    </>
  );
}
