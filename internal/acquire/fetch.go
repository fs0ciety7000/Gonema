package acquire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/netx"
)

// Limits of release file downloads.
const (
	fetchTimeout    = 60 * time.Second
	maxTorrentBytes = 10 << 20
	maxNZBBytes     = download.MaxAddDataBytes
	maxRedirects    = 5
	sniffBytes      = 64 << 10
)

// payload is what a download client receives for a release: a file, or a
// magnet URI for torrent clients.
type payload struct {
	magnet   string
	data     []byte
	filename string
	infoHash string // torrents, when known
}

// fetchRelease downloads the .nzb or .torrent of a release from the indexer,
// server side: the link (which carries the indexer credential) never reaches
// the browser nor the logs. Errors name the indexer, never the link.
func (s *Service) fetchRelease(ctx context.Context, rel *indexer.Release) (payload, error) {
	link := strings.TrimSpace(rel.DownloadURL)
	magnet := strings.TrimSpace(rel.MagnetURL)
	if rel.Protocol == decision.Torrent {
		if download.IsMagnet(link) {
			magnet, link = link, ""
		}
		if link == "" && magnet != "" {
			return magnetPayload(magnet), nil
		}
	}
	if link == "" {
		return payload{}, newError(ErrUpstream, nil, "%s gave no download link for this release.", rel.IndexerName)
	}
	data, redirect, hidden, err := s.get(s.linkScope(ctx, rel, link), link, maxBytes(rel.Protocol))
	kind := func() string {
		if hidden {
			return "the answer of a local address it redirected to"
		}
		return sniffKind(data)
	}
	switch {
	case err != nil && rel.Protocol == decision.Torrent && magnet != "":
		// The .torrent is unavailable but the indexer also gave a magnet.
		return magnetPayload(magnet), nil
	case err != nil:
		return payload{}, newError(ErrUpstream, err, "Could not download the release file from %s: %s.", rel.IndexerName, fetchReason(err))
	case redirect != "":
		if rel.Protocol != decision.Torrent {
			return payload{}, newError(ErrUpstream, nil, "%s redirected the NZB download to a magnet link.", rel.IndexerName)
		}
		return magnetPayload(redirect), nil
	}
	name := fileName(rel.Title)
	if rel.Protocol == decision.Torrent {
		hash, err := download.InfoHash(data)
		if err != nil {
			if magnet != "" {
				return magnetPayload(magnet), nil
			}
			return payload{}, newError(ErrUpstream, err, "%s sent something that is not a torrent file (%s).", rel.IndexerName, kind())
		}
		return payload{data: data, filename: name + ".torrent", infoHash: hash}, nil
	}
	if !looksLikeNZB(data) {
		return payload{}, newError(ErrUpstream, nil, "%s sent something that is not an NZB file (%s).", rel.IndexerName, kind())
	}
	return payload{data: data, filename: name + ".nzb"}, nil
}

func magnetPayload(m string) payload {
	p := payload{magnet: m}
	if h, err := download.MagnetInfoHash(m); err == nil {
		p.infoHash = h
	}
	return p
}

func maxBytes(p decision.Protocol) int64 {
	if p == decision.Torrent {
		return maxTorrentBytes
	}
	return maxNZBBytes
}

// linkScope narrows the destinations of a release download to what the
// indexer that gave the link may point to (netx.Scope): public addresses only
// when the indexer itself is public, or the indexer's own local address when
// it is on the LAN. An indexer cannot make Gonema fetch from other LAN hosts,
// directly or through a redirect. Without a known indexer, the host of the
// link stands for it.
func (s *Service) linkScope(ctx context.Context, rel *indexer.Release, link string) context.Context {
	host := ""
	if rel.IndexerID != 0 {
		if row, err := s.st.Read().GetIndexer(ctx, rel.IndexerID); err == nil {
			if u, err := url.Parse(row.BaseUrl); err == nil {
				host = u.Hostname()
			}
		}
	}
	if host == "" {
		if u, err := url.Parse(link); err == nil {
			host = u.Hostname()
		}
	}
	sc, err := netx.ScopeOfHost(ctx, host)
	if err != nil {
		sc = &netx.Scope{} // unresolvable indexer: public destinations only
	}
	return netx.WithScope(ctx, sc)
}

// errTooLarge reports a release file above the size cap.
var errTooLarge = errors.New("file too large")

// fetchError is a failed release download, without the URL.
type fetchError struct {
	status int
	msg    string
}

