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

### Build workaround (resolved in lot 2)

Lot 1 restated the glass `backdrop-filter` values in `app-shell.css` because lightningcss collapsed
`backdrop-filter` + `-webkit-backdrop-filter` into the prefixed one. Lot 2 reordered `gonema.css`
(prefixed first, unprefixed last) and removed the workaround: the built CSS keeps both declarations
for every glass surface (checked in `dist/assets/index-*.css`).

### Not matched / to review

- Poster → film shared-element transition is implemented with View Transitions (Motion-free WAAPI
  FLIP fallback, 150 ms cross-fade under reduced motion) but not frame-captured against
  `docs/design/renders/motion-poster-detail-*.jpg`.
- Browser Back from a film uses the router's plain navigation (no reverse shared-element
  transition); the in-app "Films" breadcrumb plays the reverse transition and focus returns to the poster.


---

# Fidelity — phase 7, lot 2 (add & discover, interactive search, activity, library dialogs)

Captures: `node e2e/fidelity.mjs` (production build in the Go binary, demo library of 10,000 films).
New side-by-sides: `compare-add-film-*`, `compare-add-film-discover-*`, `compare-interactive-search-*`,
`compare-queue-*`, `compare-queue-history-*`. App-only states audited too: `add-film-no-key`,
`tmdb-key-dialog`, `interactive-search-confirm`, `dialog-tags`, `dialog-move`, `dialog-rename`,
`dialog-delete-file`.

Accessibility: axe-core 4.14, same tags: **0 violations on 34/34 captures** (17 states × dark + light),
`__screenshots__/a11y-report.json`.

Mocked for captures (see `e2e/README.md`): TMDB lists and lookup (no key in the demo), the interactive
search stream (no indexer in the demo). Queue and history are real demo data.

## Deviations

### Content (data, not design)

| # | Visible difference | Why |
|---|---|---|
| C10 | Add film: result meta reads "Released 3 Apr 1998 · English · TMDB 7.1" instead of "Drama · Walter Brisk"; panel caption without runtime/genre/director | `/lookup` returns no genres names, runtime or crew. |
| C11 | Add film: no folder-template preview; the hint shows `<root>/<Title (Year)>/` | The naming preview needs a movie; the folder name follows the default template and may differ from a custom one. |
| C12 | Discover: no "Make this an import list" button | Import lists have no endpoint yet (lot 3). |
| C13 | Discover: "Filmography" asks for a TMDB person link or id instead of a name search | No person search endpoint (`/discover/person/{tmdbId}` only, name not returned). |
| C14 | Queue: every demo item is "Queued"/downloading on a disabled demo client; no failed retry row, importing or paused rows unless created | Demo data. The retry row ("Trying release #2 of 6"), importing, warning ("Retry import") and paused states are implemented from `QueueEntry.retry/actions/status`. |
| C15 | History: reasons read "(demo data)"; quality/score often empty | Demo history entries. Chains use `chainId` (left rule), none in the demo. |
| C16 | Interactive search: no "Indexer health" link in the failed-indexer notice | Health screen is lot 3. |

### Scope (hidden, never dead ends)

| # | Not in this build | Lot |
|---|---|---|
| S10 | Activity: "Blocklist" tab, "Pause all", "Export" | 3 (no pause-all / export endpoints) |
| S11 | Sidebar: Home, Collections, Calendar, Wanted, Blocklist, Health, Settings stay hidden in production | 3 |
| S12 | Queue empty state: "Search the N now" → links to the library's Wanted view instead (no bulk search endpoint) | 3 |

Lot 1 items S2–S6 are now built: "Add film" button + `A` shortcut, queue pill links to the queue,
bulk Tags / Move / Delete…, menus (Interactive search… `I`, Edit tags… `⇧T`, Move…, Rename files…,
Delete file…), film page (Interactive search, Rename preview, Delete file…, collection Open with the
whole collection and "Add the N missing", file score breakdown tags — C7 resolved).

### Design decisions taken while implementing

