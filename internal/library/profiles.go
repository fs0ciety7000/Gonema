package library

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// profileItems converts profile items to storage form: members of a group share
// the group's position and name.
func profileItems(p *quality.Profile) []store.ProfileItem {
	var out []store.ProfileItem
	for pos, it := range p.Items {
		for _, q := range it.Qualities {
			out = append(out, store.ProfileItem{
				Quality: string(q), Position: int64(pos), GroupName: it.Name, Allowed: it.Allowed,
			})
		}
	}
	return out
}

// SeedQualityProfiles inserts the starter quality profiles when the database
// has none (first run). It returns how many were created. Their scores are
// seeded with the scoring rules (SeedScoringRules).
func (s *Service) SeedQualityProfiles(ctx context.Context) (int, error) {
	created := 0
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		existing, err := q.ListQualityProfiles(ctx)
		if err != nil {
			return fmt.Errorf("list profiles: %w", err)
		}
		if len(existing) > 0 {
			return nil
		}
		for _, p := range quality.StarterProfiles() {
			langs := p.Languages
			if langs == nil {
				langs = []string{}
			}
			raw, err := json.Marshal(langs)
			if err != nil {
				return fmt.Errorf("encode languages of %q: %w", p.Name, err)
			}
			row, err := q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
				Name: p.Name, UpgradeAllowed: p.UpgradeAllowed, CutoffQuality: p.Cutoff,
				MinScore: int64(p.MinScore), CutoffScore: int64(p.CutoffScore),
				MinUpgradeStep: int64(p.MinUpgradeStep), Languages: string(raw),
			})
			if err != nil {
				return fmt.Errorf("create profile %q: %w", p.Name, err)
			}
			if err := store.ReplaceQualityProfileItems(ctx, q, row.ID, profileItems(&p)); err != nil {
				return err
			}
			created++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("library: seed quality profiles: %w", err)
	}
	return created, nil
}

// QualityProfile is a profile with its items.
type QualityProfile struct {
	db.QualityProfile
	Items []db.QualityProfileItem
}

// ListQualityProfiles returns the quality profiles with their items.
func (s *Service) ListQualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	var out []QualityProfile
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		rows, err := q.ListQualityProfiles(ctx)
		if err != nil {
			return err
		}
		out = make([]QualityProfile, 0, len(rows))
		for _, r := range rows {
			items, err := q.ListQualityProfileItems(ctx, r.ID)
			if err != nil {
				return err
			}
			out = append(out, QualityProfile{QualityProfile: r, Items: items})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("library: list quality profiles: %w", err)
	}
	return out, nil
}
