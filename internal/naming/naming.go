// Package naming renders movie file names from a template.
//
// A template is literal text with tokens in braces:
//
//	{Movie Title} ({Release Year}) {[Edition Tags]}{[Quality Full]}{-Release Group}
//
// Rules:
//
//   - A token group is "{" prefix tokens suffix "}". The prefix is a run of
//     " .-_[(" characters before the first token, the suffix a run of " .-_])"
//     after the last one. When every token of the group is empty, the whole group
//     disappears, prefix and suffix included: "{[Edition Tags]}" gives
//     "[Director's Cut]" or nothing.
//   - A group may hold several tokens separated by spaces: "{[MediaInfo AudioCodec
//     MediaInfo AudioChannels]}" gives "[TrueHD Atmos 7.1]"; empty tokens are skipped.
//   - Case variants: a token written in lower case renders in lower case
//     ("{movie title}"), in upper case renders in upper case ("{MOVIE TITLE}").
//   - Separator variants: words written with "." or "_" render with that
//     separator instead of spaces ("{Movie.Title}" gives "Dune.Part.Two").
//   - "{{" and "}}" are literal braces: "{{{tmdb-ID}}}" gives "{tmdb-438631}".
//   - Literal "()" and "[]" left empty by a missing value are removed, as are
//     doubled separators; the result is sanitized with library.SanitizeName
//     (portable on Windows, macOS and Linux, at most 200 bytes) and the
//     extension is appended.
//
// Values come from the movie (titles, year, ids) and from the parsed release
// name (quality, edition, codecs, HDR, audio, group): there is no media probe yet,
// so MediaInfo tokens describe what the release name claims.
package naming

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// DefaultFileTemplate is used when the "naming.movie_file" setting is unset:
//
//	Dune - Part Two (2024) [Bluray-2160p][DV HDR10][TrueHD Atmos 7.1]-FLUX.mkv
//	Blade Runner (1982) [Final Cut][Bluray-1080p][DTS-HD MA 5.1]-AMIABLE.mkv
//	Some Movie (2019) [WEBDL-1080p].mkv
//
// Title and year first (what media servers match on), then each optional block in
// its own brackets so that a missing value leaves no trace.
const DefaultFileTemplate = "{Movie Title} ({Release Year}) {[Edition Tags]}{[Quality Full]}" +
	"{[MediaInfo VideoDynamicRange]}{[MediaInfo AudioCodec MediaInfo AudioChannels]}{-Release Group}"

// Settings keys.
const (
	SettingFileTemplate = "naming.movie_file"   // JSON string
	SettingRenameFiles  = "naming.rename_files" // JSON bool; default true
)

// maxTemplateLen bounds a template (bytes).
const maxTemplateLen = 1024

// ErrInvalid is wrapped by template errors.
var ErrInvalid = errors.New("naming: invalid template")

// TemplateError lists the problems of a template, in plain language.
type TemplateError struct {
	Problems []string
}

func (e *TemplateError) Error() string { return strings.Join(e.Problems, "; ") }

// UserMessage returns the plain-language message (shown by the API as is).
func (e *TemplateError) UserMessage() string { return e.Error() }

// Is makes TemplateError match ErrInvalid.
func (e *TemplateError) Is(target error) bool { return target == ErrInvalid }

// Vars are the values a template can use.
type Vars struct {
	Title         string
	OriginalTitle string // falls back to Title
	Year          int    // 0 if unknown
	TmdbID        int64
	ImdbID        string // "tt0137523"
	Quality       quality.ID
	// Release is the parsed release (or file) name: revision, edition, codecs,
	// HDR, audio, group.
	Release parser.ParsedRelease
	// CustomFormats are the names of the matched scoring rules flagged
	// include_in_rename, in rule order.
	CustomFormats []string
}

type tokenID uint8

