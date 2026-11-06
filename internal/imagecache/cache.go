// Package imagecache downloads TMDB images (posters, backdrops, profiles) into a
// local cache and extracts the dominant color of posters.
//
// Files live in <cacheDir>/images/<kind>/<size>/<file>. TMDB image paths are
// content-addressed (a new image gets a new path), so a file that exists is up to
// date: no revalidation (ETag) is needed. Writes are atomic (temporary file in
// the same directory, fsync, rename), concurrent fetches of the same file share
// one download, and responses must be JPEG, PNG or WebP of at most 10 MiB.
package imagecache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register decoders for DecodeConfig and DominantColor
	_ "image/png"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/flight"
	"github.com/fs0ciety7000/gonema/internal/netx"
)

// Kind is the kind of image.
type Kind string

// Kinds of image.
const (
	Poster   Kind = "poster"
	Backdrop Kind = "backdrop"
	Profile  Kind = "profile"
)

// Sizes kept for each kind (TMDB size names).
var sizes = map[Kind][]string{
	Poster:   {"w342", "w780"},
	Backdrop: {"w1280"},
	Profile:  {"w185"},
}

// Sizes returns the sizes cached for kind.
func Sizes(kind Kind) []string { return append([]string(nil), sizes[kind]...) }

// ColorSize is the poster size DominantColor is computed from.
const ColorSize = "w342"

// Errors, matched with errors.Is.
var (
	ErrInvalidPath = errors.New("imagecache: invalid image path, kind or size")
	ErrNotFound    = errors.New("imagecache: image not found")
	ErrNotImage    = errors.New("imagecache: response is not a supported image")
	ErrTooLarge    = errors.New("imagecache: image too large")
)

// BaseURLSource gives the image base URL ("https://image.tmdb.org/t/p/"); the
// TMDB client implements it from /configuration.
type BaseURLSource interface {
	ImageBaseURL(ctx context.Context) (string, error)
}

// Options configures a Cache. Zero values select defaults.
type Options struct {
	HTTPClient *http.Client  // default: a netx client, public addresses only (a timeout applies per download)
	Timeout    time.Duration // per download, default 30 s
	MaxBytes   int64         // default 10 MiB
	MaxPixels  int           // decoded size limit, default 40 megapixels
	UserAgent  string        // default "Gonema/dev (+https://github.com/fs0ciety7000/gonema)"
}

// Cache is a local image cache. It is safe for concurrent use.
type Cache struct {
	root      string
	base      BaseURLSource
	http      *http.Client
	timeout   time.Duration
	maxBytes  int64
	maxPixels int
	ua        string
	flight    flight.Group[string]
}

// New returns a cache storing files under cacheDir/images.
func New(cacheDir string, base BaseURLSource, opts Options) (*Cache, error) {
	if cacheDir == "" || base == nil {
		return nil, errors.New("imagecache: cache directory and base URL source are required")
	}
	root := filepath.Join(cacheDir, "images")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("imagecache: create %s: %w", root, err)
	}
	c := &Cache{
		root: root, base: base, http: opts.HTTPClient,
		timeout: opts.Timeout, maxBytes: opts.MaxBytes, maxPixels: opts.MaxPixels, ua: opts.UserAgent,
	}
	if c.http == nil {
		// Public addresses only (image.tmdb.org): checked at connection time.
		c.http = netx.NewClient(netx.Public)
	}
	if c.timeout <= 0 {
		c.timeout = 30 * time.Second
	}
	if c.maxBytes <= 0 {
		c.maxBytes = 10 << 20
	}
	if c.maxPixels <= 0 {
		c.maxPixels = 40_000_000
	}
	if c.ua == "" {
		c.ua = "Gonema/dev (+https://github.com/fs0ciety7000/gonema)"
	}
	return c, nil
}

// Path returns the local path of an image, validating kind, size and the TMDB
// path ("/abc123.jpg": one segment of letters, digits, '-' or '_', and a jpg,
// jpeg, png or webp extension). The file may not exist yet.
func (c *Cache) Path(kind Kind, size, tmdbPath string) (string, error) {
	if !validSize(kind, size) || !validFile(tmdbPath) {
		return "", fmt.Errorf("%w: %s %s %q", ErrInvalidPath, kind, size, tmdbPath)
	}
	return filepath.Join(c.root, string(kind), size, tmdbPath[1:]), nil
}

func validSize(kind Kind, size string) bool {
	for _, s := range sizes[kind] {
		if s == size {
			return true
		}
	}
	return false
}

