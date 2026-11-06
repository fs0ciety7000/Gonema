package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// Settings > Quality (web UI lot 3): the quality catalog, quality profiles and
// scoring rules (custom formats), and the live preview of a release name.

// SizeRange is a size range in MB per minute of runtime (MB = MiB).
type SizeRange struct {
	MinMbPerMin       float64 `json:"minMbPerMin" minimum:"0"`
	PreferredMbPerMin float64 `json:"preferredMbPerMin" minimum:"0"`
	MaxMbPerMin       float64 `json:"maxMbPerMin" minimum:"0" doc:"0 = no upper bound"`
}

// QualityDefinition is a quality of the fixed catalog.
type QualityDefinition struct {
	ID         string    `json:"id" doc:"Stable id, e.g. bluray-1080p"`
	Name       string    `json:"name" doc:"Display name, e.g. Bluray-1080p"`
	Source     string    `json:"source"`
	Modifier   string    `json:"modifier"`
	Resolution string    `json:"resolution"`
	Rank       int       `json:"rank" doc:"Intrinsic rank, higher is better"`
	Sizes      SizeRange `json:"sizes" doc:"Global size limits (profiles may override them)"`
}

// ProfileEntry is one row of a profile: a quality, or a named group of
// qualities the profile treats as equal.
type ProfileEntry struct {
	Group     string   `json:"group,omitempty" maxLength:"100" doc:"Group name; empty for a single quality. The qualities of a group are equal (one rank): among them the best score wins. Names are unique without case and differ from quality names; rename, merge or split by sending the new entries (update the cutoff with them, or name a member quality as the cutoff)"`
	Qualities []string `json:"qualities" minItems:"1" maxItems:"30" doc:"Quality ids"`
	Allowed   bool     `json:"allowed"`
}

// ProfileSize is the size range of a quality in a profile.
type ProfileSize struct {
	Quality     string `json:"quality"`
	QualityName string `json:"qualityName"`
	SizeRange
	Custom bool `json:"custom" doc:"The profile overrides the global limits"`
}

// ProfileSizeInput overrides the size range of one quality.
type ProfileSizeInput struct {
	Quality string `json:"quality" minLength:"1" maxLength:"32"`
	SizeRange
}

// ProfileScore is the score a profile gives a scoring rule.
type ProfileScore struct {
	RuleID int64  `json:"ruleId"`
	Rule   string `json:"rule"`
	Score  int    `json:"score"`
}

// ProfileScoreInput sets the score of a rule in the profile.
type ProfileScoreInput struct {
	RuleID int64 `json:"ruleId" minimum:"1"`
	Score  int   `json:"score" minimum:"-100000" maximum:"100000"`
}

// ProfileUsage counts what uses a profile.
type ProfileUsage struct {
	Movies      int64 `json:"movies"`
	Collections int64 `json:"collections"`
	ImportLists int64 `json:"importLists"`
}

// QualityProfileDetail is a quality profile with everything the editor shows.
// It extends QualityProfile (same fields first).
type QualityProfileDetail struct {
	QualityProfile
	CutoffLabel       string         `json:"cutoffLabel" doc:"Display name of the cutoff item"`
	PreferredProtocol string         `json:"preferredProtocol" enum:",usenet,torrent" doc:"Empty: the global setting (Settings > Acquisition)"`
	Entries           []ProfileEntry `json:"entries" doc:"Best first; the editor's rows"`
	Sizes             []ProfileSize  `json:"sizes" doc:"Effective size range of every quality of the profile, in entry order"`
	Scores            []ProfileScore `json:"scores" doc:"Every scoring rule with this profile's score (0 when unscored), in rule order"`
	Usage             ProfileUsage   `json:"usage"`
}

