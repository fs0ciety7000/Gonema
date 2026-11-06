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
import { NotFound, ScreenError } from '@/components/shell/Status';
import { ALL_SECTIONS } from '@/lib/nav';
import { LibraryScreen } from '@/screens/library/LibraryScreen';
import { validateLibrarySearch } from '@/screens/library/model';
import { Placeholder } from '@/screens/misc/Placeholder';

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

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/films', replace: true });
  },
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

export const movieRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/films/$movieId',
  params: {
    parse: (p: { movieId: string }) => {
      const id = Number(p.movieId);
      if (!Number.isInteger(id) || id < 1) throw new Error('Not a film id');
      return { movieId: id };
    },
    stringify: (p: { movieId: number }) => ({ movieId: String(p.movieId) }),
  },
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(movieQuery(params.movieId));
  },
  component: lazyRouteComponent(() => import('@/screens/movie/MovieScreen'), 'MovieScreen'),
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
  indexRoute,
  libraryRoute,
  movieRoute,
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
