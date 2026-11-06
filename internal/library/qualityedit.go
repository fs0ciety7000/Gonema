package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Quality profile and scoring rule editing (Settings > Quality). Every change
// is validated by package quality, then saved in one transaction together
// with the custom scores of the files it changes (movie_files.custom_score
// follows the current rules: the library state and the upgrade decisions read
// it).

// Preferred protocols of a profile; "" follows the global setting.
const (
	ProtocolAny     = ""
	ProtocolUsenet  = "usenet"
	ProtocolTorrent = "torrent"
)

// ProfileInput creates or replaces a quality profile.
type ProfileInput struct {
	Name           string
	Items          []quality.Item // best first
	Cutoff         string         // group name or quality ID of an allowed item
	UpgradeAllowed bool
	MinScore       int
	CutoffScore    int // upgrades stop once the file reaches the cutoff and this score
	MinUpgradeStep int
	Languages      []string
	// PreferredProtocol: ProtocolAny, ProtocolUsenet or ProtocolTorrent.
	PreferredProtocol string
	// Sizes are the size limits the profile overrides; the other qualities use
	// the global limits. nil keeps the stored ones (update) or sets none
	// (create).
	Sizes quality.Sizes
	// Scores by scoring rule id; nil keeps the stored ones (update) or sets
	// none (create). A score of 0 removes the entry.
	Scores map[int64]int
}

// ScoringRuleInput creates or replaces a scoring rule.
type ScoringRuleInput struct {
	Name            string
	Description     string
	IncludeInRename bool
	Conditions      []quality.Condition
	// Scores by quality profile id; nil keeps the stored ones (update) or sets
	// none (create). A score of 0 removes the entry.
	Scores map[int64]int
}

// ScoringRule is a stored scoring rule with the score each profile gives it.
type ScoringRule struct {
	db.ScoringRule
	Conditions []quality.Condition
	Scores     map[int64]int // by profile id; profiles without an entry score 0
}

// EditResult reports the side effects of a quality settings change.
type EditResult struct {
	// RescoredFiles is the number of files whose custom score changed.
	RescoredFiles int
}

// inputErrorOf turns validation errors of package quality into one
// plain-language InputError.
func inputErrorOf(field string, err error) error {
	var msgs []string
	for line := range strings.SplitSeq(err.Error(), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, quality.ErrInvalid.Error()+": "))
		if line != "" {
			msgs = append(msgs, line)
		}
	}
	return invalid(field, "%s", strings.Join(msgs, "; "))
}

func validProtocol(p string) bool {
	return p == ProtocolAny || p == ProtocolUsenet || p == ProtocolTorrent
}

// GetQualityProfile returns one profile with its items, sizes, scores and usage.
func (s *Service) GetQualityProfile(ctx context.Context, id int64) (QualityProfile, error) {
	var out QualityProfile
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		row, err := q.GetQualityProfile(ctx, id)
		if err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("quality profile %d does not exist", id)
			}
			return err
		}
		out, err = loadProfileDetail(ctx, q, row)
		return err
	})
	if err != nil {
		return QualityProfile{}, fmt.Errorf("library: get quality profile %d: %w", id, err)
	}
	return out, nil
}

// loadProfileDetail completes a profile row.
func loadProfileDetail(ctx context.Context, q *db.Queries, row db.QualityProfile) (QualityProfile, error) {
	out := QualityProfile{QualityProfile: row, Scores: map[int64]int{}}
	var err error
	if out.Items, err = q.ListQualityProfileItems(ctx, row.ID); err != nil {
		return out, fmt.Errorf("items: %w", err)
	}
	if out.Profile, err = LoadQualityProfile(ctx, q, row.ID); err != nil {
		return out, err
	}
	if out.Sizes, err = q.ListQualityProfileSizes(ctx, row.ID); err != nil {
		return out, fmt.Errorf("sizes: %w", err)
	}
	scores, err := q.ListAllProfileScores(ctx)
	if err != nil {
		return out, fmt.Errorf("scores: %w", err)
	}
	for _, sc := range scores {
		if sc.ProfileID == row.ID {
			out.Scores[sc.RuleID] = int(sc.Score)
		}
	}
	if out.Usage, err = q.GetQualityProfileUsage(ctx, row.ID); err != nil {
		return out, fmt.Errorf("usage: %w", err)
	}
	return out, nil
}