// QualityProfileInput creates or replaces a quality profile.
type QualityProfileInput struct {
	Name              string               `json:"name" minLength:"1" maxLength:"100"`
	Entries           []ProfileEntry       `json:"entries" minItems:"1" maxItems:"40" doc:"Best first"`
	Cutoff            string               `json:"cutoff" minLength:"1" maxLength:"100" doc:"Group name or quality id at which quality upgrades stop"`
	UpgradeAllowed    bool                 `json:"upgradeAllowed"`
	MinScore          int                  `json:"minScore" minimum:"-100000" maximum:"100000" doc:"Releases scoring lower are rejected"`
	CutoffScore       int                  `json:"cutoffScore" minimum:"-100000" maximum:"100000" doc:"Upgrade until: once the file reaches the cutoff quality and this score, upgrades stop"`
	MinUpgradeStep    int                  `json:"minUpgradeStep" minimum:"0" maximum:"100000" doc:"Minimum score gain of an upgrade at the same quality"`
	Languages         []string             `json:"languages,omitempty" maxItems:"20" doc:"Wanted audio languages (en, fr, pt-BR...), original, or any; empty = any"`
	PreferredProtocol string               `json:"preferredProtocol,omitempty" enum:",usenet,torrent"`
	Sizes             *[]ProfileSizeInput  `json:"sizes,omitempty" maxItems:"40" doc:"Size overrides; absent keeps the stored ones (none on create); qualities left out use the global limits"`
	Scores            *[]ProfileScoreInput `json:"scores,omitempty" maxItems:"500" doc:"Scores by rule; absent keeps the stored ones; rules left out score 0"`
}

func (in *QualityProfileInput) profile() library.ProfileInput {
	out := library.ProfileInput{
		Name: in.Name, Cutoff: in.Cutoff, UpgradeAllowed: in.UpgradeAllowed, MinScore: in.MinScore,
		CutoffScore: in.CutoffScore, MinUpgradeStep: in.MinUpgradeStep, Languages: in.Languages,
		PreferredProtocol: in.PreferredProtocol,
	}
	for _, e := range in.Entries {
		it := quality.Item{Name: e.Group, Allowed: e.Allowed}
		for _, q := range e.Qualities {
			it.Qualities = append(it.Qualities, quality.ID(q))
		}
		out.Items = append(out.Items, it)
	}
	if in.Sizes != nil {
		out.Sizes = quality.Sizes{}
		for _, sz := range *in.Sizes {
			out.Sizes[quality.ID(sz.Quality)] = quality.SizeLimit{
				MinMBPerMin: sz.MinMbPerMin, PreferredMBPerMin: sz.PreferredMbPerMin, MaxMBPerMin: sz.MaxMbPerMin,
			}
		}
	}
	if in.Scores != nil {
		out.Scores = map[int64]int{}
		for _, sc := range *in.Scores {
			out.Scores[sc.RuleID] = sc.Score
		}
	}
	return out
}

// QualityProfileSaved is a saved profile.
type QualityProfileSaved struct {
	Profile       QualityProfileDetail `json:"profile"`
	RescoredFiles int                  `json:"rescoredFiles" doc:"Files whose custom score changed with this edit"`
}

// ScoringCondition is one test of a scoring rule.
type ScoringCondition struct {
	Kind     string  `json:"kind" enum:"title_regex,release_group,source,resolution,codec,hdr,audio,edition,language,indexer_flag,indexer,size_range,year_range"`
	Value    string  `json:"value,omitempty" maxLength:"1000" doc:"Every kind but size_range and year_range; see GET /scoring-rules/kinds"`
	Min      float64 `json:"min,omitempty" minimum:"0" doc:"size_range (GB) and year_range"`
	Max      float64 `json:"max,omitempty" minimum:"0" doc:"0 = no upper bound"`
	Negate   bool    `json:"negate,omitempty" doc:"The condition must not match"`
	Required bool    `json:"required,omitempty" doc:"Must match; conditions of one kind that are not required are alternatives (or)"`
}

func conditionsOf(in []ScoringCondition) []quality.Condition {
	out := make([]quality.Condition, 0, len(in))
	for _, c := range in {
		out = append(out, quality.Condition{Kind: quality.ConditionKind(c.Kind), Value: c.Value, Min: c.Min, Max: c.Max, Negate: c.Negate, Required: c.Required})
	}
	return out
}

func conditionsView(in []quality.Condition) []ScoringCondition {
	out := make([]ScoringCondition, 0, len(in))
	for _, c := range in {
		out = append(out, ScoringCondition{Kind: string(c.Kind), Value: c.Value, Min: c.Min, Max: c.Max, Negate: c.Negate, Required: c.Required})
	}
	return out
}

