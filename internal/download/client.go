package download

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
)

// Kind names a download client implementation (download_clients.kind).
type Kind string

// Supported kinds.
const (
	KindSABnzbd      Kind = "sabnzbd"
	KindNZBGet       Kind = "nzbget"
	KindQBittorrent  Kind = "qbittorrent"
	KindDeluge       Kind = "deluge"
	KindTransmission Kind = "transmission"
)

// Protocol returns the protocol handled by kind, or "" for an unknown kind.
func (k Kind) Protocol() decision.Protocol {
	switch k {
	case KindSABnzbd, KindNZBGet:
		return decision.Usenet
	case KindQBittorrent, KindDeluge, KindTransmission:
		return decision.Torrent
	}
	return ""
}

// Status is the normalised state of a download (downloads.status). Clients
// only report the states up to StatusWarning; importing, imported and removed
// are set by Gonema itself.
type Status string

// Download statuses, see docs/data-model.md.
const (
	StatusQueued      Status = "queued"
	StatusDownloading Status = "downloading"
	StatusPaused      Status = "paused"
	StatusCompleted   Status = "completed"
	StatusImporting   Status = "importing"
	StatusImported    Status = "imported"
	StatusFailed      Status = "failed"
	StatusWarning     Status = "warning"
	StatusRemoved     Status = "removed"
)

// Priority is the queue priority asked when adding a download.
type Priority string

// Priorities. The zero value means PriorityNormal.
const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityForce  Priority = "force" // start now, ignoring the client's queue limits
)

// Client is a download client. Implementations are safe for concurrent use and
// honour ctx on every call. Errors are *Error values matching one of the
// sentinels of this package; they never contain credentials.
type Client interface {
	// Kind returns the implementation kind.
	Kind() Kind
	// Test checks reachability, URL base, TLS and credentials. Its error is
	// written for the onboarding screen.
	Test(ctx context.Context) error
	// Add sends a release to the client and returns the client's identifier
	// for it (nzo_id, NZBID, info hash), stored as downloads.client_ref.
	Add(ctx context.Context, req AddRequest) (ref string, err error)
	// Items lists the downloads known by the client, in the configured
	// category when one is set.
	Items(ctx context.Context) ([]Item, error)
	// Remove deletes a download from the client. deleteData asks the client to
	// also delete the data it manages; callers pass true only on an explicit
	// user request. Removing an unknown ref returns ErrNotFound.
	Remove(ctx context.Context, ref string, deleteData bool) error
	// Pause and Resume act on one download.
	Pause(ctx context.Context, ref string) error
	Resume(ctx context.Context, ref string) error
	// Categories lists the categories (labels) defined in the client. Clients
	// without categories return ErrUnsupported.
	Categories(ctx context.Context) ([]string, error)
}

// AddRequest describes a release to add. Exactly one of URL and Data is set.
type AddRequest struct {
	Protocol decision.Protocol
	// URL is an http(s) link to the .nzb or .torrent file, or a magnet URI.
	URL string
	// Data is the raw .nzb or .torrent file, named Filename.
	Data     []byte
	Filename string
	// Name is an optional job name (Usenet clients); empty keeps the client's.
	Name string
	// Category overrides the client's configured category when not empty.
	Category string
	Priority Priority
	Paused   bool
	// SeedRatio and SeedTime limit seeding (torrents, when the client supports
	// per-torrent limits). Zero keeps the client's defaults.
	SeedRatio float64
	SeedTime  time.Duration
}

// MaxAddDataBytes bounds AddRequest.Data (a .nzb can be large, a .torrent is small).
const MaxAddDataBytes = 64 << 20

// Validate checks the request for a client of protocol p.
func (r *AddRequest) Validate(p decision.Protocol) error {
	if r.Protocol != p {
		return fmt.Errorf("download: %w: a %s client cannot add a %s release", ErrInvalidRequest, p, orUnknown(string(r.Protocol)))
	}
	hasURL, hasData := r.URL != "", len(r.Data) > 0
	switch {
	case hasURL == hasData:
		return fmt.Errorf("download: %w: set either a URL or the file content", ErrInvalidRequest)
	case hasData && len(r.Data) > MaxAddDataBytes:
		return fmt.Errorf("download: %w: file larger than %d bytes", ErrInvalidRequest, MaxAddDataBytes)
	case hasData && strings.TrimSpace(r.Filename) == "":
		return fmt.Errorf("download: %w: file name missing", ErrInvalidRequest)
	case hasURL:
		if IsMagnet(r.URL) {
			if p != decision.Torrent {
				return fmt.Errorf("download: %w: magnet links are for torrents only", ErrInvalidRequest)
			}
			break
		}
		u, err := url.Parse(r.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("download: %w: the link must be an http(s) URL or a magnet URI", ErrInvalidRequest)
		}
	}
	switch r.Priority {
	case "", PriorityLow, PriorityNormal, PriorityHigh, PriorityForce:
	default:
		return fmt.Errorf("download: %w: unknown priority %q", ErrInvalidRequest, r.Priority)
	}
	if r.SeedRatio < 0 || r.SeedTime < 0 {
		return fmt.Errorf("download: %w: negative seeding limit", ErrInvalidRequest)
	}
	return nil
}

// CategoryOr returns the request category, or def when it is empty.
func (r *AddRequest) CategoryOr(def string) string {
	if r.Category != "" {
		return r.Category
	}
	return def
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// IsMagnet reports whether s is a magnet URI.
func IsMagnet(s string) bool {
	return len(s) > 7 && strings.EqualFold(s[:7], "magnet:")
}

// Item is a download as reported by a client, normalised.
type Item struct {
	Ref      string // client identifier (downloads.client_ref)
	Name     string
	Category string
	Status   Status // queued, downloading, paused, completed, failed or warning
	RawState string // the client's own state, for display and diagnosis
	Message  string // client message explaining Status (failure reason, warning); may be empty

	Progress   float64       // 0..1
	Size       int64         // bytes wanted; 0 if unknown
	Downloaded int64         // bytes done
	Speed      int64         // download rate in bytes/s
	ETA        time.Duration // 0 if unknown or done

	// OutputPath is the path of the data as seen by the client (a remote path:
	// translate it with PathMapper). Empty while the client does not know it.
	OutputPath string
	Ratio      float64 // seeding ratio (torrents)
}

// Clamp01 bounds a progress value to [0, 1]; NaN becomes 0.
func Clamp01(v float64) float64 {
	switch {
	case v != v || v < 0: // NaN or negative
		return 0
	case v > 1:
		return 1
	}
	return v
}
