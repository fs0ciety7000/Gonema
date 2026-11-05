package parser

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// CleanTitle normalises a title for matching against metadata titles.
//
// Rules, applied in order:
//  1. Letters are lower-cased; Latin diacritics are folded to ASCII (é -> e,
//     ß -> ss, æ -> ae, ø -> o, ł -> l...); combining marks are dropped.
//  2. Apostrophes are removed without a space ("Ocean's" -> "oceans").
//  3. Every other non-letter, non-digit rune (punctuation, "&", ":", "·") is a
//     word break.
//  4. The conjunction "and" is dropped ("&" and "and" are interchangeable in
//     titles: "Fast & Furious" == "Fast and Furious").
//  5. Leading English articles ("the", "a", "an") are dropped, repeatedly, as
//     long as at least one word remains ("The Dark Knight" -> "dark knight",
//     "A" -> "a"). Articles inside the title and non-English articles are kept.
//  6. Words are joined with a single space.
//
// The result is never longer than the input and CleanTitle is idempotent.
func CleanTitle(title string) string {
	var buf [128]byte
	b := buf[:0]
	space := true // suppress leading spaces
	for i := 0; i < len(title); {
		r, size := utf8.DecodeRuneInString(title[i:])
		i += size
		switch {
		case r < utf8.RuneSelf && isAlnum(byte(r)):
			b = append(b, lowerASCII(byte(r)))
			space = false
		case r == '\'' || r == '’' || r == '`' || r == '´':
			// apostrophes join words
		case unicode.Is(unicode.Mn, r):
			// combining marks (decomposed accents) are dropped
		case r >= utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if f, ok := foldLatin(r); ok {
				b = append(b, f...)
			} else if l := unicode.ToLower(r); utf8.RuneLen(l) <= size {
				b = utf8.AppendRune(b, l)
			} else {
				b = utf8.AppendRune(b, r)
			}
			space = false
		default:
			if !space {
				b = append(b, ' ')
				space = true
			}
		}
	}
	return dropStopWords(strings.TrimRight(string(b), " "))
}

// dropStopWords applies rules 4 and 5 of CleanTitle on a space-separated string.
func dropStopWords(s string) string {
	if s == "" {
		return s
	}
	if strings.Contains(" "+s+" ", " and ") {
		words := strings.Fields(s)
		kept := words[:0]
		for _, w := range words {
			if w != "and" {
				kept = append(kept, w)
			}
		}
		if len(kept) > 0 {
			s = strings.Join(kept, " ")
		}
	}
	for {
		i := strings.IndexByte(s, ' ')
		if i < 0 {
			return s
		}
		switch s[:i] {
		case "the", "a", "an":
			s = s[i+1:]
		default:
			return s
		}
	}
}

func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// foldLatin maps Latin letters with diacritics or ligatures to ASCII. Every
// replacement is at most as long, in bytes, as the UTF-8 encoding of the rune.
func foldLatin(r rune) (string, bool) {
	if r < 0xC0 || r > 0x17F {
		if r == 0x1E9E { // capital sharp s
			return "ss", true
		}
		return "", false
	}
	f := latinFold[r-0xC0]
	return f, f != ""
}

// latinFold covers Latin-1 Supplement (U+00C0..U+00FF) and Latin Extended-A
// (U+0100..U+017F). Empty entries are not letters (×, ÷) and fall through.
var latinFold = [...]string{
	// U+00C0
	"a", "a", "a", "a", "a", "a", "ae", "c", "e", "e", "e", "e", "i", "i", "i", "i",
	"d", "n", "o", "o", "o", "o", "o", "", "o", "u", "u", "u", "u", "y", "th", "ss",
	"a", "a", "a", "a", "a", "a", "ae", "c", "e", "e", "e", "e", "i", "i", "i", "i",
	"d", "n", "o", "o", "o", "o", "o", "", "o", "u", "u", "u", "u", "y", "th", "y",
	// U+0100
	"a", "a", "a", "a", "a", "a", "c", "c", "c", "c", "c", "c", "c", "c", "d", "d",
	"d", "d", "e", "e", "e", "e", "e", "e", "e", "e", "e", "e", "g", "g", "g", "g",
	"g", "g", "g", "g", "h", "h", "h", "h", "i", "i", "i", "i", "i", "i", "i", "i",
	"i", "i", "ij", "ij", "j", "j", "k", "k", "k", "l", "l", "l", "l", "l", "l", "l",
	"l", "l", "l", "n", "n", "n", "n", "n", "n", "n", "n", "n", "o", "o", "o", "o",
	"o", "o", "oe", "oe", "r", "r", "r", "r", "r", "r", "s", "s", "s", "s", "s", "s",
	"s", "s", "t", "t", "t", "t", "t", "t", "u", "u", "u", "u", "u", "u", "u", "u",
	"u", "u", "u", "u", "w", "w", "y", "y", "y", "z", "z", "z", "z", "z", "z", "s",
}
