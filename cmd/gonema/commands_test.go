package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/store"
)

func TestRunOpenAPI(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"openapi"}, &out, &errOut, func(string) string { return "" }); code != 0 {
		t.Fatalf("openapi = %d: %s", code, errOut.String())
	}
	var doc struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/movies", "/movies/ids", "/movies/{id}", "/views", "/views/{id}", "/library/stats",
		"/queue/summary", "/images/{kind}/{size}/{file}", "/queue", "/import/settings", "/notifications", "/health/issues"} {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("path %s missing", p)
		}
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Errorf("openapi = %q", doc.OpenAPI)
	}
}

func TestRunDemoSeed(t *testing.T) {
	getenv := func(string) string { return "" }
	var out, errOut bytes.Buffer
	dir := filepath.Join(t.TempDir(), "demo")
	if code := run(context.Background(), []string{"demo", "seed", "-films", "25", "-data-dir", dir}, &out, &errOut, getenv); code != 0 {
		t.Fatalf("demo seed = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "25 films") {
		t.Errorf("output = %q", out.String())
	}
	// A real database is refused and left alone.
	realDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(realDir, "gonema.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	before, _ := os.ReadFile(filepath.Join(realDir, "gonema.db"))
	errOut.Reset()
	if code := run(context.Background(), []string{"demo", "seed", "-data-dir", realDir}, &out, &errOut, getenv); code != 1 ||
		!strings.Contains(errOut.String(), "non-demo") || !strings.Contains(errOut.String(), "nothing was changed") {
		t.Errorf("real db = %d %q", code, errOut.String())
	}
	after, _ := os.ReadFile(filepath.Join(realDir, "gonema.db"))
	if !bytes.Equal(before, after) {
		t.Error("the real database changed")
	}
	if code := run(context.Background(), []string{"demo", "seed"}, &out, &errOut, getenv); code != 2 {
		t.Errorf("no data dir = %d", code)
	}
	if code := run(context.Background(), []string{"demo"}, &out, &errOut, getenv); code != 2 {
		t.Errorf("no subcommand = %d", code)
	}
}
