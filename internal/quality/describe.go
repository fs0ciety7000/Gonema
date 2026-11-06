package quality

import (
	"slices"
	"strconv"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// Input says how a rule editor asks for the value of a condition kind.
type Input string

// Inputs of KindInfo.
const (
	InputChoice Input = "choice" // one of KindInfo.Values
	InputText   Input = "text"   // free text (group, indexer name, flag)
	InputRegex  Input = "regex"  // a regular expression
	InputRange  Input = "range"  // Min and Max
)

// KindInfo describes a condition kind for a rule editor.
type KindInfo struct {
	Kind   ConditionKind `json:"kind"`
	Label  string        `json:"label"`
	Input  Input         `json:"input"`
	Values []string      `json:"values,omitempty"` // InputChoice only
	Unit   string        `json:"unit,omitempty"`   // InputRange only: "GB" or "year"
}

// ConditionKinds lists every condition kind with what its value can be, in the
// order an editor shows them. Each call returns fresh slices.
func ConditionKinds() []KindInfo {
	return []KindInfo{
		{Kind: KindReleaseGroup, Label: "Release group", Input: InputText},
		{Kind: KindSource, Label: "Source", Input: InputChoice, Values: append(parser.SourceValues(), parser.ModifierValues()...)},
		{Kind: KindResolution, Label: "Resolution", Input: InputChoice, Values: parser.ResolutionValues()},
		{Kind: KindCodec, Label: "Video codec", Input: InputChoice, Values: parser.CodecValues()},
		{Kind: KindHDR, Label: "HDR format", Input: InputChoice, Values: parser.HDRValues()},
		{Kind: KindAudio, Label: "Audio", Input: InputChoice, Values: append(parser.AudioValues(), "atmos")},
		{Kind: KindEdition, Label: "Edition", Input: InputChoice, Values: parser.EditionValues()},
		{Kind: KindLanguage, Label: "Language", Input: InputChoice, Values: append(parser.LanguageValues(), "multi")},
		{Kind: KindTitleRegex, Label: "Title matches", Input: InputRegex},
		{Kind: KindIndexer, Label: "Indexer", Input: InputText},
		{Kind: KindIndexerFlag, Label: "Indexer flag", Input: InputText},
		{Kind: KindSizeRange, Label: "Size", Input: InputRange, Unit: "GB"},
		{Kind: KindYearRange, Label: "Year", Input: InputRange, Unit: "year"},
	}
}

// ValidKind reports whether k is a known condition kind.
func ValidKind(k ConditionKind) bool {
	return slices.ContainsFunc(ConditionKinds(), func(i KindInfo) bool { return i.Kind == k })
}

// Describe returns the conditions of a rule as one plain English sentence,
// following the matching semantics of Rule.Matches: required conditions are
// all needed; non-required conditions of one kind are alternatives ("or"),
// distinct kinds are all needed ("and"). Example: "the release has HDR format
// dv or hdr10 and is not from MiNiPiX".
func Describe(conds []Condition) string {
	var groups []string
	byKind := map[ConditionKind][]string{}
	var order []ConditionKind
	for _, c := range conds {
		p := phrase(c)
		if c.Required {
			groups = append(groups, p)
			continue
		}
		if _, ok := byKind[c.Kind]; !ok {
			order = append(order, c.Kind)
		}
		byKind[c.Kind] = append(byKind[c.Kind], p)
	}
	for _, k := range order {
		groups = append(groups, joinOr(byKind[k]))
	}
	if len(groups) == 0 {
		return ""
	}
	return "the release " + strings.Join(groups, " and ")
}

func joinOr(list []string) string {
	if len(list) == 1 {
		return list[0]
	}
	return strings.Join(list[:len(list)-1], ", ") + " or " + list[len(list)-1]
}

func phrase(c Condition) string {
	v := strings.TrimSpace(c.Value)
	pos, neg := "", ""
	switch c.Kind {
	case KindTitleRegex:
		pos, neg = "has a title matching /"+v+"/", "has a title not matching /"+v+"/"
	case KindReleaseGroup:
		pos, neg = "is from "+v, "is not from "+v
	case KindSource:
		pos, neg = "has source "+v, "does not have source "+v
	case KindResolution:
		pos, neg = "is "+v, "is not "+v
	case KindCodec:
		pos, neg = "is encoded in "+v, "is not encoded in "+v
	case KindHDR:
		pos, neg = "is "+v, "is not "+v
	case KindAudio:
		pos, neg = "has "+v+" audio", "has no "+v+" audio"
	case KindEdition:
		pos, neg = "is the "+v+" edition", "is not the "+v+" edition"
	case KindLanguage:
		if strings.EqualFold(v, "multi") {
			pos, neg = "has several audio languages", "does not have several audio languages"
		} else {
			pos, neg = "is in "+v, "is not in "+v
		}
	case KindIndexerFlag:
		pos, neg = "is flagged "+v, "is not flagged "+v
	case KindIndexer:
		pos, neg = "comes from "+v, "does not come from "+v
	case KindSizeRange:
		r := rangeText(c.Min, c.Max, " GB")
		pos, neg = "weighs "+r, "does not weigh "+r
	case KindYearRange:
		r := rangeText(c.Min, c.Max, "")
		pos, neg = "is from the years "+r, "is not from the years "+r
	default:
		pos, neg = string(c.Kind)+" "+v, "not "+string(c.Kind)+" "+v
	}
	if c.Negate {
		return neg
	}
	return pos
}

func rangeText(lo, hi float64, unit string) string {
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	switch {
	case hi == 0:
		return "at least " + f(lo) + unit
	case lo == 0:
		return "at most " + f(hi) + unit
	}
	return f(lo) + " to " + f(hi) + unit
}
