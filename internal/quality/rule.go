package quality

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// ConditionKind is what a condition tests. Values are stored in the database.
type ConditionKind string

// Condition kinds. Value formats are documented on Condition.
const (
	KindTitleRegex   ConditionKind = "title_regex"
	KindReleaseGroup ConditionKind = "release_group"
	KindSource       ConditionKind = "source"
	KindResolution   ConditionKind = "resolution"
	KindCodec        ConditionKind = "codec"
	KindHDR          ConditionKind = "hdr"
	KindAudio        ConditionKind = "audio"
	KindEdition      ConditionKind = "edition"
	KindLanguage     ConditionKind = "language"
	KindIndexerFlag  ConditionKind = "indexer_flag"
	KindIndexer      ConditionKind = "indexer"
	KindSizeRange    ConditionKind = "size_range"
	KindYearRange    ConditionKind = "year_range"
)

// maxRegexLen bounds title_regex patterns. Go regexps run in linear time, so this
// only limits memory and keeps patterns reviewable.
const maxRegexLen = 1000

// Condition is one test of a scoring rule.
//
// Value by kind:
//   - title_regex: a Go (RE2) regular expression matched case-insensitively against
//     the raw release title.
//   - release_group: a group name, compared case-insensitively.
//   - source: a parser source ("cam", "webdl", "bluray"...) or modifier ("remux",
//     "brdisk", "dvdr", "regional", "rawhd").
//   - resolution: "480p", "576p", "720p", "1080p" or "2160p".
//   - codec: "h264", "h265", "av1", "vc1", "xvid", "divx", "mpeg2", "vp9".
//   - hdr: "hdr10", "hdr10+", "dv", "hlg" or "sdr"; matches if any HDR format of the
//     release is that one.
//   - audio: an audio format ("truehd", "dts-x", "eac3"...) or "atmos".
//   - edition: an edition flag ("directors", "extended", "imax"...).
//   - language: a BCP 47 code as produced by the parser ("en", "fr", "pt-BR"...) or
//     "multi" (MULTi, DL, DUAL).
//   - indexer_flag: a flag reported by the indexer ("freeleech", "internal"...),
//     compared case-insensitively.
//   - indexer: the name of the indexer offering the release, compared
//     case-insensitively (a renamed indexer no longer matches).
//
// size_range uses Min and Max in GB, year_range in years, both inclusive; Max 0
// means no upper bound. A release of unknown size or year never matches a range
// (before Negate).
type Condition struct {
	Kind     ConditionKind `json:"kind"`
	Value    string        `json:"value,omitempty"`
	Min      float64       `json:"min,omitempty"`
	Max      float64       `json:"max,omitempty"`
	Negate   bool          `json:"negate,omitempty"`
	Required bool          `json:"required,omitempty"`
}

// condition is a validated Condition with its value decoded.
type condition struct {
	Condition
	re       *regexp.Regexp
	lower    string // release_group, indexer_flag, indexer
	source   parser.Source
	modifier parser.Modifier // source condition on a modifier when != ModifierNone
	res      parser.Resolution
	codec    parser.Codec
	hdr      parser.HDRFormat
	audio    parser.AudioFormat
	isAtmos  bool
	edition  parser.Edition
	lang     parser.Language
	isMulti  bool
	min, max float64 // size in bytes, or years
}

// Rule is an immutable, validated scoring rule. Build it with NewRule.
type Rule struct {
	name        string
	description string
	conds       []condition
	kinds       []ConditionKind // distinct kinds of non-required conditions, in first-seen order
}

// NewRule validates and compiles a rule. Errors wrap ErrInvalid and name the
// offending condition (1-based) so they can be shown to the user as is.
func NewRule(name, description string, conds ...Condition) (*Rule, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: scoring rule name is empty", ErrInvalid)
	}
	if len(conds) == 0 {
		return nil, fmt.Errorf("%w: scoring rule %q has no condition", ErrInvalid, name)
	}
	r := &Rule{name: name, description: description, conds: make([]condition, 0, len(conds))}
	var errs []error
	for i, c := range conds {
		cc, err := compile(c)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: scoring rule %q, condition %d (%s): %w", ErrInvalid, name, i+1, c.Kind, err))
			continue
		}
		r.conds = append(r.conds, cc)
		if !c.Required && !slices.Contains(r.kinds, c.Kind) {
			r.kinds = append(r.kinds, c.Kind)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return r, nil
}

// MustRule is NewRule for rules known to be valid at compile time; it panics on error.
func MustRule(name, description string, conds ...Condition) *Rule {
	r, err := NewRule(name, description, conds...)
	if err != nil {
		panic(err)
	}
	return r
}

// Name returns the unique rule name.
func (r *Rule) Name() string { return r.name }

// Description returns the rule description.
func (r *Rule) Description() string { return r.description }

// Conditions returns a copy of the rule conditions.
func (r *Rule) Conditions() []Condition {
	out := make([]Condition, len(r.conds))
	for i := range r.conds {
		out[i] = r.conds[i].Condition
	}
	return out
}