const (
	tokMovieTitle tokenID = iota
	tokCleanTitle
	tokTitleThe
	tokOriginalTitle
	tokYear
	tokQualityFull
	tokQualityTitle
	tokVideoCodec
	tokDynamicRange
	tokAudioCodec
	tokAudioChannels
	tokEdition
	tokGroup
	tokCustomFormats
	tokTmdb
	tokImdb
)

// Token documents a template token.
type Token struct {
	Name        string `json:"name"`        // as written between braces
	Description string `json:"description"` // plain language
	Example     string `json:"example"`
}

type tokenDef struct {
	Token
	id    tokenID
	words []string // lower-case words of Name
}

var tokenDefs = func() []tokenDef {
	defs := []tokenDef{
		{Token{"Movie Title", "Title of the movie", "Dune - Part Two"}, tokMovieTitle, nil},
		{Token{"Movie CleanTitle", "Title without punctuation", "Dune Part Two"}, tokCleanTitle, nil},
		{Token{"Movie TitleThe", "Title with a leading article moved to the end", "Matrix, The"}, tokTitleThe, nil},
		{Token{"Original Title", "Title in the original language", "Le Fabuleux Destin d'Amélie Poulain"}, tokOriginalTitle, nil},
		{Token{"Release Year", "Year of release", "2024"}, tokYear, nil},
		{Token{"Quality Full", "Quality with PROPER/REPACK revision", "Bluray-2160p Proper"}, tokQualityFull, nil},
		{Token{"Quality Title", "Quality", "Bluray-2160p"}, tokQualityTitle, nil},
		{Token{"MediaInfo VideoCodec", "Video codec (from the release name)", "h265"}, tokVideoCodec, nil},
		{Token{"MediaInfo VideoDynamicRange", "HDR formats (from the release name)", "DV HDR10"}, tokDynamicRange, nil},
		{Token{"MediaInfo AudioCodec", "Main audio codec (from the release name)", "TrueHD Atmos"}, tokAudioCodec, nil},
		{Token{"MediaInfo AudioChannels", "Audio channels (from the release name)", "7.1"}, tokAudioChannels, nil},
		{Token{"Edition Tags", "Edition", "Director's Cut"}, tokEdition, nil},
		{Token{"Release Group", "Release group", "FLUX"}, tokGroup, nil},
		{Token{"Custom Formats", "Matched scoring rules marked \"include in rename\"", "HDR10+"}, tokCustomFormats, nil},
		{Token{"tmdb-ID", "TMDB id", "tmdb-693134"}, tokTmdb, nil},
		{Token{"imdb-ID", "IMDb id", "imdb-tt15239678"}, tokImdb, nil},
	}
	for i := range defs {
		defs[i].words = strings.Fields(strings.ToLower(defs[i].Name))
	}
	// Longest names first: "MediaInfo AudioCodec" before a hypothetical "MediaInfo".
	slices.SortStableFunc(defs, func(a, b tokenDef) int { return len(b.words) - len(a.words) })
	return defs
}()

// Tokens returns the documented tokens, in display order.
func Tokens() []Token {
	out := make([]Token, len(tokenDefs))
	for _, d := range tokenDefs {
		out[d.id] = d.Token
	}
	return out
}

type caseMode uint8

const (
	caseAsIs caseMode = iota
	caseLower
	caseUpper
)

type tokenRef struct {
	id   tokenID
	mode caseMode
}

type segment struct {
	literal string
	// group, when tokens is not empty
	prefix, suffix, sep string
	tokens              []tokenRef
}

// Template is a parsed template, safe for concurrent use.
type Template struct {
	src  string
	segs []segment
}

// String returns the template text.
func (t *Template) String() string { return t.src }

const (
	prefixChars = " .-_[("
	suffixChars = " .-_])"
)

