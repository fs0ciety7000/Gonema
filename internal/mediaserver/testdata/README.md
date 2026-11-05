# Media server fixtures

Handwritten from the official references cited in `../doc.go` (2026-11-05),
not recorded from real servers:

- `plex_sections.json`: shape of `GET /library/sections/all` with
  `Accept: application/json` (developer.plex.tv/pms, "Get all library
  sections" sample: `MediaContainer.Directory[].key/type/title/Location[].path`).
  Paths are the server's view.
- `jellyfin_system_info.json`: subset of `SystemInfo` (`GET /System/Info`,
  Jellyfin OpenAPI 12.4.0).

Kodi answers are built inline in the tests (JSON-RPC 2.0 envelopes from the
xbmc `methods.json` schema).
