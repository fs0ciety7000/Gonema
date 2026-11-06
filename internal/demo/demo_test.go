package demo

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/store"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func TestSeedCreatesAFlaggedDemoLibrary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	res, err := Seed(ctx, Options{DataDir: dir, Films: 300, Seed: 7, Now: now, Prerender: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Films != 300 || res.WithFile == 0 || res.Downloading == 0 || res.Posters != 300 {
		t.Errorf("result = %+v", res)
	}
	st, err := store.Open(ctx, filepath.Join(dir, DBFile))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, err := store.GetSetting[bool](ctx, st, library.SettingDemo); err != nil || !v {
		t.Fatalf("demo setting = %v, %v", v, err)
	}
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(dir, DBFile)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var total, onDisk, downloading, missing, unmonitored int64
	for _, c := range []struct {
		n *int64
		q string
	}{
		{&total, "SELECT count(*) FROM movies"},
		{&onDisk, "SELECT count(*) FROM movie_files"},
		{&downloading, "SELECT count(*) FROM movie_state WHERE state = 'downloading'"},
		{&missing, "SELECT count(*) FROM movie_state WHERE state = 'missing'"},
		{&unmonitored, "SELECT count(*) FROM movies WHERE monitored = 0"},
	} {
		if err := conn.QueryRowContext(ctx, c.q).Scan(c.n); err != nil {
			t.Fatal(err)
		}
	}
	if total != 300 || onDisk != int64(res.WithFile) || downloading != int64(res.Downloading) || missing == 0 || unmonitored == 0 {
		t.Errorf("counts %d %d %d %d %d (result %+v)", total, onDisk, downloading, missing, unmonitored, res)
	}
	// The same seed gives the same library.
	again := t.TempDir()
	res2, err := Seed(ctx, Options{DataDir: again, Films: 300, Seed: 7, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if res2.WithFile != res.WithFile || res2.Downloading != res.Downloading || res2.Failed != res.Failed {
		t.Errorf("not deterministic: %+v vs %+v", res, res2)
	}
	// A poster is a JPEG of the w342 size.
	f, err := os.ReadFile(filepath.Join(dir, "cache", "images", "poster", "w342", "demo-p-000001.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(f))
	if err != nil || cfg.Width != 342 || cfg.Height != 513 {
		t.Errorf("poster = %+v, %v", cfg, err)
	}

	// Other sizes are rendered on first request, without network.
	cache, err := imagecache.New(filepath.Join(dir, "cache"), noBase{}, imagecache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	im := NewImages(cache, st)
	p, err := im.Fetch(ctx, imagecache.Backdrop, "w1280", "/demo-b-000002.jpg")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != 1280 || cfg.Height != 720 {
		t.Errorf("backdrop = %+v, %v", cfg, err)
	}
	if _, err := im.Fetch(ctx, imagecache.Poster, "w780", "/unknown.jpg"); !errors.Is(err, imagecache.ErrNotFound) {
		t.Errorf("unknown image: %v", err)
	}
	if _, err := im.Fetch(ctx, imagecache.Poster, "w1280", "/demo-p-000001.jpg"); !errors.Is(err, imagecache.ErrInvalidPath) {
		t.Errorf("bad size: %v", err)
	}

	// Seeding again into the same (now populated) demo directory is refused.
	if _, err := Seed(ctx, Options{DataDir: dir, Films: 10, Now: now}); !errors.Is(err, ErrAlreadySeeded) {
		t.Errorf("reseed: %v", err)
	}
}

func TestSeedRefusesARealDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, DBFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, DBFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(ctx, Options{DataDir: dir, Films: 10, Now: now}); !errors.Is(err, ErrNotDemo) {
		t.Fatalf("Seed on a real database: %v, want ErrNotDemo", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, DBFile))
	if !bytes.Equal(before, after) {
		t.Error("the real database was modified")
	}
	// Not even a SQLite file: refused as well.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, DBFile), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDataDir(ctx, other); !errors.Is(err, ErrNotDemo) {
		t.Errorf("garbage file: %v", err)
	}
	if _, err := Seed(ctx, Options{DataDir: t.TempDir(), Films: 0}); err == nil {
		t.Error("0 films accepted")
	}
}

func TestRender(t *testing.T) {
	for _, tc := range []struct{ w, h int }{{342, 513}, {780, 1170}, {1280, 720}, {185, 278}} {
		data, err := Render(Art{Title: "The Quiet Meridian: Part Two", Year: 2024, Hue: 20}, tc.w, tc.h)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != tc.w || cfg.Height != tc.h {
			t.Errorf("%dx%d: %+v, %v", tc.w, tc.h, cfg, err)
		}
	}
	if c := DominantColor(20); len(c) != 7 || c[0] != '#' {
		t.Errorf("DominantColor = %q", c)
	}
	if h := hueOf(DominantColor(200)); h < 195 || h > 205 {
		t.Errorf("hueOf round trip = %v", h)
	}
}
