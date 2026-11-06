package decision

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// YearTolerance is how far the release year may be from the movie year. Release
// years differ by one around New Year and between festival and theatrical dates.
const YearTolerance = 1

// ReleasedFallback is used for minimum availability "released" when no digital or
// physical date is known: the movie is assumed released this long after cinemas.
const ReleasedFallback = 90 * 24 * time.Hour

// ErrInvalidConfig is wrapped by NewEvaluator errors.
var ErrInvalidConfig = errors.New("decision: invalid config")

// Evaluator decides on the candidates of one search. Build it with NewEvaluator;
// it is immutable and safe for concurrent use. The Config passed to NewEvaluator
// must not be modified afterwards.
type Evaluator struct {
	cfg       Config
	profile   *quality.Profile
	titles    map[string]struct{} // clean titles of the movie
	wanted    wantedLanguages
	blockRel  map[string]struct{} // lower-cased titles
	blockGUID map[string]struct{}
	blockHash map[string]struct{} // lower-cased
	blockGrp  map[string]struct{} // lower-cased
}

// NewEvaluator validates the configuration and precomputes what every candidate
// needs. Errors wrap ErrInvalidConfig and, for profile problems, quality.ErrInvalid.
func NewEvaluator(cfg Config) (*Evaluator, error) {
	var errs []error
	if strings.TrimSpace(cfg.Movie.Title) == "" {
		errs = append(errs, fmt.Errorf("%w: movie title is empty", ErrInvalidConfig))
	}
	if cfg.Options.Now.IsZero() {
		errs = append(errs, fmt.Errorf("%w: Options.Now is not set", ErrInvalidConfig))
	}
	if err := quality.ValidateRules(cfg.Rules); err != nil {
		errs = append(errs, fmt.Errorf("%w: %w", ErrInvalidConfig, err))
	} else if err := cfg.Profile.Validate(cfg.Rules); err != nil {
		errs = append(errs, fmt.Errorf("%w: %w", ErrInvalidConfig, err))
	}
	if err := cfg.Sizes.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("%w: %w", ErrInvalidConfig, err))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	e := &Evaluator{
		cfg:       cfg,
		titles:    make(map[string]struct{}, 1+len(cfg.Movie.AlternateTitles)),
		blockRel:  make(map[string]struct{}),
		blockGUID: make(map[string]struct{}),
		blockHash: make(map[string]struct{}),
		blockGrp:  make(map[string]struct{}),
	}
	e.profile = &e.cfg.Profile
	if e.cfg.Movie.CleanTitle == "" {
		e.cfg.Movie.CleanTitle = parser.CleanTitle(cfg.Movie.Title)
	}
	e.titles[e.cfg.Movie.CleanTitle] = struct{}{}
	for _, t := range cfg.Movie.AlternateTitles {
		if ct := parser.CleanTitle(t); ct != "" {
			e.titles[ct] = struct{}{}
		}
	}
	e.wanted = resolveWanted(e.profile, cfg.Movie.OriginalLanguage)
	for i := range cfg.Blocklist {
		b := &cfg.Blocklist[i]
		switch b.Kind {
		case BlockGroup:
			addLower(e.blockGrp, b.ReleaseGroup)
		default:
			addLower(e.blockRel, b.SourceTitle)
			if b.GUID != "" {
				e.blockGUID[b.GUID] = struct{}{}
			}
			addLower(e.blockHash, b.InfoHash)
		}
	}
	return e, nil
}

func addLower(m map[string]struct{}, s string) {
	if s = strings.TrimSpace(s); s != "" {
		m[strings.ToLower(s)] = struct{}{}
	}
}

// Profile returns the profile of the evaluator.
func (e *Evaluator) Profile() *quality.Profile { return e.profile }

// decider collects the outcome of one evaluation.
type decider struct {
	e *Evaluator
	c *Candidate
	d Decision
}

func (x *decider) reject(code Code, permanent bool, format string, args ...any) {
	x.d.Rejections = append(x.d.Rejections, Rejection{Code: code, Message: fmt.Sprintf(format, args...), Permanent: permanent})
}

func (x *decider) note(format string, args ...any) {
	x.d.Notes = append(x.d.Notes, fmt.Sprintf(format, args...))
}