// Parse parses and validates a template. Errors wrap ErrInvalid and list every
// problem (unknown tokens with the list of known ones, unbalanced braces...).
func Parse(tmpl string) (*Template, error) {
	var problems []string
	switch {
	case strings.TrimSpace(tmpl) == "":
		return nil, &TemplateError{Problems: []string{"the file name template is empty"}}
	case len(tmpl) > maxTemplateLen:
		return nil, &TemplateError{Problems: []string{fmt.Sprintf("the file name template is too long (%d characters at most)", maxTemplateLen)}}
	case !utf8.ValidString(tmpl):
		return nil, &TemplateError{Problems: []string{"the file name template is not valid text (UTF-8)"}}
	case strings.ContainsFunc(tmpl, func(r rune) bool { return r >= 0xe000 && r <= 0xf8ff }):
		return nil, &TemplateError{Problems: []string{"the file name template contains a private-use character"}}
	}
	t := &Template{src: tmpl}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t.segs = append(t.segs, segment{literal: lit.String()})
			lit.Reset()
		}
	}
	unknown := map[string]bool{}
	hasTitle := false
	for i := 0; i < len(tmpl); i++ {
		c := tmpl[i]
		switch {
		case c == '{' && i+1 < len(tmpl) && tmpl[i+1] == '{':
			lit.WriteByte('{')
			i++
		case c == '}' && i+1 < len(tmpl) && tmpl[i+1] == '}':
			lit.WriteByte('}')
			i++
		case c == '}':
			problems = append(problems, fmt.Sprintf("unmatched \"}\" at position %d (write \"}}\" for a literal brace)", i+1))
		case c == '{':
			end := strings.IndexAny(tmpl[i+1:], "{}")
			if end < 0 || tmpl[i+1+end] != '}' {
				problems = append(problems, fmt.Sprintf("unclosed \"{\" at position %d (write \"{{\" for a literal brace)", i+1))
				i = len(tmpl)
				continue
			}
			body := tmpl[i+1 : i+1+end]
			i += end + 1
			seg, bad := parseGroup(body)
			if len(bad) > 0 {
				for _, b := range bad {
					unknown[b] = true
				}
				continue
			}
			for _, r := range seg.tokens {
				hasTitle = hasTitle || r.id == tokMovieTitle || r.id == tokCleanTitle || r.id == tokTitleThe || r.id == tokOriginalTitle
			}
			flush()
			t.segs = append(t.segs, seg)
		case c == '/' || c == '\\':
			problems = append(problems, "a file name cannot contain \"/\" or \"\\\" (the movie folder is set by the folder template)")
		case c < 0x20 || c == 0x7f:
			problems = append(problems, "the file name template contains a control character")
		default:
			lit.WriteByte(c)
		}
	}
	flush()
	if len(unknown) > 0 {
		names := make([]string, 0, len(unknown))
		for n := range unknown {
			names = append(names, "{"+n+"}")
		}
		slices.Sort(names)
		known := make([]string, 0, len(tokenDefs))
		for _, tk := range Tokens() {
			known = append(known, "{"+tk.Name+"}")
		}
		problems = append(problems, fmt.Sprintf("unknown token %s; available: %s", strings.Join(names, ", "), strings.Join(known, ", ")))
	}
	if !hasTitle && len(problems) == 0 {
		problems = append(problems, "the file name template must contain {Movie Title}, {Movie CleanTitle}, {Movie TitleThe} or {Original Title}")
	}
	if len(problems) > 0 {
		return nil, &TemplateError{Problems: dedupe(problems)}
	}
	return t, nil
}

