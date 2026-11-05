// Package qbittorrent implements download.Client for qBittorrent (WebUI API v2).
//
// Sources: the official WebUI API documentation,
// https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-(qBittorrent-5.0)
// and WebUI-API-(qBittorrent-4.1) (fetched 2026-11-05), the WebAPI changelog
// (WebAPI_Changelog.md) and the torrent states of
// src/webui/api/serialize/serialize_torrent.cpp in
// https://github.com/qbittorrent/qBittorrent.
//
// # Protocol
//
// POST /api/v2/auth/login (form username, password) answers "Ok." with a
// session cookie (SID); "Fails." or, since WebAPI 2.14, HTTP 401 means wrong
// credentials; HTTP 403 means the address is banned after too many failures.
// The cookie is sent on every call with a Referer/Origin equal to the base URL
// (CSRF protection). An expired session answers 403: the client logs in again
// once (concurrent re-logins are collapsed) and retries the call.
//
// WebAPI 2.11 (qBittorrent 5.0) renamed torrents/pause and torrents/resume to
// torrents/stop and torrents/start, the add parameter "paused" to "stopped" and
// the states pausedUP/pausedDL to stoppedUP/stoppedDL. The client reads
// /api/v2/app/webapiVersion once per session and picks the endpoint; add sends
// both "paused" and "stopped"; both state names are mapped.
//
// torrents/add takes "urls" (http(s) links or magnets) or "torrents" (file
// parts), category, ratioLimit and seedingTimeLimit (minutes). The reference is
// the lowercase v1 info hash: computed locally from the .torrent bytes or the
// magnet URI, else read from "added_torrent_ids" (WebAPI 2.14+). Adding an
// http(s) .torrent link to an older qBittorrent returns ErrUnknownRef: Gonema
// fetches .torrent files itself and adds them as bytes.
//
// # Mapping
//
//	error, missingFiles                              -> failed
//	uploading, stalledUP, queuedUP, forcedUP,
//	checkingUP, pausedUP, stoppedUP                  -> completed
//	downloading, forcedDL, metaDL, forcedMetaDL,
//	allocating, checkingDL, moving                   -> downloading
//	queuedDL, checkingResumeData                     -> queued
//	pausedDL, stoppedDL                              -> paused
//	stalledDL                                        -> warning (no peers)
//	unknown and any other value                      -> warning
//
// OutputPath is content_path (root folder or single file), falling back to
// save_path. ETA 8640000 (qBittorrent's "infinity") is reported as 0.
// Remove maps deleteData to deleteFiles. PriorityForce sets "force start"
// (torrents/setForceStart) after the add; other priorities are not sent
// (qBittorrent queue priorities are positions, not levels).
package qbittorrent
