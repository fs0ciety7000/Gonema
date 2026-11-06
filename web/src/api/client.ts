import createClient from 'openapi-fetch';
import type { paths } from './schema';

/** API root, resolved against <base href> so the SPA works under GONEMA_BASE_URL. */
export const API_ROOT = new URL('api/v1', document.baseURI).href;

let unauthorized: (() => void) | null = null;

/** Called when the server answers 401 outside /auth (session ended or never started). */
export function setUnauthorizedHandler(fn: (() => void) | null) {
  unauthorized = fn;
}

/** Reports a 401 to the app (sign-in screen, then back to where the user was). */
export function notifyUnauthorized() {
  unauthorized?.();
}

// fetch is looked up per call (not captured at creation) so tests and polyfills can replace it.
export const api = createClient<paths>({
  baseUrl: API_ROOT,
  fetch: async (req: Request) => {
    const res = await globalThis.fetch(req);
    if (res.status === 401 && !req.url.includes('/api/v1/auth/')) notifyUnauthorized();
    return res;
  },
});

/** Problem+json error from the server (RFC 9457), or a network failure (status 0). */
export class ApiError extends Error {
  readonly status: number;
  readonly detail: string;
  constructor(status: number, detail: string) {
    super(detail);
    this.name = 'ApiError';
    this.status = status;
    this.detail = detail;
  }
  get isNetwork() {
    return this.status === 0;
  }
}

type FetchResult<T> = { data?: T; error?: unknown; response: Response };

function detailOf(error: unknown, response: Response): string {
  if (error && typeof error === 'object') {
    const e = error as { detail?: unknown; title?: unknown };
    if (typeof e.detail === 'string' && e.detail) return e.detail;
    if (typeof e.title === 'string' && e.title) return e.title;
  }
  return `The server answered ${response.status} ${response.statusText}`.trim();
}

/** Awaits an openapi-fetch call and returns its data, or throws an ApiError. */
export async function unwrap<T>(call: Promise<FetchResult<T>>): Promise<T> {
  let res: FetchResult<T>;
  try {
    res = await call;
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new ApiError(0, 'Gonema could not be reached.');
  }
  if (res.error !== undefined || !res.response.ok)
    throw new ApiError(res.response.status, detailOf(res.error, res.response));
  return res.data as T;
}