// Messages that say nothing of the destination: a link to a refused or local
// address must not tell what answers there.
const (
	msgBlocked     = "the link leads to an address Gonema does not download from (see Settings > Security)"
	msgNoBodyLocal = "the link was redirected to a local address that did not provide the file"
)

func (e *fetchError) Error() string { return e.msg }

// get downloads link. When the server redirects to a magnet URI, it is
// returned as redirect instead of being followed. hidden is true when the
// answer came from a local address through a cross-host redirect: callers
// must not describe it.
func (s *Service) get(ctx context.Context, link string, limit int64) (data []byte, redirect string, hidden bool, err error) {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", false, &fetchError{msg: "the download link is not an http(s) URL"}
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	client := *s.http
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if strings.EqualFold(req.URL.Scheme, "magnet") {
			return http.ErrUseLastResponse
		}
		if len(via) >= maxRedirects {
			return &fetchError{msg: "too many redirects"}
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return &fetchError{msg: "redirected to an unsupported link"}
		}
		// Every hop is checked against the scope before any connection
		// (the dialer checks again at connection time).
		return netx.CheckHost(req.Context(), netx.Integration, req.URL.Hostname())
	}
	if err := netx.CheckHost(ctx, netx.Integration, u.Hostname()); errors.Is(err, netx.ErrBlocked) {
		return nil, "", false, &fetchError{msg: msgBlocked}
	}
	var remote atomic.Pointer[netip.Addr] // address of the last connection used
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if ap, err := netip.ParseAddrPort(info.Conn.RemoteAddr().String()); err == nil {
			a := ap.Addr().Unmap()
			remote.Store(&a)
		}
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, "", false, &fetchError{msg: "invalid download link"}
	}
	req.Header.Set("User-Agent", "Gonema/"+s.version+" (+https://github.com/fs0ciety7000/gonema)")
	req.Header.Set("Accept", "application/x-nzb, application/x-bittorrent, application/octet-stream;q=0.9, */*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL: it carries the API key
		}
		var fe *fetchError
		var blocked *netx.BlockedError
		switch {
		case errors.As(err, &fe):
			return nil, "", false, fe
		case errors.As(err, &blocked):
			return nil, "", false, &fetchError{msg: msgBlocked}
		case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
			return nil, "", false, &fetchError{msg: "no answer in time"}
		default:
			return nil, "", false, &fetchError{msg: "the indexer could not be reached"}
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if loc := resp.Header.Get("Location"); resp.StatusCode/100 == 3 && download.IsMagnet(loc) {
		return nil, loc, false, nil
	}
	// A response from a local address reached through a redirect gets no
	// detail (status, content): it would describe a LAN service.
	hide := false
	if a := remote.Load(); a != nil && !netx.IsPublic(*a) && !strings.EqualFold(resp.Request.URL.Host, u.Host) {
		hide = true
	}
	if resp.StatusCode != http.StatusOK {
		if hide {
			return nil, "", true, &fetchError{msg: msgNoBodyLocal}
		}
		return nil, "", false, &fetchError{status: resp.StatusCode, msg: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", false, &fetchError{msg: "the download was interrupted"}
	}
	if int64(len(data)) > limit {
		return nil, "", false, &fetchError{msg: errTooLarge.Error()}
	}
	return data, "", hide, nil
}

func fetchReason(err error) string {
	var fe *fetchError
	if errors.As(err, &fe) {
		switch fe.status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Sprintf("access refused (HTTP %d): check the API key and the account limits", fe.status)
		case http.StatusTooManyRequests:
			return "the download limit of the account is reached (HTTP 429)"
		case http.StatusNotFound, http.StatusGone:
			return fmt.Sprintf("the release is no longer available (HTTP %d)", fe.status)
		}
		return fe.msg
	}
	return "unexpected error"
}

// looksLikeNZB checks the start of an NZB document.
func looksLikeNZB(data []byte) bool {
	head := bytes.ToLower(data[:min(len(data), sniffBytes)])
	return bytes.Contains(head, []byte("<nzb")) && !bytes.Contains(head, []byte("<html"))
}

// sniffKind describes unexpected content for the error message.
func sniffKind(data []byte) string {
	if len(data) == 0 {
		return "an empty answer"
	}
	ct := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(ct, "text/html"):
		return "an HTML page, often an error or a login page"
	case strings.HasPrefix(ct, "text/"):
		return "a text answer"
	}
	return ct
}

// fileName turns a release title into a safe file name.
func fileName(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r < 32, strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), ". ")
	if name == "" {
		name = "release"
	}
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	return name
}
