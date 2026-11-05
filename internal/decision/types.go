package decision

import (
	"fmt"
	"time"

	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// Protocol is how a release is downloaded.
type Protocol string

// Protocols.
const (
	Usenet  Protocol = "usenet"
	Torrent Protocol = "torrent"
)

// Candidate is one search result.
type Candidate struct {
	Title  string               `json:"title"`
	Parsed parser.ParsedRelease `json:"parsed"` // parser.Parse(Title), done by the caller
	Size   int64                `json:"size"`   // bytes; 0 if unknown

	IndexerID       int64     `json:"indexerId"`
	IndexerName     string    `json:"indexerName"`
	IndexerPriority int       `json:"indexerPriority"` // lower is preferred (1..50, default 25)
	Protocol        Protocol  `json:"protocol"`
	Seeders         int       `json:"seeders"` // torrents; negative if unknown
	Leechers        int       `json:"leechers"`
	PublishDate     time.Time `json:"publishDate"` // zero if unknown
	IndexerFlags    []string  `json:"indexerFlags,omitempty"`

	DownloadURL string `json:"downloadUrl"`
	GUID        string `json:"guid"`
	InfoHash    string `json:"infoHash,omitempty"`

	// AlreadyGrabbed is set by the caller when this release is already in the
	// download queue or in the grab history of the movie.
	AlreadyGrabbed bool `json:"alreadyGrabbed,omitempty"`
}

// Availability is a stage of a movie release, used both for the current status of
// a movie and for its minimum availability.
type Availability uint8

// Availability stages, in order.
const (
	Announced Availability = iota
	InCinemas
	Released
)

var availabilityNames = [...]string{"announced", "in cinemas", "released"}

// String returns the display text of a.
func (a Availability) String() string {
	if int(a) < len(availabilityNames) {
		return availabilityNames[a]
	}
	return fmt.Sprintf("Availability(%d)", uint8(a))
}

// Movie is the movie searched for.
type Movie struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Year  int    `json:"year"` // 0 if unknown: the year is not checked
	// CleanTitle is parser.CleanTitle(Title); computed when empty.
	CleanTitle string `json:"cleanTitle,omitempty"`
	// AlternateTitles are other known titles (translations, original title, AKA).
	// They are cleaned with parser.CleanTitle, so raw or clean titles both work.
	AlternateTitles  []string        `json:"alternateTitles,omitempty"`
	RuntimeMinutes   int             `json:"runtimeMinutes"`             // 0 if unknown: sizes are not checked
	OriginalLanguage parser.Language `json:"originalLanguage,omitempty"` // 0 if unknown

	Status              Availability `json:"status"`
	MinimumAvailability Availability `json:"minimumAvailability"`
	InCinemas           time.Time    `json:"inCinemas"`       // zero if unknown
	DigitalRelease      time.Time    `json:"digitalRelease"`  // zero if unknown
	PhysicalRelease     time.Time    `json:"physicalRelease"` // zero if unknown

	Monitored bool `json:"monitored"`
}

// FileQuality is what upgrade decisions compare.
type FileQuality struct {
	Quality     quality.ID      `json:"quality"`
	Revision    parser.Revision `json:"revision"`
	CustomScore int             `json:"customScore"`
}

// CurrentFile is the file the movie already has.
type CurrentFile struct {
	FileQuality
	Languages []parser.Language `json:"languages,omitempty"` // audio languages; empty if unknown
}

// BlocklistKind tells what a blocklist entry blocks.
type BlocklistKind string

// Blocklist kinds.
const (
	BlockRelease BlocklistKind = "release" // matched by title, GUID or info hash
	BlockGroup   BlocklistKind = "group"   // matched by release group
)

// BlocklistEntry is a release or group that must not be grabbed again.
type BlocklistEntry struct {
	Kind         BlocklistKind `json:"kind"`
	SourceTitle  string        `json:"sourceTitle,omitempty"`
	GUID         string        `json:"guid,omitempty"`
	InfoHash     string        `json:"infoHash,omitempty"`
	ReleaseGroup string        `json:"releaseGroup,omitempty"`
}

// IndexerSettings are the per-indexer settings that decisions use.
type IndexerSettings struct {
	MinSeeders int `json:"minSeeders"`
}

// DefaultMinSeeders applies to torrent indexers without settings.
const DefaultMinSeeders = 1

