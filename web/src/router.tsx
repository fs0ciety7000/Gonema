import type { QueryClient } from '@tanstack/react-query';
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  Outlet,
  redirect,
} from '@tanstack/react-router';
import { lazy, Suspense } from 'react';
import { libraryQuery, movieQuery, statusQuery } from '@/api/queries';
import { AppShell } from '@/components/shell/AppShell';
import { ScreenError } from '@/components/shell/Status';
import { ALL_SECTIONS } from '@/lib/nav';
import { LibraryScreen } from '@/screens/library/LibraryScreen';
import { validateLibrarySearch } from '@/screens/library/model';
import { Placeholder } from '@/screens/misc/Placeholder';

const LazyNotFound = lazy(() => import('@/components/shell/NotFound').then((m) => ({ default: m.NotFound })));
function NotFound() {
  return (
    <Suspense fallback={null}>
      <LazyNotFound />
    </Suspense>
  );
}

// Dev-only system sheet: its import is dead code in production builds.
const SystemSheet = __GONEMA_DEV__ ? lazy(() => import('@/screens/system/SystemSheet')) : () => null;

const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
  notFoundComponent: () => <NotFound />,
  errorComponent: ({ error, reset }) => <ScreenError error={error} reset={reset} />,
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(statusQuery);
  },
});

export const homeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: async ({ context }) => {
    // First run: the guided setup until it is complete or skipped (resumable: its state is on the server).
    const { needsOnboarding } = await import('@/api/onboarding');
    if (await needsOnboarding(context.queryClient)) throw redirect({ to: '/setup', replace: true });
  },
  component: lazyRouteComponent(() => import('@/screens/home/HomeScreen'), 'HomeScreen'),
});

export const libraryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/films',
  validateSearch: (raw: Record<string, unknown>) => validateLibrarySearch(raw),
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(libraryQuery);
  },
  component: LibraryScreen,
  head: () => ({ meta: [{ title: 'Films · Gonema' }] }),
});

/** Positive integer id in the path (films, downloads). */
const positiveId = (v: string) => {
  const id = Number(v);
  if (!Number.isInteger(id) || id < 1) throw new Error('Not an id');
  return id;
};
const parseMovieId = {
  parse: (p: { movieId: string }) => ({ movieId: positiveId(p.movieId) }),
  stringify: (p: { movieId: number }) => ({ movieId: String(p.movieId) }),
};

export const movieRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/films/$movieId',
  params: parseMovieId,
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(movieQuery(params.movieId));
  },
  component: lazyRouteComponent(() => import('@/screens/movie/MovieScreen'), 'MovieScreen'),
});

export const searchRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/films/$movieId/search',
  params: parseMovieId,
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(movieQuery(params.movieId));
  },
  component: lazyRouteComponent(() => import('@/screens/search/InteractiveSearchScreen'), 'InteractiveSearchScreen'),
});

export type DiscoverSearch = { tab?: 'discover'; q?: string; list?: string; person?: number };

export const discoverRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/discover',
  validateSearch: (raw: Record<string, unknown>): DiscoverSearch => ({
    tab: raw.tab === 'discover' ? 'discover' : undefined,
    q: typeof raw.q === 'string' && raw.q.length <= 200 ? raw.q : undefined,
    list: typeof raw.list === 'string' && /^[a-z_-]{1,32}$/.test(raw.list) ? raw.list : undefined,
    person: Number.isInteger(Number(raw.person)) && Number(raw.person) > 0 ? Number(raw.person) : undefined,
  }),
  component: lazyRouteComponent(() => import('@/screens/discover/AddFilmScreen'), 'AddFilmScreen'),
  head: () => ({ meta: [{ title: 'Add & discover · Gonema' }] }),
});

export type HistorySearch = { event?: string; movie?: number; since?: string };

const queueRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/queue',
  component: lazyRouteComponent(() => import('@/screens/activity/ActivityScreen'), 'QueueScreen'),
  head: () => ({ meta: [{ title: 'Queue · Gonema' }] }),
});

export const historyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/history',
  validateSearch: (raw: Record<string, unknown>): HistorySearch => {
    const movie = Number(raw.movie);
    return {
      event: typeof raw.event === 'string' && /^[a-z_]{1,40}$/.test(raw.event) ? raw.event : undefined,
      movie: Number.isInteger(movie) && movie > 0 ? movie : undefined,
      since: raw.since === '1d' || raw.since === '7d' || raw.since === '30d' ? raw.since : undefined,
    };
  },
  component: lazyRouteComponent(() => import('@/screens/activity/ActivityScreen'), 'HistoryScreen'),
  head: () => ({ meta: [{ title: 'History · Gonema' }] }),
});

