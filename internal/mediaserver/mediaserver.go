package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// Kind names a media server implementation (media_servers.kind).
type Kind string

// Kinds.
const (
	Plex     Kind = "plex"
	Jellyfin Kind = "jellyfin"
	Kodi     Kind = "kodi"
)

// Kinds lists the supported kinds.
var Kinds = []Kind{Plex, Jellyfin, Kodi}

// Valid reports whether k is supported.
func (k Kind) Valid() bool { return k == Plex || k == Jellyfin || k == Kodi }

// Label is the product name shown to the user.
func (k Kind) Label() string {
	switch k {
	case Plex:
		return "Plex"
	case Jellyfin:
		return "Jellyfin"
	case Kodi:
		return "Kodi"
	}
	return string(k)
}

// Reason is why a folder is refreshed (and a media_servers.refresh_on value).
type Reason string

// Reasons.
const (
	ReasonAdd     Reason = "add"     // a movie file was imported for the first time
	ReasonUpgrade Reason = "upgrade" // a better file replaced the previous one
	ReasonRename  Reason = "rename"  // files were renamed
	ReasonDelete  Reason = "delete"  // a file was removed (recycled)
)

// Reasons lists every reason, in display order.
var Reasons = []Reason{ReasonAdd, ReasonUpgrade, ReasonRename, ReasonDelete}

// Valid reports whether r is a known reason.
func (r Reason) Valid() bool {
	return r == ReasonAdd || r == ReasonUpgrade || r == ReasonRename || r == ReasonDelete
}

// merge combines two reasons for one folder within the debounce window: an
// addition stays an addition; a deletion followed by anything else is a
// replacement (upgrade); upgrade wins over rename.
func merge(a, b Reason) Reason {
	switch {
	case a == "":
		return b
	case a == b:
		return a
	case a == ReasonAdd || b == ReasonAdd:
		return ReasonAdd
	case a == ReasonDelete || b == ReasonDelete, a == ReasonUpgrade || b == ReasonUpgrade:
		return ReasonUpgrade
	}
	return ReasonRename
}

// RefreshRequest asks a server to rescan one movie folder.
type RefreshRequest struct {
	// Path is the movie folder as Gonema sees it; the server's path mappings
	// translate it into the server's view.
	Path   string
	Reason Reason
}

// Server is a media server Gonema keeps up to date.
type Server interface {
	// Test checks the address and the credentials.
	Test(ctx context.Context) error
	// Refresh asks the server to rescan a movie folder.
	Refresh(ctx context.Context, req RefreshRequest) error
}

// ServerPathMapping translates a folder seen by Gonema into the same folder seen by the
// media server (another container, another machine, smb:// for Kodi).
type ServerPathMapping struct {
	From string `json:"from" doc:"Folder as Gonema sees it, e.g. /data/movies"`
	To   string `json:"to" doc:"Same folder as the media server sees it, e.g. /media/movies or smb://nas/movies"`
}

// Config is the configuration of one server.
type Config struct {
	Kind     Kind
	BaseURL  string
	Token    string // Plex and Jellyfin
	Username string // Kodi
	Password string // Kodi
	Mappings []ServerPathMapping
	// CleanOnDelete (Kodi): run VideoLibrary.Clean on the folder after a deletion.
	CleanOnDelete bool
}

// Options are the shared dependencies of the implementations.
type Options struct {
	HTTP      *http.Client // default outbound.NewHTTPClient()
	UserAgent string       // default "Gonema"
	Timeout   time.Duration
	Version   string // Gonema version, sent by the Jellyfin client header
}

// ErrNoLibrary: the server has no library folder containing the movie folder
// (path mappings missing or wrong).
var ErrNoLibrary = errors.New("mediaserver: no library contains the folder")

type noLibraryError struct{ service, path string }

func (e *noLibraryError) Error() string {
	return fmt.Sprintf("%s has no library folder containing %s: add a path mapping from Gonema's folder to the server's", e.service, e.path)
}

func (e *noLibraryError) Is(target error) bool { return target == ErrNoLibrary }

