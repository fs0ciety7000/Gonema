package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// settingScoringSeeded records that the starter scoring rules were seeded once,
// so that a user who deletes every rule does not get them back at the next start.
const settingScoringSeeded = "quality.starter_rules_seeded"

// conditionValue is the JSON stored in scoring_conditions.value: the parts of a
// quality.Condition that have no column of their own.
type conditionValue struct {
	Value string  `json:"value,omitempty"`
	Min   float64 `json:"min,omitempty"`
	Max   float64 `json:"max,omitempty"`
}

// SeedScoringRules inserts the starter scoring rules and the starter scores of
// the profiles named like a starter profile, once per database (first run, or
// first start after the upgrade that introduced them). It returns how many rules
// were created.
func (s *Service) SeedScoringRules(ctx context.Context) (int, error) {
	created := 0
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetSetting(ctx, settingScoringSeeded); err == nil {
			return nil
		} else if !errors.Is(store.MapError(err), store.ErrNotFound) {
			return fmt.Errorf("read %s: %w", settingScoringSeeded, err)
		}
		n, err := q.CountScoringRules(ctx)
		if err != nil {
			return fmt.Errorf("count scoring rules: %w", err)
		}
		if n == 0 {
			if created, err = insertStarterRules(ctx, q); err != nil {
				return err
			}
		}
		return q.SetSetting(ctx, db.SetSettingParams{Key: settingScoringSeeded, Value: "true"})
	})
	if err != nil {
		return 0, fmt.Errorf("library: seed scoring rules: %w", err)
	}
	return created, nil
}

func insertStarterRules(ctx context.Context, q *db.Queries) (int, error) {
	ruleIDs := make(map[string]int64)
	rules := quality.StarterRules()
	for _, r := range rules {
		row, err := q.CreateScoringRule(ctx, db.CreateScoringRuleParams{Name: r.Name(), Description: r.Description()})
		if err != nil {
			return 0, fmt.Errorf("create scoring rule %q: %w", r.Name(), err)
		}
		ruleIDs[r.Name()] = row.ID
		for _, c := range r.Conditions() {
			raw, err := json.Marshal(conditionValue{Value: c.Value, Min: c.Min, Max: c.Max})
			if err != nil {
				return 0, fmt.Errorf("encode condition of %q: %w", r.Name(), err)
			}
			if err := q.InsertScoringCondition(ctx, db.InsertScoringConditionParams{
				RuleID: row.ID, Kind: string(c.Kind), Negate: c.Negate, Required: c.Required, Value: string(raw),
			}); err != nil {
				return 0, fmt.Errorf("insert condition of %q: %w", r.Name(), err)
			}
		}
	}
	profiles, err := q.ListQualityProfiles(ctx)
	if err != nil {
		return 0, fmt.Errorf("list profiles: %w", err)
	}
	for _, p := range profiles {
		starter, ok := quality.StarterProfile(p.Name)
		if !ok {
			continue
		}
		for name, score := range starter.Scores {
			if err := q.SetProfileScore(ctx, db.SetProfileScoreParams{ProfileID: p.ID, RuleID: ruleIDs[name], Score: int64(score)}); err != nil {
				return 0, fmt.Errorf("set score of %q in %q: %w", name, p.Name, err)
			}
		}
	}
	return len(rules), nil
}

// LoadScoringRules builds the scoring rules stored in the database, in creation
// order. q may be a reader or a transaction.
func LoadScoringRules(ctx context.Context, q *db.Queries) ([]*quality.Rule, error) {
	rows, err := q.ListScoringRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list scoring rules: %w", err)
	}
	conds, err := q.ListScoringConditions(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list scoring conditions: %w", err)
	}
	byRule := make(map[int64][]quality.Condition, len(rows))
	for _, c := range conds {
		var v conditionValue
		if err := json.Unmarshal([]byte(c.Value), &v); err != nil {
			return nil, fmt.Errorf("library: decode condition %d: %w", c.ID, err)
		}
		byRule[c.RuleID] = append(byRule[c.RuleID], quality.Condition{
			Kind: quality.ConditionKind(c.Kind), Value: v.Value, Min: v.Min, Max: v.Max,
			Negate: c.Negate, Required: c.Required,
		})
	}
	out := make([]*quality.Rule, 0, len(rows))
	for _, r := range rows {
		rule, err := quality.NewRule(r.Name, r.Description, byRule[r.ID]...)
		if err != nil {
			return nil, fmt.Errorf("library: scoring rule %q: %w", r.Name, err)
		}
		out = append(out, rule)
	}
	return out, nil
}

