/* First-run setup (GET/PUT /onboarding). Loaded on demand by the home route and the setup screen. */
import type { QueryClient } from '@tanstack/react-query';
import { queryOptions } from '@tanstack/react-query';
import { api, unwrap } from './client';
import type { SetupStatus } from './schema';

/** UI step ids, in the order of the guided setup. */
export type OnboardingStepId = 'tmdb' | 'folder' | 'quality' | 'indexer' | 'client' | 'film';

const SERVER_ID: Record<OnboardingStepId, string> = {
  tmdb: 'tmdb',
  folder: 'root_folder',
  quality: 'quality_profile',
  indexer: 'indexer',
  client: 'download_client',
  film: 'first_movie',
};

export type OnboardingState = {
  complete: boolean;
  /** "Skip for now" (stored on the server: the setup stays skipped on every device). */
  dismissed: boolean;
  done: Record<OnboardingStepId, boolean>;
};

/** Validates the server's answer into the UI model (unknown step ids are ignored). */
export function parseOnboarding(raw: SetupStatus | null | undefined): OnboardingState {
  const steps = raw?.steps ?? [];
  const done = Object.fromEntries(
    (Object.keys(SERVER_ID) as OnboardingStepId[]).map((id) => [
      id,
      !!steps.find((s) => s.id === SERVER_ID[id])?.done,
    ]),
  ) as Record<OnboardingStepId, boolean>;
  return { complete: !!raw?.complete, dismissed: !!raw?.dismissed, done };
}

export const onboardingQuery = queryOptions({
  queryKey: ['onboarding'] as const,
  queryFn: async ({ signal }) => {
    try {
      return parseOnboarding(await unwrap(api.GET('/onboarding', { signal })));
    } catch (e) {
      // server without the endpoint: no guided setup
      if ((e as { status?: number }).status === 404) return null;
      throw e;
    }
  },
  staleTime: 30_000,
});

/** Skip (dismissed) or resume the setup, for every browser. */
export async function setOnboardingDismissed(qc: QueryClient, dismissed: boolean) {
  const r = await unwrap(api.PUT('/onboarding', { body: { dismissed } }));
  qc.setQueryData(onboardingQuery.queryKey, parseOnboarding(r));
}

/** True when the first run should open the guided setup instead of the home screen. */
export async function needsOnboarding(qc: QueryClient): Promise<boolean> {
  try {
    const s = await qc.ensureQueryData(onboardingQuery);
    return !!s && !s.complete && !s.dismissed;
  } catch {
    return false; // never block the app on this check
  }
}
