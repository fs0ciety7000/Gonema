package match

import (
	"strings"
	"unicode/utf8"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// Key returns the matching key of a title: two titles match when their keys are
// equal. It extends parser.CleanTitle (accents folded, punctuation and "&"/"and"
// dropped, leading English article dropped) with:
//
//   - French and Italian elisions split before cleaning: "L'Odyssée" and
//     "L.Odyssee" both give "l odyssee" (CleanTitle alone would glue "lodyssee").
//   - "vs" and "versus" read as "v".
//   - Roman numerals II to XX read as digits ("Rocky IV" == "Rocky 4"). "I" is
//     read as 1 only after a sequel marker ("Part I"), never alone (pronoun).
//   - Sequel markers (part, pt, chapter, chap, vol, volume, episode, ep, book,
//     partie, chapitre, teil, parte) are dropped before a number, and English
//     number words one..twenty after them read as digits: "Dune: Part Two",
//     "Dune.Part.2" and "Dune 2" share a key. Number words elsewhere are kept
//     ("Ocean's Eleven" is not "Ocean's 11").
//   - One leading French, German or Spanish article is dropped (le, la, les, l,
//     der, die, das, el, los, las), when a word remains.
//   - Words are joined without spaces, so word splits do not matter:
//     "Spider-Man" == "Spiderman", "Ocean.s.Eleven" == "Ocean's Eleven",
//     "d'Amélie" == "dAmelie".
func Key(title string) string {
	_, k := keys(title)
	return k
}

// keys returns the key before and after dropping the leading foreign article.
func keys(title string) (full, short string) {
	cleaned := parser.CleanTitle(splitElisions(title))
	if cleaned == "" {
		return "", ""
	}
	var wordsBuf [24]string
	words := wordsBuf[:0]
	for w := range strings.FieldsSeq(cleaned) {
		words = append(words, w)
	}
	// Map words, then drop sequel markers that precede a number.
	var outBuf [24]string
	out := outBuf[:0]
	for i, w := range words {
		prevMarker := i > 0 && sequelMarkers[words[i-1]]
		switch {
		case w == "vs" || w == "versus":
			w = "v"
		case prevMarker && w == "i":
			w = "1"
		case prevMarker && numberWords[w] != "":
			w = numberWords[w]
		case romanNumerals[w] != "":
			w = romanNumerals[w]
		}
		if prevMarker && isDigits(w) && len(out) > 0 {
			out = out[:len(out)-1] // drop the marker
		}
		out = append(out, w)
	}
	full = strings.Join(out, "")
	if len(out) > 1 && foreignArticles[out[0]] {
		out = out[1:]
	}
	return full, strings.Join(out, "")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// splitElisions replaces the apostrophe of an elided article or preposition by a
// space: "L'Odyssée" -> "L Odyssée", "d'Amélie" -> "d Amélie", "dell'amore" ->
// "dell amore". Possessives ("Ocean's") and other apostrophes are kept.
func splitElisions(s string) string {
	if !strings.ContainsAny(s, "'’`´") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	wordStart := 0 // byte offset of the current word in s
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '\'' || r == '’' || r == '`' || r == '´' {
			if elisions[strings.ToLower(s[wordStart:i])] && i+size < len(s) {
				b.WriteByte(' ')
			} else {
				b.WriteString(s[i : i+size])
			}
			i += size
			wordStart = i
			continue
		}
		if !isWordRune(r) {
			wordStart = i + size
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

func isWordRune(r rune) bool {
	return r >= utf8.RuneSelf || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

var elisions = map[string]bool{
	"l": true, "d": true, "j": true, "m": true, "n": true, "s": true, "t": true, "c": true, "qu": true,
	"dell": true, "nell": true, "all": true, "dall": true, "sull": true, "un": true,
}

var sequelMarkers = map[string]bool{
	"part": true, "pt": true, "chapter": true, "chap": true, "vol": true, "volume": true,
	"episode": true, "ep": true, "book": true, "partie": true, "chapitre": true, "teil": true, "parte": true,
}

var foreignArticles = map[string]bool{
	"le": true, "la": true, "les": true, "l": true,
	"der": true, "die": true, "das": true,
	"el": true, "los": true, "las": true,
}

var romanNumerals = map[string]string{
	"ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6", "vii": "7", "viii": "8", "ix": "9", "x": "10",
	"xi": "11", "xii": "12", "xiii": "13", "xiv": "14", "xv": "15", "xvi": "16", "xvii": "17", "xviii": "18",
	"xix": "19", "xx": "20",
}

var numberWords = map[string]string{
	"one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "seven": "7", "eight": "8",
	"nine": "9", "ten": "10", "eleven": "11", "twelve": "12", "thirteen": "13", "fourteen": "14",
	"fifteen": "15", "sixteen": "16", "seventeen": "17", "eighteen": "18", "nineteen": "19", "twenty": "20",
}
