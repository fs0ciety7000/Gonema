// Package mediaserver keeps media servers (Plex, Jellyfin, Kodi) in step with
// the library: after an import, an upgrade, a rename or a removed file, the
// server rescans the movie folder only.
//
// # Flow
//
// The Service follows the event bus (moviefile.imported, moviefile.renamed,
// moviefile.deleted), resolves the movie folder as Gonema sees it, and asks
// each enabled server whose refresh_on includes the reason. Requests are
// debounced per server and folder (5 s after the last one, 30 s at most), so
// that an upgrade, which recycles a file, imports one and may rename, costs a
// single refresh. A worker pool runs them with retries (backoff, Retry-After);
// a refresh that still fails is recorded on the server (failure_count,
// last_error) and the first failure raises a health issue (health.changed,
// source "mediaServer"); the next success publishes the recovery. Nothing is
// written to history: refreshes are not library changes.
//
// Path mappings translate Gonema's folder into the server's view (another
// container, another machine, or an smb:// source for Kodi). They use the
// semantics of the download clients' remote path mappings (download.PathMapper):
// longest prefix, whole path components, Windows paths compared
// case-insensitively.
//
// # References
//
// Checked against the official documentation on 2026-11-05:
//
//   - Plex Media Server API, https://developer.plex.tv/pms/ — X-Plex-Token
//     header ("Most endpoints require token based authentication, and the token
//     is expected to be sent in the X-Plex-Token header"), JSON with
//     "Accept: application/json"; GET /library/sections/all lists the libraries
//     with their Location paths; "Refresh section" is
//     POST /library/sections/{sectionId}/refresh with the query parameter
//     path ("Restrict refresh to the specified path"). Older servers route the
//     same refresh with GET and list libraries at /library/sections: both are
//     used as fallbacks on 404/405.
//   - Jellyfin OpenAPI 12.4.0, https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json —
//     POST /Library/Media/Updated with MediaUpdateInfoDto
//     {"Updates":[{"Path","UpdateType":"Created|Modified|Deleted"}]} (204);
//     POST /Library/Refresh starts a full scan (administrator); GET /System/Info.
//     Authentication: the "Authorization: MediaBrowser Token=..." header, as
//     parsed by Jellyfin's AuthorizationContext.cs (jellyfin/jellyfin on
//     GitHub); X-Emby-Token is only read when legacy authorization is enabled.
//   - Kodi JSON-RPC, schema methods.json of xbmc/xbmc (xbmc/interfaces/json-rpc/schema;
//     the kodi.wiki pages refuse automated clients): VideoLibrary.Scan
//     {directory, showdialogs}, VideoLibrary.Clean {showdialogs, content,
//     directory}, JSONRPC.Ping returns "pong". HTTP transport: POST /jsonrpc
//     with HTTP basic authentication (Kodi's web server settings).
//
// # Security
//
// URLs are http or https with a host and no user info; redirects are never
// followed; responses are capped (outbound.MaxBodyBytes); tokens and passwords
// are redacted from every error and never logged.
package mediaserver