// Evaluate decides on one candidate. Every applicable rejection is collected, in
// the order of the Code constants.
func (e *Evaluator) Evaluate(c *Candidate) Decision {
	x := decider{e: e, c: c, d: Decision{SizeDelta: -1}}
	x.checkTitle()
	x.checkContent()
	x.checkQuality()
	langOK := x.checkLanguage()
	x.checkHardcodedSubs()
	x.checkScore()
	x.checkSize()
	x.checkBlocklist()
	x.checkMonitored()
	x.checkAvailability()
	x.checkProtocol()
	if c.AlreadyGrabbed {
		x.reject(CodeAlreadyGrabbed, false, "Release is already in the download queue or was already grabbed")
	}
	x.checkUpgrade(langOK)
	x.d.Accepted = len(x.d.Rejections) == 0
	x.d.Overridden = e.cfg.Options.Override && !x.d.Accepted
	return x.d
}

// EvaluateAll evaluates and ranks candidates.
func (e *Evaluator) EvaluateAll(cands []Candidate) []Evaluated {
	out := make([]Evaluated, len(cands))
	for i := range cands {
		out[i] = Evaluated{Candidate: cands[i], Decision: e.Evaluate(&cands[i])}
	}
	return e.Rank(out)
}

// Rank is Rank with the preferred protocol of the evaluator options.
func (e *Evaluator) Rank(evs []Evaluated) []Evaluated {
	return Rank(evs, e.cfg.Options.PreferredProtocol)
}

func (x *decider) checkTitle() {
	p := &x.c.Parsed
	m := &x.e.cfg.Movie
	clean := p.CleanTitle
	if clean == "" {
		clean = parser.CleanTitle(p.Title)
	}
	if clean == "" {
		x.reject(CodeTitleMismatch, true, "Could not read a movie title from the release name")
	} else if !x.matchesTitle(clean) {
		x.reject(CodeTitleMismatch, true, "Title %q does not match %q", p.Title, m.Title)
	}
	switch {
	case p.Year == 0:
		x.note("release has no year: year not checked")
	case m.Year != 0 && abs(p.Year-m.Year) > YearTolerance:
		x.reject(CodeYearMismatch, true, "Year %d does not match %d (±%d tolerated)", p.Year, m.Year, YearTolerance)
	}
}

// fixContents names what each repair pack contains, for not_a_movie messages.
var fixContents = map[parser.FixKind]string{
	parser.FixNFO:    "a repaired NFO",
	parser.FixSample: "a repaired sample",
	parser.FixSub:    "repaired subtitles",
	parser.FixSync:   "a sync repair",
	parser.FixProof:  "a repaired proof picture",
}

// checkContent rejects releases that are not the movie: repair packs (every fix
// but DIRFIX), bonus-only releases and samples.
func (x *decider) checkContent() {
	p := &x.c.Parsed
	if what, ok := fixContents[p.Fix]; ok {
		x.reject(CodeNotAMovie, true, "%sFIX releases only contain %s, not the movie", strings.ToUpper(p.Fix.String()), what)
	}
	if p.Extras {
		x.reject(CodeNotAMovie, true, "Bonus-only release, not the movie")
	}
	if p.Sample {
		x.reject(CodeNotAMovie, true, "Sample-only release, not the movie")
	}
}

func (x *decider) matchesTitle(clean string) bool {
	if _, ok := x.e.titles[clean]; ok {
		return true
	}
	for _, alt := range x.c.Parsed.AlternateTitles {
		if _, ok := x.e.titles[parser.CleanTitle(alt)]; ok {
			return true
		}
	}
	return false
}

func (x *decider) checkQuality() {
	q := quality.FromParsed(&x.c.Parsed)
	x.d.Quality = q
	if q == quality.Unknown {
		x.reject(CodeUnknownQuality, true, "Quality could not be determined from the release name")
		return
	}
	rank, allowed := x.e.profile.Rank(q)
	x.d.QualityRank = rank
	if !allowed {
		x.reject(CodeQualityNotAllowed, true, "%s is not allowed by profile %s", q.Name(), x.e.profile.Name)
	}
}

// checkLanguage reports whether the assumed audio languages satisfy the profile.
func (x *decider) checkLanguage() bool {
	p := &x.c.Parsed
	langs, tagged := audioLanguages(p, x.e.cfg.Movie.OriginalLanguage)
	if x.e.wanted.satisfied(langs, !tagged || p.Multi) {
		return true
	}
	assumed := ""
	if !tagged {
		assumed = " (assumed: untagged)"
	}
	word := "Language"
	if len(langs) > 1 {
		word = "Languages"
	}
	x.reject(CodeWrongLanguage, true, "%s %s%s not wanted by profile %s (wanted: %s)",
		word, languageList(langs), assumed+isAre(len(langs)), x.e.profile.Name, x.e.wanted.label)
	return false
}

func isAre(n int) string {
	if n > 1 {
		return " are"
	}
	return " is"
}