func dedupe(s []string) []string {
	out := s[:0]
	for _, v := range s {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// parseGroup parses the inside of braces. bad lists unknown token texts.
func parseGroup(body string) (seg segment, bad []string) {
	start := 0
	for start < len(body) && strings.IndexByte(prefixChars, body[start]) >= 0 {
		start++
	}
	end := len(body)
	for end > start && strings.IndexByte(suffixChars, body[end-1]) >= 0 {
		end--
	}
	mid := body[start:end]
	if mid == "" {
		return segment{}, []string{body}
	}
	seg.prefix, seg.suffix = body[:start], body[end:]
	seg.sep = " "
	if k := strings.IndexAny(mid, "._"); k >= 0 {
		seg.sep = mid[k : k+1]
	}
	words := strings.FieldsFunc(mid, func(r rune) bool { return r == ' ' || r == '.' || r == '_' })
	for i := 0; i < len(words); {
		matched := false
		for _, d := range tokenDefs {
			n := len(d.words)
			if i+n > len(words) {
				continue
			}
			ok := true
			for k := range n {
				if !strings.EqualFold(words[i+k], d.words[k]) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			seg.tokens = append(seg.tokens, tokenRef{id: d.id, mode: caseOf(words[i : i+n])})
			i += n
			matched = true
			break
		}
		if !matched {
			return segment{}, []string{mid}
		}
	}
	return seg, nil
}

// caseOf tells the case variant of a token as written.
func caseOf(words []string) caseMode {
	lower, upper := true, true
	for _, w := range words {
		for _, r := range w {
			if unicode.IsUpper(r) {
				lower = false
			}
			if unicode.IsLower(r) {
				upper = false
			}
		}
	}
	switch {
	case lower:
		return caseLower
	case upper:
		return caseUpper
	}
	return caseAsIs
}

// Validate checks a template (see Parse).
func Validate(tmpl string) error {
	_, err := Parse(tmpl)
	return err
}

// Render returns the file name without extension: rendered, cleaned, sanitized.
// It never returns "": a name that sanitizes to nothing falls back to the title,
// then to "Movie".
//
// Cleaning (empty brackets, doubled separators) only applies to the literal text
// of the template, never inside values: "Mr. Smith" keeps its dot.
func (t *Template) Render(v *Vars) string {
	var skel strings.Builder
	skel.Grow(64)
	var groups [16]string
	vals := groups[:0]
	var got [4]string
	for i := range t.segs {
		s := &t.segs[i]
		if len(s.tokens) == 0 {
			skel.WriteString(s.literal)
			continue
		}
		parts := got[:0]
		for _, r := range s.tokens {
			if val := value(r, s.sep, v); val != "" {
				parts = append(parts, val)
			}
		}
		if len(parts) == 0 {
			continue
		}
		var b strings.Builder
		b.WriteString(s.prefix)
		for k, val := range parts {
			if k > 0 {
				b.WriteString(s.sep)
			}
			b.WriteString(val)
		}
		b.WriteString(s.suffix)
		// Values are replaced by private-use markers while the literal text is
		// tidied, then put back.
		skel.WriteRune(marker + rune(len(vals)))
		vals = append(vals, b.String())
	}
	out := tidy(skel.String())
	if len(vals) > 0 {
		var b strings.Builder
		b.Grow(len(out) + 64)
		for _, r := range out {
			if i := int(r - marker); r >= marker && i < len(vals) {
				b.WriteString(vals[i])
				continue
			}
			b.WriteRune(r)
		}
		out = b.String()
	}
	name := library.SanitizeName(out)
	if name == "" {
		name = library.SanitizeName(v.Title)
	}
	if name == "" {
		name = "Movie"
	}
	return name
}

// marker is the first private-use code point standing for a rendered group.
// Templates are at most 1024 bytes, so there are fewer than 6400 groups.
const marker = '\ue000'

// FileName renders the name and appends ext (with or without its dot). The
// extension is lower-cased; an invalid one is dropped.
func (t *Template) FileName(v *Vars, ext string) string {
	name := t.Render(v)
	if e := CleanExt(ext); e != "" {
		return name + e
	}
	return name
}

// CleanExt returns ".ext" in lower case, or "" when ext is not 1 to 10 ASCII
// letters and digits.
func CleanExt(ext string) string {
	ext = strings.TrimPrefix(ext, ".")
	if ext == "" || len(ext) > 10 {
		return ""
	}
	for i := range len(ext) {
		c := ext[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return ""
		}
	}
	return "." + strings.ToLower(ext)
}

// Preview parses tmpl and renders the file name for v (ext like "mkv").
func Preview(tmpl string, v *Vars, ext string) (string, error) {
	t, err := Parse(tmpl)
	if err != nil {
		return "", err
	}
	return t.FileName(v, ext), nil
}

// Example returns the values of a real movie and a typical release, used to
// preview templates when no library movie is chosen. The UI labels it as an
// example.
func Example() Vars {
	p := parser.Parse("Dune.Part.Two.2024.2160p.UHD.BluRay.DV.HDR10.TrueHD.Atmos.7.1.x265-FLUX.mkv")
	return Vars{
		Title: "Dune: Part Two", OriginalTitle: "Dune: Part Two", Year: 2024, TmdbID: 693134, ImdbID: "tt15239678",
		Quality: quality.FromParsed(&p), Release: p,
	}
}

// tidy removes what empty values leave behind: empty brackets, doubled
// separators, separators at the ends.
func tidy(s string) string {
	for {
		n := len(s)
		for _, empty := range []string{"()", "[]", "{}", "( )", "[ ]"} {
			s = strings.ReplaceAll(s, empty, "")
		}
		for _, dbl := range [][2]string{{"..", "."}, {"__", "_"}, {"  ", " "}, {"--", "-"}, {" - -", " -"}} {
			s = strings.ReplaceAll(s, dbl[0], dbl[1])
		}
		if len(s) == n {
			break
		}
	}
	return strings.Trim(s, " .-_")
}

func value(r tokenRef, sep string, v *Vars) string {
	s := rawValue(r.id, v)
	if s == "" {
		return ""
	}
	if sep != " " {
		s = strings.Join(strings.Fields(s), sep)
	}
	switch r.mode {
	case caseLower:
		return strings.ToLower(s)
	case caseUpper:
		return strings.ToUpper(s)
	}
	return s
}

func rawValue(id tokenID, v *Vars) string {
	p := &v.Release
	switch id {
	case tokMovieTitle:
		return v.Title
	case tokCleanTitle:
		return cleanTitle(v.Title)
	case tokTitleThe:
		return titleThe(v.Title)
	case tokOriginalTitle:
		if v.OriginalTitle != "" {
			return v.OriginalTitle
		}
		return v.Title
	case tokYear:
		if v.Year > 0 {
			return strconv.Itoa(v.Year)
		}
	case tokQualityFull:
		q := qualityTitle(v.Quality)
		if q == "" {
			return ""
		}
		if r := revisionTag(p.Revision); r != "" {
			return q + " " + r
		}
		return q
	case tokQualityTitle:
		return qualityTitle(v.Quality)
	case tokVideoCodec:
		return videoCodec(p.VideoCodec)
	case tokDynamicRange:
		return dynamicRange(p.HDR)
	case tokAudioCodec:
		return audioCodec(p)
	case tokAudioChannels:
		return p.Channels
	case tokEdition:
		return editionTags(p.Edition)
	case tokGroup:
		return p.ReleaseGroup
	case tokCustomFormats:
		return strings.Join(v.CustomFormats, " ")
	case tokTmdb:
		if v.TmdbID > 0 {
			return "tmdb-" + strconv.FormatInt(v.TmdbID, 10)
		}
	case tokImdb:
		if v.ImdbID != "" {
			return "imdb-" + v.ImdbID
		}
	}
	return ""
}

func qualityTitle(id quality.ID) string {
	if id == "" || id == quality.Unknown {
		return ""
	}
	return id.Name()
}

func revisionTag(r parser.Revision) string {
	var parts []string
	if r.Real {
		parts = append(parts, "REAL")
	}
	switch {
	case r.Version == 2:
		parts = append(parts, "Proper")
	case r.Version > 2:
		parts = append(parts, "Proper v"+strconv.Itoa(r.Version))
	}
	return strings.Join(parts, " ")
}

func videoCodec(c parser.Codec) string {
	switch c {
	case parser.CodecH264:
		return "h264"
	case parser.CodecH265:
		return "h265"
	case parser.CodecAV1:
		return "AV1"
	case parser.CodecVC1:
		return "VC1"
	case parser.CodecXviD:
		return "XviD"
	case parser.CodecDivX:
		return "DivX"
	case parser.CodecMPEG2:
		return "MPEG2"
	case parser.CodecVP9:
		return "VP9"
	}
	return ""
}

func dynamicRange(hdr []parser.HDRFormat) string {
	order := []struct {
		f    parser.HDRFormat
		name string
	}{{parser.DolbyVision, "DV"}, {parser.HDR10Plus, "HDR10+"}, {parser.HDR10, "HDR10"}, {parser.HLG, "HLG"}}
	var parts []string
	for _, o := range order {
		if slices.Contains(hdr, o.f) {
			parts = append(parts, o.name)
		}
	}
	return strings.Join(parts, " ")
}

var audioNames = map[parser.AudioFormat]string{
	parser.AudioAAC: "AAC", parser.AudioAC3: "AC3", parser.AudioEAC3: "EAC3", parser.AudioTrueHD: "TrueHD",
	parser.AudioDTS: "DTS", parser.AudioDTSHD: "DTS-HD", parser.AudioDTSHDMA: "DTS-HD MA",
	parser.AudioDTSHDHRA: "DTS-HD HRA", parser.AudioDTSX: "DTS-X", parser.AudioFLAC: "FLAC",
	parser.AudioPCM: "PCM", parser.AudioOpus: "Opus", parser.AudioMP3: "MP3",
}

func audioCodec(p *parser.ParsedRelease) string {
	if len(p.Audio) == 0 {
		if p.Atmos {
			return "Atmos"
		}
		return ""
	}
	name := audioNames[p.Audio[0]]
	if p.Atmos && (p.Audio[0] == parser.AudioTrueHD || p.Audio[0] == parser.AudioEAC3) {
		name += " Atmos"
	}
	return name
}

var editionNames = []struct {
	f    parser.Edition
	name string
}{
	{parser.EditionDirectors, "Director's Cut"}, {parser.EditionExtended, "Extended"},
	{parser.EditionTheatrical, "Theatrical"}, {parser.EditionUnrated, "Unrated"}, {parser.EditionUncut, "Uncut"},
	{parser.EditionFinalCut, "Final Cut"}, {parser.EditionUltimate, "Ultimate"},
	{parser.EditionAnniversary, "Anniversary"}, {parser.EditionCollectors, "Collector's Edition"},
	{parser.EditionSpecial, "Special Edition"}, {parser.EditionCriterion, "Criterion"},
	{parser.EditionRemastered, "Remastered"}, {parser.EditionIMAX, "IMAX"},
	{parser.EditionOpenMatte, "Open Matte"}, {parser.EditionRedux, "Redux"}, {parser.EditionFanEdit, "Fan Edit"},
}

func editionTags(e parser.Edition) string {
	if e == 0 {
		return ""
	}
	var parts []string
	for _, n := range editionNames {
		if e.Has(n.f) {
			parts = append(parts, n.name)
		}
	}
	return strings.Join(parts, " ")
}

// cleanTitle drops punctuation: apostrophes vanish, "&" becomes "and", other
// symbols become spaces. Case and accents are kept.
func cleanTitle(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\'' || r == '’' || r == '`':
		case r == '&':
			b.WriteString(" and ")
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// titleThe moves a leading English article to the end: "The Matrix" gives
// "Matrix, The".
func titleThe(s string) string {
	s = strings.TrimSpace(s)
	for _, art := range []string{"The", "A", "An"} {
		if len(s) > len(art)+1 && strings.EqualFold(s[:len(art)], art) && s[len(art)] == ' ' {
			return strings.TrimSpace(s[len(art)+1:]) + ", " + s[:len(art)]
		}
	}
	return s
}
