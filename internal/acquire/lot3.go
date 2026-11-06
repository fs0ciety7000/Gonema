package acquire

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// ----------------------------------------------------------------- release preview

// PreviewRequest asks how a release name is read and scored.
type PreviewRequest struct {
	Title        string
	ProfileID    int64 // the profile to score with; default the movie's
	MovieID      int64 // optional: also decide against this movie and its file
	Size         int64 // bytes, 0 if unknown
	Indexer      string
	IndexerFlags []string
	Protocol     decision.Protocol // default usenet
}

// PreviewFile is the file a release is compared with.
type PreviewFile struct {
	Quality     quality.ID `json:"quality"`
	QualityName string     `json:"qualityName"`
	CustomScore int        `json:"customScore" doc:"Stored score of the file"`
}

// PreviewDecision is the verdict on the release for a movie, as a search
// would give it now.
type PreviewDecision struct {
	MovieID     int64                 `json:"movieId"`
	MovieTitle  string                `json:"movieTitle"`
	Accepted    bool                  `json:"accepted"`
	Rejections  []decision.Rejection  `json:"rejections"`
	Upgrade     *decision.UpgradeInfo `json:"upgrade,omitempty" doc:"Set when the release would replace the file on disk"`
	CurrentFile *PreviewFile          `json:"currentFile" doc:"null when the movie has no file"`
	Notes       []string              `json:"notes,omitempty" doc:"Checks that were skipped (size unknown...)"`
}

// Preview is how Gonema reads and scores a release name.
type Preview struct {
	Title          string               `json:"title"`
	Parsed         parser.ParsedRelease `json:"parsed"`
	ParseMicros    float64              `json:"parseMicros" doc:"Time the parser took, in microseconds"`
	Quality        quality.ID           `json:"quality"`
	QualityName    string               `json:"qualityName"`
	ProfileID      int64                `json:"profileId"`
	Profile        string               `json:"profile"`
	QualityAllowed bool                 `json:"qualityAllowed" doc:"The profile allows this quality"`
	QualityRank    int                  `json:"qualityRank" doc:"Rank in the profile, higher is better; 0 when absent"`
	CutoffRank     int                  `json:"cutoffRank"`
	CutoffLabel    string               `json:"cutoffLabel"`
	MeetsCutoff    bool                 `json:"meetsCutoff" doc:"A file like this one would end the upgrades: cutoff quality and cutoff score reached"`
	MinScore       int                  `json:"minScore"`
	CutoffScore    int                  `json:"cutoffScore"`
	Score          quality.Breakdown    `json:"score" doc:"Matched rules and their scores in this profile"`
	Decision       *PreviewDecision     `json:"decision" doc:"Only with movieId"`
}

