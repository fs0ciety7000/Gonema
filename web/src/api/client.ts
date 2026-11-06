import createClient from 'openapi-fetch';
import type { paths } from './schema';

/** API root, resolved against <base href> so the SPA works under GONEMA_BASE_URL. */
export const API_ROOT = new URL('api/v1', document.baseURI).href;

// fetch is looked up per call (not captured at creation) so tests and polyfills can replace it.
export const api = createClient<paths>({ baseUrl: API_ROOT, fetch: (req: Request) => globalThis.fetch(req) });

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
