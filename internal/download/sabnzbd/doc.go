// Package sabnzbd implements download.Client for SABnzbd.
//
// Source: the official API reference, https://sabnzbd.org/wiki/configuration/5.1/api
// (fetched 2026-11-05), and the job states of sabnzbd/constants.py (class
// Status) at https://github.com/sabnzbd/sabnzbd.
//
// # Protocol
//
// Every call is api?mode=…&output=json&apikey=… on the configured base URL
// (default port 8080). The API key travels in the query string of GET calls,
// as documented; it is redacted from every error. Files are uploaded with
// mode=addfile as multipart/form-data, with the API key in the form body. A
// missing or wrong key answers "error: API Key Required/Incorrect" (ErrAuth);
// the NZB key only allows adding and is refused elsewhere, so Test reports it
// as a wrong key.
//
// # Mapping
//
// The client reference is the nzo_id. Items merges the queue and the last
// HistoryLimit history entries, filtered by the configured category.
//
//	queue    Downloading, Fetching                 -> downloading
//	         Queued, Grabbing, Checking, Propagating -> queued
//	         Paused                                -> paused
//	         label ENCRYPTED                       -> warning
//	history  Completed                             -> completed
//	         Failed (fail_message as message)      -> failed
//	         Queued, QuickCheck, Verifying,
//	         Repairing, Fetching, Extracting,
//	         Moving, Running (post-processing)     -> downloading, progress 1
//	         Deleted                               -> failed
//	unknown state                                  -> warning
//
// Priorities: low -1, normal 0, high 1, force 2; a paused add uses -2.
// SABnzbd reports one speed for the whole queue: it is given to the first
// downloading job. OutputPath is the history "storage" (final folder).
//
// Remove deletes the job from the queue and from the history (archive=0).
// With deleteData, del_files=1 asks SABnzbd to delete the incomplete files of
// queued jobs and the files of failed jobs; SABnzbd never deletes the final
// folder of a completed job: the importer handles it.
package sabnzbd