// Options tune a decision run.
type Options struct {
	Now time.Time // required for availability and age checks
	// Interactive is a search started by the user (as opposed to RSS or an
	// automatic search). Interactive searches do not check Movie.Monitored.
	Interactive bool
	// Override marks a manual grab of a release regardless of its rejections:
	// Decision.Overridden is set when the release has rejections.
	Override bool
	// AllowHardcodedSubs disables the hardcoded_subs rejection.
	AllowHardcodedSubs bool
	// PreferredProtocol ranks releases of that protocol first among equals; empty
	// means no preference.
	PreferredProtocol Protocol
	// UsenetRetentionDays rejects older Usenet posts; 0 means unlimited.
	UsenetRetentionDays int
	// UsenetMinimumAge delays fresh Usenet posts (propagation); 0 disables it.
	UsenetMinimumAge time.Duration
	// AvailabilityDelay shifts every release date later (or earlier if negative).
	AvailabilityDelay time.Duration
}

// Config holds everything shared by the candidates of one search.
type Config struct {
	Movie     Movie
	Profile   quality.Profile
	Rules     []*quality.Rule
	Sizes     quality.Sizes // nil: no size limits
	Current   *CurrentFile  // nil: the movie has no file
	Blocklist []BlocklistEntry
	Indexers  map[int64]IndexerSettings
	Options   Options
}

// Code identifies a rejection reason. Codes are stable: they are stored with
// decisions and used by the UI.
type Code string

// Rejection codes, in evaluation order.
const (
	CodeTitleMismatch     Code = "title_mismatch"
	CodeYearMismatch      Code = "year_mismatch"
	CodeUnknownQuality    Code = "unknown_quality"
	CodeQualityNotAllowed Code = "quality_not_allowed"
	CodeWrongLanguage     Code = "wrong_language"
	CodeHardcodedSubs     Code = "hardcoded_subs"
	CodeBelowMinScore     Code = "below_min_score"
	CodeSizeTooSmall      Code = "size_too_small"
	CodeSizeTooLarge      Code = "size_too_large"
	CodeBlocklisted       Code = "blocklisted"
	CodeNotMonitored      Code = "not_monitored"
	CodeNotAvailable      Code = "not_available"
	CodeNotEnoughSeeders  Code = "not_enough_seeders"
	CodeMinimumAge        Code = "minimum_age"
	CodeRetention         Code = "retention"
	CodeAlreadyGrabbed    Code = "already_grabbed"
	CodeUpgradesDisabled  Code = "upgrades_disabled"
	CodeCutoffMet         Code = "cutoff_met"
	CodeNotUpgrade        Code = "not_upgrade"
)

// Rejection is one reason not to grab a release.
type Rejection struct {
	Code    Code   `json:"code"`
	Message string `json:"message"` // plain English, specific to the release
	// Permanent is false when the reason may go away by itself (release date,
	// seeders, queue, propagation delay): the release may be retried later.
	Permanent bool `json:"permanent"`
}

// UpgradeReason tells why a release upgrades the current file.
type UpgradeReason string

// Upgrade reasons.
const (
	UpgradeQuality  UpgradeReason = "quality"
	UpgradeRevision UpgradeReason = "revision"
	UpgradeScore    UpgradeReason = "score"
	UpgradeLanguage UpgradeReason = "language"
)

// UpgradeInfo describes an accepted upgrade of the current file.
type UpgradeInfo struct {
	From   FileQuality   `json:"from"`
	Reason UpgradeReason `json:"reason"`
}

// Decision is the verdict on one candidate.
type Decision struct {
	// Accepted is true when there is no rejection.
	Accepted bool `json:"accepted"`
	// Overridden is true when Options.Override grabs a rejected release; the
	// rejections are kept for history.
	Overridden bool        `json:"overridden,omitempty"`
	Rejections []Rejection `json:"rejections,omitempty"`

	Quality     quality.ID        `json:"quality"`
	QualityRank int               `json:"qualityRank"` // rank in the profile, 0 if absent
	Score       quality.Breakdown `json:"score"`
	Upgrade     *UpgradeInfo      `json:"upgrade,omitempty"`

	// SizeDelta is |size - preferred size| in bytes, or -1 when unknown (no size,
	// runtime or limits). Used for ranking.
	SizeDelta int64 `json:"sizeDelta"`
	// Notes explain checks that were skipped, e.g. "runtime unknown".
	Notes []string `json:"notes,omitempty"`
}

// Grabbable reports whether the release may be grabbed: accepted, or overridden.
func (d *Decision) Grabbable() bool { return d.Accepted || d.Overridden }

// Has reports whether the decision has a rejection with that code.
func (d *Decision) Has(c Code) bool {
	for i := range d.Rejections {
		if d.Rejections[i].Code == c {
			return true
		}
	}
	return false
}

// Evaluated is a candidate with its decision.
type Evaluated struct {
	Candidate Candidate `json:"candidate"`
	Decision  Decision  `json:"decision"`
}
