# Gonema — motion spec (phase 2B)

> Tokens: `docs/design/system/gonema.css` (§1 Motion). Live demos: `docs/design/motion/index.html` (toggle "Simulate reduced motion", or add `?motion=reduce`).

## Principles

1. **Motion explains a change of place or state**; it never decorates. If a user triggers it 100 times a day from the keyboard (command palette, J/K between films, list navigation, tab switching by shortcut), it does **not** animate.
2. **150–300 ms.** Arrivals ease out, on-screen travel eases in-out, exits are faster than entrances (≤ 150 ms). Never `ease-in` on an entrance, never `transition: all`.
3. **Only `transform` and `opacity`** move. Blur radius, `width/height/top/left` and box-shadow are not animated per frame (the poster hover shadow is a cross-fade of two shadows via a short transition, acceptable on one element at a time).
4. **Springs for things you touch** (press, hover lift, toggles, selection), as CSS `linear()` curves sampled from a damped spring, so they cost nothing at runtime and stay interruptible as transitions.
5. **Skeletons, not spinners.** No spinning indicator anywhere in the product.
6. **Reduced motion is tiered**, not a kill switch: travel and scale are removed, fades are kept and shortened to ≤ 150 ms, essential feedback (progress value, focus) stays. The same rule is read by CSS (`@media (prefers-reduced-motion: reduce)`) and JS (`matchMedia`, with a `change` listener).

## Tokens

| Token | Value | Use |
|---|---|---|
| `--dur-instant` | 100 ms | press feedback, colour |
| `--dur-fast` | 150 ms | hover, tooltip, menu exit, toast exit |
| `--dur-base` | 200 ms | menu/popover enter, tabs |
| `--dur-slow` | 250 ms | dialog, toast enter, content crossfade |
| `--dur-slower` | 300 ms | shared element poster → detail |
| `--ease-out` | `cubic-bezier(0.23, 1, 0.32, 1)` | entrances, most UI |
| `--ease-in-out` | `cubic-bezier(0.77, 0, 0.175, 1)` | travel on screen |
| `--ease-standard` | `cubic-bezier(0.2, 0, 0, 1)` | colour, opacity |
| `--ease-exit` | `cubic-bezier(0.4, 0, 1, 1)` | exits only, ≤ 150 ms |
| `--spring-snappy` | k 1400, c 70, 230 ms, 0 % overshoot | press, switch track |
| `--spring-gentle` | k 700, c 38, 330 ms, 3.6 % | hover lift, cards, toast arrival |
| `--spring-pop` | k 900, c 36, 380 ms, 9 % | checkmark, switch knob, selection |
| `--stagger` | 24 ms/item, max 12 items | first paint of a grid |

Spring durations include the settle tail; the movement reads as finished at roughly 60 % of the duration.

## 1. Poster → detail (shared element)

**Mechanism (product):** View Transitions API (`document.startViewTransition` on route change, TanStack Router). The grid poster and the detail hero share `view-transition-name: poster-<movieId>`, set only on the clicked poster just before the transition (unique names). Everything else uses the root cross-fade, overridden as below. Fallback when the API is missing: FLIP with WAAPI (as in the demo). Always guard `document.startViewTransition`.

| t (ms) | Poster | Grid / library chrome | Atmosphere (blurred backdrop + tint) | Title, meta, actions |
|---|---|---|---|---|
| 0 | at grid rect, radius 12 | opacity 1 | opacity 0 | opacity 0 |
| 75 | 5 % of the way | opacity 0.5 | 0.3 | 0 |
| 150 | 60 % | 0 | 0.6 | 0.15, +7 px |
| 225 | 96 % | 0 | 0.9 | 0.5, +4 px |
| 300 | hero rect, radius 16 | 0 | 1 | 0.9 → 1 at 320 ms |

- Poster: `transform` 300 ms `--ease-in-out`; radius 12 → 16. The poster image is the same cached file (no reload, no flash).
- Grid: opacity 150 ms linear. Atmosphere: opacity 250 ms `--ease-out`. Copy: opacity + 8 px rise, 200 ms `--ease-out`, delay 120 ms.
- The tint (`--movie-tint`) is applied at t = 0 to the new page so the atmosphere fades in already coloured.
- **Back:** reverse, 250 ms; the grid restores scroll position before the transition starts; focus returns to the poster link.
- Focus moves to the detail `<h1>` (tabindex -1) after the transition; the page title updates immediately.
- **Reduced motion:** no travel. Cross-fade of the whole view, 150 ms. Focus behaviour unchanged.
- Interrupt: a second navigation during the transition skips it (`transition.skipTransition()`).

## 2. Grid and list entrance (stagger)

- Only on the **first paint of a view** (opening the library, switching saved view). Never on scroll (virtualised rows appear instantly), never on filter/sort changes (items cross-fade 150 ms in place), never on keyboard navigation.
- Each item: opacity 0 → 1 and translateY 12 px → 0, scale 0.98 → 1, 250 ms `--ease-out`, delay `min(index, 12) × 24 ms` (≈ 290 ms for the whole screen).
- **Reduced motion:** opacity only, 150 ms, no delay.