// ProfileRuleScore is the score a profile gives the rule.
type ProfileRuleScore struct {
	ProfileID int64  `json:"profileId"`
	Profile   string `json:"profile"`
	Score     int    `json:"score"`
}

// ScoringRule is a custom format: conditions on the parsed release and the
// score each profile gives it.
type ScoringRule struct {
	ID              int64              `json:"id"`
	Name            string             `json:"name"`
	Description     string             `json:"description"`
	IncludeInRename bool               `json:"includeInRename" doc:"Available to the {Score:rules} naming token"`
	Conditions      []ScoringCondition `json:"conditions"`
	ReadsAs         string             `json:"readsAs" doc:"The conditions in plain English, e.g. \"the release is dv and is not from MiNiPiX\""`
	Scores          []ProfileRuleScore `json:"scores" doc:"Every profile, by name, with its score (0 when unscored)"`
}

// RuleScoreInput sets the score a profile gives the rule.
type RuleScoreInput struct {
	ProfileID int64 `json:"profileId" minimum:"1"`
	Score     int   `json:"score" minimum:"-100000" maximum:"100000"`
}

// ScoringRuleInput creates or replaces a scoring rule.
type ScoringRuleInput struct {
	Name            string             `json:"name" minLength:"1" maxLength:"100"`
	Description     string             `json:"description,omitempty" maxLength:"1000"`
	IncludeInRename bool               `json:"includeInRename,omitempty"`
	Conditions      []ScoringCondition `json:"conditions" minItems:"1" maxItems:"100"`
	Scores          *[]RuleScoreInput  `json:"scores,omitempty" maxItems:"200" doc:"Scores by profile; absent keeps the stored ones; profiles left out score 0"`
}

func (in *ScoringRuleInput) rule() library.ScoringRuleInput {
	out := library.ScoringRuleInput{Name: in.Name, Description: in.Description, IncludeInRename: in.IncludeInRename, Conditions: conditionsOf(in.Conditions)}
	if in.Scores != nil {
		out.Scores = map[int64]int{}
		for _, sc := range *in.Scores {
			out.Scores[sc.ProfileID] = sc.Score
		}
	}
	return out
}

// ScoringRuleSaved is a saved rule.
type ScoringRuleSaved struct {
	Rule          ScoringRule `json:"rule"`
	RescoredFiles int         `json:"rescoredFiles" doc:"Files whose custom score changed with this edit"`
}

// ConditionKindInfo describes a condition kind for the rule editor.
type ConditionKindInfo struct {
	Kind   string   `json:"kind"`
	Label  string   `json:"label"`
	Input  string   `json:"input" enum:"choice,text,regex,range"`
	Values []string `json:"values" doc:"Choices of a choice input"`
	Unit   string   `json:"unit,omitempty" doc:"range only: GB or year"`
}

// RuleMatchMovie is a library movie a rule matches.
type RuleMatchMovie struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	Year          int64   `json:"year"`
	PosterURL     *string `json:"posterUrl"`
	DominantColor *string `json:"dominantColor"`
}

// RuleMatches is how many library files a rule matches.
type RuleMatches struct {
	Count  int              `json:"count" doc:"Files of the library the conditions match"`
	Movies []RuleMatchMovie `json:"movies" doc:"The first matching movies"`
}

// PreviewInput is a release name to test.
type PreviewInput struct {
	Title        string   `json:"title" minLength:"1" maxLength:"500"`
	ProfileID    int64    `json:"profileId,omitempty" minimum:"0" doc:"Profile to score with; default the movie's"`
	MovieID      int64    `json:"movieId,omitempty" minimum:"0" doc:"Also decide against this movie and its file"`
	Size         int64    `json:"size,omitempty" minimum:"0" doc:"Bytes, for size rules and limits"`
	Indexer      string   `json:"indexer,omitempty" maxLength:"100"`
	IndexerFlags []string `json:"indexerFlags,omitempty" maxItems:"20"`
	Protocol     string   `json:"protocol,omitempty" enum:"usenet,torrent" default:"usenet"`
}

