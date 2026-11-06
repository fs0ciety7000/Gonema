package demo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fs0ciety7000/gonema/internal/flight"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Images serves the images of a demo library: a cached file is returned as
// is, a missing one is rendered from the movie it belongs to and cached. It
// never uses the network. It implements the image store of the API.
type Images struct {
	cache  *imagecache.Cache
	q      *db.Queries
	flight flight.Group[string]
}

// NewImages returns the demo image store over cache.
func NewImages(cache *imagecache.Cache, st *store.Store) *Images {
	return &Images{cache: cache, q: st.Read()}
}

// Path validates the image and returns its cache path.
func (im *Images) Path(kind imagecache.Kind, size, tmdbPath string) (string, error) {
	return im.cache.Path(kind, size, tmdbPath)
}

// Fetch returns the cached image, rendering it first if needed.
func (im *Images) Fetch(ctx context.Context, kind imagecache.Kind, size, tmdbPath string) (string, error) {
	dst, err := im.cache.Path(kind, size, tmdbPath)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return dst, nil
	}
	p, _, err := im.flight.Do(ctx, dst, func(fctx context.Context) (string, error) {
		owner, err := im.q.FindImageOwner(fctx, tmdbPath)
		if err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return "", fmt.Errorf("%w: %s", imagecache.ErrNotFound, tmdbPath)
			}
			return "", fmt.Errorf("demo: find image owner: %w", err)
		}
		hue := 210.0
		if owner.DominantColor != nil {
			hue = hueOf(*owner.DominantColor)
		}
		return dst, renderTo(im.cache, kind, size, tmdbPath, Art{Title: owner.Title, Year: owner.Year, Hue: hue})
	})
	return p, err
}

// dims are the pixel sizes of the TMDB size names.
func dims(kind imagecache.Kind, size string) (w, h int) {
	w, err := strconv.Atoi(size[1:])
	if err != nil || w <= 0 {
		w = 342
	}
	if kind == imagecache.Backdrop {
		return w, w * 9 / 16
	}
	return w, w * 3 / 2
}

// renderTo renders an image into the cache, atomically.
func renderTo(cache *imagecache.Cache, kind imagecache.Kind, size, tmdbPath string, a Art) (err error) {
	dst, err := cache.Path(kind, size, tmdbPath)
	if err != nil {
		return err
	}
	w, h := dims(kind, size)
	data, err := Render(a, w, h)
	if err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("demo: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("demo: create temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name()) // our own temporary file
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("demo: write image: %w", err)
	}
	if err = tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("demo: chmod image: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("demo: close image: %w", err)
	}
	if err = os.Rename(tmp.Name(), dst); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("demo: rename image: %w", err)
	}
	return nil
}

// hueOf returns the hue (degrees) of a "#rrggbb" color.
func hueOf(hex string) float64 {
	if len(hex) != 7 || hex[0] != '#' {
		return 210
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return 210
	}
	r, g, b := float64(v>>16&0xff)/255, float64(v>>8&0xff)/255, float64(v&0xff)/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	d := mx - mn
	if d == 0 {
		return 0
	}
	var h float64
	switch mx {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h
}