// LoadProfileOverrides returns the size limits a profile overrides and its
// preferred protocol ("" = the global setting). q may be a reader or a
// transaction.
func LoadProfileOverrides(ctx context.Context, q *db.Queries, id int64) (quality.Sizes, string, error) {
	row, err := q.GetQualityProfile(ctx, id)
	if err != nil {
		return nil, "", store.MapError(fmt.Errorf("library: get quality profile %d: %w", id, err))
	}
	rows, err := q.ListQualityProfileSizes(ctx, id)
	if err != nil {
		return nil, "", fmt.Errorf("library: sizes of profile %d: %w", id, err)
	}
	sizes := make(quality.Sizes, len(rows))
	for _, r := range rows {
		sizes[quality.ID(r.Quality)] = quality.SizeLimit{
			MinMBPerMin: r.MinMbPerMin, PreferredMBPerMin: r.PreferredMbPerMin, MaxMBPerMin: r.MaxMbPerMin,
		}
	}
	return sizes, row.PreferredProtocol, nil
}

// EffectiveSizes returns global limits overridden by a profile's.
func EffectiveSizes(global, profile quality.Sizes) quality.Sizes {
	out := maps.Clone(global)
	if out == nil {
		out = quality.Sizes{}
	}
	maps.Copy(out, profile)
	return out
}

// validateProfile checks an input against the rules and returns the profile.
func validateProfile(in *ProfileInput, rules []*quality.Rule, ruleNames map[int64]string) (quality.Profile, error) {
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) > 100 {
		return quality.Profile{}, invalid("name", "the profile name must be at most 100 characters long")
	}
	if !validProtocol(in.PreferredProtocol) {
		return quality.Profile{}, invalid("preferredProtocol", "the preferred protocol must be usenet, torrent or empty (the global setting)")
	}
	p := quality.Profile{
		Name: in.Name, Items: in.Items, Cutoff: in.Cutoff, UpgradeAllowed: in.UpgradeAllowed, MinScore: in.MinScore,
		CutoffScore: in.CutoffScore, MinUpgradeStep: in.MinUpgradeStep, Languages: in.Languages,
	}
	for i := range p.Items {
		p.Items[i].Name = strings.TrimSpace(p.Items[i].Name)
	}
	if len(in.Scores) > 0 {
		p.Scores = make(map[string]int, len(in.Scores))
		for id, sc := range in.Scores {
			name, ok := ruleNames[id]
			if !ok {
				return quality.Profile{}, invalid("scores", "scoring rule %d does not exist", id)
			}
			if sc != 0 {
				p.Scores[name] = sc
			}
		}
	}
	if err := p.Validate(rules); err != nil {
		return quality.Profile{}, inputErrorOf("profile", err)
	}
	if err := in.Sizes.Validate(); err != nil {
		return quality.Profile{}, inputErrorOf("sizes", err)
	}
	return p, nil
}

// ruleNames maps the stored rule ids to their names.
func ruleNames(ctx context.Context, q *db.Queries) (map[int64]string, error) {
	rows, err := q.ListScoringRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list scoring rules: %w", err)
	}
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

func profileConflict(err error, name string) error {
	if errors.Is(store.MapError(err), store.ErrConflict) {
		return &conflictError{InputError{Field: "name", Msg: fmt.Sprintf("a quality profile named %q already exists", name)}}
	}
	return err
}

// saveProfile writes the row values, items, sizes and scores of a profile.
func saveProfile(ctx context.Context, q *db.Queries, id int64, in *ProfileInput, p *quality.Profile) error {
	if err := store.ReplaceQualityProfileItems(ctx, q, id, profileItems(p)); err != nil {
		return err
	}
	if in.Sizes != nil {
		if err := q.DeleteQualityProfileSizes(ctx, id); err != nil {
			return fmt.Errorf("delete sizes: %w", err)
		}
		for _, qid := range slices.Sorted(maps.Keys(in.Sizes)) {
			l := in.Sizes[qid]
			if err := q.InsertQualityProfileSize(ctx, db.InsertQualityProfileSizeParams{
				ProfileID: id, Quality: string(qid), MinMbPerMin: l.MinMBPerMin, PreferredMbPerMin: l.PreferredMBPerMin, MaxMbPerMin: l.MaxMBPerMin,
			}); err != nil {
				return fmt.Errorf("insert size of %s: %w", qid, err)
			}
		}
	}
	for ruleID, sc := range in.Scores {
		if err := setScore(ctx, q, id, ruleID, sc); err != nil {
			return err
		}
	}
	return nil
}

