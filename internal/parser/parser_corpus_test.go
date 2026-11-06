package parser_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// corpusPath is the golden corpus of real release names (see testdata/releases/README.md).
var corpusPath = filepath.Join("..", "..", "testdata", "releases", "corpus.jsonl")

// corpusEntry is one line of corpus.jsonl.
type corpusEntry struct {
	Name   string               `json:"name"`
	Source string               `json:"source"` // predb | known | synthetic
	Want   parser.ParsedRelease `json:"want"`
	Note   string               `json:"note,omitempty"`
	line   int
}

// loadCorpus reads and validates the corpus file. Unknown JSON fields are rejected so
// that a typo in an expected field name cannot silently weaken the test.
func loadCorpus(tb testing.TB) []corpusEntry {
	tb.Helper()
	data, err := os.ReadFile(corpusPath)
	if err != nil {
		tb.Fatalf("read corpus: %v", err)
	}
	var entries []corpusEntry
	seen := make(map[string]int)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		var e corpusEntry
		if err := dec.Decode(&e); err != nil {
			tb.Fatalf("corpus line %d: %v", n, err)
		}
		switch e.Source {
		case "predb", "known", "synthetic":
		default:
			tb.Fatalf("corpus line %d: invalid source %q", n, e.Source)
		}
		if prev, dup := seen[e.Name]; dup {
			tb.Fatalf("corpus line %d: duplicate name %q (first on line %d)", n, e.Name, prev)
		}
		seen[e.Name] = n
		// Revision defaults to version 1: the corpus omits it unless it differs.
		if e.Want.Revision.Version == 0 {
			e.Want.Revision.Version = 1
		}
		e.line = n
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		tb.Fatalf("scan corpus: %v", err)
	}
	return entries
}

func TestCorpus(t *testing.T) {
	t.Parallel()
	entries := loadCorpus(t)
	if len(entries) < 300 {
		t.Fatalf("corpus has %d entries, want at least 300", len(entries))
	}
	counts := map[string]int{}
	for _, e := range entries {
		counts[e.Source]++
	}
	if counts["synthetic"]*2 >= len(entries) {
		t.Errorf("synthetic entries must stay a minority: %v", counts)
	}
	t.Logf("corpus: %d entries %v", len(entries), counts)

	for _, e := range entries {
		t.Run(e.Name, func(t *testing.T) {
			t.Parallel()
			got := parser.Parse(e.Name)
			if diff := cmp.Diff(e.Want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("corpus line %d (%s): Parse(%q) mismatch (-want +got):\n%s", e.line, e.Source, e.Name, diff)
			}
		})
	}
}