## 3. Live progress ("breathing")

- The value moves with `transform: scaleX(var(--p))`, 900 ms `--ease-standard`, so updates arriving every 1–2 s over SSE glide instead of jumping. Never animate backwards except on reset.
- While active, a soft light sheen (38 % white, 40 % of the bar's width) crosses **the filled part only**, every 2.4 s, then rests for 30 % of the cycle. No opacity pulsing, no colour change, nothing outside the bar.
- Sidebar "Queue" live dot: opacity 1 ↔ 0.35, 2.4 s. It is the only looping element in the chrome.
- Paused: sheen stops, colour turns `text-muted`. Failed: sheen stops, colour turns `status-failed`, with the status icon and label.
- **Reduced motion:** sheen and dot pulse removed; value still updates (with the same 900 ms glide shortened to 150 ms).
- Screen readers: `role="progressbar"` with `aria-valuenow`; announce only state changes (completed, failed), never every percent.

## 4. Hover and press springs

| Element | Hover | Press |
|---|---|---|
| Button | background/border colour 150 ms | `scale(0.97)`, `--spring-snappy` 230 ms; release returns immediately |
| Icon button | background 150 ms | `scale(0.94)` |
| Chip | background 150 ms | `scale(0.96)` |
| Poster card | lift −4 px, `scale(1.015)`, stronger shadow, `--spring-gentle`; chrome (select, actions, quality) fades in 150 ms and rises 6 px in 200 ms | `translateY(-2px) scale(0.99)` in 100 ms |
| Switch | — | knob slides with `--spring-pop` |
| Checkbox | — | checkmark scales 0 → 1 with `--spring-pop` |
| Selection (poster) | — | `scale(0.97)` + 2 px accent ring, `--spring-gentle` |

- Hover effects are gated behind `@media (hover: hover) and (pointer: fine)` in product (touch shows chrome on long-press instead).
- Keyboard focus shows the same poster chrome as hover, but the focus ring appears **instantly** (never animated).
- **Reduced motion:** no lift and no scale; colour changes and chrome fades stay.

## 5. Toast and Undo

- Reversible destructive actions (unmonitor, remove from library without deleting files, blocklist, clear queue item) are applied **optimistically** and show a toast with **Undo** for 8 s. Deleting files on disk always uses a confirmation dialog instead (and still moves files to the recycle bin).
- Enter: opacity 0 → 1 and 16 px rise, `--spring-gentle` 330 ms, from the bottom centre (above the bulk-action bar when present). Stack: max 3, newer on top, older ones shift up with the same spring.
- Timer: a 2 px bar drains linearly over 8 s; it **pauses** on hover and on focus within the toast, and while the window is hidden.
- Exit: opacity + 12 px drop, 150 ms `--ease-exit`. Swipe/drag down dismisses on touch.
- Undo: restores the item (150 ms fade back to full opacity, no re-entrance), toast exits, focus returns to where it was. `⌘Z` / `Ctrl+Z` triggers the most recent Undo while its toast is visible.
- Accessibility: `role="status"` + `aria-live="polite"` (errors: `role="alert"`); the Undo button is reachable with F6 (toast region landmark).
- **Reduced motion:** fade only, 150 ms in and out.

## 6. Skeleton shimmer

- Skeletons reproduce the final layout exactly (poster 2:3, two text lines, table cells at real widths) so content replaces them without layout shift.
- Shimmer: a soft highlight sweeps left → right, 1.6 s `--ease-in-out`, then rests 40 % of the cycle; each item is delayed 60 ms from its neighbour so the wave reads across the row.
- Content arrival: the real item cross-fades over the skeleton in 250 ms `--ease-standard`.
- Show skeletons only after 150 ms of waiting (fast responses never flash a skeleton); once shown, keep them at least 300 ms.
- Interactive search: each indexer's pending results are a group of skeleton rows labelled "Waiting for <indexer>" that are replaced as soon as that indexer answers; a failed indexer collapses its skeletons into a calm inline notice.
- **Reduced motion:** static skeleton (no sweep), cross-fade kept at 150 ms.

## 7. Other overlays (reference)

- Menu / context menu / select: opacity + `scale(0.96 → 1)` from the trigger (`transform-origin` at the trigger side), 200 ms `--ease-out`; exit 150 ms. Opened by keyboard: no animation.
- Tooltip: 150 ms after a 500 ms delay; once one tooltip is open, the next ones open instantly without animation.
- Dialog: scrim fade 200 ms; dialog opacity + 8 px rise + `scale(0.98 → 1)`, 250 ms `--ease-out`, from the centre. Exit 150 ms.
- Command palette: **no animation** (opened hundreds of times a day).
- Theme switch: View Transition cross-fade 200 ms; none under reduced motion.

## Testing

- Playwright: snapshot every screen with `?freeze=1` (loops paused at a fixed frame) and with `?motion=reduce`.
- Slow-motion check during review (DevTools animation panel at 10 %).
- Perf budget: library grid of 10 000 films scrolls at 60 fps with hover active on one card; no `backdrop-filter` element is stacked on another glass element.
