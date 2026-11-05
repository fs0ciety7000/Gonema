package health

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/metadata/tmdb"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Check IDs.
const (
	CheckMetadata    = "metadata"
	CheckRootFolders = "rootfolders"
	CheckIndexers    = "indexers"
	CheckClients     = "downloadclients"
	CheckDownloads   = "downloads"
	CheckRecycleBin  = "recyclebin"
	CheckDatabase    = "database"
)

// Settings pages the issues link to (paths of the web app).
const (
	linkMetadata    = "/settings/metadata"
	linkRootFolders = "/settings/library/root-folders"
	linkRecycleBin  = "/settings/library/recycle-bin"
	linkIndexers    = "/settings/indexers"
	linkClients     = "/settings/download-clients"
	linkMappings    = "/settings/download-clients/remote-path-mappings"
	linkQueue       = "/queue"
	linkBackups     = "/settings/backups"
	linkSystem      = "/system/status"
)

// MaxClockSkew is the clock difference with TMDB above which the clock check
// speaks up.
const MaxClockSkew = 5 * time.Minute

// fsTimeout bounds a file system call: a dead network mount can hang forever.
// A variable for the tests.
var fsTimeout = 2 * time.Second

var errFSTimeout = errors.New("the file system did not answer within 2 seconds")

// withFSTimeout runs fn in a goroutine and gives up after fsTimeout. A hung
// call leaves its goroutine blocked in the kernel; the buffered channel lets it
// exit if the call ever returns.
func withFSTimeout[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := fn()
		ch <- result{v, err}
	}()
	timer := time.NewTimer(fsTimeout)
	defer timer.Stop()
	var zero T
	select {
	case r := <-ch:
		return r.v, r.err
	case <-timer.C:
		return zero, errFSTimeout
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// ----------------------------------------------------------------- metadata

// MetadataSource gives the metadata provider (library.Service).
type MetadataSource interface {
	Provider() (library.KeyedProvider, error)
}

// serverClock is implemented by providers that record the server time
// (tmdb.Client).
type serverClock interface {
	LastServerClock() (tmdb.ServerClock, bool)
}

// MetadataCheck verifies the TMDB key (missing, rejected, unreachable) and
// compares the server clock with TMDB's Date header. It calls TMDB once per
// run, every 6 hours by default.
func MetadataCheck(src MetadataSource, now func() time.Time) Check {
	return Check{
		ID: CheckMetadata, Interval: 6 * time.Hour, Timeout: time.Minute,
		Triggers: []events.Type{events.SettingsChanged},
		Run: func(ctx context.Context) ([]Issue, error) {
			p, err := src.Provider()
			if errors.Is(err, library.ErrNoProvider) {
				return []Issue{{
					ID: "tmdb.key_missing", Severity: Error, Title: "No TMDB key yet",
					Message: "Gonema gets films, posters and release dates from TMDB and needs your own free key to ask it. " +
						"Until one is set, films can't be added and the ones in your library aren't refreshed.",
					Fix: []string{
						"Create a free account on themoviedb.org and open https://www.themoviedb.org/settings/api.",
						"Copy the API Read Access Token (or the API Key).",
						"Paste it in Settings > Metadata.",
					},
					Link: linkMetadata,
				}}, nil
			}
			if err != nil {
				return nil, err
			}
			verr := p.ValidateKey(ctx)
			var issues []Issue
			switch {
			case verr == nil:
			case errors.Is(verr, metadata.ErrUnauthorized):
				issues = append(issues, Issue{
					ID: "tmdb.key_rejected", Severity: Error, Title: "TMDB rejected the key",
					Message: "TMDB no longer accepts the key Gonema uses, so films can't be added and their details aren't refreshed. " +
						"Your library, downloads and imports carry on as usual.",
					Fix: []string{
						"Check the key on https://www.themoviedb.org/settings/api: it may have been regenerated or revoked.",
						"Paste the current API Read Access Token in Settings > Metadata.",
					},
					Link: linkMetadata,
				})
			case ctx.Err() != nil:
				return nil, ctx.Err()
			default:
				issues = append(issues, Issue{
					ID: "tmdb.unreachable", Severity: Warning, Title: "TMDB is not answering",
					Message: "Gonema couldn't reach TMDB just now. Details and posters already saved still show; adding films " +
						"waits until TMDB answers. Gonema checks again in 6 hours, or when you run the checks. " +
						"The last attempt failed with: " + verr.Error() + ".",
					Fix: []string{
						"Check that the server running Gonema has internet access and can resolve api.themoviedb.org.",
						"If a proxy or firewall filters traffic, allow HTTPS to api.themoviedb.org.",
					},
					Link: linkMetadata,
				})
			}
			if c, ok := p.(serverClock); ok {
				if sc, ok := c.LastServerClock(); ok && now().Sub(sc.Local) < time.Hour {
					if is, bad := clockSkew(sc.Local.Sub(sc.Server)); bad {
						issues = append(issues, is)
					}
				}
			}
			return issues, nil
		},
	}
}

// clockSkew returns the clock issue when skew (local minus TMDB) is beyond
// MaxClockSkew.
func clockSkew(skew time.Duration) (Issue, bool) {
	abs, way := skew, "ahead of"
	if skew < 0 {
		abs, way = -skew, "behind"
	}
	if abs <= MaxClockSkew {
		return Issue{}, false
	}
	return Issue{
		ID: "clock.skew", Severity: Warning, Title: "The server clock is off by " + duration(abs),
		Message: fmt.Sprintf("This server's clock is %s %s TMDB's. Release dates, the minimum age of Usenet posts and "+
			"retry times are computed from it, so some may come too early or too late.", duration(abs), way),
		Fix: []string{
			"Turn on automatic time synchronization (NTP) on the server; on most Linux systems: timedatectl set-ntp true.",
			"In Docker, the container uses the host's clock: fix it on the host.",
			"Run the checks again once the clock is right.",
		},
		Link: linkSystem,
	}, true
}

// ----------------------------------------------------------------- root folders

// RootFolderStore reads the root folders and the sizes of their files
// (*db.Queries).
type RootFolderStore interface {
	ListRootFolders(ctx context.Context) ([]db.RootFolder, error)
	ListFileSizesInRootFolder(ctx context.Context, rootFolderID int64) ([]int64, error)
}

// RootFoldersCheck verifies that root folders exist, are writable and have
// room left (thresholds from settings), every 15 minutes and when folders are
// added or removed.
func RootFoldersCheck(q RootFolderStore, fsys library.FS, settings func(context.Context) Settings) Check {
	return Check{
		ID: CheckRootFolders, Interval: 15 * time.Minute,
		Triggers: []events.Type{events.RootFolderAdded, events.RootFolderDeleted, events.SettingsChanged},
		Run: func(ctx context.Context) ([]Issue, error) {
			rows, err := q.ListRootFolders(ctx)
			if err != nil {
				return nil, fmt.Errorf("list root folders: %w", err)
			}
			if len(rows) == 0 {
				return []Issue{{
					ID: "rootfolder.none", Severity: Error, Title: "No root folder yet",
					Message: "Gonema needs a folder to keep your films in before it can add or import any.",
					Fix:     []string{"Add one in Settings > Library > Root folders, for example /movies (in Docker, a mounted volume)."},
					Link:    linkRootFolders,
				}}, nil
			}
			set := settings(ctx)
			var issues []Issue
			for _, rf := range rows {
				is, err := checkRootFolder(ctx, q, fsys, &set, rf)
				if err != nil {
					return nil, err
				}
				if is != nil {
					issues = append(issues, *is)
				}
			}
			return issues, nil
		},
	}
}

func checkRootFolder(ctx context.Context, q RootFolderStore, fsys library.FS, set *Settings, rf db.RootFolder) (*Issue, error) {
	id := strconv.FormatInt(rf.ID, 10)
	link := linkRootFolders + "/" + id
	fi, err := withFSTimeout(ctx, func() (fs.FileInfo, error) { return fsys.Stat(rf.Path) })
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(err, errFSTimeout):
		return &Issue{
			ID: "rootfolder.unresponsive:" + id, Severity: Warning, Title: rf.Path + " is not responding",
			Message: rf.Path + " didn't answer within 2 seconds, which usually means a stalled network share. " +
				"Imports into it wait; Gonema checks again in 15 minutes.",
			Fix:  []string{"Check the network share or drive behind " + rf.Path + " and remount it if needed."},
			Link: link,
		}, nil
	case errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.IsDir()):
		return &Issue{
			ID: "rootfolder.missing:" + id, Severity: Error, Title: rf.Path + " is missing",
			Message: "Gonema can't find " + rf.Path + " (unmounted drive or missing volume?). Films stored there can't be " +
				"imported, renamed or upgraded until it's back; nothing is removed from your library.",
			Fix: []string{
				"Check that the drive or network share is mounted.",
				"In Docker, check the volume that provides " + rf.Path + ".",
				"If the folder moved for good, add the new location in Settings > Library > Root folders.",
			},
			Link: link,
		}, nil
	case errors.Is(err, fs.ErrPermission):
		return &Issue{
			ID: "rootfolder.unreadable:" + id, Severity: Error, Title: "Gonema can't open " + rf.Path,
			Message: "The folder exists but Gonema isn't allowed to read it, so films there can't be imported or upgraded.",
			Fix:     []string{"Give the user Gonema runs as (PUID and PGID in Docker) read and write access to " + rf.Path + "."},
			Link:    link,
		}, nil
	case err != nil:
		return &Issue{
			ID: "rootfolder.unreadable:" + id, Severity: Error, Title: "Gonema can't open " + rf.Path,
			Message: "Reading " + rf.Path + " failed: " + err.Error() + ". Imports into it wait until it's readable.",
			Fix:     []string{"Check the drive or share behind " + rf.Path + "."},
			Link:    link,
		}, nil
	}
	if _, err := withFSTimeout(ctx, func() (struct{}, error) { return struct{}{}, fsys.CheckWritable(rf.Path) }); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &Issue{
			ID: "rootfolder.readonly:" + id, Severity: Error, Title: "Gonema can't write in " + rf.Path,
			Message: "Gonema can read " + rf.Path + " but not write in it, so imports into it fail. " +
				"Downloads carry on and wait in the queue.",
			Fix: []string{
				"Give the user Gonema runs as (PUID and PGID in Docker) write access to " + rf.Path + ".",
				"Check that the share isn't mounted read-only.",
			},
			Link: link,
		}, nil
	}
	type space struct{ free, total uint64 }
	sp, err := withFSTimeout(ctx, func() (space, error) {
		f, t, err := fsys.DiskSpace(rf.Path)
		return space{f, t}, err
	})
	if err != nil || sp.total == 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil // unknown on this platform or file system: nothing to say
	}
	free, total := int64(min(sp.free, 1<<62)), int64(min(sp.total, 1<<62))
	lowPct := set.LowSpacePercent > 0 && free*100 < int64(set.LowSpacePercent)*total
	lowAbs := set.LowSpaceGB > 0 && free < int64(set.LowSpaceGB)*1_000_000_000
	if !lowPct && !lowAbs {
		return nil, nil
	}
	sizes, err := q.ListFileSizesInRootFolder(ctx, rf.ID)
	if err != nil {
		return nil, fmt.Errorf("list file sizes of root folder %d: %w", rf.ID, err)
	}
	med := median(sizes)
	pct := free * 100 / total
	msg := fmt.Sprintf("%s has %s left (%d%% of the disk).", rf.Path, size(free), pct)
	sev := Warning
	if med > 0 {
		room := int(free / med)
		msg += fmt.Sprintf(" That's room for about %s at the usual size here (%s).", plural(room, "film", "films"), size(med))
		if room == 0 {
			sev = Error
			msg = fmt.Sprintf("%s has %s left, less than a typical film here (%s).", rf.Path, size(free), size(med))
		}
	}
	msg += " Gonema keeps downloading, but imports fail once the disk is full."
	return &Issue{
		ID: "rootfolder.low_space:" + id, Severity: sev, Title: rf.Path + " is running out of space",
		Message: msg,
		Fix: []string{
			"Free up space on that disk, or add another root folder in Settings > Library and move some films there.",
			"Replaced files wait in the recycle bin: review and purge it in Settings > Library > Recycle bin.",
			fmt.Sprintf("The thresholds (%d%% or %d GB) can be changed in Settings > Health.", set.LowSpacePercent, set.LowSpaceGB),
		},
		Link: link,
	}, nil
}

// median returns the median of sorted sizes, 0 for none.
func median(sorted []int64) int64 {
	n := len(sorted)
	switch {
	case n == 0:
		return 0
	case n%2 == 1:
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
