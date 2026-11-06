# End-to-end, performance and fidelity checks

All three run against a real Gonema server on a **demo** database (never your real one):

```bash
make release                                            # SPA built and embedded in bin/gonema
bin/gonema demo seed -films 10000 -data-dir /tmp/gonema-demo
bin/gonema serve -data-dir /tmp/gonema-demo &           # http://127.0.0.1:7676
cd web
npm run e2e        # library, filter, select, open a film, monitored + Undo, J/K (node:test + Playwright)
npm run perf       # bundle sizes, first poster, CLS, scroll frame times (grid, table, CPU 1x/4x) -> e2e/perf.json
npm run fidelity   # screenshots + side-by-side with docs/design/renders + axe-core -> e2e/__screenshots__/
```

`GONEMA_E2E_URL` overrides the server URL; `PLAYWRIGHT_CHROMIUM` the browser binary
(default `/opt/pw-browsers/chromium`; the `playwright` package drives it, no browser download).