// PreviewRelease parses and scores a release name with the pure engine, and
// with a movie also returns the decision a search would take. It does no
// network access.
func (s *Service) PreviewRelease(ctx context.Context, req PreviewRequest) (Preview, error) {
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 500 {
		return Preview{}, invalid("The release name must be 1 to 500 characters long.")
	}
	if req.ProfileID <= 0 && req.MovieID <= 0 {
		return Preview{}, invalid("Give a quality profile or a movie to score the release with.")
	}
	if req.Size < 0 {
		return Preview{}, invalid("The size must not be negative.")
	}
	switch req.Protocol {
	case "":
		req.Protocol = decision.Usenet
	case decision.Usenet, decision.Torrent:
	default:
		return Preview{}, invalid("The protocol must be usenet or torrent.")
	}
	start := time.Now()
	parsed := parser.Parse(req.Title)
	out := Preview{Title: req.Title, Parsed: parsed, ParseMicros: float64(time.Since(start).Nanoseconds()) / 1e3}
	out.Quality = quality.FromParsed(&parsed)
	out.QualityName = out.Quality.Name()

	var cfg *decision.Config
	var movieTitle string
	if req.MovieID > 0 {
		mc, err := s.loadMovie(ctx, req.MovieID, nil, s.searchOptions(ctx, true))
		if err != nil {
			return Preview{}, err
		}
		cfg, movieTitle = &mc.cfg, mc.row.Title
		if req.ProfileID == 0 {
			req.ProfileID = mc.row.QualityProfileID
		}
	}
	var prof quality.Profile
	var rules []*quality.Rule
	var name string
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		row, err := q.GetQualityProfile(ctx, req.ProfileID)
		if err != nil {
			return mapNotFound(err, "Quality profile %d does not exist.", req.ProfileID)
		}
		name = row.Name
		if prof, err = library.LoadQualityProfile(ctx, q, req.ProfileID); err != nil {
			return err
		}
		rules, err = library.LoadScoringRules(ctx, q)
		return err
	})
	if err != nil {
		var ae *Error
		if errors.As(err, &ae) {
			return Preview{}, err
		}
		return Preview{}, fmt.Errorf("acquire: preview: %w", err)
	}
	out.ProfileID, out.Profile = req.ProfileID, name
	out.QualityRank, out.QualityAllowed = prof.Rank(out.Quality)
	out.CutoffRank, out.CutoffLabel = prof.CutoffRank(), prof.CutoffLabel()
	out.MinScore, out.CutoffScore = prof.MinScore, prof.CutoffScore
	rel := quality.Release{Title: req.Title, Parsed: &parsed, Size: req.Size, Indexer: req.Indexer, IndexerFlags: req.IndexerFlags}
	out.Score = quality.Score(&prof, rules, &rel)
	out.MeetsCutoff = out.QualityAllowed && decision.CutoffMet(decision.FileQuality{Quality: out.Quality, Revision: parsed.Revision, CustomScore: out.Score.Total}, &prof)
	if cfg == nil {
		return out, nil
	}
	cfg.Profile, cfg.Rules = prof, rules
	ev, err := decision.NewEvaluator(*cfg)
	if err != nil {
		return Preview{}, unusableProfile(err)
	}
	cand := decision.Candidate{
		Title: req.Title, Parsed: parsed, Size: req.Size, IndexerName: req.Indexer, Protocol: req.Protocol,
		Seeders: -1, Leechers: -1, IndexerFlags: req.IndexerFlags,
	}
	d := ev.Evaluate(&cand)
	pd := &PreviewDecision{
		MovieID: req.MovieID, MovieTitle: movieTitle, Accepted: d.Accepted, Rejections: d.Rejections,
		Upgrade: d.Upgrade, Notes: d.Notes,
	}
	if pd.Rejections == nil {
		pd.Rejections = []decision.Rejection{}
	}
	if c := cfg.Current; c != nil {
		pd.CurrentFile = &PreviewFile{Quality: c.Quality, QualityName: c.Quality.Name(), CustomScore: c.CustomScore}
	}
	out.Decision = pd
	return out, nil
}

// ----------------------------------------------------------------- per-indexer retry

// RetryIndexer asks one indexer of an interactive search again. New releases
// join the same search and stream as search.* events; search.finished is sent
// again when the retry ends. force first closes the indexer's circuit breaker
// (an indexer paused after repeated failures is then tried at once).
func (s *Service) RetryIndexer(ctx context.Context, searchID string, indexerID int64, force bool) (SearchSnapshot, error) {
	sess, ok := s.searches.get(searchID)
	if !ok {
		return SearchSnapshot{}, notFound("This search has expired or does not exist: search again.")
	}
	sess.mu.Lock()
	i := slices.IndexFunc(sess.indexers, func(st IndexerStatus) bool { return st.ID == indexerID })
	var status string
	if i >= 0 {
		status = sess.indexers[i].Status
	}
	sess.mu.Unlock()
	if i < 0 {
		return SearchSnapshot{}, notFound("Indexer %d is not part of this search.", indexerID)
	}
	if status == "pending" {
		return SearchSnapshot{}, conflict("This indexer has not answered yet; wait for it before trying again.")
	}
	mc, err := s.loadMovie(ctx, sess.movieID, nil, s.searchOptions(ctx, true))
	if err != nil {
		return SearchSnapshot{}, err
	}
	ev, err := decision.NewEvaluator(mc.cfg)
	if err != nil {
		return SearchSnapshot{}, unusableProfile(err)
	}
	row, err := s.st.Read().GetIndexer(ctx, indexerID)
	if err != nil {
		return SearchSnapshot{}, mapNotFound(err, "Indexer %d no longer exists.", indexerID)
	}
	ixs, used := s.indexersFor([]db.Indexer{row}, forInteractive, nil, true)
	if len(ixs) == 0 {
		return SearchSnapshot{}, newError(ErrUnavailable, nil, "%s cannot be used for interactive searches now: check it in Settings > Indexers.", row.Name)
	}
	if force {
		s.orch.Reset(indexerID)
	}
	sess.mu.Lock()
	// Checked again under the lock: two retries of one indexer must not run at once.
	if sess.indexers[i].Status == "pending" {
		sess.mu.Unlock()
		return SearchSnapshot{}, conflict("This indexer has not answered yet; wait for it before trying again.")
	}
	sess.indexers[i] = IndexerStatus{ID: indexerID, Name: sess.indexers[i].Name, Status: "pending"}
	sess.running++
	sess.status = "running"
	sess.mu.Unlock()
	s.log.Info("interactive search: indexer retried", slog.String("search_id", sess.id), slog.String("indexer", row.Name), slog.Bool("force", force))
	q := mc.query()
	s.goBackground(func(base context.Context) {
		sctx, cancel := context.WithTimeout(base, searchTimeout)
		defer cancel()
		s.runInteractive(sctx, sess, mc, ev, q, ixs)
		s.persistHealth(base, used)
	})
	return sess.snapshot(), nil
}

