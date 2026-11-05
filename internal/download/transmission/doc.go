// Package transmission implements download.Client for Transmission.
//
// Sources: the official RPC specification, docs/rpc-spec.md of
// https://github.com/transmission/transmission at tag 4.0.6 (the protocol used
// here) and on main (fetched 2026-11-05); the tr_stat error codes come from
// libtransmission/transmission.h.
//
// # Protocol
//
// POST {"method", "arguments", "tag"} to /transmission/rpc (default port
// 9091; with a URL base, <urlbase>/rpc). Every request carries
// X-Transmission-Session-Id: a 409 answer gives the current id in the same
// header and the request is sent again (CSRF handshake, spec 2.3.1). HTTP basic
// authentication is used when a user name or password is set; 401 means wrong
// credentials, 403 an address outside rpc-whitelist. Answers carry "result"
// ("success" or an error string) and "arguments".
//
// Transmission 4.1 added a JSON-RPC 2.0 protocol with snake_case names and
// deprecated this one, which every 2.x–4.x release still serves. Moving to
// JSON-RPC 2.0 is left for when the legacy protocol is announced removed.
//
// # Behaviour
//
// torrent-add takes "filename" (http(s) link or magnet) or "metainfo" (base64
// .torrent), "paused", "labels" (the category; RPC version 16, Transmission 3.0,
// older versions ignore it) and, when Config.Directory is set, "download-dir"
// = Directory/category. The reference is "hashString" of torrent-added, or of
// torrent-duplicate when the torrent was already there. Priorities map to
// bandwidthPriority (low -1, normal 0, high and force 1); force also calls
// torrent-start-now. SeedRatio sets seedRatioLimit with seedRatioMode 1.
// Transmission has no seeding-time limit: SeedTime sets the idle seeding limit
// (seedIdleLimit, minutes, seedIdleMode 1), the closest setting it offers.
// Transmission has no category list: Categories returns ErrUnsupported.
//
// Items keeps, when a category is set, the torrents labeled with it or stored
// in its directory.
//
// # Mapping (torrent-get "status", "error")
//
//	0 stopped                 -> completed when nothing is left, else paused
//	1 queued to verify        -> queued
//	2 verifying               -> downloading
//	3 queued to download      -> queued
//	4 downloading             -> downloading
//	5 queued to seed, 6 seeding -> completed
//	error 3 (local error)     -> failed (errorString)
//	error 1, 2 (tracker)      -> warning unless completed (errorString)
//	other status              -> warning
//
// OutputPath is downloadDir joined with the torrent name. Remove maps
// deleteData to delete-local-data.
package transmission