func (x *decider) checkHardcodedSubs() {
	p := &x.c.Parsed
	if !p.HardcodedSubs || x.e.cfg.Options.AllowHardcodedSubs {
		return
	}
	if p.HardcodedSubsTag != "" {
		x.reject(CodeHardcodedSubs, true, "Release has hardcoded subtitles (%s)", p.HardcodedSubsTag)
		return
	}
	x.reject(CodeHardcodedSubs, true, "Release has hardcoded subtitles")
}

func (x *decider) checkScore() {
	x.d.Score = quality.Score(x.e.profile, x.e.cfg.Rules, &quality.Release{
		Title: x.c.Title, Parsed: &x.c.Parsed, Size: x.c.Size, Indexer: x.c.IndexerName, IndexerFlags: x.c.IndexerFlags,
	})
	if minScore := x.e.profile.MinScore; x.d.Score.Total < minScore {
		x.reject(CodeBelowMinScore, true, "Custom score %d is below the minimum of %d for profile %s",
			x.d.Score.Total, minScore, x.e.profile.Name)
	}
}

func (x *decider) checkSize() {
	q := x.d.Quality
	if q == quality.Unknown {
		return
	}
	limit, ok := x.e.cfg.Sizes.Limit(q)
	if !ok {
		return
	}
	runtime := x.e.cfg.Movie.RuntimeMinutes
	switch {
	case x.c.Size <= 0:
		x.note("size unknown: size not checked")
		return
	case runtime <= 0:
		x.note("runtime unknown: size not checked")
		return
	}
	low, pref, high := limit.Bytes(runtime)
	if pref > 0 {
		x.d.SizeDelta = abs64(x.c.Size - pref)
	}
	if x.c.Size < low {
		x.reject(CodeSizeTooSmall, true, "Size %s is below the %s minimum for %s at %d min",
			formatGB(x.c.Size), formatGB(low), q.Name(), runtime)
	}
	if high > 0 && x.c.Size > high {
		x.reject(CodeSizeTooLarge, true, "Size %s is above the %s maximum for %s at %d min",
			formatGB(x.c.Size), formatGB(high), q.Name(), runtime)
	}
}

func formatGB(b int64) string { return fmt.Sprintf("%.1f GB", float64(b)/quality.GB) }

func (x *decider) checkBlocklist() {
	if msg := x.e.blocked(x.c); msg != "" {
		x.reject(CodeBlocklisted, true, "%s", msg)
	}
}

// blocked returns why c is blocklisted, or "".
func (e *Evaluator) blocked(c *Candidate) string {
	return blockReason(c, e.blockHash, e.blockGUID, e.blockRel, e.blockGrp)
}

func blockReason(c *Candidate, hashes, guids, titles, groups map[string]struct{}) string {
	if c.InfoHash != "" {
		if _, ok := hashes[strings.ToLower(c.InfoHash)]; ok {
			return "Torrent hash " + strings.ToLower(c.InfoHash) + " is blocklisted"
		}
	}
	if c.GUID != "" {
		if _, ok := guids[c.GUID]; ok {
			return "Release is blocklisted (same indexer GUID)"
		}
	}
	if _, ok := titles[strings.ToLower(strings.TrimSpace(c.Title))]; ok {
		return "Release is blocklisted"
	}
	if g := c.Parsed.ReleaseGroup; g != "" {
		if _, ok := groups[strings.ToLower(g)]; ok {
			return "Release group " + g + " is blocklisted"
		}
	}
	return ""
}

func (x *decider) checkMonitored() {
	if !x.e.cfg.Options.Interactive && !x.e.cfg.Movie.Monitored {
		x.reject(CodeNotMonitored, true, "Movie is not monitored")
	}
}

func (x *decider) checkAvailability() {
	ok, at := Available(&x.e.cfg.Movie, x.e.cfg.Options.Now, x.e.cfg.Options.AvailabilityDelay)
	if ok {
		return
	}
	level := x.e.cfg.Movie.MinimumAvailability
	if at.IsZero() {
		x.reject(CodeNotAvailable, false, "Movie is not available yet: minimum availability is %q and no date is known", level)
		return
	}
	x.reject(CodeNotAvailable, false, "Movie is not available yet: minimum availability is %q, expected %s",
		level, at.UTC().Format(time.DateOnly))
}