// ----------------------------------------------------------------- pause and resume all

// ClientPauseResult is what pausing or resuming every download did in one client.
type ClientPauseResult struct {
	ClientID int64  `json:"clientId"`
	Name     string `json:"name"`
	Kind     string `json:"kind" enum:"sabnzbd,nzbget,qbittorrent,deluge,transmission"`
	// Supported: the client can pause and resume single downloads (all five
	// supported clients can; only Gonema's downloads are touched, never the
	// client's whole queue).
	Supported bool     `json:"supported" doc:"The client can pause and resume single downloads; only Gonema's downloads are touched"`
	Changed   int      `json:"changed" doc:"Downloads paused or resumed"`
	Failed    int      `json:"failed"`
	Errors    []string `json:"errors" doc:"Why downloads failed, at most 5"`
}

// PauseAllResult reports a pause-all or resume-all.
type PauseAllResult struct {
	Paused  bool                `json:"paused" doc:"true for pause, false for resume"`
	Changed int                 `json:"changed"`
	Failed  int                 `json:"failed"`
	Skipped int                 `json:"skipped" doc:"Downloads already in that state, importing, failed to import, or without a client"`
	Clients []ClientPauseResult `json:"clients" doc:"Per download client with active downloads, by name"`
}

// maxPauseErrors bounds the errors reported per client.
const maxPauseErrors = 5

// SetAllPaused pauses (or resumes) every active download of the queue in its
// client. Failures are reported per client and do not stop the others.
func (s *Service) SetAllPaused(ctx context.Context, paused bool) (PauseAllResult, error) {
	items, err := s.Queue(ctx)
	if err != nil {
		return PauseAllResult{}, err
	}
	rows, err := s.st.Read().ListDownloadClients(ctx)
	if err != nil {
		return PauseAllResult{}, store.MapError(fmt.Errorf("acquire: list download clients: %w", err))
	}
	clients := make(map[int64]*db.DownloadClient, len(rows))
	for i := range rows {
		clients[rows[i].ID] = &rows[i]
	}
	res := PauseAllResult{Paused: paused, Clients: []ClientPauseResult{}}
	byClient := map[int64]*ClientPauseResult{}
	for i := range items {
		it := &items[i]
		eligible := it.Status == string(download.StatusPaused)
		if paused {
			importFailed := it.Status == string(download.StatusWarning) && it.CompletedAt != nil
			eligible = !importFailed && (it.Status == string(download.StatusQueued) || it.Status == string(download.StatusDownloading) ||
				it.Status == string(download.StatusWarning))
		}
		var c *db.DownloadClient
		if it.ClientID != nil {
			c = clients[*it.ClientID]
		}
		if !eligible || c == nil {
			res.Skipped++
			continue
		}
		cr, ok := byClient[c.ID]
		if !ok {
			cr = &ClientPauseResult{ClientID: c.ID, Name: c.Name, Kind: c.Kind, Supported: download.Kind(c.Kind).Protocol() != "", Errors: []string{}}
			byClient[c.ID] = cr
		}
		if !cr.Supported {
			res.Skipped++
			continue
		}
		if _, err := s.SetPaused(ctx, it.ID, paused); err != nil {
			cr.Failed++
			res.Failed++
			if len(cr.Errors) < maxPauseErrors {
				msg := err.Error()
				var ae *Error
				if errors.As(err, &ae) {
					msg = ae.UserMessage()
				}
				cr.Errors = append(cr.Errors, it.Title+": "+msg)
			}
			continue
		}
		cr.Changed++
		res.Changed++
	}
	for _, cr := range byClient {
		res.Clients = append(res.Clients, *cr)
	}
	slices.SortFunc(res.Clients, func(a, b ClientPauseResult) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), cmp.Compare(a.ClientID, b.ClientID))
	})
	s.log.Info("queue paused or resumed", slog.Bool("paused", paused), slog.Int("changed", res.Changed),
		slog.Int("failed", res.Failed), slog.Int("skipped", res.Skipped))
	return res, nil
}
