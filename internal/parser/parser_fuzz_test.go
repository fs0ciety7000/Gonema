package parser_test

import (
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// FuzzParse checks the invariants that must hold for any input:
// no panic, determinism, a plausible year, and no output string longer than the input.
func FuzzParse(f *testing.F) {
	entries := loadCorpus(f)
	for i := range entries {
		f.Add(entries[i].Name)
	}
	for _, s := range []string{"", "-", ".", "[", "]", "[]", "-[]-", "....", "(((", "a-", "-a", "AKA", "a.AKA", "\xff\xfe", "Ⱥ.2019", "ǅ & ß"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := parser.Parse(in)
		if again := parser.Parse(in); !cmp.Equal(got, again) {
			t.Fatalf("Parse(%q) is not deterministic:\n%s", in, cmp.Diff(got, again))
		}
		if got.Year != 0 && (got.Year < 1900 || got.Year > 2100) {
			t.Errorf("Parse(%q).Year = %d, want 0 or 1900..2100", in, got.Year)
		}
		if got.Revision.Version < 1 {
			t.Errorf("Parse(%q).Revision.Version = %d, want >= 1", in, got.Revision.Version)
		}
		fields := map[string]string{
			"Title": got.Title, "CleanTitle": got.CleanTitle, "EditionText": got.EditionText,
			"Subs": got.Subs, "HardcodedSubsTag": got.HardcodedSubsTag, "ReleaseGroup": got.ReleaseGroup,
			"Container": got.Container, "Streaming": got.Streaming, "Hash": got.Hash, "Channels": got.Channels,
		}
		for i, alt := range got.AlternateTitles {
			fields["AlternateTitles"+string(rune('0'+i%10))] = alt
		}
		for name, v := range fields {
			if len(v) > len(in) {
				t.Errorf("Parse(%q).%s = %q is longer than the input", in, name, v)
			}
		}
		if got.CleanTitle != parser.CleanTitle(got.CleanTitle) {
			t.Errorf("CleanTitle is not idempotent for %q: %q -> %q", in, got.CleanTitle, parser.CleanTitle(got.CleanTitle))
		}
		if utf8.ValidString(in) && !utf8.ValidString(got.Title) {
			t.Errorf("Parse(%q).Title = %q is not valid UTF-8", in, got.Title)
		}
	})
}
