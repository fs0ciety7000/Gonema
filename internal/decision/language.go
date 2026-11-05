package decision

import (
	"slices"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// languageNames are English display names for messages.
var languageNames = map[parser.Language]string{
	parser.LanguageEnglish: "English", parser.LanguageFrench: "French", parser.LanguageFrenchCanadian: "French (Canada)",
	parser.LanguageGerman: "German", parser.LanguageItalian: "Italian", parser.LanguageSpanish: "Spanish",
	parser.LanguageLatino: "Spanish (Latin America)", parser.LanguagePortuguese: "Portuguese",
	parser.LanguageBrazilian: "Portuguese (Brazil)", parser.LanguageDutch: "Dutch", parser.LanguageFlemish: "Flemish",
	parser.LanguagePolish: "Polish", parser.LanguageRussian: "Russian", parser.LanguageUkrainian: "Ukrainian",
	parser.LanguageCzech: "Czech", parser.LanguageSlovak: "Slovak", parser.LanguageHungarian: "Hungarian",
	parser.LanguageRomanian: "Romanian", parser.LanguageBulgarian: "Bulgarian", parser.LanguageGreek: "Greek",
	parser.LanguageTurkish: "Turkish", parser.LanguageDanish: "Danish", parser.LanguageSwedish: "Swedish",
	parser.LanguageNorwegian: "Norwegian", parser.LanguageFinnish: "Finnish", parser.LanguageIcelandic: "Icelandic",
	parser.LanguageEstonian: "Estonian", parser.LanguageLithuanian: "Lithuanian", parser.LanguageLatvian: "Latvian",
	parser.LanguageNordic: "Nordic", parser.LanguageJapanese: "Japanese", parser.LanguageKorean: "Korean",
	parser.LanguageChinese: "Chinese", parser.LanguageHindi: "Hindi", parser.LanguageTamil: "Tamil",
	parser.LanguageTelugu: "Telugu", parser.LanguageThai: "Thai", parser.LanguageVietnamese: "Vietnamese",
	parser.LanguageArabic: "Arabic", parser.LanguageHebrew: "Hebrew", parser.LanguagePersian: "Persian",
	parser.LanguageCatalan: "Catalan",
}

func languageName(l parser.Language) string {
	if n, ok := languageNames[l]; ok {
		return n
	}
	return l.String()
}

// wantedLanguages is a profile language list resolved for one movie.
type wantedLanguages struct {
	isAny       bool
	langs       []parser.Language // explicit codes, and the original language when known
	isOriginal  bool              // "original" is wanted
	isOrigKnown bool              // the original language of the movie is known
	label       string            // for messages, e.g. "English, original"
}

func resolveWanted(p *quality.Profile, original parser.Language) wantedLanguages {
	w := wantedLanguages{isAny: p.WantsAnyLanguage(), isOrigKnown: original != 0}
	if w.isAny {
		return w
	}
	labels := make([]string, 0, len(p.Languages))
	for _, s := range p.Languages {
		if s == quality.LanguageOriginal {
			w.isOriginal = true
			if original != 0 {
				w.langs = append(w.langs, original)
				labels = append(labels, "original ("+languageName(original)+")")
			} else {
				labels = append(labels, "original")
			}
			continue
		}
		var l parser.Language
		if l.UnmarshalText([]byte(s)) == nil {
			w.langs = append(w.langs, l)
			labels = append(labels, languageName(l))
		}
	}
	w.label = strings.Join(labels, ", ")
	return w
}

// audioLanguages returns the audio languages a release is assumed to carry:
//   - the tagged languages;
//   - plus the original language for MULTi/DL/DUAL releases (a multi-language
//     release carries the original track);
//   - an untagged release is assumed to be in the original language, or English
//     when the original language is unknown (scene convention).
//
// The second result is false when the languages are only assumed.
func audioLanguages(p *parser.ParsedRelease, original parser.Language) ([]parser.Language, bool) {
	if len(p.Languages) == 0 {
		if original == 0 {
			original = parser.LanguageEnglish
		}
		return []parser.Language{original}, false
	}
	if p.Multi && original != 0 && !slices.Contains(p.Languages, original) {
		out := make([]parser.Language, 0, len(p.Languages)+1)
		out = append(out, p.Languages...)
		return append(out, original), true
	}
	return p.Languages, true
}

// satisfied reports whether any of langs is wanted. When the original language is
// wanted but unknown, an untagged or multi-language release satisfies it.
func (w *wantedLanguages) satisfied(langs []parser.Language, untaggedOrMulti bool) bool {
	if w.isAny {
		return true
	}
	for _, l := range langs {
		if slices.Contains(w.langs, l) {
			return true
		}
	}
	return w.isOriginal && !w.isOrigKnown && untaggedOrMulti
}

func languageList(langs []parser.Language) string {
	names := make([]string, len(langs))
	for i, l := range langs {
		names[i] = languageName(l)
	}
	return strings.Join(names, ", ")
}