func (s *Server) profileDetail(p *library.QualityProfile, global quality.Sizes, rules []library.ScoringRule) QualityProfileDetail {
	d := QualityProfileDetail{
		QualityProfile: qualityProfileOf(p), CutoffLabel: p.Profile.CutoffLabel(), PreferredProtocol: p.PreferredProtocol,
		Entries: make([]ProfileEntry, 0, len(p.Profile.Items)), Sizes: []ProfileSize{}, Scores: make([]ProfileScore, 0, len(rules)),
		Usage: ProfileUsage{Movies: p.Usage.Movies, Collections: p.Usage.Collections, ImportLists: p.Usage.ImportLists},
	}
	custom := make(map[string]quality.SizeLimit, len(p.Sizes))
	for _, sz := range p.Sizes {
		custom[sz.Quality] = quality.SizeLimit{MinMBPerMin: sz.MinMbPerMin, PreferredMBPerMin: sz.PreferredMbPerMin, MaxMBPerMin: sz.MaxMbPerMin}
	}
	for _, it := range p.Profile.Items {
		e := ProfileEntry{Group: it.Name, Allowed: it.Allowed, Qualities: make([]string, 0, len(it.Qualities))}
		for _, q := range it.Qualities {
			e.Qualities = append(e.Qualities, string(q))
			l, ok := custom[string(q)]
			if !ok {
				l = global[q]
			}
			d.Sizes = append(d.Sizes, ProfileSize{
				Quality: string(q), QualityName: q.Name(), Custom: ok,
				SizeRange: SizeRange{MinMbPerMin: l.MinMBPerMin, PreferredMbPerMin: l.PreferredMBPerMin, MaxMbPerMin: l.MaxMBPerMin},
			})
		}
		d.Entries = append(d.Entries, e)
	}
	for _, r := range rules {
		d.Scores = append(d.Scores, ProfileScore{RuleID: r.ID, Rule: r.Name, Score: p.Scores[r.ID]})
	}
	return d
}

func ruleOf(r *library.ScoringRule, profiles []library.QualityProfile) ScoringRule {
	out := ScoringRule{
		ID: r.ID, Name: r.Name, Description: r.Description, IncludeInRename: r.IncludeInRename,
		Conditions: conditionsView(r.Conditions), ReadsAs: quality.Describe(r.Conditions), Scores: make([]ProfileRuleScore, 0, len(profiles)),
	}
	for i := range profiles {
		out.Scores = append(out.Scores, ProfileRuleScore{ProfileID: profiles[i].ID, Profile: profiles[i].Name, Score: r.Scores[profiles[i].ID]})
	}
	return out
}

