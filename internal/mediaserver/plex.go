package mediaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// plex talks to Plex Media Server (see doc.go for the reference).
type plex struct {
	c     *outbound.Client
	base  string
	token string
}

type plexSections struct {
	MediaContainer struct {
		Directory []plexSection `json:"Directory"`
	} `json:"MediaContainer"`
}

type plexSection struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Location []struct {
		Path string `json:"path"`
	} `json:"Location"`
}

func (p *plex) header() http.Header {
	return http.Header{
		"Accept":                   {"application/json"},
		"X-Plex-Token":             {p.token},
		"X-Plex-Product":           {"Gonema"},
		"X-Plex-Client-Identifier": {"gonema"},
	}
}

// sections lists the libraries: GET /library/sections/all (current reference),
// falling back to /library/sections on servers that predate it.
func (p *plex) sections(ctx context.Context) ([]plexSection, error) {
	var resp *outbound.Response
	for _, path := range []string{"/library/sections/all", "/library/sections"} {
		r, err := p.c.Do(ctx, http.MethodGet, p.base+path, p.header(), nil)
		if err != nil {
			return nil, err
		}
		resp = r
		if r.Status != http.StatusNotFound {
			break
		}
	}
	if err := p.c.Check(resp, ""); err != nil {
		return nil, err
	}
	var out plexSections
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, p.c.Fail(outbound.ErrResponse, resp.Status, "the library list is not JSON (is this a Plex Media Server URL?)")
	}
	return out.MediaContainer.Directory, nil
}

func (p *plex) Test(ctx context.Context) error {
	if p.token == "" {
		return p.c.Fail(outbound.ErrAuth, 0, "a Plex token is required")
	}
	_, err := p.sections(ctx)
	return err
}

// section returns the library whose folder contains path (the deepest one
// when libraries are nested), preferring movie libraries.
func (p *plex) section(ctx context.Context, path string) (plexSection, error) {
	secs, err := p.sections(ctx)
	if err != nil {
		return plexSection{}, err
	}
	var (
		best      plexSection
		bestDepth = -1
		bestMovie bool
	)
	for _, s := range secs {
		for _, l := range s.Location {
			if !under(path, l.Path) {
				continue
			}
			depth, movie := len(splitAny(l.Path)), s.Type == "movie"
			if (movie && !bestMovie) || (movie == bestMovie && depth > bestDepth) {
				best, bestDepth, bestMovie = s, depth, movie
			}
		}
	}
	if bestDepth < 0 {
		return plexSection{}, &noLibraryError{service: "Plex", path: path}
	}
	return best, nil
}

// Refresh runs a partial scan of the movie folder (of its parent after a
// deletion, so that Plex notices the folder is gone).
func (p *plex) Refresh(ctx context.Context, req RefreshRequest) error {
	sec, err := p.section(ctx, req.Path)
	if err != nil {
		return err
	}
	target := req.Path
	if req.Reason == ReasonDelete {
		if parent := parentOf(req.Path); parent != "" && inAny(parent, sec) {
			target = parent
		}
	}
	u := p.base + "/library/sections/" + url.PathEscape(sec.Key) + "/refresh?" + url.Values{"path": {target}}.Encode()
	// The current reference documents POST; older servers only route GET.
	resp, err := p.c.Do(ctx, http.MethodPost, u, p.header(), nil)
	if err != nil {
		return err
	}
	if resp.Status == http.StatusNotFound || resp.Status == http.StatusMethodNotAllowed {
		if resp, err = p.c.Do(ctx, http.MethodGet, u, p.header(), nil); err != nil {
			return err
		}
	}
	return p.c.Check(resp, "")
}

func inAny(path string, s plexSection) bool {
	for _, l := range s.Location {
		if under(path, l.Path) {
			return true
		}
	}
	return false
}