| # | Difference | Justification |
|---|---|---|
| D12 | Bulk bar "Delete…" opens a dialog: "Keep the files" (default, then the Undo flow) or "Also move files to recycle bin" (button says exactly that). Menus keep the immediate "Remove from library" with Undo | Confirmation is reserved for files on disk; removal alone stays Undo (D1). |
| D13 | "Delete file…" confirmation states the recycle bin and its retention (from `/import/settings`, 30 days by default); the success toast offers "Restore" | Nothing is deleted for good; restore uses `/recycle-bin/restore`. |
| D14 | Queue remove: options popover (blocklist ✓, search again ✓, delete data ✗), applied at once with Undo; the DELETE is sent when the toast closes | motion.md §5 (clear queue item = Undo). |
| D15 | Interactive search: "Retry" of a failed indexer restarts the whole search | No per-indexer retry endpoint; the notice says so. |
| D16 | Rejected rows with only permanent reasons show an info toggle; "Grab anyway…" is then inside the expanded decision panel (soft reasons keep the button in the row, as in the mockup) | Mockup behaviour, made reachable for every rejected release. |
| D17 | Interactive search table virtualised above 100 rows (spacer rows); keyboard: ↑/↓ rows, Enter expands, G grabs (rejected → confirmation), Esc back to the film | Spec. |
| D18 | Discover posters without a TMDB image show the typographic placeholder; poster click opens the options dialog, "+" adds with the last options | Same placeholder rule as C1. |
| D19 | Add options remembered in `localStorage` (`gn-add-options`), validated against existing folders/profiles | "remember last choices". |
| D20 | "Whole collection": the film is added, then the collection is monitored with the same options (its other films added); Undo removes all of them | `/lookup` has no collection id before adding. |
| D21 | The library table is a lazy chunk (preloaded when idle); `vite.config.ts` groups everything the first screen imports into one chunk (Rolldown `$initial`) | Initial JS budget: 198.3 KB gzip (lot 1: 199.4 KB) with lot 2's menu items, routes and stores. |

### Measurements (this machine, 2026-11-06)

- Initial JS for the library: **198,270 B gzip** (≤ 200 KB); CSS 18.2 KB. Lazy chunks: interactive search 8.9 KB,
  activity 7.7 KB, add 5.9 KB + discover 5.5 KB, dialogs 5.3 KB, lot2.css 5.1 KB (gzip).
- Scroll (10k films, `e2e/perf.json`): grid 1× 54–59 fps, table 1× 42–51 fps. The lot 1 build measured on the same machine
  and server in the same session: grid 55 fps, table 51.4 fps — the table is below its lot 1 figure (57.2) for both builds,
  so the drop is the environment, not lot 2; to re-measure on an idle machine.


---

# Fidelity — phase 7, lot 3 (home, calendar, settings, setup, ⌘K palette, wanted, blocklist, health, failed download)

Captures: `node e2e/fidelity.mjs` (production build in the Go binary, demo library of 10,000 films).
New side-by-sides: `compare-dashboard-*`, `compare-calendar-*`, `compare-calendar-agenda-*`, `compare-command-palette-*`
(`-query`, `-empty`), `compare-settings-quality-*`, `compare-settings-index-*`, `compare-onboarding-{welcome,tmdb,folder,quality,client-error}-*`,
`compare-failed-download-*`, `compare-404-*`. App-only: `onboarding-film`, `wanted`, `blocklist`, `health`, `settings-indexer-dialog`.

Accessibility: axe-core 4.14, same tags: **0 violations on 72/72 captures** (36 states × dark + light), no console error.

Mocked for captures: the client connection test of `onboarding-client-error` (no reachable client), and one queue entry shown
as failed for `failed-download` (the demo queue has none). Everything else is the real demo server.

## Deviations

### Content (data, not design)

| # | Visible difference | Why |
|---|---|---|
| C17 | Home: "Good morning/afternoon/evening" without a name; no notifications bell | No users before phase 8 (D4); no notification feed endpoint (S2). |
| C18 | Home alert is the most severe health issue (the demo: "No TMDB key yet"), with "Check again" + "Health" instead of "Test now" | The alert shows whatever `/dashboard.health.top[0]` is; a per-indexer test belongs to the indexer, on the Health page. |
| C19 | Home "Wanted" tile links to Wanted ("See what’s missing") instead of "Search the 12 released" | Still no bulk search endpoint (S12). |
| C20 | Calendar, home, palette: generated posters (C1), titles repeat in the demo | Demo data. |
| C21 | Palette: no "Releases from the last search" group, no recent "Interactive search: …" entries, no person "3 films in your library" | `GET /search` returns library films and TMDB films/people only. Recents are films and sections opened from the palette (this browser). |
| C22 | Failed download: releases tried come from the film's history (grabs and failures); no "Next / +2 more" rows, no client log timestamps | The API does not expose the remaining candidate list; `QueueEntry.messages` are plain strings. |
| C23 | Settings overview: no Security, Backups, Logs panels; no "Import lists", "Security", "Backups", "Logs", "About" in the section nav | No endpoints yet (phase 8). Hidden, never dead ends. |
| C24 | Setup: 7 steps (Welcome, TMDB, folder, quality, indexer, client, first film) instead of 8; no Account step; indexer before client | No sign-in before phase 8; order given by the brief of this lot. Step state comes from `GET /onboarding`. |
| C25 | Setup "first film": no grabbed-print frame in the capture (`onboarding-grabbed` not compared) | Needs TMDB + an indexer + a client; the step shows the live queue entry when there is one. |

