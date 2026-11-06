/* Sign-in state (GET /auth/status, public). Part of the first screen: the router guard and the
   sidebar read it. Everything else about accounts is in the lazy screens. */
import { type QueryClient, queryOptions } from '@tanstack/react-query';
import { api, unwrap } from './client';

export const authKey = ['auth', 'status'] as const;

export const authStatusQuery = queryOptions({
  queryKey: authKey,
  queryFn: ({ signal }) => unwrap(api.GET('/auth/status', { signal })),
  staleTime: Number.POSITIVE_INFINITY,
});

/** A return path inside the app ("/films?layout=table"), never another origin or the sign-in page. */
export function safeRedirect(raw: unknown): string | undefined {
  if (typeof raw !== 'string' || raw.length > 2000) return undefined;
  if (!raw.startsWith('/') || raw.startsWith('//') || raw.includes('\\')) return undefined;
  if (/^\/(login|setup)(?=$|[?#/])/.test(raw)) return undefined;
  return raw;
}

/** Ends this browser's session and forgets every cached answer (they belong to the account). */
export async function signOut(qc: QueryClient) {
  try {
    await api.POST('/auth/logout');
  } finally {
    qc.clear();
  }
}
