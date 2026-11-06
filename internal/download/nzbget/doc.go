// Package nzbget implements download.Client for NZBGet 18 and later.
//
// Sources: the official API reference, https://nzbget.com/documentation/api/
// and its method pages (append, listgroups, history, editqueue, pausedownload,
// version), fetched 2026-11-05; parameter parsing of append checked in
// daemon/remote/XmlRpc.cpp of https://github.com/nzbgetcom/nzbget.
//
// # Protocol
//
// JSON-RPC over HTTP POST to /jsonrpc (default port 6789) with HTTP basic
// authentication (ControlUsername/ControlPassword; the restricted "add" user
// cannot list the queue). Parameters are positional and all mandatory. 64-bit
// sizes come as Hi/Lo 32-bit halves. editqueue uses the v18 signature
// (Command, Param, IDs): Test refuses older versions.
//
// append is sent with 10 parameters (Filename, Content, Category, Priority,
// AddToTop, AddPaused, DupeKey, DupeScore, DupeMode, PPParameters), accepted by
// v16 and later; Content is the URL or the base64 of the .nzb. The reference is
// the NZBID returned by append (decimal string).
//
// # Mapping
//
//	listgroups QUEUED, FETCHING                       -> queued
//	           PAUSED                                 -> paused
//	           DOWNLOADING                            -> downloading
//	           PP_QUEUED, LOADING_PARS, VERIFYING_SOURCES, REPAIRING,
//	           VERIFYING_REPAIRED, RENAMING, UNPACKING, MOVING,
//	           POST_UNPACK_RENAMING, POST_DOWNLOAD_RENAMING,
//	           EXECUTING_SCRIPT, PP_FINISHED          -> downloading (post-processing)
//	history    SUCCESS/*                              -> completed
//	           WARNING/*                              -> warning (status explained in Message)
//	           FAILURE/*, DELETED/*                   -> failed (status explained in Message)
//	unknown state                                     -> warning
//
// Priorities: low -50, normal 0, high 50, force 900. NZBGet reports one
// download rate (method status): it is given to the group with active
// downloads. OutputPath is FinalDir when a script set it, else DestDir.
//
// # Removal
//
// The API has no "delete files" flag: deleteData has no effect. A queued group
// is removed with GroupFinalDelete (no history entry); NZBGet deletes its
// partial files according to its own DeleteCleanupDisk option. A history entry
// is removed with HistoryFinalDelete; NZBGet never deletes the final directory
// of a completed download, the importer handles it.
package nzbget
