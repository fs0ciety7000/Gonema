# End-to-end, performance and fidelity checks

All three run against a real Gonema server on a **demo** database (never your real one):

```bash
make release                                            # SPA built and embedded in bin/gonema
bin/gonema demo seed -films 10000 -data-dir /tmp/gonema-demo
bin/gonema serve -data-dir /tmp/gonema-demo &           # http://127.0.0.1:7676
cd web
npm run e2e        # lot 1: library, filter, select, film, Undo, J/K · lot 2: add (TMDB mocked), interactive search
                   # (stream mocked), queue pause/remove + Undo, history filter, bulk tags + Undo, delete file confirm
npm run perf       # bundle sizes, first poster, CLS, scroll frame times (grid, table, CPU 1x/4x) -> e2e/perf.json
npm run fidelity   # screenshots + side-by-side with docs/design/renders + axe-core -> e2e/__screenshots__/
```

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
