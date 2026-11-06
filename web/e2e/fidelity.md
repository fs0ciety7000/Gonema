# Fidelity — phase 7, lot 1 (library grid, library table, film page)

Captures: `node e2e/fidelity.mjs` against the production build embedded in the Go binary, demo
library of 10,000 films (`gonema demo seed -films 10000`), 1440 × 900 (tablet: 1024 × 768),
dark and light. App on the left, mockup render on the right: `__screenshots__/compare-*.png`.
App only: `__screenshots__/<screen>-<theme>.png`. States mirror the mockups (grid: 2 films
selected, pointer on a third poster; table: one row selected, its context menu open).

Accessibility: axe-core 4.14 (wcag2a, wcag2aa, wcag21a, wcag21aa, best-practice) on every capture,
including the 404 page: **0 violations on 10/10 captures** (`__screenshots__/a11y-report.json`).

## Deviations

### Content (data, not design)

| # | Visible difference | Why |
|---|---|---|
| C1 | Posters are flat gradients with the title in a mono face; cast portraits are initials | Demo data: the seed renders its own images (no TMDB, no network). With a TMDB key the server's cached images are shown. A typographic placeholder (film colour + title) is used when a film has no image at all. |
| C2 | "Demo mode" banner above the top bar (40 px) | Required when `system/status.demo` is true. Pushes the screens down by 40 px versus the renders. |
| C3 | Different titles, counts, sizes, dates, collection names | Real API data (fictional demo library) instead of the mockup's hand-picked films. |
| C4 | Saved views: "All films · 4K · Wanted · Recently added" | Built-in views computed from real fields; the mockup's "4K HDR", "Wanted this year", "Over 40 GB" were examples. Views saved with "+ View" go to `/views` and appear after them. |
| C5 | No active filter chips in the grid capture | Chips appear only for active filters (the mockup showed two example filters). |
| C6 | Film page: no IMDb ring, no studio, no tagline in the mockup's words, no trailer duration | Not in the demo data / not in the API (studio, trailer length). The ring/credit/button appear when the data exists. |
| C7 | File card: no score breakdown tags ("DV +40", …) | The API returns the file's total custom score only. Needs a breakdown field (backend). |
| C8 | Release dates: no region label ("United States") | Not exposed by `GET /movies/{id}`. |
| C9 | Collection: no "part 2" in the overline; "N in your library" | Part order is not in the detail payload; the count of TMDB parts appears when `GET /collections/{id}` answers (TMDB needed). |

### Scope of this build (hidden, never dead ends)

Production builds hide what later lots build; development builds show an honest placeholder.

| # | Hidden in production | Lot |
|---|---|---|
| S1 | Sidebar: Home, Collections, Calendar, Add & discover, Queue, History, Wanted, Blocklist, Health, Settings (only **Films** remains; empty section headings are dropped) | 2–3 |
| S2 | Top bar: "Add film" button and notifications bell (no notification feed endpoint) | 2 |
| S3 | Queue pill is a status (not a link) until the Queue screen exists | 2 |
| S4 | Bulk bar: Tags, Move, Delete… (they need their dialogs) | 2 |
| S5 | Menus: Interactive search…, Edit tags…, Rename files… | 2 |
| S6 | Film page: "Interactive search" button, "Rename preview", "Delete file…", collection "Open" | 2 |
| S7 | ⌘K opens nothing yet: it focuses the search field, which filters the library (palette = lot 3) | 3 |
| S8 | 404: the "Search ⌘K" button | 3 |
| S9 | Offline card: "Troubleshooting guide" link (no published guide yet) | — |

### Design decisions taken while implementing

| # | Difference | Justification |
|---|---|---|
| D1 | "Remove" (bulk) and "Remove from library" (menus) apply at once with **Undo**, no dialog, no ellipsis | DESIGN.md: confirmation dialogs are reserved for deleting files; removal from the library keeps files, so it is the Undo pattern (motion.md §5). The DELETE is sent when the toast closes. |
| D2 | Bulk bar shows "Unmonitor" when every selected film is monitored | Same slot as "Monitor", states what the button will do. |
| D3 | Tagline in Geist italic | The mockup uses Instrument Serif, which DESIGN.md (écart 5) limits to the generated posters; the product keeps the kit's three families. |
| D4 | User row: "This server · No sign-in yet · v…" with a neutral icon | No users before phase 8; "Margaux · Admin" was fictional. |
| D5 | Search field shows "Ctrl K" on non-Apple systems | Truthful shortcut label. |
| D6 | Poster status line for missing films: "Wanted", "In cinemas", "Announced", "Out 14 Nov" | Derived from monitored + release stage + release dates; the mockup's "Searching daily" is not a fact the API exposes. |
| D7 | "Upgraded" badge only on the film page (latest import was an upgrade) | The list has no "recently upgraded" signal; grid/table show Available for upgradable films. |
| D8 | Poster hover controls are frosted only under a hovering fine pointer | Measured scroll cost: their backdrop blur was painted on every card even at opacity 0. Keyboard focus shows them on their 55 % dark fill (same contrast). |
| D9 | Table header offset by the banner height when the demo/offline banner is shown | Keeps the sticky header under the top bar instead of behind it. |
| D10 | Offline card: log tip reads `docker logs --tail 50 gonema`; port from the page URL | The server logs to stderr (no `/config/logs/gonema.log`); the mockup's port 7878 is not Gonema's (7676). |
| D11 | Light/dark captured; tablet capture without a side-by-side composite | The composite script pairs 1440-wide images only; compare `library-grid-tablet-*.png` with `docs/design/renders/library-grid-tablet-*.jpg` directly (rail, 5 columns, no queue pill, as designed). |

### Build workaround that affects rendering

`gonema.css` writes `backdrop-filter` then `-webkit-backdrop-filter` with `var()` values. The CSS
minifier used by Tailwind 4 and Vite 8 (lightningcss) collapses the pair into the prefixed one only,
which Chromium and Firefox ignore: every glass surface (top bar, menus, dialogs, toasts, bulk bar,
on-poster badges) lost its frost in production builds. `src/styles/app-shell.css` restates the same
values unprefixed (the minifier then emits both). Reported so `gonema.css` can be reordered or the
minifier fixed upstream; no token value is re-typed.

### Not matched / to review

- Poster → film shared-element transition is implemented with View Transitions (Motion-free WAAPI
  FLIP fallback, 150 ms cross-fade under reduced motion) but not frame-captured against
  `docs/design/renders/motion-poster-detail-*.jpg`.
- Browser Back from a film uses the router's plain navigation (no reverse shared-element
  transition); the in-app "Films" breadcrumb plays the reverse transition and focus returns to the poster.