### Scope (hidden, never dead ends)

| # | Not in this build | Why |
|---|---|---|
| S13 | Calendar "iCal feed" button | No feed endpoint. |
| S14 | Quality editor "Group" button (merging qualities into a group) | Groups from the server are shown and kept; creating one in the UI is left for later. |
| S15 | Failed download "Stop retrying" | No endpoint to stop the automatic retries of one film. |
| S16 | Library empty state "Import a folder" | The library import flow (`/library-import/*`) has no screen yet. |
| S17 | Collections section | No collection list endpoint. |

Lot 2 items now built: C13 (person search by name, `GET /discover/people`), C16 + D15 (per-indexer **Retry**
`POST /searches/{id}/indexers/{indexerId}/retry?force=true` and the "Indexer health" link), S10 (Blocklist tab, **Pause all /
Resume all**, history **Export** CSV), C8 (release regions with `Intl.DisplayNames`, shown in the panel header when shared),
C6 (studios in the credits), S7/S8 (⌘K palette, 404 "Search" button).

### Design decisions taken while implementing

| # | Difference | Justification |
|---|---|---|
| D22 | Health lives at `/system/health` (sidebar entry unchanged) | The Go server answers `GET /health` itself (liveness probe): a reload of `/health` would show its JSON. |
| D23 | ⌘K and Ctrl+K open the palette everywhere; "/" focuses the top-bar field; on screens without a list to filter, the field opens the palette | Truthful shortcuts; the library keeps its instant filter field. |
| D24 | Palette: own ranking from `GET /search` (every keystroke), TMDB films and people with `remote=true` after 350 ms and only when a key is configured; scopes chips + `>` / `@` prefixes, Tab cycles scopes | Contract advice; no request to TMDB without a key. No open/close animation (motion.md §7). |
| D25 | Removing a blocklist entry, a connection, a root folder, a tag, a profile or a rule: applied at once with **Undo**, the DELETE is sent when the toast closes | Same pattern as D1/D14: confirmation dialogs stay reserved for files on disk; nothing here touches files. |
| D26 | Connection forms (indexers, clients, media servers, notifications) are dialogs with **Test** before **Save**; secret fields are `type=password`, empty, "Saved · leave blank to keep it" | Secrets are write-only (`…Set` flags only); a blank secret keeps the stored one. |
| D27 | Quality list: reorder by drag **or** keyboard (grip button, ↑/↓, announced); cutoff chosen from a select (the marker follows) | Accessible reorder; the mockup's dragged state is not reproduced in the capture. |
| D28 | Rule scores edited in the list are saved with the profile (one PUT); per-profile scores in the rule editor are saved with the rule | Matches the API: profile scores ride on `PUT /quality-profiles/{id}`, rule scores on `PUT /scoring-rules/{id}`. |
| D29 | Release tester scores against the selected profile and the first film of that profile with a file (decision shown when the server returns one) | `POST /quality/preview` decides only with a `movieId`. |
| D30 | Calendar: month grid shows at most 4 releases per day, then "+N more"; types and "Announced & unmonitored" are chips (the latter in the URL, `all=1`, off by default: PO decision 6) | Keeps the grid legible with 10,000 films. |
| D31 | The 404 page is a lazy chunk; the library route's first load is otherwise unchanged | Initial JS budget (below). |

### Measurements (this machine, 2026-11-06)

- Initial JS for the library: **199,805 B gzip** (≤ 200 KB; lot 2: 198,270 B). The 1.5 KB growth is the router (12 new routes),
  the palette store and shortcut, and the shell changes; every lot 3 screen and cmdk are lazy chunks. CSS 18.3 KB.
  Lazy chunks (gzip): palette 20.8 KB (cmdk), quality editor 8.8 KB, settings 8.3 KB, setup 8.1 KB, home 4.3 KB, failed download 3.3 KB,
  calendar 3.2 KB, health 2.6 KB, wanted 2.1 KB, lot3.css 7.0 KB.
