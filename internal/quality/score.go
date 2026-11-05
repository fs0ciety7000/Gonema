package quality

import "github.com/fs0ciety7000/gonema/internal/parser"

// Release is what scoring rules look at.
type Release struct {
	Title        string                // raw release title, for title_regex
	Parsed       *parser.ParsedRelease // must not be nil
	Size         int64                 // bytes; 0 if unknown
	IndexerFlags []string              // e.g. "freeleech", "internal"
}

// RuleScore is the contribution of one matched rule.
type RuleScore struct {
	Rule  string `json:"rule"`
	Score int    `json:"score"` // 0 when the profile does not score the rule
}

// Breakdown is a custom score with the rules that produced it.
type Breakdown struct {
	Total int         `json:"total"`
	Rules []RuleScore `json:"rules,omitempty"` // matched rules, in rule order
}

// Score evaluates every rule against the release and sums the scores the profile
// gives to the matched ones. Matched rules the profile does not score are listed
// with a zero contribution, so the user can see them.
func Score(p *Profile, rules []*Rule, rel *Release) Breakdown {
	var b Breakdown
	for _, r := range rules {
		if !r.Matches(rel) {
			continue
		}
		s := p.Scores[r.name]
		b.Total += s
		b.Rules = append(b.Rules, RuleScore{Rule: r.name, Score: s})
	}
	return b
}
