package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// ImportStuckAfter is how long a download may wait after a failed import
// before the downloads check mentions it.
const ImportStuckAfter = time.Hour

// failingAfter is the number of consecutive failures from which a working
// indexer is mentioned (info): one failure is noise.
const failingAfter = 2

// ----------------------------------------------------------------- indexers

// IndexerSource gives the indexers with their breaker state (acquire.Service).
type IndexerSource interface {
	IndexerStates(ctx context.Context) ([]acquire.IndexerState, error)
}

// IndexersCheck reports a missing indexer, paused or failing indexers, and
// all indexers down. It reads the breaker states (no network access), every 5
// minutes and when an indexer is paused or comes back.
func IndexersCheck(src IndexerSource, now func() time.Time, loc *time.Location) Check {
	if loc == nil {
		loc = time.Local
	}
	return Check{
		ID: CheckIndexers, Interval: 5 * time.Minute,
		Triggers: []events.Type{events.HealthChanged, events.SettingsChanged},
		Run: func(ctx context.Context) ([]Issue, error) {
			states, err := src.IndexerStates(ctx)
			if err != nil {
				return nil, err
			}
			return indexerIssues(states, now(), loc), nil
		},
	}
}

func indexerDown(h *search.Health, now time.Time) bool {
	return h.State != search.Closed && h.DisabledUntil.After(now)
}

func indexerIssues(states []acquire.IndexerState, now time.Time, loc *time.Location) []Issue {
	var enabled []acquire.IndexerState
	for i := range states {
		if states[i].Enabled {
			enabled = append(enabled, states[i])
		}
	}
	if len(enabled) == 0 {
		title, msg := "No indexer is set up", "Gonema searches indexers to find releases."
		if len(states) > 0 {
			title, msg = "Every indexer is turned off", "All your indexers have RSS, automatic and interactive searches turned off."
		}
		return []Issue{{
			ID: "indexer.none", Severity: Error, Title: title,
			Message: msg + " Until one is on, missing films aren't searched and RSS finds nothing.",
			Fix: []string{
				"Add a Newznab (Usenet) or Torznab (torrent) indexer in Settings > Indexers.",
				"Use Test before saving: it tells you if the address or the API key is wrong.",
			},
			Link: linkIndexers,
		}}
	}
	var down []acquire.IndexerState
	for i := range enabled {
		if indexerDown(&enabled[i].Health, now) {
			down = append(down, enabled[i])
		}
	}
	working := len(enabled) - len(down)
	var issues []Issue
	if working == 0 {
		first := slices.MinFunc(down, func(a, b acquire.IndexerState) int { return a.Health.DisabledUntil.Compare(b.Health.DisabledUntil) })
		what := "Your indexer isn't answering"
		if len(down) > 1 {
			what = fmt.Sprintf("None of your %d indexers is answering", len(down))
		}
		issues = append(issues, Issue{
			ID: "indexer.all_down", Severity: Error, Title: "No indexer is answering",
			Message: fmt.Sprintf("%s, so searches and RSS find nothing for now. Gonema tries again as soon as a pause ends "+
				"(the first at %s); downloads already in the queue are not affected.", what, clock(first.Health.DisabledUntil, now, loc)),
			Fix: []string{
				"Check that the server running Gonema has internet access.",
				"Open each indexer in Settings > Indexers and use Test: its answer says what is wrong (address, API key, request limit).",
			},
			Link: linkIndexers,
		})
	}
	for i := range enabled {
		s := &enabled[i]
		h := &s.Health
		id := strconv.FormatInt(s.ID, 10)
		link := linkIndexers + "/" + id
		lastErr := strings.TrimPrefix(h.LastError, s.Name+": ")
		switch {
		case indexerDown(h, now):
			var b strings.Builder
			switch {
			case strings.Contains(h.LastError, indexer.ErrRateLimited.Error()):
				b.WriteString(s.Name + " says its request limit is reached.")
			case !h.LastOKAt.IsZero() && now.Sub(h.LastOKAt) >= time.Minute:
				b.WriteString(s.Name + " hasn't answered for " + duration(now.Sub(h.LastOKAt)) + ".")
			default:
				b.WriteString(s.Name + " failed its last " + plural(max(h.ConsecutiveFailures, 1), "request", "requests") + ".")
			}
			switch {
			case working == 1:
				b.WriteString(" The other indexer keeps searching, so nothing is blocked.")
			case working > 1:
				fmt.Fprintf(&b, " The other %d indexers keep searching, so nothing is blocked.", working)
			}
			fmt.Fprintf(&b, " Gonema paused it until %s and will try again then.", clock(h.DisabledUntil, now, loc))
			if lastErr != "" {
				b.WriteString(" Last error: " + lastErr + ".")
			}
			sev := Warning
			fix := []string{
				"Open " + s.Name + " in Settings > Indexers and use Test to see its answer now.",
				"If the site is down, there's nothing to do: Gonema resumes on its own.",
				"If your API key or the site's address changed, update them there.",
			}
			if strings.Contains(h.LastError, indexer.ErrUnauthorized.Error()) || strings.Contains(h.LastError, indexer.ErrSuspended.Error()) {
				fix = []string{
					"Log in to " + s.Name + "'s website and check your account and API key.",
					"Paste the current API key in Settings > Indexers, then use Test.",
				}
			}
			issues = append(issues, Issue{
				ID: "indexer.unreachable:" + id, Severity: sev, Title: s.Name + " is not answering",
				Message: b.String(), Fix: fix, Link: link, Since: h.LastOKAt,
			})
		case h.ConsecutiveFailures >= failingAfter:
			msg := fmt.Sprintf("%s failed its last %d requests. Gonema keeps using it and pauses it for a while if it keeps failing.",
				s.Name, h.ConsecutiveFailures)
			if lastErr != "" {
				msg += " Last error: " + lastErr + "."
			}
			issues = append(issues, Issue{
				ID: "indexer.failing:" + id, Severity: Info, Title: s.Name + " is having trouble",
				Message: msg,
				Fix:     []string{"If it lasts, open " + s.Name + " in Settings > Indexers and use Test to see its answer."},
				Link:    link,
			})
		}
	}
	return issues
}