func setScore(ctx context.Context, q *db.Queries, profileID, ruleID int64, score int) error {
	if score == 0 {
		if err := q.DeleteProfileScore(ctx, db.DeleteProfileScoreParams{ProfileID: profileID, RuleID: ruleID}); err != nil {
			return fmt.Errorf("delete score: %w", err)
		}
		return nil
	}
	if err := q.SetProfileScore(ctx, db.SetProfileScoreParams{ProfileID: profileID, RuleID: ruleID, Score: int64(score)}); err != nil {
		return fmt.Errorf("set score: %w", err)
	}
	return nil
}

func languagesJSON(langs []string) (string, error) {
	if langs == nil {
		langs = []string{}
	}
	raw, err := json.Marshal(langs)
	if err != nil {
		return "", fmt.Errorf("encode languages: %w", err)
	}
	return string(raw), nil
}

// CreateQualityProfile creates a profile.
func (s *Service) CreateQualityProfile(ctx context.Context, in ProfileInput) (QualityProfile, EditResult, error) {
	var out QualityProfile
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		rules, err := LoadScoringRules(ctx, q)
		if err != nil {
			return err
		}
		names, err := ruleNames(ctx, q)
		if err != nil {
			return err
		}
		p, err := validateProfile(&in, rules, names)
		if err != nil {
			return err
		}
		langs, err := languagesJSON(p.Languages)
		if err != nil {
			return err
		}
		row, err := q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
			Name: p.Name, UpgradeAllowed: p.UpgradeAllowed, CutoffQuality: p.Cutoff, MinScore: int64(p.MinScore),
			CutoffScore: int64(p.CutoffScore), MinUpgradeStep: int64(p.MinUpgradeStep), Languages: langs,
			PreferredProtocol: in.PreferredProtocol,
		})
		if err != nil {
			return profileConflict(err, p.Name)
		}
		if err := saveProfile(ctx, q, row.ID, &in, &p); err != nil {
			return err
		}
		out, err = loadProfileDetail(ctx, q, row)
		return err
	})
	if err != nil {
		return QualityProfile{}, EditResult{}, fmt.Errorf("library: create quality profile: %w", err)
	}
	s.log.Info("quality profile created", slog.Int64("profile_id", out.ID), slog.String("name", out.Name))
	s.publishQualityChanged()
	return out, EditResult{}, nil
}

// UpdateQualityProfile replaces a profile. Files of its movies are rescored.
func (s *Service) UpdateQualityProfile(ctx context.Context, id int64, in ProfileInput) (QualityProfile, EditResult, error) {
	var out QualityProfile
	var res EditResult
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetQualityProfile(ctx, id); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("quality profile %d does not exist", id)
			}
			return err
		}
		rules, err := LoadScoringRules(ctx, q)
		if err != nil {
			return err
		}
		names, err := ruleNames(ctx, q)
		if err != nil {
			return err
		}
		if in.Scores == nil { // keep: validate the stored scores with the new profile
			in.Scores = map[int64]int{}
			stored, err := q.ListAllProfileScores(ctx)
			if err != nil {
				return fmt.Errorf("scores: %w", err)
			}
			for _, sc := range stored {
				if sc.ProfileID == id {
					in.Scores[sc.RuleID] = int(sc.Score)
				}
			}
		} else {
			for ruleID := range names { // replace: rules left out score 0
				if _, ok := in.Scores[ruleID]; !ok {
					in.Scores[ruleID] = 0
				}
			}
		}
		p, err := validateProfile(&in, rules, names)
		if err != nil {
			return err
		}
		langs, err := languagesJSON(p.Languages)
		if err != nil {
			return err
		}
		row, err := q.UpdateQualityProfile(ctx, db.UpdateQualityProfileParams{
			ID: id, Name: p.Name, UpgradeAllowed: p.UpgradeAllowed, CutoffQuality: p.Cutoff, MinScore: int64(p.MinScore),
			CutoffScore: int64(p.CutoffScore), MinUpgradeStep: int64(p.MinUpgradeStep), Languages: langs,
			PreferredProtocol: in.PreferredProtocol,
		})
		if err != nil {
			return profileConflict(err, p.Name)
		}
		if err := saveProfile(ctx, q, id, &in, &p); err != nil {
			return err
		}
		if res.RescoredFiles, err = rescoreFiles(ctx, q); err != nil {
			return err
		}
		out, err = loadProfileDetail(ctx, q, row)
		return err
	})
	if err != nil {
		return QualityProfile{}, EditResult{}, fmt.Errorf("library: update quality profile %d: %w", id, err)
	}
	s.log.Info("quality profile updated", slog.Int64("profile_id", id), slog.Int("rescored_files", res.RescoredFiles))
	s.publishQualityChanged()
	return out, res, nil
}