// Lot 3 screens set their own document title (they know the section, month or film).
const settingsScreen = lazyRouteComponent(() => import('@/screens/settings/SettingsScreen'), 'SettingsScreen');

const blocklistRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/blocklist',
  component: lazyRouteComponent(() => import('@/screens/activity/ActivityScreen'), 'BlocklistScreen'),
});

export type CalendarSearch = { view?: 'agenda'; month?: string; all?: true };

export const calendarRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/calendar',
  validateSearch: (raw: Record<string, unknown>): CalendarSearch => ({
    view: raw.view === 'agenda' ? 'agenda' : undefined,
    month: typeof raw.month === 'string' && /^\d{4}-(0[1-9]|1[0-2])$/.test(raw.month) ? raw.month : undefined,
    all: raw.all === true || raw.all === '1' || raw.all === 1 ? true : undefined,
  }),
  component: lazyRouteComponent(() => import('@/screens/calendar/CalendarScreen'), 'CalendarScreen'),
});

export type WantedSearch = { kind?: 'cutoff' };

export const wantedRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/wanted',
  validateSearch: (raw: Record<string, unknown>): WantedSearch => ({
    kind: raw.kind === 'cutoff' ? 'cutoff' : undefined,
  }),
  component: lazyRouteComponent(() => import('@/screens/wanted/WantedScreen'), 'WantedScreen'),
});

const healthRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/system/health',
  component: lazyRouteComponent(() => import('@/screens/health/HealthScreen'), 'HealthScreen'),
});

const settingsIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: settingsScreen,
});

export const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings/$section',
  component: settingsScreen,
});

const settingsAnchorRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings/$section/$anchor',
  component: settingsScreen,
});

export type SetupSearch = { step?: string };

export const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  validateSearch: (raw: Record<string, unknown>): SetupSearch => ({
    step: typeof raw.step === 'string' && /^[a-z]{2,16}$/.test(raw.step) ? raw.step : undefined,
  }),
  component: lazyRouteComponent(() => import('@/screens/onboarding/OnboardingScreen'), 'OnboardingScreen'),
});

const failedDownloadRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/queue/$downloadId',
  params: {
    parse: (p: { downloadId: string }) => ({ downloadId: positiveId(p.downloadId) }),
    stringify: (p: { downloadId: number }) => ({ downloadId: String(p.downloadId) }),
  },
  component: lazyRouteComponent(() => import('@/screens/activity/FailedDownloadScreen'), 'FailedDownloadScreen'),
});

const placeholderRoutes = __GONEMA_DEV__
  ? ALL_SECTIONS.filter((s) => !s.built).map((s) =>
      createRoute({
        getParentRoute: () => rootRoute,
        path: s.path,
        component: () => <Placeholder section={s.id} />,
      }),
    )
  : [];

const systemRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/__system',
  component: () =>
    __GONEMA_DEV__ ? (
      <Suspense fallback={null}>
        <SystemSheet />
      </Suspense>
    ) : (
      <NotFound />
    ),
});

const routeTree = rootRoute.addChildren([
  homeRoute,
  libraryRoute,
  movieRoute,
  searchRoute,
  discoverRoute,
  queueRoute,
  historyRoute,
  blocklistRoute,
  failedDownloadRoute,
  calendarRoute,
  wantedRoute,
  healthRoute,
  settingsIndexRoute,
  settingsRoute,
  settingsAnchorRoute,
  setupRoute,
  ...(__GONEMA_DEV__ ? [systemRoute] : []),
  ...placeholderRoutes,
]);

/** Base path from <base href> (the Go server rewrites it to GONEMA_BASE_URL). */
export const basepath = new URL(document.baseURI).pathname.replace(/\/$/, '') || '/';

export function makeRouter(queryClient: QueryClient) {
  return createRouter({
    routeTree,
    context: { queryClient },
    basepath,
    defaultPreload: 'intent',
    defaultPreloadStaleTime: 0,
    scrollRestoration: true,
    defaultNotFoundComponent: () => <NotFound />,
  });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof makeRouter>;
  }
}