// ----------------------------------------------------------------- download clients

// ClientSource tests the download clients (acquire.Service).
type ClientSource interface {
	ProbeClients(ctx context.Context) ([]acquire.ClientState, error)
}

// ClientsCheck reports a missing download client, a client that can't be
// reached, and refused credentials. It tests each enabled client (one light
// request), every 5 minutes and when a client's health changes.
func ClientsCheck(src ClientSource) Check {
	return Check{
		ID: CheckClients, Interval: 5 * time.Minute, Timeout: 45 * time.Second,
		Triggers: []events.Type{events.HealthChanged, events.SettingsChanged},
		Run: func(ctx context.Context) ([]Issue, error) {
			states, err := src.ProbeClients(ctx)
			if err != nil {
				return nil, err
			}
			return clientIssues(states), nil
		},
	}
}

func protocolWord(p string) string {
	if p == "usenet" {
		return "Usenet"
	}
	return "torrent"
}

func clientIssues(states []acquire.ClientState) []Issue {
	var enabled []acquire.ClientState
	for _, c := range states {
		if c.Enabled {
			enabled = append(enabled, c)
		}
	}
	if len(enabled) == 0 {
		title := "No download client is set up"
		if len(states) > 0 {
			title = "Every download client is turned off"
		}
		return []Issue{{
			ID: "client.none", Severity: Error, Title: title,
			Message: "Gonema hands the releases it picks to a download client. Without one, it finds releases but can't download them.",
			Fix: []string{
				"Add your Usenet or torrent client in Settings > Download clients.",
				"Use Test before saving: it tells you if the address or the credentials are wrong.",
			},
			Link: linkClients,
		}}
	}
	var issues []Issue
	for _, c := range enabled {
		if c.Err == nil {
			continue
		}
		id := strconv.FormatInt(c.ID, 10)
		link := linkClients + "/" + id
		if errors.Is(c.Err, download.ErrAuth) {
			issues = append(issues, Issue{
				ID: "client.auth:" + id, Severity: Error, Title: c.Name + " refused the login",
				Message: c.Name + " rejected the user name, password or API key Gonema uses, so new releases can't be sent to it " +
					"and the queue isn't updated. Downloads already running carry on in " + c.Name + ".",
				Fix: []string{
					"Open " + c.Name + " in Settings > Download clients, enter the credentials again, then use Test.",
					"If " + c.Name + " bans addresses after failed logins, lift the ban for the Gonema server in its settings.",
				},
				Link: link,
			})
			continue
		}
		var other string
		for _, o := range enabled {
			if o.ID != c.ID && o.Protocol == c.Protocol && o.Err == nil {
				other = o.Name
				break
			}
		}
		sev, then := Error, "New "+protocolWord(c.Protocol)+" releases can't be sent until it answers."
		if other != "" {
			sev, then = Warning, "New "+protocolWord(c.Protocol)+" releases go to "+other+" meanwhile, so nothing is blocked."
		}
		issues = append(issues, Issue{
			ID: "client.unreachable:" + id, Severity: sev, Title: c.Name + " is not answering",
			Message: fmt.Sprintf("Gonema can't reach %s at %s:%d. %s Downloads already running carry on in %s, and the queue "+
				"updates again as soon as it answers. Last error: %s.", c.Name, c.Host, c.Port, then, c.Name, c.Err.Error()),
			Fix: []string{
				"Check that " + c.Name + " is running and reachable from the Gonema server (same Docker network, firewall).",
				"Open it in Settings > Download clients and use Test to check the host, port and URL base.",
			},
			Link: link,
		})
	}
	return issues
}