// DeleteQualityProfile removes an unused profile. A profile used by movies,
// collections or import lists, or the last profile, is refused (conflict).
func (s *Service) DeleteQualityProfile(ctx context.Context, id int64) error {
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetQualityProfile(ctx, id); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("quality profile %d does not exist", id)
			}
			return err
		}
		u, err := q.GetQualityProfileUsage(ctx, id)
		if err != nil {
			return fmt.Errorf("usage: %w", err)
		}
		if u.Movies+u.Collections+u.ImportLists > 0 {
			return &conflictError{InputError{Field: "id", Msg: fmt.Sprintf(
				"this profile is used by %d movies, %d collections and %d import lists; give them another profile first",
				u.Movies, u.Collections, u.ImportLists)}}
		}
		n, err := q.CountQualityProfiles(ctx)
		if err != nil {
			return fmt.Errorf("count profiles: %w", err)
		}
		if n <= 1 {
			return &conflictError{InputError{Field: "id", Msg: "this is the last quality profile; create another one first"}}
		}
		if _, err := q.DeleteQualityProfile(ctx, id); err != nil {
			return store.MapError(err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: delete quality profile %d: %w", id, err)
	}
	s.log.Info("quality profile deleted", slog.Int64("profile_id", id))
	s.publishQualityChanged()
	return nil
}

func (s *Service) publishQualityChanged() {
	s.publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"setting": "quality"}})
}

// ----------------------------------------------------------------- scoring rules

// ListScoringRules returns every rule, in creation order (the order of the
// score breakdowns), with its conditions and the scores of the profiles.
func (s *Service) ListScoringRules(ctx context.Context) ([]ScoringRule, error) {
	var out []ScoringRule
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		out, err = loadRules(ctx, q, 0)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("library: list scoring rules: %w", err)
	}
	return out, nil
}

// GetScoringRule returns one rule.
func (s *Service) GetScoringRule(ctx context.Context, id int64) (ScoringRule, error) {
	var out []ScoringRule
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		out, err = loadRules(ctx, q, id)
		return err
	})
	if err != nil {
		return ScoringRule{}, fmt.Errorf("library: get scoring rule %d: %w", id, err)
	}
	if len(out) == 0 {
		return ScoringRule{}, notFound("scoring rule %d does not exist", id)
	}
	return out[0], nil
}

// loadRules reads the rules (only id when id > 0).
func loadRules(ctx context.Context, q *db.Queries, id int64) ([]ScoringRule, error) {
	rows, err := q.ListScoringRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	conds, err := q.ListScoringConditions(ctx)
	if err != nil {
		return nil, fmt.Errorf("conditions: %w", err)
	}
	scores, err := q.ListAllProfileScores(ctx)
	if err != nil {
		return nil, fmt.Errorf("scores: %w", err)
	}
	out := make([]ScoringRule, 0, len(rows))
	byID := make(map[int64]int, len(rows))
	for _, r := range rows {
		if id > 0 && r.ID != id {
			continue
		}
		byID[r.ID] = len(out)
		out = append(out, ScoringRule{ScoringRule: r, Conditions: []quality.Condition{}, Scores: map[int64]int{}})
	}
	for _, c := range conds {
		i, ok := byID[c.RuleID]
		if !ok {
			continue
		}
		var v conditionValue
		if err := json.Unmarshal([]byte(c.Value), &v); err != nil {
			return nil, fmt.Errorf("decode condition %d: %w", c.ID, err)
		}
		out[i].Conditions = append(out[i].Conditions, quality.Condition{
			Kind: quality.ConditionKind(c.Kind), Value: v.Value, Min: v.Min, Max: v.Max, Negate: c.Negate, Required: c.Required,
		})
	}
	for _, sc := range scores {
		if i, ok := byID[sc.RuleID]; ok {
			out[i].Scores[sc.ProfileID] = int(sc.Score)
		}
	}
	return out, nil
}