- Scroll (10k films): grid 1× 59.8 fps (p95 16.7 ms), table 1× 51.8 fps, grid 4× 24.7 fps, table 4× 13.3 fps — same as lot 2 on this machine.
- First poster 466 ms, CLS 0.023.


---

# Fidelity — phase 8 (sign-in, account step, Security, Backups, Logs, About, health, scroll)

Captures: `node e2e/fidelity.mjs` (production build in the Go binary, demo library of 10,000 films, server in
`GONEMA_AUTH=disabled_for_local`). New app-only captures (no mockup render exists for them): `login`, `login-error`,
`login-throttled`, `onboarding-account`, `settings-security`, `settings-security-account`, `settings-backups`,
`settings-backups-restore`, `settings-logs`, `settings-about`; `settings-index` now shows the Security, Backups and Logs cards.

Accessibility: axe-core 4.14, same tags: **0 violations on 92/92 captures** (46 states × dark + light), plus the sign-in page
with an error and the account step with the API key in `e2e/fresh/auth.test.mjs`. Console: only the 401 / 429 resource
messages of the mocked failed sign-ins.

## Deviations

### Restored

- **C24 resolved**: the setup has its 8 steps again, with **Account** second (onboarding.html `?step=account`).
  Overlines count from it ("Step 1 · Security" … "Step 7 · First screening"); Welcome says "Seven short steps" and lists
  "Secure the booth". **C23 resolved**: Settings has a **System** group (Security, Backups, Logs, About) and the overview
  shows the three cards of `settings-quality.html?section=index`. **D4 / C17 resolved**: the sidebar user row shows the
  username ("This server · Local access" in the local mode) and a **Sign out** button.

### Design decisions taken while implementing

| # | Difference | Justification |
|---|---|---|
| D32 | **Sign-in page derived from onboarding.html** (no mockup): same top bar (brand + theme), one glass-2 card of 440 px, overline + Archivo title "Back to the booth", one primary button, "Forgot the password?" disclosure. No poster wall (the library cannot be read signed out), no Kernel | DESIGN.md lists where Kernel may appear; sign-in is not one of them, and a mascot next to a password field reads as noise. |
| D33 | Wrong credentials: a calm alert above the fields, the password cleared and focused. **429**: a "Too many attempts." status with a `m:ss` countdown from `Retry-After`; the button stays disabled until 0; screen readers hear the wait once (in minutes), not every second | Brief: "calm countdown". |
| D34 | Account step: no "Sign-in method" select, no "add more people later" | The auth mode is a start option (`GONEMA_AUTH`), not an API setting; one account only (docs/security.md §8). The step says how to start in the local mode instead. Password hint "at least 10" (server rule), the mockup's "12" was an example. |
| D35 | **Setup token** field on the account step only when `setupTokenRequired` (another machine than the server); the hint says where the token is (log line "no account exists yet", `docker logs gonema`) | Contract of the security fixes. 403 messages from the server (token, `GONEMA_ALLOWED_HOSTS`) are shown as they are. |
| D36 | API key shown once in a sunken panel (read-only mono field, Copy, "You won’t see it again."); kept only in component state, dropped on Continue / dialog close; copy falls back to select + execCommand on plain-HTTP origins | Brief. The e2e checks the key is gone from the DOM after the step. |
| D37 | Welcome's "Already have a backup? Restore it instead" is not shown | Restoring needs a signed-in caller; after the account step, Settings › Backups → Upload… → Restore… does it. |
| D38 | Until the account exists, the other setup steps are shown but not links, "Skip for now" is hidden, and nothing else is fetched | Every other endpoint answers 401 before sign-in. In the local mode the account step stays optional (Skip works). |
| D39 | Security: session revoke (other browsers) applies at once with **Undo** (DELETE sent when the toast closes); the current browser has "Sign out". API key **Regenerate… asks for confirmation** (the old key stops working at once), then shows the new key once in the same dialog. Password change can also replace the key (checkbox). | D25 pattern for reversible removals; the key replacement is not reversible, hence the confirm. |
| D40 | Outbound policy, cookie mode, allowed hosts, trusted proxies and trusted origins are read only, with the variable names to change them | They are start options; the API exposes them read-only. |
| D41 | Backups: **Restore…** and **Delete…** both confirm in a dialog. Restore says a backup of the current state is made first, that files on disk are untouched, that the master key is not included (opt-in checkbox when the archive holds one), and that it applies at the next restart; a staged restore shows a notice with **Cancel restore** | Brief; backup files are Gonema's own but are files: confirm, never Undo. Upload… accepts a `.zip` (server validates it; 413 / 507 messages shown). |
| D42 | Logs: virtualised viewer (dynamic line heights), level filter (server-side `level`), search (server-side `q` for the history, same test client-side for the live tail), **Live** switch over `GET /logs/stream`, "N new" button when scrolled up, Download, and the runtime level (Segmented, "until the next restart") | The overview card's "Level" control is the runtime level too, as in the mockup. Log region is focusable (keyboard scrolling). |
| D43 | Health: interrupted-import issues get **Mark as handled…** (dialog: Gonema changes no file) and a panel lists the import journal; the backup issue links "Open backups" | The API requires `confirm=true`; the dialog is the confirmation. |
| D44 | Logs icon `file-video` in the settings nav (as the mockup) | Mockup fidelity; `file-text` would read better (to review). |

