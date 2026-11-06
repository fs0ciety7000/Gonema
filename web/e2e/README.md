# End-to-end, performance and fidelity checks

All three run against a real Gonema server on a **demo** database (never your real one):

```bash
make release                                            # SPA built and embedded in bin/gonema
bin/gonema demo seed -films 10000 -data-dir /tmp/gonema-demo
GONEMA_AUTH=disabled_for_local bin/gonema serve -data-dir /tmp/gonema-demo &   # http://127.0.0.1:7676, no sign-in from 127.0.0.1
cd web
npm run e2e        # system.test.mjs: sign-in states, Security, Backups (restore staged then canceled, delete), Logs
                   # v11.test.mjs: collections, Import a folder (temporary folder), stop retrying, bulk search
                   # lot 1: library, filter, select, film, Undo, J/K · lot 2: add (TMDB mocked), interactive search
                   # (stream mocked), queue pause/remove + Undo, history filter, bulk tags + Undo, delete file confirm
npm run perf       # bundle sizes, first poster, CLS, scroll frame times (grid, table, CPU 1x/4x) -> e2e/perf.json
npm run fidelity   # screenshots + side-by-side with docs/design/renders + axe-core -> e2e/__screenshots__/
```

Since phase 8 every API route needs a signed-in caller. The demo suites expect the **local mode**
(`GONEMA_AUTH=disabled_for_local`: loopback and LAN addresses reached directly pass without a session);
`ensureServer()` (helpers.mjs) checks `GET /api/v1/auth/status` and stops with an explicit message
otherwise. Against a server with sign-in on, set `GONEMA_E2E_API_KEY`: helpers.mjs then sends it in
`X-Api-Key` from Node (`api()`) and from the browser (`extraHTTPHeaders`). No helper reads or sets the
session cookie (its name may be `gonema_session` or `__Host-gonema_session`).

### Fresh-server suites (`npm run e2e:fresh`)

`e2e/fresh/` needs servers on **empty data directories**, with the default sign-in (no local mode),
reached from 127.0.0.1 (so `setupTokenRequired` is false):

```bash
bin/gonema serve -data-dir "$(mktemp -d)" -bind 127.0.0.1:7677 &   # GONEMA_E2E_AUTH_URL
bin/gonema serve -data-dir "$(mktemp -d)" -bind 127.0.0.1:7678 &   # GONEMA_E2E_JOURNEY_URL
npm run e2e:fresh
```

- `auth.test.mjs`: first visit → setup, only Welcome/Account open → account created → API key shown once
  (copy, "you won't see it again", works in `X-Api-Key`, gone after Continue, last 4 only in Settings ›
  Security) → sign out from the sidebar → protected URL → sign-in with `?redirect=` → wrong password
  (calm alert) → sign-in → back to `/films?layout=table` → sessions revoked with the API key → next
  click lands on sign-in with the way back → 25 failures from Node → 429 countdown, button disabled.
  It creates the account and leaves sign-in throttled for 15 minutes: restart it on a new directory.
- `journey.test.mjs`: welcome → account → TMDB → library folder (a real new directory) → quality →
  indexer → client → first film. **Mocked with `page.route`** (they need the outside world):
  `PUT/GET /settings/tmdb` (TMDB validates the key), `GET /lookup` and `POST /movies` (TMDB),
  `POST /indexers/test`, `POST /download-clients/test` (no reachable indexer or client). The account,
  root folder, profile choice, indexer and client records are the real server; the test checks the
  server's onboarding state at the end.

`GONEMA_E2E_URL` overrides the server URL; `PLAYWRIGHT_CHROMIUM` the browser binary
(default `/opt/pw-browsers/chromium`; the `playwright` package drives it, no browser download).

### What is mocked (`e2e/mocks.mjs`, `page.route`)

The demo has no TMDB key, no network and no indexer, so these are mocked in e2e and fidelity runs:

- TMDB-backed endpoints: `/lookup`, `/discover/*`, `POST /movies` (+ `DELETE` of the fake added id), `GET /settings/tmdb`,
  and the `PUT /settings/tmdb` validation answer. Films "in the library" in these mocks are real demo films.
- Interactive search: `POST /movies/{id}/search/interactive`, `GET /searches/{id}`, `POST …/grab` and the event stream
  (one SSE body ending with `retry: 600000`).
- The pause call of a queue item (the demo download client is disabled). The queue list, removal (Undo, nothing sent),
  history, tags, bulk restore, move/rename previews and the file deletion are the real server.
- Lot 3: the indexer connection test (`POST /indexers/test`) and the download client test of the setup capture (no network);
  TMDB people search and filmography (`/discover/people`, `/discover/person/{id}`, `GET /settings/tmdb` answered as configured);
  the per-indexer retry of the mocked interactive search; one queue entry re-shaped as failed for the failed-download capture
  (`GET /queue`). Home, calendar, palette search, wanted, blocklist, health, settings (quality profiles, rules, tester, naming),
  setup state (`PUT /onboarding` is real: the suites leave it dismissed), pause all and history export are the real server.
- Phase 8 fidelity: `login` answers `GET /auth/status` as signed out (the demo is in the local mode), `login-error` and
  `login-throttled` also answer `POST /auth/login` with 401 / 429 + `Retry-After`; `settings-security-account` completes the
  real `GET /settings/security` with an account, two sessions and a key (the local demo has none). Security (local mode),
  Backups, the restore dialog (not confirmed), Logs (live stream) and About are the real server. The lot 3 setup test now
  lands on the Account step (the demo has no account).

- Phase 8 bis (approved mockups): `onboarding-account*` answer `GET /auth/status` as a browser on another machine
  (`setupTokenRequired`) and, for the key state, `POST /auth/setup` with a fictional key; `settings-security-apikey` uses
  the same completed security answer as `settings-security-account` (three sessions). `settings-backups*` and
  `e2e/system.test.mjs` make real archives on the demo, stage a real restore and cancel it; the system suite also deletes
  the archive it made and puts the backup schedule back as it found it.

- v1.1 (`e2e/v11.test.mjs`, fidelity `collections*` and `library-import*`): the empty collections list (the demo has
  collections); `PUT /collections/{id}/monitor` in the suite (it adds films from TMDB; the request bodies are checked);
  `GET /settings/tmdb` answered as configured on the import pages; the scan job (`POST/GET/DELETE /library-import/scans*`),
  `POST /library-import/candidates` and `POST /library-import/confirm` (`mockLibraryImport`: they need TMDB). The import
  folder is **real**: the suite and the captures make a temporary folder with sparse video files, add it as a root folder
  (`POST /root-folders`, removed at the end), and the folder picker lists it with the real `GET /library-import/folders`;
  the suite also runs a real scan, which fails without a TMDB key, and checks the screen says so. Stop / resume retrying,
  the bulk search (start, reload, cancel; the suite adds an indexer that cannot be reached and removes it), collections
  grid and detail are the real server. The failed-download capture's mocked entry now has `stopRetrying`.