// validateRule compiles a rule input with package quality.
func validateRule(in *ScoringRuleInput) (*quality.Rule, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if len(in.Name) > 100 {
		return nil, invalid("name", "the rule name must be at most 100 characters long")
	}
	if len(in.Description) > 1000 {
		return nil, invalid("description", "the description must be at most 1000 characters long")
	}
	if len(in.Conditions) > 100 {
		return nil, invalid("conditions", "a rule has at most 100 conditions")
	}
	for i := range in.Conditions {
		in.Conditions[i].Value = strings.TrimSpace(in.Conditions[i].Value)
	}
	r, err := quality.NewRule(in.Name, in.Description, in.Conditions...)
	if err != nil {
		return nil, inputErrorOf("conditions", err)
	}
	return r, nil
}

func ruleConflict(err error, name string) error {
	if errors.Is(store.MapError(err), store.ErrConflict) {
		return &conflictError{InputError{Field: "name", Msg: fmt.Sprintf("a scoring rule named %q already exists", name)}}
	}
	return err
}

// saveRule writes the conditions and scores of a rule.
func saveRule(ctx context.Context, q *db.Queries, id int64, in *ScoringRuleInput) error {
	if err := q.DeleteScoringConditions(ctx, id); err != nil {
		return fmt.Errorf("delete conditions: %w", err)
	}
	for _, c := range in.Conditions {
		raw, err := json.Marshal(conditionValue{Value: c.Value, Min: c.Min, Max: c.Max})
		if err != nil {
			return fmt.Errorf("encode condition: %w", err)
		}
		if err := q.InsertScoringCondition(ctx, db.InsertScoringConditionParams{
			RuleID: id, Kind: string(c.Kind), Negate: c.Negate, Required: c.Required, Value: string(raw),
		}); err != nil {
			return fmt.Errorf("insert condition: %w", err)
		}
	}
	if in.Scores == nil {
		return nil
	}
	profiles, err := q.ListQualityProfiles(ctx)
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}
	known := make(map[int64]bool, len(profiles))
	for _, p := range profiles {
		known[p.ID] = true
	}
	for pid := range in.Scores {
		if !known[pid] {
			return invalid("scores", "quality profile %d does not exist", pid)
		}
	}
	for _, p := range profiles { // replace: profiles left out score 0
		if err := setScore(ctx, q, p.ID, id, in.Scores[p.ID]); err != nil {
			return err
		}
	}
	return nil
}

// CreateScoringRule creates a rule. Files are rescored when a profile scores it.
func (s *Service) CreateScoringRule(ctx context.Context, in ScoringRuleInput) (ScoringRule, EditResult, error) {
	var out ScoringRule
	var res EditResult
	if _, err := validateRule(&in); err != nil {
		return ScoringRule{}, res, err
	}
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		row, err := q.CreateScoringRule(ctx, db.CreateScoringRuleParams{Name: in.Name, Description: in.Description, IncludeInRename: in.IncludeInRename})
		if err != nil {
			return ruleConflict(err, in.Name)
		}
		if err := saveRule(ctx, q, row.ID, &in); err != nil {
			return err
		}
		if res.RescoredFiles, err = rescoreFiles(ctx, q); err != nil {
			return err
		}
		list, err := loadRules(ctx, q, row.ID)
		if err != nil {
			return err
		}
		out = list[0]
		return nil
	})
	if err != nil {
		return ScoringRule{}, EditResult{}, fmt.Errorf("library: create scoring rule: %w", err)
	}
	s.log.Info("scoring rule created", slog.Int64("rule_id", out.ID), slog.String("name", out.Name), slog.Int("rescored_files", res.RescoredFiles))
	s.publishQualityChanged()
	return out, res, nil
}