// New returns the implementation of cfg.Kind. The URL is validated; secrets are
// registered for redaction in every error.
func New(cfg Config, opts Options) (Server, error) {
	if !cfg.Kind.Valid() {
		return nil, fmt.Errorf("mediaserver: unknown kind %q", cfg.Kind)
	}
	base, err := outbound.ValidateURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	base.Path = strings.TrimRight(base.Path, "/")
	base.RawQuery = ""
	red := &outbound.Redactor{}
	red.Add(cfg.Token, cfg.Password)
	hc := opts.HTTP
	if hc == nil {
		hc = outbound.NewHTTPClient()
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = "Gonema"
	}
	c := &outbound.Client{Service: cfg.Kind.Label(), HTTP: hc, Timeout: opts.Timeout, UserAgent: ua, Redactor: red}
	var inner Server
	switch cfg.Kind {
	case Plex:
		inner = &plex{c: c, base: base.String(), token: cfg.Token}
	case Jellyfin:
		inner = &jellyfin{c: c, base: base.String(), token: cfg.Token, version: opts.Version}
	case Kodi:
		inner = &kodi{c: c, endpoint: base.String() + "/jsonrpc", user: cfg.Username, pass: cfg.Password, clean: cfg.CleanOnDelete}
	}
	return &mapped{inner: inner, mapper: newMapper(cfg.Mappings)}, nil
}

// mapped applies the path mappings before the implementation sees the path.
type mapped struct {
	inner  Server
	mapper *download.PathMapper
}

func (m *mapped) Test(ctx context.Context) error { return m.inner.Test(ctx) }

func (m *mapped) Refresh(ctx context.Context, req RefreshRequest) error {
	req.Path = MapPath(m.mapper, req.Path)
	return m.inner.Refresh(ctx, req)
}

// mapHost is the single host key of the mapper: media server mappings have no
// host dimension.
const mapHost = "server"

func newMapper(ms []ServerPathMapping) *download.PathMapper {
	pm := make([]download.PathMapping, 0, len(ms))
	for _, m := range ms {
		pm = append(pm, download.PathMapping{Host: mapHost, RemotePath: m.From, LocalPath: m.To})
	}
	return download.NewPathMapper(pm)
}

// MapPath translates a Gonema path with the mapper (longest prefix, whole path
// components, same semantics as the remote path mappings of download clients).
// A path no mapping covers is returned unchanged.
func MapPath(m *download.PathMapper, p string) string {
	out, _ := m.Map(mapHost, p)
	return out
}

// ------------------------------------------------------------------ paths

// splitAny splits a path on both separators, dropping empty components. A
// URL-like path ("smb://nas/share") keeps its scheme as the first component.
func splitAny(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
}

// windowsLike reports a drive or UNC path, compared case-insensitively.
func windowsLike(p string) bool {
	return (len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))) ||
		strings.HasPrefix(p, `\\`)
}

// under reports whether child is parent or inside it (whole components).
func under(child, parent string) bool {
	c, p := splitAny(child), splitAny(parent)
	if len(p) == 0 || len(p) > len(c) {
		return false
	}
	fold := windowsLike(parent) || windowsLike(child)
	for i := range p {
		if fold {
			if !strings.EqualFold(p[i], c[i]) {
				return false
			}
		} else if p[i] != c[i] {
			return false
		}
	}
	return true
}

// sepOf returns the separator a path uses.
func sepOf(p string) string {
	if windowsLike(p) && !strings.Contains(p, "/") {
		return `\`
	}
	return "/"
}

// parentOf returns the parent folder of p in its own style ("" for a root).
func parentOf(p string) string {
	trimmed := strings.TrimRight(p, `/\`)
	i := strings.LastIndexAny(trimmed, `/\`)
	if i <= 0 {
		return ""
	}
	parent := trimmed[:i]
	if strings.HasSuffix(parent, ":") || strings.HasSuffix(parent, ":/") || parent == `\` {
		return "" // "C:" or "smb:/": no usable parent
	}
	return parent
}

// withTrailingSep returns p ending with its separator (Kodi wants directories
// that way).
func withTrailingSep(p string) string {
	if strings.HasSuffix(p, "/") || strings.HasSuffix(p, `\`) {
		return p
	}
	return p + sepOf(p)
}
