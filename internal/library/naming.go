package library

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// DefaultFolderTemplate names a movie folder when the "naming.movie_folder"
// setting is unset.
const DefaultFolderTemplate = "{Title} ({Year})"

// SettingFolderTemplate is the settings key of the movie folder template.
const SettingFolderTemplate = "naming.movie_folder"

// maxFolderBytes bounds a folder name: most file systems allow 255 bytes per
// name; the margin leaves room for the collision suffix.
const maxFolderBytes = 200

// FolderVars are the values a folder template can use.
type FolderVars struct {
	Title         string
	OriginalTitle string
	Year          int
	TmdbID        int64
	ImdbID        string
}

var folderTokens = []string{"Title", "OriginalTitle", "Year", "TmdbId", "ImdbId"}

// ValidateFolderTemplate checks that a template only uses known tokens and
// contains {Title} or {OriginalTitle}. A token is "{" letters "}"; other braces
// are literal, so "{tmdb-{TmdbId}}" gives "{tmdb-438631}".
func ValidateFolderTemplate(tmpl string) error {
	hasTitle := false
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '{' {
			continue
		}
		j := i + 1
		for j < len(tmpl) && (tmpl[j] >= 'A' && tmpl[j] <= 'Z' || tmpl[j] >= 'a' && tmpl[j] <= 'z') {
			j++
		}
		if j == i+1 || j >= len(tmpl) || tmpl[j] != '}' {
			continue // literal brace
		}
		tok := tmpl[i+1 : j]
		if !slices.Contains(folderTokens, tok) {
			return &InputError{Field: "template", Msg: fmt.Sprintf("the folder template uses an unknown token {%s}; available: {%s}", tok, strings.Join(folderTokens, "}, {"))}
		}
		hasTitle = hasTitle || tok == "Title" || tok == "OriginalTitle"
		i = j
	}
	if !hasTitle {
		return &InputError{Field: "template", Msg: "the folder template must contain {Title} or {OriginalTitle}"}
	}
	return nil
}

// FolderName renders the template with v and sanitises the result into a single
// folder name valid on Windows, macOS and Linux. The template must be valid.
func FolderName(tmpl string, v FolderVars) string {
	year, tmdb := "", ""
	if v.Year > 0 {
		year = strconv.Itoa(v.Year)
	}
	if v.TmdbID > 0 {
		tmdb = strconv.FormatInt(v.TmdbID, 10)
	}
	orig := v.OriginalTitle
	if orig == "" {
		orig = v.Title
	}
	s := strings.NewReplacer(
		"{Title}", v.Title, "{OriginalTitle}", orig, "{Year}", year,
		"{TmdbId}", tmdb, "{ImdbId}", v.ImdbID,
	).Replace(tmpl)
	// Unknown values leave empty brackets behind: "Title ()" -> "Title".
	for _, empty := range []string{"()", "[]", "{}", "{tmdb-}", "{imdb-}", "[tmdbid-]", "[imdbid-]"} {
		s = strings.ReplaceAll(s, empty, "")
	}
	name := SanitizeName(s)
	if name == "" {
		name = SanitizeName(fmt.Sprintf("Movie %d", v.TmdbID))
	}
	return name
}

// CollisionName is the folder used when FolderName is already taken by another
// movie of the same root folder: the TMDB id is appended in the form Plex and
// Jellyfin recognise ("Dune (2021) {tmdb-438631}").
func CollisionName(name string, tmdbID int64) string {
	suffix := fmt.Sprintf(" {tmdb-%d}", tmdbID)
	return truncateBytes(name, maxFolderBytes) + suffix
}

// windowsReserved are device names Windows refuses as file names, with or
// without an extension.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SanitizeName turns s into one portable folder or file name:
//
//   - ": " becomes " - " and other ':' become '-' ("Mission: Impossible" ->
//     "Mission - Impossible");
//   - '/' and '\' become '-'; '"' becomes '\''; '<', '>', '|', '?', '*' and
//     control characters are removed;
//   - runs of spaces collapse; leading spaces and dots, trailing spaces and dots
//     are trimmed (Windows drops trailing dots, a leading dot hides the folder);
//   - Windows device names (CON, NUL, COM1...) get a trailing '_';
//   - the result is cut to 200 bytes on a character boundary.
//
// It returns "" when nothing printable remains.
func SanitizeName(s string) string {
	s = strings.ReplaceAll(s, ": ", " - ")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
		case r == ':' || r == '/' || r == '\\':
			b.WriteByte('-')
		case r == '"':
			b.WriteByte('\'')
		case r == '<' || r == '>' || r == '|' || r == '?' || r == '*':
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		case unicode.IsControl(r) || r == '\u200b' || r == '\ufeff':
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = truncateBytes(out, maxFolderBytes)
	out = strings.TrimLeft(out, ". ")
	out = strings.TrimRight(out, ". ")
	if out == "" {
		return ""
	}
	stem := out
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	if windowsReserved[strings.ToUpper(strings.TrimSpace(stem))] {
		out += "_"
	}
	return out
}

// truncateBytes cuts s to at most n bytes without splitting a character.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], ". ")
}

// SortTitle returns the sort key of a title: lower case, accents folded,
// punctuation dropped and a leading English article removed ("The Matrix" sorts
// as "matrix"), as parser.CleanTitle does.
func SortTitle(title string) string {
	if s := parser.CleanTitle(title); s != "" {
		return s
	}
	return strings.ToLower(strings.TrimSpace(title))
}
