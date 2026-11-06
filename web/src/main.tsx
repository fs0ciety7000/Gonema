import './styles/app.css';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { ApiError } from '@/api/client';
import { reviewFlag } from '@/lib/flags';
import { initTheme } from '@/lib/theme';
import { makeRouter } from './router';

initTheme();
const root = document.documentElement;
if (reviewFlag('motion') === 'reduce') root.dataset.motion = 'reduce';
const density = reviewFlag('density');
if (density === 'compact' || density === 'airy') root.dataset.density = density;
if (reviewFlag('freeze')) root.dataset.freeze = '';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Network errors are retried by the reconnecting event stream, which refetches on return.
      retry: (n, e) => !(e instanceof ApiError && e.status >= 400 && e.status < 500) && n < 2,
      refetchOnWindowFocus: false,
    },
  },
});

const router = makeRouter(queryClient);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