func validFile(p string) bool {
	if len(p) < 3 || len(p) > 128 || p[0] != '/' {
		return false
	}
	name := p[1:]
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 {
		return false
	}
	switch name[dot+1:] {
	case "jpg", "jpeg", "png", "webp":
	default:
		return false
	}
	for i := range dot {
		ch := name[i]
		ok := 'a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' || '0' <= ch && ch <= '9' || ch == '-' || ch == '_'
		if !ok {
			return false
		}
	}
	return true
}

// Fetch returns the local path of the image, downloading it if it is not cached.
func (c *Cache) Fetch(ctx context.Context, kind Kind, size, tmdbPath string) (string, error) {
	dst, err := c.Path(kind, size, tmdbPath)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(dst); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
		return dst, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("imagecache: stat %s: %w", dst, err)
	}
	p, _, err := c.flight.Do(ctx, dst, func(fctx context.Context) (string, error) {
		// Another download may have finished between Stat and Do.
		if fi, err := os.Stat(dst); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
			return dst, nil
		}
		return dst, c.download(fctx, size, tmdbPath, dst)
	})
	if err != nil {
		return "", err
	}
	return p, nil
}

func (c *Cache) download(ctx context.Context, size, tmdbPath, dst string) error {
	base, err := c.base.ImageBaseURL(ctx)
	if err != nil {
		return fmt.Errorf("imagecache: image base URL: %w", err)
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("imagecache: image base URL %q must be an https URL", base)
	}
	src := strings.TrimRight(base, "/") + "/" + size + tmdbPath

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, http.NoBody)
	if err != nil {
		return fmt.Errorf("imagecache: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "image/webp,image/png,image/jpeg")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("imagecache: GET %s: %w", src, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, src)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("imagecache: GET %s: HTTP %d", src, resp.StatusCode)
	}
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !supportedType(mt) {
		return fmt.Errorf("%w: content type %q", ErrNotImage, resp.Header.Get("Content-Type"))
	}
	if resp.ContentLength > c.maxBytes {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrTooLarge, resp.ContentLength, c.maxBytes)
	}
	return c.store(resp.Body, dst)
}

func supportedType(mt string) bool {
	switch mt {
	case "image/jpeg", "image/png", "image/webp":
		return true
	}
	return false
}

// store writes r to dst atomically after checking size, magic bytes and, for
// JPEG and PNG, the decoded dimensions.
func (c *Cache) store(r io.Reader, dst string) (err error) {
	data, err := io.ReadAll(io.LimitReader(r, c.maxBytes+1))
	if err != nil {
		return fmt.Errorf("imagecache: read body: %w", err)
	}
	if int64(len(data)) > c.maxBytes {
		return fmt.Errorf("%w: more than %d bytes", ErrTooLarge, c.maxBytes)
	}
	sniffed := http.DetectContentType(data)
	if !supportedType(sniffed) {
		return fmt.Errorf("%w: content is %s", ErrNotImage, sniffed)
	}
	if sniffed != "image/webp" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("%w: %w", ErrNotImage, err)
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > c.maxPixels/max(cfg.Height, 1) {
			return fmt.Errorf("%w: %dx%d pixels", ErrTooLarge, cfg.Width, cfg.Height)
		}
	}

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("imagecache: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("imagecache: create temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name()) // our own temporary file, never user data
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("imagecache: write %s: %w", tmp.Name(), err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("imagecache: sync %s: %w", tmp.Name(), err)
	}
	if err = tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("imagecache: chmod %s: %w", tmp.Name(), err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("imagecache: close %s: %w", tmp.Name(), err)
	}
	if err = os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("imagecache: rename to %s: %w", dst, err)
	}
	return nil
}

// PosterColor returns the dominant color of a poster ("#rrggbb"), fetching its
// w342 version if needed.
func (c *Cache) PosterColor(ctx context.Context, tmdbPath string) (string, error) {
	p, err := c.Fetch(ctx, Poster, ColorSize, tmdbPath)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return "", fmt.Errorf("imagecache: open %s: %w", p, err)
	}
	defer f.Close()
	return c.decodeColor(f)
}

// DominantColorOf decodes a JPEG or PNG image and returns its dominant color.
func DominantColorOf(r io.Reader) (string, error) {
	return (&Cache{maxBytes: 10 << 20, maxPixels: 40_000_000}).decodeColor(r)
}

func (c *Cache) decodeColor(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, c.maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("imagecache: read image: %w", err)
	}
	if int64(len(data)) > c.maxBytes {
		return "", fmt.Errorf("%w: more than %d bytes", ErrTooLarge, c.maxBytes)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > c.maxPixels/max(cfg.Height, 1) {
		return "", fmt.Errorf("%w: %dx%d pixels", ErrTooLarge, cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotImage, err)
	}
	return DominantColor(img), nil
}
