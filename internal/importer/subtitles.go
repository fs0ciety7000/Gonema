package importer

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/fsx"
)

// subLanguages maps the language tags found in subtitle names to ISO 639-1.
var subLanguages = map[string]string{
	"en": "en", "eng": "en", "english": "en",
	"fr": "fr", "fre": "fr", "fra": "fr", "french": "fr", "francais": "fr", "français": "fr", "vf": "fr", "vff": "fr", "vostfr": "fr",
	"de": "de", "ger": "de", "deu": "de", "german": "de", "deutsch": "de",
	"es": "es", "spa": "es", "spanish": "es", "espanol": "es", "español": "es",
	"it": "it", "ita": "it", "italian": "it", "italiano": "it",
	"pt": "pt", "por": "pt", "portuguese": "pt", "pob": "pt-BR", "pt-br": "pt-BR", "brazilian": "pt-BR",
	"nl": "nl", "dut": "nl", "nld": "nl", "dutch": "nl",
	"sv": "sv", "swe": "sv", "swedish": "sv", "no": "no", "nor": "no", "norwegian": "no",
	"da": "da", "dan": "da", "danish": "da", "fi": "fi", "fin": "fi", "finnish": "fi",
	"pl": "pl", "pol": "pl", "polish": "pl", "ru": "ru", "rus": "ru", "russian": "ru",
	"uk": "uk", "ukr": "uk", "ukrainian": "uk", "cs": "cs", "cze": "cs", "ces": "cs", "czech": "cs",
	"hu": "hu", "hun": "hu", "hungarian": "hu", "ro": "ro", "rum": "ro", "ron": "ro", "romanian": "ro",
	"el": "el", "gre": "el", "ell": "el", "greek": "el", "tr": "tr", "tur": "tr", "turkish": "tr",
	"ja": "ja", "jpn": "ja", "japanese": "ja", "ko": "ko", "kor": "ko", "korean": "ko",
	"zh": "zh", "chi": "zh", "zho": "zh", "chinese": "zh", "ar": "ar", "ara": "ar", "arabic": "ar",
	"he": "he", "heb": "he", "hebrew": "he", "hin": "hi", "hindi": "hi",
	"ca": "ca", "cat": "ca", "catalan": "ca", "bg": "bg", "bul": "bg", "bulgarian": "bg",
}

// subTag describes a subtitle file: its language and flags.
type subTag struct {
	lang   string // ISO 639-1, "" if unknown
	forced bool
	sdh    bool
}

// suffix renders the tag for a file name: ".en.forced".
func (t subTag) suffix() string {
	var b strings.Builder
	if t.lang != "" {
		b.WriteString("." + t.lang)
	}
	if t.forced {
		b.WriteString(".forced")
	}
	if t.sdh {
		b.WriteString(".sdh")
	}
	return b.String()
}

// parseSubTag reads the language and flags from the words of a subtitle name
// ("Movie.2020.en.forced.srt", "Subs/2_English.srt", "French (SDH).srt").
func parseSubTag(name string) subTag {
	stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	words := strings.FieldsFunc(strings.ToLower(stem), func(r rune) bool {
		return r == '.' || r == '_' || r == ' ' || r == '(' || r == ')' || r == '[' || r == ']' || r == ','
	})
	var t subTag
	// The language is usually last: read from the end.
	for i := len(words) - 1; i >= 0; i-- {
		w := words[i]
		switch w {
		case "forced", "foreign":
			t.forced = true
			continue
		case "sdh", "hi", "cc", "hearing":
			// "hi" is "hearing impaired" in subtitle names; Hindi is "hin".
			t.sdh = true
			continue
		}
		if t.lang == "" {
			if l, ok := subLanguages[w]; ok {
				t.lang = l
			} else if j := strings.LastIndexByte(w, '-'); j >= 0 {
				if l, ok := subLanguages[w[j+1:]]; ok {
					t.lang = l
				}
			}
		}
	}
	return t
}

// importSubtitles transfers the subtitles next to the new movie file, named
// "<movie file stem><tag><ext>". Failures are reported, never fatal: the movie
// is imported already.
func (s *Service) importSubtitles(ctx context.Context, subs []subFile, destDir, stem string, mode fsx.Mode) (done, notes []string) {
	used := map[string]bool{}
	for _, sf := range subs {
		ext := strings.ToLower(filepath.Ext(sf.path))
		tag := parseSubTag(sf.rel)
		name := ""
		for n := 1; n <= 20; n++ {
			cand := stem + tag.suffix() + ext
			if n > 1 {
				cand = stem + tag.suffix() + "." + strconv.Itoa(n) + ext
			}
			if used[cand] {
				continue
			}
			if _, err := s.fs.Lstat(filepath.Join(destDir, cand)); err == nil {
				continue
			}
			name = cand
			break
		}
		if name == "" {
			notes = append(notes, filepath.Base(sf.path)+": no free name for the subtitle, skipped")
			continue
		}
		used[name] = true
		if _, err := s.fs.Transfer(ctx, sf.path, filepath.Join(destDir, name), mode); err != nil {
			notes = append(notes, filepath.Base(sf.path)+": subtitle not imported ("+err.Error()+")")
			continue
		}
		done = append(done, name)
	}
	return done, notes
}