// UpdateScoringRule replaces a rule; files are rescored.
func (s *Service) UpdateScoringRule(ctx context.Context, id int64, in ScoringRuleInput) (ScoringRule, EditResult, error) {
	var out ScoringRule
	var res EditResult
	if _, err := validateRule(&in); err != nil {
		return ScoringRule{}, res, err
	}
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.UpdateScoringRule(ctx, db.UpdateScoringRuleParams{
			ID: id, Name: in.Name, Description: in.Description, IncludeInRename: in.IncludeInRename,
		}); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("scoring rule %d does not exist", id)
			}
			return ruleConflict(err, in.Name)
		}
		if err := saveRule(ctx, q, id, &in); err != nil {
			return err
		}
		var err error
		if res.RescoredFiles, err = rescoreFiles(ctx, q); err != nil {
			return err
		}
		list, err := loadRules(ctx, q, id)
		if err != nil {
			return err
		}
		out = list[0]
		return nil
	})
	if err != nil {
		return ScoringRule{}, EditResult{}, fmt.Errorf("library: update scoring rule %d: %w", id, err)
	}
	s.log.Info("scoring rule updated", slog.Int64("rule_id", id), slog.Int("rescored_files", res.RescoredFiles))
	s.publishQualityChanged()
	return out, res, nil
}

// DeleteScoringRule removes a rule and its scores; files are rescored.
func (s *Service) DeleteScoringRule(ctx context.Context, id int64) (EditResult, error) {
	var res EditResult
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.DeleteScoringRule(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound("scoring rule %d does not exist", id)
		}
		res.RescoredFiles, err = rescoreFiles(ctx, q)
		return err
	})
	if err != nil {
		return EditResult{}, fmt.Errorf("library: delete scoring rule %d: %w", id, err)
	}
	s.log.Info("scoring rule deleted", slog.Int64("rule_id", id), slog.Int("rescored_files", res.RescoredFiles))
	s.publishQualityChanged()
	return res, nil
}

// RuleMatches counts the library files a draft rule matches, with the first
// movie ids (limit), from the stored release names: what the editor shows as
// "matched 41 films in your library".
func (s *Service) RuleMatches(ctx context.Context, conds []quality.Condition, limit int) (count int, movieIDs []int64, err error) {
	in := ScoringRuleInput{Name: "draft", Conditions: conds}
	r, err := validateRule(&in)
	if err != nil {
		return 0, nil, err
	}
	files, err := s.st.Read().ListMovieFilesForScoring(ctx)
	if err != nil {
		return 0, nil, store.MapError(fmt.Errorf("library: list files: %w", err))
	}
	movieIDs = []int64{}
	for i := range files {
		f := &files[i]
		var p parser.ParsedRelease
		_ = json.Unmarshal([]byte(f.Parsed), &p) // stored by the importer; a bad value matches the name only
		if r.Matches(&quality.Release{Title: f.SceneName, Parsed: &p, Size: f.Size}) {
			count++
			if len(movieIDs) < limit {
				movieIDs = append(movieIDs, f.MovieID)
			}
		}
	}
	return count, movieIDs, nil
}

// rescoreFiles recomputes the custom score of every file with the current
// rules and profiles, and stores the scores that changed. It runs in the
// transaction of the change. It returns the number of files rescored.
func rescoreFiles(ctx context.Context, q *db.Queries) (int, error) {
	files, err := q.ListMovieFilesForScoring(ctx)
	if err != nil {
		return 0, fmt.Errorf("list files: %w", err)
	}
	if len(files) == 0 {
		return 0, nil
	}
	rules, err := LoadScoringRules(ctx, q)
	if err != nil {
		return 0, err
	}
	profiles := map[int64]*quality.Profile{}
	n := 0
	for i := range files {
		f := &files[i]
		p, ok := profiles[f.QualityProfileID]
		if !ok {
			prof, err := LoadQualityProfile(ctx, q, f.QualityProfileID)
			if err != nil {
				return 0, err
			}
			p = &prof
			profiles[f.QualityProfileID] = p
		}
		var parsed parser.ParsedRelease
		_ = json.Unmarshal([]byte(f.Parsed), &parsed) // stored by the importer; a bad value scores the name only
		b := quality.Score(p, rules, &quality.Release{Title: f.SceneName, Parsed: &parsed, Size: f.Size})
		if int64(b.Total) == f.CustomScore {
			continue
		}
		if _, err := q.UpdateMovieFileScore(ctx, db.UpdateMovieFileScoreParams{ID: f.ID, CustomScore: int64(b.Total)}); err != nil {
			return 0, fmt.Errorf("update score of file %d: %w", f.ID, err)
		}
		n++
	}
	return n, nil
}

// QualitySizes returns the global size limits: the defaults overridden by
// the stored ones (quality_sizes).
func (s *Service) QualitySizes(ctx context.Context) (quality.Sizes, error) {
	return LoadQualitySizes(ctx, s.st.Read())
}
