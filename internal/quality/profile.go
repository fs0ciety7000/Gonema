package quality

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// ErrInvalid is wrapped by every validation error of this package.
var ErrInvalid = errors.New("quality: invalid")

// Special values of Profile.Languages.
const (
	LanguageAny      = "any"      // any language is accepted
	LanguageOriginal = "original" // the original language of the movie
)

// Item is one entry of a profile: a single quality, or a named group of qualities
// that the profile treats as equivalent (same rank).
type Item struct {
	Name      string `json:"name,omitempty"` // group name; empty for a single quality
	Qualities []ID   `json:"qualities"`
	Allowed   bool   `json:"allowed"`
}

// IsGroup reports whether the item is a named group.
func (it *Item) IsGroup() bool { return it.Name != "" }

// Label returns the group name, or the display name of the single quality.
func (it *Item) Label() string {
	if it.IsGroup() || len(it.Qualities) == 0 {
		return it.Name
	}
	return it.Qualities[0].Name()
}

// Profile is a quality profile. Items are ordered from most to least preferred,
// as displayed to the user.
type Profile struct {
	Name  string `json:"name"`
	Items []Item `json:"items"`
	// Cutoff names the item at or above which quality upgrades stop: a group
	// name, a single quality, or a member quality of a group (which stands for
	// its group: members of a group share one rank). The database stores it the
	// same way (quality_profiles.cutoff_quality, see the movie_state view).
	Cutoff         string `json:"cutoff"`
	UpgradeAllowed bool   `json:"upgradeAllowed"`
	// MinScore rejects releases whose custom score is lower.
	MinScore int `json:"minScore"`
	// CutoffScore: once the file reaches the cutoff quality AND this score, it is final.
	CutoffScore int `json:"cutoffScore"`
	// MinUpgradeStep is the minimum score gain for an upgrade at the same quality rank.
	// Values below 1 are treated as 1.
	MinUpgradeStep int `json:"minUpgradeStep"`
	// Languages lists wanted audio languages: BCP 47 codes as produced by the parser
	// ("en", "fr", "pt-BR"...), LanguageOriginal, or LanguageAny alone. Empty means any.
	Languages []string `json:"languages,omitempty"`
	// Scores maps scoring rule names to the score the profile gives them.
	Scores map[string]int `json:"scores,omitempty"`
}

// find returns the index of the item holding id, or -1.
func (p *Profile) find(id ID) int {
	for i := range p.Items {
		for _, q := range p.Items[i].Qualities {
			if q == id {
				return i
			}
		}
	}
	return -1
}

// Rank returns the rank of id in the profile (higher is better; qualities of one group
// share a rank) and whether it is allowed. A quality absent from the profile has rank 0
// and is not allowed.
func (p *Profile) Rank(id ID) (rank int, allowed bool) {
	i := p.find(id)
	if i < 0 {
		return 0, false
	}
	return len(p.Items) - i, p.Items[i].Allowed
}

// Allowed reports whether the profile allows id.
func (p *Profile) Allowed(id ID) bool {
	_, ok := p.Rank(id)
	return ok
}

// ItemOf returns the item holding id.
func (p *Profile) ItemOf(id ID) (Item, bool) {
	if i := p.find(id); i >= 0 {
		return p.Items[i], true
	}
	return Item{}, false
}

// cutoffIndex returns the index of the cutoff item, or -1: the group named
// Cutoff, else the item holding the quality Cutoff (a single quality or a
// member of a group). Group names are never quality IDs (see Validate), so
// the two cannot be confused.
func (p *Profile) cutoffIndex() int {
	if p.Cutoff == "" {
		return -1
	}
	for i := range p.Items {
		if it := &p.Items[i]; it.IsGroup() && it.Name == p.Cutoff {
			return i
		}
	}
	return p.find(ID(p.Cutoff))
}

// CutoffRank returns the rank of the cutoff item, or 0 if the cutoff is not in the profile.
func (p *Profile) CutoffRank() int {
	if i := p.cutoffIndex(); i >= 0 {
		return len(p.Items) - i
	}
	return 0
}

// CutoffLabel returns the display name of the cutoff item.
func (p *Profile) CutoffLabel() string {
	if i := p.cutoffIndex(); i >= 0 {
		return p.Items[i].Label()
	}
	return p.Cutoff
}