// ----------------------------------------------------------------- downloads

// Statter is the file system access of the downloads check (library.FS).
type Statter interface {
	Stat(name string) (fs.FileInfo, error)
}

// DownloadsCheck reports completed downloads whose files Gonema can't see
// (remote path mapping missing or wrong), and downloads waiting more than an
// hour after a failed import. Every 5 minutes and when the queue changes.
func DownloadsCheck(st *store.Store, fsys Statter, now func() time.Time) Check {
	return Check{
		ID: CheckDownloads, Interval: 5 * time.Minute,
		Triggers: []events.Type{acquire.EventDownloadUpdated, acquire.EventDownloadImported, acquire.EventDownloadFailed,
			acquire.EventDownloadRemoved, events.SettingsChanged},
		Run: func(ctx context.Context) ([]Issue, error) {
			return downloadIssues(ctx, st, fsys, now())
		},
	}
}

// waitingDownload is a completed download whose files are not visible.
type waitingDownload struct {
	title, path string
}

func downloadIssues(ctx context.Context, st *store.Store, fsys Statter, now time.Time) ([]Issue, error) {
	var (
		active   []db.Download
		clients  []db.DownloadClient
		mappings []db.RemotePathMapping
		failed   []db.ListFailedImportsRow
	)
	err := st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if active, err = q.ListActiveDownloads(ctx); err != nil {
			return fmt.Errorf("list active downloads: %w", err)
		}
		if clients, err = q.ListDownloadClients(ctx); err != nil {
			return fmt.Errorf("list download clients: %w", err)
		}
		if mappings, err = q.ListRemotePathMappings(ctx); err != nil {
			return fmt.Errorf("list remote path mappings: %w", err)
		}
		if failed, err = q.ListFailedImports(ctx); err != nil {
			return fmt.Errorf("list failed imports: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("health: downloads: %w", err)
	}
	byID := make(map[int64]*db.DownloadClient, len(clients))
	for i := range clients {
		byID[clients[i].ID] = &clients[i]
	}
	missing := make(map[int64]bool)                // download ids whose path is not visible
	perClient := make(map[int64][]waitingDownload) // client id -> downloads
	var order []int64
	for i := range active {
		d := &active[i]
		if d.OutputPath == nil || *d.OutputPath == "" || d.CompletedAt == nil || d.ClientID == nil {
			continue
		}
		switch d.Status {
		case "completed", "importing", "warning":
		default:
			continue
		}
		_, err := withFSTimeout(ctx, func() (fs.FileInfo, error) { return fsys.Stat(*d.OutputPath) })
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		missing[d.ID] = true
		if _, ok := perClient[*d.ClientID]; !ok {
			order = append(order, *d.ClientID)
		}
		perClient[*d.ClientID] = append(perClient[*d.ClientID], waitingDownload{title: d.ReleaseTitle, path: *d.OutputPath})
	}
	var issues []Issue
	for _, cid := range order {
		c := byID[cid]
		if c == nil {
			continue
		}
		issues = append(issues, pathIssue(c, perClient[cid], mappings))
	}
	for _, f := range failed {
		if missing[f.ID] {
			continue // the path issue says it better
		}
		at := store.FromMillis(f.FailedAt)
		if now.Sub(at) < ImportStuckAfter {
			continue
		}
		var msgs []string
		_ = json.Unmarshal([]byte(f.StatusMessages), &msgs)
		why := "the reason is in the queue and the history"
		if len(msgs) > 0 {
			why = strings.TrimSuffix(strings.TrimPrefix(msgs[0], "Import failed: "), ".")
		}
		issues = append(issues, Issue{
			ID: "download.import_stuck:" + strconv.FormatInt(f.ID, 10), Severity: Warning,
			Title: f.ReleaseTitle + " is waiting to be imported",
			Message: fmt.Sprintf("Its import failed %s ago: %s. The download stays in the queue; Gonema doesn't retry on its own, "+
				"so nothing is repeated or lost.", duration(now.Sub(at)), why),
			Fix: []string{
				"Fix the cause above (permissions on the root folder, free space, files the import refused).",
				"Then use Retry import on it in the queue, or import it for another film from there.",
			},
			Link: linkQueue, Since: at,
		})
	}
	return issues, nil
}

// pathIssue explains completed downloads of a client that Gonema can't see,
// and suggests the remote path mapping to add or fix.
func pathIssue(c *db.DownloadClient, dls []waitingDownload, mappings []db.RemotePathMapping) Issue {
	first := dls[0]
	var applied *db.RemotePathMapping
	for i := range mappings {
		m := &mappings[i]
		if strings.EqualFold(m.Host, c.Host) && hasPathPrefix(first.path, m.LocalPath) {
			applied = m
			break
		}
	}
	waiting := ""
	if len(dls) > 1 {
		waiting = fmt.Sprintf(" (%d downloads are waiting)", len(dls))
	}
	msg := fmt.Sprintf("%s finished %s in %s, but that path doesn't exist for Gonema%s, so it can't be imported. ",
		c.Name, first.title, first.path, waiting)
	var fix []string
	if applied != nil {
		msg += fmt.Sprintf("The remote path mapping %s → %s applies, so the local side of it is probably wrong or not mounted.",
			applied.RemotePath, applied.LocalPath)
		fix = []string{
			fmt.Sprintf("Check that %s exists on the Gonema server (in Docker, that it's a mounted volume).", applied.LocalPath),
			"Correct the mapping in Settings > Download clients > Remote path mappings if needed.",
			"Then use Retry import on each waiting download in the queue.",
		}
	} else {
		remote := parentDir(first.path)
		msg += "Gonema and " + c.Name + " probably run in separate containers or machines that see the same folder under different paths."
		fix = []string{
			fmt.Sprintf("In Settings > Download clients > Remote path mappings, add a mapping for host %s with remote path %s "+
				"and, as local path, the same folder as Gonema sees it (for example the volume mounted in Gonema's container).", c.Host, remote),
			"Then use Retry import on each waiting download in the queue.",
		}
	}
	msg += " The downloads are kept until then."
	return Issue{
		ID: "download.path_unreachable:" + strconv.FormatInt(c.ID, 10), Severity: Error,
		Title: "Gonema can't see " + c.Name + "'s finished downloads", Message: msg, Fix: fix, Link: linkMappings,
	}
}

// hasPathPrefix reports whether p is prefix or inside it, on whole path
// components, with either separator.
func hasPathPrefix(p, prefix string) bool {
	norm := func(s string) string { return strings.TrimRight(strings.ReplaceAll(s, `\`, "/"), "/") }
	p, prefix = norm(p), norm(prefix)
	if prefix == "" {
		return true
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// parentDir returns the folder holding p, with a trailing separator, for
// either separator: "/downloads/complete/x" -> "/downloads/complete/".
func parentDir(p string) string {
	sep := "/"
	if strings.Contains(p, `\`) && !strings.Contains(p, "/") {
		sep = `\`
	}
	t := strings.TrimRight(p, sep)
	i := strings.LastIndex(t, sep)
	if i < 0 {
		return p
	}
	return t[:i+1]
}
