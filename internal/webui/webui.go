// Package webui serves the single-page web interface embedded in the binary.
//
// The SPA is built by `make web` (web/ with Vite) and copied to
// internal/webui/dist, which is embedded. A binary built without it serves a
// placeholder page instead, so `go build` always works.
//
// Serving rules:
//   - files under assets/ have hashed names: cached for a year, immutable;
//   - index.html is never cached (no-cache + ETag) and is served for every
//     path without a file extension (client-side routes);
//   - a missing file with an extension is a 404, not the SPA;
//   - precompressed siblings (.br, .gz) are served when the client accepts them;
//   - index.html gets <base href> and window.__GONEMA__ = {baseUrl} injected,
//     so the SPA works under GONEMA_BASE_URL;
//   - every response carries a strict Content-Security-Policy (inline scripts
//     of index.html are allowed by hash) and the usual hardening headers.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

//go:embed placeholder.html
var placeholder []byte

// Options configures the handler.
type Options struct {
	// BaseURL is the URL path prefix ("" or "/gonema"), without trailing slash.
	BaseURL string
	// FS holds the built SPA (index.html at its root); nil = the embedded build.
	FS fs.FS
}

// Config is injected into index.html as window.__GONEMA__.
type Config struct {
	BaseURL string `json:"baseUrl"`
}

type variant struct {
	data []byte
	etag string
}

type asset struct {
	plain   variant
	br, gz  *variant
	ctype   string
	caching string
}

// Handler serves the SPA. It is safe for concurrent use.
type Handler struct {
	assets map[string]*asset // by slash path without leading "/"
	index  *asset
	csp    string
	built  bool
}

// Cache-Control values.
const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheShort     = "public, max-age=3600"
	cacheNone      = "no-cache"
)