// UpgradeStep returns MinUpgradeStep, at least 1.
func (p *Profile) UpgradeStep() int { return max(p.MinUpgradeStep, 1) }

// WantsAnyLanguage reports whether the profile accepts every language.
func (p *Profile) WantsAnyLanguage() bool {
	return len(p.Languages) == 0 || (len(p.Languages) == 1 && p.Languages[0] == LanguageAny)
}

// MaxGroupName is the longest group name.
const MaxGroupName = 100

// qualityNamed reports whether a lower-case name is a quality ID or display
// name of the catalog.
func qualityNamed(name string) bool {
	for i := range catalog {
		if q := &catalog[i]; strings.EqualFold(string(q.ID), name) || strings.EqualFold(q.Name, name) {
			return true
		}
	}
	return false
}

// Validate reports every problem of the profile, joined, each wrapping ErrInvalid.
// rules is the rule set that Scores refers to; a score for an unknown rule is an
// error. Pass nil to skip that check.
func (p *Profile) Validate(rules []*Rule) error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: profile %q: %s", ErrInvalid, p.Name, fmt.Sprintf(format, args...)))
	}
	if strings.TrimSpace(p.Name) == "" {
		add("name is empty")
	}
	if len(p.Items) == 0 {
		add("has no qualities")
	}
	seenQ := make(map[ID]string, len(catalog))
	seenG := make(map[string]bool)
	anyAllowed := false
	for i := range p.Items {
		it := &p.Items[i]
		anyAllowed = anyAllowed || it.Allowed
		switch {
		case len(it.Qualities) == 0:
			add("item %d (%q) has no qualities", i+1, it.Name)
		case len(it.Qualities) > 1 && !it.IsGroup():
			add("item %d groups %d qualities but has no group name", i+1, len(it.Qualities))
		}
		if it.IsGroup() {
			key := strings.ToLower(strings.TrimSpace(it.Name))
			switch {
			case key == "":
				add("item %d has a blank group name", i+1)
			case len(it.Name) > MaxGroupName:
				add("group name %q is longer than %d characters", it.Name, MaxGroupName)
			case seenG[key]:
				add("group name %q is used twice (names are compared without case)", it.Name)
			case qualityNamed(key):
				add("group name %q is also the name of a quality: choose another name", it.Name)
			}
			seenG[key] = true
		}
		for _, q := range it.Qualities {
			if !q.Valid() {
				add("unknown quality %q", q)
				continue
			}
			if prev, dup := seenQ[q]; dup {
				add("quality %s appears twice (in %s and %s)", q.Name(), prev, it.Label())
				continue
			}
			seenQ[q] = it.Label()
		}
	}
	if len(p.Items) > 0 && !anyAllowed {
		add("allows no quality")
	}
	if ci := p.cutoffIndex(); ci < 0 {
		add("cutoff %q is not a quality or group of the profile", p.Cutoff)
	} else if !p.Items[ci].Allowed {
		add("cutoff %s is not an allowed quality", p.Items[ci].Label())
	}
	if p.MinUpgradeStep < 0 {
		add("minimum upgrade step %d is negative", p.MinUpgradeStep)
	}
	errs = append(errs, p.validateLanguages()...)
	if rules != nil {
		known := make(map[string]bool, len(rules))
		for _, r := range rules {
			known[r.Name()] = true
		}
		for _, name := range sortedKeys(p.Scores) {
			if !known[name] {
				add("score given to unknown scoring rule %q", name)
			}
		}
	}
	return errors.Join(errs...)
}

func (p *Profile) validateLanguages() []error {
	var errs []error
	seen := make(map[string]bool, len(p.Languages))
	for _, l := range p.Languages {
		if seen[l] {
			errs = append(errs, fmt.Errorf("%w: profile %q: language %q is listed twice", ErrInvalid, p.Name, l))
			continue
		}
		seen[l] = true
		switch l {
		case LanguageOriginal:
		case LanguageAny:
			if len(p.Languages) > 1 {
				errs = append(errs, fmt.Errorf("%w: profile %q: language %q cannot be combined with other languages", ErrInvalid, p.Name, l))
			}
		default:
			var lang parser.Language
			if err := lang.UnmarshalText([]byte(l)); err != nil {
				errs = append(errs, fmt.Errorf("%w: profile %q: unknown language %q", ErrInvalid, p.Name, l))
			}
		}
	}
	return errs
}

// sortedKeys returns the keys of m in increasing order.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
