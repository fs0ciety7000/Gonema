package acquire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/indexer"
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
	data, redirect, err := s.get(ctx, link, maxBytes(rel.Protocol))
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
			return payload{}, newError(ErrUpstream, err, "%s sent something that is not a torrent file (%s).", rel.IndexerName, sniffKind(data))
		}
		return payload{data: data, filename: name + ".torrent", infoHash: hash}, nil
	}
	if !looksLikeNZB(data) {
		return payload{}, newError(ErrUpstream, nil, "%s sent something that is not an NZB file (%s).", rel.IndexerName, sniffKind(data))
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

// errTooLarge reports a release file above the size cap.
var errTooLarge = errors.New("file too large")

// fetchError is a failed release download, without the URL.
type fetchError struct {
	status int
	msg    string
}

func (e *fetchError) Error() string { return e.msg }

// get downloads link. When the server redirects to a magnet URI, it is
// returned as redirect instead of being followed.
func (s *Service) get(ctx context.Context, link string, limit int64) (data []byte, redirect string, err error) {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", &fetchError{msg: "the download link is not an http(s) URL"}
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
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, "", &fetchError{msg: "invalid download link"}
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
		switch {
		case errors.As(err, &fe):
			return nil, "", fe
		case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
			return nil, "", &fetchError{msg: "no answer in time"}
		default:
			return nil, "", &fetchError{msg: "the indexer could not be reached"}
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if loc := resp.Header.Get("Location"); resp.StatusCode/100 == 3 && download.IsMagnet(loc) {
		return nil, loc, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", &fetchError{status: resp.StatusCode, msg: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", &fetchError{msg: "the download was interrupted"}
	}
	if int64(len(data)) > limit {
		return nil, "", &fetchError{msg: errTooLarge.Error()}
	}
	return data, "", nil
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