// Available reports whether the movie has reached its minimum availability at now,
// and the date it does (zero if unknown). The movie is available when its Status
// has reached the minimum, or when the date of the minimum stage, plus delay, has
// passed: InCinemas for "in cinemas"; the earliest digital or physical release for
// "released", or InCinemas + ReleasedFallback when neither is known.
func Available(m *Movie, now time.Time, delay time.Duration) (bool, time.Time) {
	if m.Status >= m.MinimumAvailability {
		return true, time.Time{}
	}
	var at time.Time
	switch m.MinimumAvailability {
	case InCinemas:
		at = m.InCinemas
	default: // Released
		at = earliest(m.DigitalRelease, m.PhysicalRelease)
		if at.IsZero() && !m.InCinemas.IsZero() {
			at = m.InCinemas.Add(ReleasedFallback)
		}
	}
	if at.IsZero() {
		return false, at
	}
	at = at.Add(delay)
	return !now.Before(at), at
}

func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero() || a.Before(b):
		return a
	default:
		return b
	}
}

func (x *decider) checkProtocol() {
	c := x.c
	opts := &x.e.cfg.Options
	switch c.Protocol {
	case Torrent:
		minSeeders := DefaultMinSeeders
		if s, ok := x.e.cfg.Indexers[c.IndexerID]; ok {
			minSeeders = s.MinSeeders
		}
		if c.Seeders >= 0 && c.Seeders < minSeeders {
			x.reject(CodeNotEnoughSeeders, false, "%d %s, indexer %s requires at least %d",
				c.Seeders, plural(c.Seeders, "seeder", "seeders"), indexerLabel(c), minSeeders)
		}
	case Usenet:
		if c.PublishDate.IsZero() {
			return
		}
		age := opts.Now.Sub(c.PublishDate)
		if opts.UsenetMinimumAge > 0 && age < opts.UsenetMinimumAge {
			x.reject(CodeMinimumAge, false, "Posted %s ago, waiting for the %s minimum age",
				formatDuration(age), formatDuration(opts.UsenetMinimumAge))
		}
		if days := opts.UsenetRetentionDays; days > 0 && age > time.Duration(days)*24*time.Hour {
			x.reject(CodeRetention, true, "Posted %d days ago, beyond the %d-day Usenet retention",
				int(age/(24*time.Hour)), days)
		}
	}
}

func indexerLabel(c *Candidate) string {
	if c.IndexerName != "" {
		return c.IndexerName
	}
	return fmt.Sprintf("#%d", c.IndexerID)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d/time.Minute))
	}
	return fmt.Sprintf("%.1f h", d.Hours())
}

func (x *decider) checkUpgrade(langOK bool) {
	cur := x.e.cfg.Current
	if cur == nil {
		return
	}
	p := x.e.profile
	cand := FileQuality{Quality: x.d.Quality, Revision: x.c.Parsed.Revision, CustomScore: x.d.Score.Total}
	curName := cur.Quality.Name()
	if !p.UpgradeAllowed {
		x.reject(CodeUpgradesDisabled, true, "Movie already has a %s file and profile %s does not allow upgrades", curName, p.Name)
		return
	}
	// A wanted language replaces a file without one, whatever the quality.
	if langOK && len(cur.Languages) > 0 && !x.e.wanted.satisfied(cur.Languages, false) {
		x.d.Upgrade = &UpgradeInfo{From: cur.FileQuality, Reason: UpgradeLanguage}
		return
	}
	if reason, ok := IsUpgrade(cur.FileQuality, cand, p); ok {
		x.d.Upgrade = &UpgradeInfo{From: cur.FileQuality, Reason: reason}
		return
	}
	if CutoffMet(cur.FileQuality, p) {
		x.reject(CodeCutoffMet, true, "Existing %s file (score %d) already meets the cutoff of profile %s (%s, score %d)",
			curName, cur.CustomScore, p.Name, p.CutoffLabel(), p.CutoffScore)
		return
	}
	rc, _ := p.Rank(cur.Quality)
	rn := x.d.QualityRank
	switch {
	case rn < rc:
		x.reject(CodeNotUpgrade, true, "%s ranks below the existing %s file in profile %s", cand.Quality.Name(), curName, p.Name)
	case cand.Quality == cur.Quality && CompareRevision(cand.Revision, cur.Revision) < 0:
		x.reject(CodeNotUpgrade, true, "Revision %s is older than the existing file's %s", revisionText(cand.Revision), revisionText(cur.Revision))
	default:
		x.reject(CodeNotUpgrade, true, "Custom score %d does not beat the existing %s file's %d by the minimum step of %d",
			cand.CustomScore, curName, cur.CustomScore, p.UpgradeStep())
	}
}

func revisionText(r parser.Revision) string {
	s := fmt.Sprintf("v%d", r.Version)
	if r.Real {
		s += " REAL"
	}
	return s
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