// LoadQualityProfile builds the quality profile id with its items and scores.
// Items keep the stored order; members of a group share a position.
func LoadQualityProfile(ctx context.Context, q *db.Queries, id int64) (quality.Profile, error) {
	row, err := q.GetQualityProfile(ctx, id)
	if err != nil {
		return quality.Profile{}, store.MapError(fmt.Errorf("library: get quality profile %d: %w", id, err))
	}
	items, err := q.ListQualityProfileItems(ctx, id)
	if err != nil {
		return quality.Profile{}, fmt.Errorf("library: list items of profile %d: %w", id, err)
	}
	scores, err := q.ListProfileScores(ctx, id)
	if err != nil {
		return quality.Profile{}, fmt.Errorf("library: list scores of profile %d: %w", id, err)
	}
	p := quality.Profile{
		Name: row.Name, Cutoff: row.CutoffQuality, UpgradeAllowed: row.UpgradeAllowed,
		MinScore: int(row.MinScore), CutoffScore: int(row.CutoffScore), MinUpgradeStep: int(row.MinUpgradeStep),
	}
	if err := json.Unmarshal([]byte(row.Languages), &p.Languages); err != nil {
		return quality.Profile{}, fmt.Errorf("library: decode languages of profile %d: %w", id, err)
	}
	if len(p.Languages) == 0 {
		p.Languages = nil
	}
	// Rows are ordered by (position, quality): the order of the members of a
	// group is not stored. Groups are rebuilt best first in catalog order, as the
	// starter profiles list them; members of a group are equivalent anyway.
	for _, it := range items {
		n := len(p.Items)
		if n > 0 && int64(n-1) == it.Position {
			p.Items[n-1].Qualities = append(p.Items[n-1].Qualities, quality.ID(it.Quality))
			continue
		}
		item := quality.Item{Qualities: []quality.ID{quality.ID(it.Quality)}, Allowed: it.Allowed}
		if it.GroupName != nil {
			item.Name = *it.GroupName
		}
		p.Items = append(p.Items, item)
	}
	for i := range p.Items {
		slices.SortStableFunc(p.Items[i].Qualities, func(a, b quality.ID) int { return catalogIndex(b) - catalogIndex(a) })
	}
	// The cutoff column may name a member of a group (movie_state accepts it):
	// quality.Profile resolves it to the group itself, so it is kept as stored.
	if len(scores) > 0 {
		p.Scores = make(map[string]int, len(scores))
		for _, sc := range scores {
			p.Scores[sc.RuleName] = int(sc.Score)
		}
	}
	return p, nil
}

// catalogIndex returns the intrinsic rank of id in the quality catalog
// (higher is better), -1 for an unknown ID.
func catalogIndex(id quality.ID) int {
	if q, ok := quality.Lookup(id); ok {
		return q.Rank
	}
	return -1
}

// LoadQualitySizes returns the default size limits overridden by the stored ones.
func LoadQualitySizes(ctx context.Context, q *db.Queries) (quality.Sizes, error) {
	rows, err := q.ListQualitySizes(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list quality sizes: %w", err)
	}
	sizes := quality.DefaultSizes()
	for _, r := range rows {
		l := quality.SizeLimit{MinMBPerMin: r.MinMbPerMin}
		if r.PreferredMbPerMin != nil {
			l.PreferredMBPerMin = *r.PreferredMbPerMin
		}
		if r.MaxMbPerMin != nil {
			l.MaxMBPerMin = *r.MaxMbPerMin
		}
		sizes[quality.ID(r.Quality)] = l
	}
	return sizes, nil
}