// Matches reports whether the rule matches the release:
//  1. every Required condition matches, and
//  2. for each kind present among the non-required conditions, at least one
//     non-required condition of that kind matches.
//
// Negate inverts a single condition before it is combined. So conditions of one
// kind are alternatives (OR) and different kinds must all hold (AND): "codec h265,
// resolution 720p, resolution 1080p" reads "h265 AND (720p OR 1080p)".
func (r *Rule) Matches(rel *Release) bool {
	for i := range r.conds {
		c := &r.conds[i]
		if c.Required && !c.test(rel) {
			return false
		}
	}
	for _, k := range r.kinds {
		ok := false
		for i := range r.conds {
			c := &r.conds[i]
			if !c.Required && c.Kind == k && c.test(rel) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// ValidateRules reports duplicate rule names (nil rules are errors too).
func ValidateRules(rules []*Rule) error {
	var errs []error
	seen := make(map[string]bool, len(rules))
	for i, r := range rules {
		if r == nil {
			errs = append(errs, fmt.Errorf("%w: scoring rule %d is nil", ErrInvalid, i+1))
			continue
		}
		if seen[r.name] {
			errs = append(errs, fmt.Errorf("%w: scoring rule name %q is used twice", ErrInvalid, r.name))
		}
		seen[r.name] = true
	}
	return errors.Join(errs...)
}

func compile(c Condition) (condition, error) {
	cc := condition{Condition: c}
	v := strings.TrimSpace(c.Value)
	needValue := c.Kind != KindSizeRange && c.Kind != KindYearRange
	if needValue && v == "" {
		return cc, errors.New("value is empty")
	}
	var err error
	switch c.Kind {
	case KindTitleRegex:
		if len(c.Value) > maxRegexLen {
			return cc, fmt.Errorf("pattern is longer than %d characters", maxRegexLen)
		}
		cc.re, err = regexp.Compile("(?i)" + c.Value)
		if err != nil {
			var se *syntax.Error
			if errors.As(err, &se) {
				return cc, fmt.Errorf("invalid regular expression %q: %s", c.Value, se.Code)
			}
			return cc, fmt.Errorf("invalid regular expression %q: %w", c.Value, err)
		}
	case KindReleaseGroup, KindIndexerFlag, KindIndexer:
		cc.lower = strings.ToLower(v)
	case KindSource:
		if cc.source.UnmarshalText([]byte(v)) != nil {
			if cc.modifier.UnmarshalText([]byte(v)) != nil || cc.modifier == parser.ModifierNone {
				return cc, fmt.Errorf("unknown source %q", v)
			}
		}
	case KindResolution:
		err = decode(&cc.res, v, "resolution")
	case KindCodec:
		err = decode(&cc.codec, v, "codec")
	case KindHDR:
		err = decode(&cc.hdr, v, "HDR format")
	case KindAudio:
		if strings.EqualFold(v, "atmos") {
			cc.isAtmos = true
		} else {
			err = decode(&cc.audio, v, "audio format")
		}
	case KindEdition:
		err = decode(&cc.edition, v, "edition")
		if err == nil && cc.edition == 0 {
			err = fmt.Errorf("unknown edition %q", v)
		}
	case KindLanguage:
		if strings.EqualFold(v, "multi") {
			cc.isMulti = true
		} else {
			err = decode(&cc.lang, v, "language")
		}
	case KindSizeRange:
		err = checkRange(c.Min, c.Max)
		cc.min, cc.max = c.Min*GB, c.Max*GB
	case KindYearRange:
		err = checkRange(c.Min, c.Max)
		if err == nil && (c.Min != math.Trunc(c.Min) || c.Max != math.Trunc(c.Max)) {
			err = errors.New("years must be whole numbers")
		}
		cc.min, cc.max = c.Min, c.Max
	default:
		return cc, fmt.Errorf("unknown condition kind %q", c.Kind)
	}
	return cc, err
}

type textUnmarshaler interface{ UnmarshalText([]byte) error }

func decode(dst textUnmarshaler, v, what string) error {
	if dst.UnmarshalText([]byte(v)) != nil {
		return fmt.Errorf("unknown %s %q", what, v)
	}
	return nil
}

func checkRange(lo, hi float64) error {
	switch {
	case math.IsNaN(lo) || math.IsNaN(hi) || math.IsInf(lo, 0) || math.IsInf(hi, 0):
		return errors.New("range bounds must be finite numbers")
	case lo < 0 || hi < 0:
		return errors.New("range bounds must not be negative")
	case hi != 0 && hi < lo:
		return fmt.Errorf("range maximum %g is below the minimum %g", hi, lo)
	}
	return nil
}

// test evaluates the condition, Negate included.
func (c *condition) test(rel *Release) bool {
	return c.match(rel) != c.Negate
}

func (c *condition) match(rel *Release) bool {
	p := rel.Parsed
	switch c.Kind {
	case KindTitleRegex:
		return c.re.MatchString(rel.Title)
	case KindReleaseGroup:
		return strings.EqualFold(p.ReleaseGroup, c.lower)
	case KindSource:
		if c.modifier != parser.ModifierNone {
			return p.Modifier == c.modifier
		}
		return p.Source == c.source
	case KindResolution:
		return p.Resolution == c.res
	case KindCodec:
		return p.VideoCodec == c.codec
	case KindHDR:
		return slices.Contains(p.HDR, c.hdr)
	case KindAudio:
		if c.isAtmos {
			return p.Atmos
		}
		return slices.Contains(p.Audio, c.audio)
	case KindEdition:
		return p.Edition.Has(c.edition)
	case KindLanguage:
		if c.isMulti {
			return p.Multi
		}
		return slices.Contains(p.Languages, c.lang)
	case KindIndexerFlag:
		for _, f := range rel.IndexerFlags {
			if strings.EqualFold(f, c.lower) {
				return true
			}
		}
		return false
	case KindIndexer:
		return rel.Indexer != "" && strings.EqualFold(rel.Indexer, c.lower)
	case KindSizeRange:
		return rel.Size > 0 && inRange(float64(rel.Size), c.min, c.max)
	case KindYearRange:
		return p.Year > 0 && inRange(float64(p.Year), c.min, c.max)
	}
	return false
}

func inRange(v, lo, hi float64) bool { return v >= lo && (hi == 0 || v <= hi) }