### Scroll performance (library, 10,000 films)

| # | Change | Why |
|---|---|---|
| P1 | Table rows are grid rows placed one by one (`translateY`, `contain: strict`) instead of a `<tbody>` translated as a whole; the header sticks as a block | A row entering the window no longer moves (and repaints) every other row. Roles are explicit on every element (`grid/rowgroup/row/columnheader/gridcell`), so the grid semantics do not depend on table display. |
| P2 | Table thumbnails that enter **while the list scrolls** show the film's flat colour, the image comes when scrolling stops (150 ms); fallback tiles are flat in the table | The server only has w342 posters: decoding one per row and frame for a 20–28 px slot was the largest single cost. A w92 poster size in `internal/imagecache` would let the table keep images during scroll (backend suggestion). |
| P3 | Table status and "more" icons are cached background images (same Lucide glyphs and DESIGN.md colours, per theme, `styles/table.css`, lazy) instead of an inline SVG per row; the header is memoised | Fewer DOM nodes per cell, cheaper paint. |
| P4 | Poster cards mount the hover controls (shade, quality line, Search / Monitor / More, checkbox) only on the card under a mouse pointer, the grid's tab stop, or while selecting; `@starting-style` keeps their rise-in | ~40 % fewer nodes per card that scrolls in. Keyboard and touch paths unchanged (the tab stop has its controls). |
| P5 | While the grid / table scrolls (`data-scrolling`), hover changes apply without transitions | A card passing under a resting pointer no longer starts transform / shadow / filter transitions every few frames. Hover itself stays active (motion.md perf budget). |

### Initial JS (library route)

- **154,168 B gzip** (lot 3: 199,805 B): menus (Base UI Menu + Floating UI) are behind a facade (`components/ui/Menu.tsx`) and
  load when idle, on hover / focus of a trigger, or on first click (`MenuImpl.tsx`, 11.5 KB gzip + shared Base UI chunk).
  The grid and table context menus are one anchored menu per list (`ListMenu`), so the list is no longer wrapped by Base UI.
  Everything of phase 8 is lazy: sign-in 2.1 KB, Security 4.0 KB, Backups 3.2 KB, Logs 3.0 KB, About 1.0 KB, overview cards
  1.5 KB, ops API 1.1 KB (gzip). The auth guard, 401 handler and user row add ~0.8 KB to the first chunk.
- First load: the server status is requested in parallel with the sign-in state, so the demo banner does not arrive one
  round trip later (CLS 0.003–0.023 measured, it depends on timing).

### Measurements (this machine, 2026-11-06, same session, interleaved runs, median of 3)

| | lot 3 build | phase 8 build |
|---|---|---|
| grid 1× | 59.8 fps | 60 fps |
| table 1× | 56 fps (p95 33 ms) | **60 fps** (p95 16.8 ms) |
| grid 4× | 23.2 fps | **30 fps** |
| table 4× | 14.4 fps | **25.6 fps** |

`npm run perf` on the final phase 8 build: grid 59.4 / 28.9 fps, table 60 / 22.0 fps (1× / 4×), first poster 487 ms, CLS 0.002. The machine was shared with
other builds and tests (load 1.4–9): absolute numbers move between runs, the interleaved comparison does not.
