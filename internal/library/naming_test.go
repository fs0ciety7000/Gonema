package library

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFolderName(t *testing.T) {
	tests := []struct {
		tmpl string
		v    FolderVars
		want string
	}{
		{DefaultFolderTemplate, FolderVars{Title: "Fight Club", Year: 1999}, "Fight Club (1999)"},
		{DefaultFolderTemplate, FolderVars{Title: "Mission: Impossible", Year: 1996}, "Mission - Impossible (1996)"},
		{DefaultFolderTemplate, FolderVars{Title: "What If...?", Year: 2021}, "What If... (2021)"},
		{DefaultFolderTemplate, FolderVars{Title: "AC/DC: Live", Year: 1992}, "AC-DC - Live (1992)"},
		{DefaultFolderTemplate, FolderVars{Title: `Say "Cheese" <now> | *`, Year: 2000}, "Say 'Cheese' now (2000)"},
		{DefaultFolderTemplate, FolderVars{Title: "Untitled Project", TmdbID: 5}, "Untitled Project"},
		{DefaultFolderTemplate, FolderVars{Title: "...And Justice", Year: 1979}, "And Justice (1979)"},
		{DefaultFolderTemplate, FolderVars{Title: "Tab\tand\nnewline  spaces", Year: 2001}, "Tab and newline spaces (2001)"},
		{DefaultFolderTemplate, FolderVars{Title: "???", TmdbID: 77}, "Movie 77"},
		{"{Title}", FolderVars{Title: "CON"}, "CON_"},
		{"{Title}", FolderVars{Title: "nul.txt"}, "nul.txt_"},
		{"{Title}", FolderVars{Title: "Console"}, "Console"},
		{"{Title}", FolderVars{Title: "Amélie"}, "Amélie"},
		{"{Title}", FolderVars{Title: "Trailing dots..."}, "Trailing dots"},
		{"{OriginalTitle} ({Year}) {tmdb-{TmdbId}}", FolderVars{Title: "Spirited Away", OriginalTitle: "千と千尋の神隠し", Year: 2001, TmdbID: 129}, "千と千尋の神隠し (2001) {tmdb-129}"},
		{"{Title} [imdbid-{ImdbId}]", FolderVars{Title: "X"}, "X"},
	}
	for _, tt := range tests {
		if err := ValidateFolderTemplate(tt.tmpl); err != nil {
			t.Errorf("template %q invalid: %v", tt.tmpl, err)
		}
		if got := FolderName(tt.tmpl, tt.v); got != tt.want {
			t.Errorf("FolderName(%q, %+v) = %q, want %q", tt.tmpl, tt.v, got, tt.want)
		}
	}
}

func TestFolderNameLength(t *testing.T) {
	long := strings.Repeat("é", 300) // 600 bytes
	got := FolderName(DefaultFolderTemplate, FolderVars{Title: long, Year: 2000})
	if len(got) > maxFolderBytes || !utf8.ValidString(got) {
		t.Errorf("len = %d, valid UTF-8 = %v", len(got), utf8.ValidString(got))
	}
	c := CollisionName(got, 123456)
	if !strings.HasSuffix(c, " {tmdb-123456}") || len(c) > 255 {
		t.Errorf("collision name %q (%d bytes)", c, len(c))
	}
}

func TestValidateFolderTemplate(t *testing.T) {
	for _, bad := range []string{"{Year}", "{Title} {Rating}", "{Titl", "plain"} {
		if err := ValidateFolderTemplate(bad); err == nil {
			t.Errorf("ValidateFolderTemplate(%q) = nil", bad)
		}
	}
}

// SanitizeName never yields a separator, a reserved character or an edge dot or
// space, whatever the input.
func FuzzSanitizeName(f *testing.F) {
	for _, s := range []string{"Fight Club", "a/b\\c:d", " .x. ", "CON", "\x00\x01", "é"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := SanitizeName(s)
		if strings.ContainsAny(got, `/\:<>"|?*`) {
			t.Fatalf("SanitizeName(%q) = %q has a reserved character", s, got)
		}
		if got != "" && (strings.HasPrefix(got, ".") || strings.HasPrefix(got, " ") || strings.HasSuffix(got, ".") || strings.HasSuffix(got, " ")) {
			t.Fatalf("SanitizeName(%q) = %q has an edge dot or space", s, got)
		}
		if len(got) > maxFolderBytes+1 || !utf8.ValidString(got) {
			t.Fatalf("SanitizeName(%q) = %q: %d bytes, valid %v", s, got, len(got), utf8.ValidString(got))
		}
	})
}

func TestSortTitle(t *testing.T) {
	for in, want := range map[string]string{
		"The Matrix": "matrix", "Amélie": "amelie", "A Quiet Place": "quiet place", "2001: A Space Odyssey": "2001 a space odyssey",
	} {
		if got := SortTitle(in); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