// qualityContext reads what the profile and rule views need.
func (s *Server) qualityContext(ctx context.Context) (quality.Sizes, []library.ScoringRule, []library.QualityProfile, error) {
	global, err := s.d.Library.QualitySizes(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	rules, err := s.d.Library.ListScoringRules(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	profiles, err := s.d.Library.ListQualityProfiles(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	return global, rules, profiles, nil
}

func (s *Server) savedProfile(ctx context.Context, p *library.QualityProfile, res library.EditResult) (*struct{ Body QualityProfileSaved }, error) {
	global, rules, _, err := s.qualityContext(ctx)
	if err != nil {
		return nil, s.problem(ctx, err, false)
	}
	return &struct{ Body QualityProfileSaved }{QualityProfileSaved{Profile: s.profileDetail(p, global, rules), RescoredFiles: res.RescoredFiles}}, nil
}

func (s *Server) savedRule(ctx context.Context, r *library.ScoringRule, res library.EditResult) (*struct{ Body ScoringRuleSaved }, error) {
	profiles, err := s.d.Library.ListQualityProfiles(ctx)
	if err != nil {
		return nil, s.problem(ctx, err, false)
	}
	return &struct{ Body ScoringRuleSaved }{ScoringRuleSaved{Rule: ruleOf(r, profiles), RescoredFiles: res.RescoredFiles}}, nil
}

func (s *Server) qualityRoutes() {
	tags := []string{"Quality"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-qualities", Method: http.MethodGet, Path: "/qualities", Summary: "The quality catalog",
		Description: "Every quality, worst first, with the global size limits.", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []QualityDefinition }, error) {
		global, err := s.d.Library.QualitySizes(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		all := quality.All()
		out := make([]QualityDefinition, 0, len(all))
		for _, q := range all {
			l := global[q.ID]
			out = append(out, QualityDefinition{
				ID: string(q.ID), Name: q.Name, Source: q.Source.String(), Modifier: q.Modifier.String(), Resolution: q.Resolution.String(),
				Rank: q.Rank, Sizes: SizeRange{MinMbPerMin: l.MinMBPerMin, PreferredMbPerMin: l.PreferredMBPerMin, MaxMbPerMin: l.MaxMBPerMin},
			})
		}
		return &struct{ Body []QualityDefinition }{out}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "list-quality-profiles", Method: http.MethodGet, Path: "/quality-profiles",
		Summary: "List quality profiles", Description: "By name, with the editor's details (entries, sizes, scores, usage).", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []QualityProfileDetail }, error) {
		global, rules, profiles, err := s.qualityContext(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := make([]QualityProfileDetail, 0, len(profiles))
		for i := range profiles {
			out = append(out, s.profileDetail(&profiles[i], global, rules))
		}
		return &struct{ Body []QualityProfileDetail }{out}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "get-quality-profile", Method: http.MethodGet, Path: "/quality-profiles/{id}", Summary: "Get a quality profile", Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body QualityProfileDetail }, error) {
		p, err := s.d.Library.GetQualityProfile(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		global, rules, _, err := s.qualityContext(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body QualityProfileDetail }{s.profileDetail(&p, global, rules)}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "create-quality-profile", Method: http.MethodPost, Path: "/quality-profiles", Summary: "Create a quality profile",
		Description: "Validated by the quality engine: every problem is reported at once (422). A duplicate name is a conflict (409).",
		Tags:        tags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct{ Body QualityProfileInput }) (*struct{ Body QualityProfileSaved }, error) {
		p, res, err := s.d.Library.CreateQualityProfile(ctx, in.Body.profile())
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return s.savedProfile(ctx, &p, res)
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "update-quality-profile", Method: http.MethodPut, Path: "/quality-profiles/{id}", Summary: "Replace a quality profile",
		Description: "The files of the library are rescored with the new scores (rescoredFiles).", Tags: tags,
	}, func(ctx context.Context, in *struct {
		IDParam
		Body QualityProfileInput
	}) (*struct{ Body QualityProfileSaved }, error) {
		p, res, err := s.d.Library.UpdateQualityProfile(ctx, in.ID, in.Body.profile())
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return s.savedProfile(ctx, &p, res)
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "delete-quality-profile", Method: http.MethodDelete, Path: "/quality-profiles/{id}", Summary: "Delete an unused quality profile",
		Description: "Refused with 409 while movies, collections or import lists use it, or when it is the last profile.",
		Tags:        tags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		if err := s.d.Library.DeleteQualityProfile(ctx, in.ID); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return nil, nil
	})

	rTags := []string{"Scoring rules"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-scoring-rules", Method: http.MethodGet, Path: "/scoring-rules", Summary: "List scoring rules",
		Description: "In creation order (the order of score breakdowns), with conditions, a plain-English reading and the score of every profile.",
		Tags:        rTags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []ScoringRule }, error) {
		_, rules, profiles, err := s.qualityContext(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := make([]ScoringRule, 0, len(rules))
		for i := range rules {
			out = append(out, ruleOf(&rules[i], profiles))
		}
		return &struct{ Body []ScoringRule }{out}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "list-condition-kinds", Method: http.MethodGet, Path: "/scoring-rules/kinds", Summary: "Condition kinds of the rule editor",
		Description: "Each kind with its input type and, for choices, the values the parser knows.", Tags: rTags,
	}, func(context.Context, *struct{}) (*struct{ Body []ConditionKindInfo }, error) {
		kinds := quality.ConditionKinds()
		out := make([]ConditionKindInfo, 0, len(kinds))
		for _, k := range kinds {
			v := k.Values
			if v == nil {
				v = []string{}
			}
			out = append(out, ConditionKindInfo{Kind: string(k.Kind), Label: k.Label, Input: string(k.Input), Values: v, Unit: k.Unit})
		}
		return &struct{ Body []ConditionKindInfo }{out}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "get-scoring-rule", Method: http.MethodGet, Path: "/scoring-rules/{id}", Summary: "Get a scoring rule", Tags: rTags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body ScoringRule }, error) {
		r, err := s.d.Library.GetScoringRule(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		profiles, err := s.d.Library.ListQualityProfiles(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body ScoringRule }{ruleOf(&r, profiles)}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "create-scoring-rule", Method: http.MethodPost, Path: "/scoring-rules", Summary: "Create a scoring rule",
		Description: "Conditions are validated (and regular expressions compiled) by the quality engine; errors name the condition (422).",
		Tags:        rTags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct{ Body ScoringRuleInput }) (*struct{ Body ScoringRuleSaved }, error) {
		r, res, err := s.d.Library.CreateScoringRule(ctx, in.Body.rule())
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return s.savedRule(ctx, &r, res)
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "update-scoring-rule", Method: http.MethodPut, Path: "/scoring-rules/{id}", Summary: "Replace a scoring rule",
		Description: "The files of the library are rescored (rescoredFiles).", Tags: rTags,
	}, func(ctx context.Context, in *struct {
		IDParam
		Body ScoringRuleInput
	}) (*struct{ Body ScoringRuleSaved }, error) {
		r, res, err := s.d.Library.UpdateScoringRule(ctx, in.ID, in.Body.rule())
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return s.savedRule(ctx, &r, res)
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "delete-scoring-rule", Method: http.MethodDelete, Path: "/scoring-rules/{id}", Summary: "Delete a scoring rule",
		Description: "Its scores go with it; the files of the library are rescored.", Tags: rTags,
	}, func(ctx context.Context, in *IDParam) (*struct {
		Body struct {
			RescoredFiles int `json:"rescoredFiles"`
		}
	}, error) {
		res, err := s.d.Library.DeleteScoringRule(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &struct {
			Body struct {
				RescoredFiles int `json:"rescoredFiles"`
			}
		}{}
		out.Body.RescoredFiles = res.RescoredFiles
		return out, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "match-scoring-rule", Method: http.MethodPost, Path: "/scoring-rules/matches", Summary: "Library files a draft rule matches",
		Description: "Tests conditions (saved or not) against the release names of the library files: \"matched 41 films\".", Tags: rTags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Conditions []ScoringCondition `json:"conditions" minItems:"1" maxItems:"100"`
			Limit      int                `json:"limit,omitempty" minimum:"0" maximum:"100" default:"12" doc:"Movies returned"`
		}
	}) (*struct{ Body RuleMatches }, error) {
		n, ids, err := s.d.Library.RuleMatches(ctx, conditionsOf(in.Body.Conditions), in.Body.Limit)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := RuleMatches{Count: n, Movies: make([]RuleMatchMovie, 0, len(ids))}
		for _, id := range ids {
			m, err := s.d.Library.GetMovie(ctx, id)
			if err != nil {
				continue // removed meanwhile
			}
			out.Movies = append(out.Movies, RuleMatchMovie{
				ID: m.Movie.ID, Title: m.Movie.Title, Year: m.Movie.Year, DominantColor: m.Movie.DominantColor,
				PosterURL: s.imageURL(imagecache.Poster, gridPosterSize, m.Movie.PosterPath),
			})
		}
		return &struct{ Body RuleMatches }{out}, nil
	})
}

// previewRoute needs the acquisition service (decisions against a movie).
func (s *Server) previewRoute(a *acquire.Service) {
	huma.Register(s.api, huma.Operation{
		OperationID: "preview-release", Method: http.MethodPost, Path: "/quality/preview", Summary: "Test a release name",
		Description: "Parses the name, maps it to a quality and scores it with a profile: the matched rules and their scores. " +
			"With movieId, also the decision a search would take now (accepted, rejections, upgrade of the file on disk). " +
			"Pure engine, no network access.",
		Tags: []string{"Quality"},
	}, func(ctx context.Context, in *struct{ Body PreviewInput }) (*struct{ Body acquire.Preview }, error) {
		b := in.Body
		p, err := a.PreviewRelease(ctx, acquire.PreviewRequest{
			Title: b.Title, ProfileID: b.ProfileID, MovieID: b.MovieID, Size: b.Size, Indexer: b.Indexer,
			IndexerFlags: b.IndexerFlags, Protocol: decisionProtocol(b.Protocol),
		})
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		if p.Score.Rules == nil {
			p.Score.Rules = []quality.RuleScore{}
		}
		return &struct{ Body acquire.Preview }{p}, nil
	})
}