// New loads the SPA files and prepares index.html.
func New(opts Options) (*Handler, error) {
	fsys := opts.FS
	if fsys == nil {
		sub, err := fs.Sub(embedded, "dist")
		if err != nil {
			return nil, fmt.Errorf("webui: embedded files: %w", err)
		}
		fsys = sub
	}
	h := &Handler{assets: make(map[string]*asset)}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(path.Base(p), ".") {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		h.add(p, data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("webui: read files: %w", err)
	}
	h.link()

	raw := placeholder
	if idx, ok := h.assets["index.html"]; ok {
		raw, h.built = idx.plain.data, true
	}
	page, hashes, err := injectConfig(raw, opts.BaseURL)
	if err != nil {
		return nil, err
	}
	h.index = &asset{plain: newVariant(page), ctype: "text/html; charset=utf-8", caching: cacheNone}
	h.assets["index.html"] = h.index
	h.csp = csp(hashes)
	return h, nil
}

// Built reports whether a real SPA build is embedded (not the placeholder).
func (h *Handler) Built() bool { return h.built }

func newVariant(data []byte) variant {
	sum := sha256.Sum256(data)
	return variant{data: data, etag: `"` + hex.EncodeToString(sum[:12]) + `"`}
}

// add records a file; .br and .gz files are attached to their original by link.
func (h *Handler) add(p string, data []byte) {
	ctype := mime.TypeByExtension(path.Ext(p))
	switch path.Ext(p) {
	case ".webmanifest":
		ctype = "application/manifest+json"
	case ".woff2":
		ctype = "font/woff2"
	case "":
		ctype = "application/octet-stream"
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	caching := cacheShort
	if strings.HasPrefix(p, "assets/") {
		caching = cacheImmutable
	}
	h.assets[p] = &asset{plain: newVariant(data), ctype: ctype, caching: caching}
}

func (h *Handler) link() {
	for p, a := range h.assets {
		for _, enc := range []string{".br", ".gz"} {
			orig, ok := strings.CutSuffix(p, enc)
			if !ok {
				continue
			}
			if o, ok := h.assets[orig]; ok {
				v := a.plain
				v.etag = strings.TrimSuffix(v.etag, `"`) + strings.TrimPrefix(enc, ".") + `"`
				if enc == ".br" {
					o.br = &v
				} else {
					o.gz = &v
				}
			}
		}
	}
}

// injectConfig adds <base href> and the runtime configuration at the start of
// <head>, and returns the CSP hashes of every inline script of the page.
func injectConfig(page []byte, baseURL string) (out []byte, scriptHashes []string, err error) {
	cfg, err := json.Marshal(Config{BaseURL: baseURL}) // escapes <, > and &: safe inside <script>
	if err != nil {
		return nil, nil, fmt.Errorf("webui: encode config: %w", err)
	}
	// The base URL is validated by package config (letters, digits, - _ . /);
	// it is escaped anyway.
	inject := `<base href="` + html.EscapeString(baseURL+"/") + `"><script>window.__GONEMA__=` + string(cfg) + `;</script>`
	lower := bytes.ToLower(page)
	i := bytes.Index(lower, []byte("<head"))
	if i < 0 {
		return nil, nil, errors.New("webui: index.html has no <head>")
	}
	end := bytes.IndexByte(page[i:], '>')
	if end < 0 {
		return nil, nil, errors.New("webui: index.html has an unterminated <head>")
	}
	at := i + end + 1
	out = make([]byte, 0, len(page)+len(inject))
	out = append(out, page[:at]...)
	out = append(out, inject...)
	out = append(out, page[at:]...)
	return out, inlineScriptHashes(out), nil
}

// inlineScriptHashes returns 'sha256-...' sources for the inline <script>
// elements of page (those without a src attribute).
func inlineScriptHashes(page []byte) []string {
	var hashes []string
	lower := bytes.ToLower(page)
	for off := 0; ; {
		i := bytes.Index(lower[off:], []byte("<script"))
		if i < 0 {
			return hashes
		}
		start := off + i
		gt := bytes.IndexByte(page[start:], '>')
		if gt < 0 {
			return hashes
		}
		tag := lower[start : start+gt]
		body := start + gt + 1
		closeAt := bytes.Index(lower[body:], []byte("</script"))
		if closeAt < 0 {
			return hashes
		}
		if !bytes.Contains(tag, []byte(" src=")) {
			sum := sha256.Sum256(page[body : body+closeAt])
			hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
		}
		off = body + closeAt
	}
}

// csp is the Content-Security-Policy of the UI. Images, fonts and API calls
// are same-origin (TMDB images are proxied by /api/v1/images); trailers are
// embedded from the privacy-enhanced YouTube domain only.
func csp(scriptHashes []string) string {
	script := "'self'"
	if len(scriptHashes) > 0 {
		script += " " + strings.Join(scriptHashes, " ")
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + script,
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self'",
		"connect-src 'self'",
		"media-src 'self'",
		"manifest-src 'self'",
		"worker-src 'self'",
		"frame-src https://www.youtube-nocookie.com",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

// PermissionsPolicy turns off every powerful browser feature; the trailer
// player (privacy-enhanced YouTube, the only frame-src) keeps fullscreen,
// autoplay, picture-in-picture and encrypted media. The API serves it too.
const PermissionsPolicy = "accelerometer=(), ambient-light-sensor=(), autoplay=(self \"https://www.youtube-nocookie.com\"), " +
	"battery=(), bluetooth=(), browsing-topics=(), camera=(), display-capture=(), " +
	"encrypted-media=(self \"https://www.youtube-nocookie.com\"), fullscreen=(self \"https://www.youtube-nocookie.com\"), " +
	"gamepad=(), geolocation=(), gyroscope=(), hid=(), identity-credentials-get=(), idle-detection=(), " +
	"local-fonts=(), magnetometer=(), microphone=(), midi=(), otp-credentials=(), payment=(), " +
	"picture-in-picture=(self \"https://www.youtube-nocookie.com\"), publickey-credentials-create=(), " +
	"publickey-credentials-get=(), screen-wake-lock=(), serial=(), storage-access=(), usb=(), " +
	"web-share=(), window-management=(), xr-spatial-tracking=()"

func (h *Handler) securityHeaders(hd http.Header) {
	hd.Set("Content-Security-Policy", h.csp)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Cross-Origin-Opener-Policy", "same-origin")
	hd.Set("Permissions-Policy", PermissionsPolicy)
}

// ServeHTTP serves a file of the SPA, or index.html for client-side routes.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	h.securityHeaders(hd)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hd.Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	p := path.Clean("/" + r.URL.Path)
	if p == "/api" || strings.HasPrefix(p, "/api/") || p == "/health" {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(p, "/")
	a, ok := h.assets[name]
	switch {
	case ok && !strings.HasSuffix(name, ".br") && !strings.HasSuffix(name, ".gz"):
	case name == "" || !strings.Contains(path.Base(name), "."):
		a = h.index // client-side route
	default:
		http.NotFound(w, r)
		return
	}

	v := &a.plain
	if a.br != nil || a.gz != nil {
		hd.Add("Vary", "Accept-Encoding")
		ae := r.Header.Get("Accept-Encoding")
		switch {
		case a.br != nil && accepts(ae, "br"):
			v = a.br
			hd.Set("Content-Encoding", "br")
		case a.gz != nil && accepts(ae, "gzip"):
			v = a.gz
			hd.Set("Content-Encoding", "gzip")
		}
	}
	hd.Set("Content-Type", a.ctype)
	hd.Set("Cache-Control", a.caching)
	hd.Set("ETag", v.etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(v.data))
}

// accepts reports whether an Accept-Encoding header allows enc (q=0 refuses).
func accepts(header, enc string) bool {
	for part := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), enc) {
			continue
		}
		q := strings.ReplaceAll(params, " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}
