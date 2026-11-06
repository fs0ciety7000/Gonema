// Package deluge implements download.Client for Deluge 2 through its Web UI
// JSON-RPC API.
//
// Sources: the official reference "Deluge Web JSON-RPC API",
// https://deluge.readthedocs.io/en/latest/reference/webapi.html (fetched
// 2026-11-05), and, because the RPC API page renders empty, the exported
// methods of deluge/core/core.py, deluge/ui/web/json_api.py (error codes) and
// the TORRENT_STATE list of deluge/common.py in
// https://github.com/deluge-torrent/deluge (develop branch).
//
// # Protocol
//
// POST /json (default port 8112) with {"method", "params", "id"}; answers
// carry "result" and "error" ({"message", "code"}: 1 not authenticated, 2
// unknown method, 3 and 4 call failures). auth.login(password) sets the
// _session_id cookie. The Web UI must then be connected to a daemon:
// web.connected, else web.get_hosts and web.connect to the first host. Core
// methods only exist once connected (code 2 before). The client logs in and
// connects lazily, and once again when a call answers code 1 or 2.
//
// Add uses core.add_torrent_magnet, core.add_torrent_url or
// core.add_torrent_file (base64) with the options add_paused, stop_at_ratio
// and stop_ratio; Deluge has no per-torrent seeding time limit, SeedTime is
// ignored. High and force priorities move the torrent to the top of the queue
// (core.queue_top). Categories are labels of the optional Label plugin
// (label.get_labels, label.add, label.set_torrent); Deluge labels are
// lowercase. Without the plugin, Categories returns ErrUnsupported and Test
// fails when a category is configured.
//
// # Mapping
//
//	Downloading, Checking, Moving         -> downloading
//	Allocating                            -> queued
//	Queued                                -> queued (completed when is_finished)
//	Paused                                -> paused (completed when is_finished)
//	Seeding                               -> completed
//	Error                                 -> failed (Deluge message)
//	unknown state                         -> warning
//
// The reference is the info hash (torrent_id). OutputPath is the download
// location joined with the torrent name. Remove maps deleteData to
// core.remove_torrent's remove_data.
package deluge
