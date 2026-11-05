package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// More settings read by the service (settings table, JSON values). With
// SettingMaxAttempts, SettingBlocklistGroup and SettingRSSInterval they make
// up AcquisitionSettings.
const (
	// SettingUsenetRetention is the Usenet provider retention in days; older
	// posts are rejected. 0 means unlimited (default).
	SettingUsenetRetention = "indexers.usenet_retention_days"
	// SettingUsenetMinimumAge delays fresh Usenet posts, in minutes. Default 0.
	SettingUsenetMinimumAge = "indexers.usenet_minimum_age_min"
	// SettingPreferredProtocol ranks releases of that protocol first among
	// equals: "usenet", "torrent" or "none" (default).
	SettingPreferredProtocol = "indexers.preferred_protocol"
	// SettingSearchMissingInterval is the period of the search for missing and
	// upgradable movies, in hours. Default 24.
	SettingSearchMissingInterval = "indexers.search_missing_interval_h"
)

// Bounds of the acquisition settings.
const (
	maxRetentionDays        = 10000
	maxMinimumAgeMin        = 1440
	minAttempts             = 1
	maxAttempts             = 50
	minRSSIntervalMin       = 10
	maxRSSIntervalMin       = 120
	minSearchMissingHours   = 6
	maxSearchMissingHours   = 168
	defaultSearchMissingHrs = int(SearchMissingTick / time.Hour)
	settingsReadTimeout     = 5 * time.Second
)

// Preferred protocol values.
const (
	ProtocolNone    = "none"
	ProtocolUsenet  = "usenet"
	ProtocolTorrent = "torrent"
)

// AcquisitionSettings tune searches, grabs and failure handling.
type AcquisitionSettings struct {
	UsenetRetentionDays        int    `json:"usenetRetentionDays" doc:"Usenet provider retention in days; older posts are rejected. 0: unlimited"`
	UsenetMinimumAgeMin        int    `json:"usenetMinimumAgeMin" doc:"Wait this many minutes after a Usenet post before grabbing it (propagation). 0: no wait"`
	PreferredProtocol          string `json:"preferredProtocol" enum:"usenet,torrent,none" doc:"Ranks releases of this protocol first among otherwise equal releases"`
	MaxAttemptsPerDay          int    `json:"maxAttemptsPerDay" doc:"Releases tried per movie and per day, first grab included, before automatic retries stop (1 to 50)"`
	BlocklistGroupOnFailure    bool   `json:"blocklistGroupOnFailure" doc:"A failed download also blocks its release group for the movie"`
	RSSIntervalMin             int    `json:"rssIntervalMin" doc:"Minutes between two RSS syncs (10 to 120)"`
	SearchMissingIntervalHours int    `json:"searchMissingIntervalHours" doc:"Hours between two searches for missing and upgradable movies (6 to 168)"`
}

// DefaultAcquisitionSettings returns the settings used when none are stored.
func DefaultAcquisitionSettings() AcquisitionSettings {
	return AcquisitionSettings{
		PreferredProtocol: ProtocolNone, MaxAttemptsPerDay: DefaultMaxAttempts,
		RSSIntervalMin: int(DefaultRSSInterval / time.Minute), SearchMissingIntervalHours: defaultSearchMissingHrs,
	}
}

// Validate reports every out-of-range value in one plain-language message.
func (a *AcquisitionSettings) Validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if a.UsenetRetentionDays < 0 || a.UsenetRetentionDays > maxRetentionDays {
		add("Usenet retention must be between 0 (unlimited) and %d days; your provider's site states it (often 3000 to 6000 days).", maxRetentionDays)
	}
	if a.UsenetMinimumAgeMin < 0 || a.UsenetMinimumAgeMin > maxMinimumAgeMin {
		add("The minimum age must be between 0 and %d minutes (a day); a few minutes is usually enough.", maxMinimumAgeMin)
	}
	switch a.PreferredProtocol {
	case ProtocolNone, ProtocolUsenet, ProtocolTorrent:
	default:
		add("The preferred protocol must be usenet, torrent or none, not %q.", a.PreferredProtocol)
	}
	if a.MaxAttemptsPerDay < minAttempts || a.MaxAttemptsPerDay > maxAttempts {
		add("Attempts per day must be between %d and %d (default %d).", minAttempts, maxAttempts, DefaultMaxAttempts)
	}
	if a.RSSIntervalMin < minRSSIntervalMin || a.RSSIntervalMin > maxRSSIntervalMin {
		add("The RSS interval must be between %d and %d minutes: indexers may block more frequent syncs.", minRSSIntervalMin, maxRSSIntervalMin)
	}
	if a.SearchMissingIntervalHours < minSearchMissingHours || a.SearchMissingIntervalHours > maxSearchMissingHours {
		add("The search for missing movies must run every %d to %d hours (a week); RSS catches new releases in between.", minSearchMissingHours, maxSearchMissingHours)
	}
	if len(problems) == 0 {
		return nil
	}
	return invalid("%s", strings.Join(problems, " "))
}

// AcquisitionSettings returns the stored settings, defaults for unset or
// invalid values (an invalid stored value is logged).
func (s *Service) AcquisitionSettings(ctx context.Context) AcquisitionSettings {
	d := DefaultAcquisitionSettings()
	a := AcquisitionSettings{
		UsenetRetentionDays:        s.settingInt(ctx, SettingUsenetRetention, d.UsenetRetentionDays),
		UsenetMinimumAgeMin:        s.settingInt(ctx, SettingUsenetMinimumAge, d.UsenetMinimumAgeMin),
		PreferredProtocol:          s.settingString(ctx, SettingPreferredProtocol, d.PreferredProtocol),
		MaxAttemptsPerDay:          s.settingInt(ctx, SettingMaxAttempts, d.MaxAttemptsPerDay),
		BlocklistGroupOnFailure:    s.settingBool(ctx, SettingBlocklistGroup),
		RSSIntervalMin:             s.settingInt(ctx, SettingRSSInterval, d.RSSIntervalMin),
		SearchMissingIntervalHours: s.settingInt(ctx, SettingSearchMissingInterval, d.SearchMissingIntervalHours),
	}
	// Values written before validation existed (or by hand) are brought back
	// into range rather than refused: searches must keep working.
	a.UsenetRetentionDays = min(max(a.UsenetRetentionDays, 0), maxRetentionDays)
	a.UsenetMinimumAgeMin = min(max(a.UsenetMinimumAgeMin, 0), maxMinimumAgeMin)
	if a.PreferredProtocol != ProtocolUsenet && a.PreferredProtocol != ProtocolTorrent {
		a.PreferredProtocol = ProtocolNone
	}
	a.MaxAttemptsPerDay = min(max(a.MaxAttemptsPerDay, minAttempts), maxAttempts)
	a.RSSIntervalMin = min(max(a.RSSIntervalMin, minRSSIntervalMin), maxRSSIntervalMin)
	a.SearchMissingIntervalHours = min(max(a.SearchMissingIntervalHours, minSearchMissingHours), maxSearchMissingHours)
	return a
}

// SetAcquisitionSettings validates and stores the settings in one transaction,
// then applies changed intervals to the recurring jobs.
func (s *Service) SetAcquisitionSettings(ctx context.Context, in AcquisitionSettings) (AcquisitionSettings, error) {
	in.PreferredProtocol = strings.ToLower(strings.TrimSpace(in.PreferredProtocol))
	if in.PreferredProtocol == "" {
		in.PreferredProtocol = ProtocolNone
	}
	if err := in.Validate(); err != nil {
		return AcquisitionSettings{}, err
	}
	prev := s.AcquisitionSettings(ctx)
	values := []struct {
		key string
		v   any
	}{
		{SettingUsenetRetention, in.UsenetRetentionDays},
		{SettingUsenetMinimumAge, in.UsenetMinimumAgeMin},
		{SettingPreferredProtocol, in.PreferredProtocol},
		{SettingMaxAttempts, in.MaxAttemptsPerDay},
		{SettingBlocklistGroup, in.BlocklistGroupOnFailure},
		{SettingRSSInterval, in.RSSIntervalMin},
		{SettingSearchMissingInterval, in.SearchMissingIntervalHours},
	}
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		for _, kv := range values {
			raw, err := json.Marshal(kv.v)
			if err != nil {
				return fmt.Errorf("encode %s: %w", kv.key, err)
			}
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: kv.key, Value: string(raw)}); err != nil {
				return fmt.Errorf("save %s: %w", kv.key, err)
			}
		}
		return nil
	})
	if err != nil {
		return AcquisitionSettings{}, fmt.Errorf("acquire: save acquisition settings: %w", err)
	}
	if s.resched != nil {
		if prev.RSSIntervalMin != in.RSSIntervalMin {
			s.resched.Reschedule(JobRSSSync)
		}
		if prev.SearchMissingIntervalHours != in.SearchMissingIntervalHours {
			s.resched.Reschedule(JobSearchMissing)
		}
	}
	s.log.Info("acquisition settings updated", slog.Int("usenet_retention_days", in.UsenetRetentionDays),
		slog.Int("usenet_minimum_age_min", in.UsenetMinimumAgeMin), slog.String("preferred_protocol", in.PreferredProtocol),
		slog.Int("max_attempts_per_day", in.MaxAttemptsPerDay), slog.Bool("blocklist_group_on_failure", in.BlocklistGroupOnFailure),
		slog.Int("rss_interval_min", in.RSSIntervalMin), slog.Int("search_missing_interval_h", in.SearchMissingIntervalHours))
	s.publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"setting": "acquisition"}})
	return in, nil
}

// decisionOptions returns the decision options of a search from the settings.
func (a *AcquisitionSettings) decisionOptions(interactive bool) decision.Options {
	o := decision.Options{
		Interactive:         interactive,
		UsenetRetentionDays: a.UsenetRetentionDays,
		UsenetMinimumAge:    time.Duration(a.UsenetMinimumAgeMin) * time.Minute,
	}
	switch a.PreferredProtocol {
	case ProtocolUsenet:
		o.PreferredProtocol = decision.Usenet
	case ProtocolTorrent:
		o.PreferredProtocol = decision.Torrent
	}
	return o
}

// searchOptions reads the settings and returns the decision options of a search.
func (s *Service) searchOptions(ctx context.Context, interactive bool) decision.Options {
	a := s.AcquisitionSettings(ctx)
	return a.decisionOptions(interactive)
}

// intervalReader returns a function reading an interval setting, for the job
// scheduler (which calls it outside any request).
func (s *Service) intervalReader(get func(a *AcquisitionSettings) time.Duration) func() time.Duration {
	return func() time.Duration {
		ctx, cancel := context.WithTimeout(context.Background(), settingsReadTimeout)
		defer cancel()
		a := s.AcquisitionSettings(ctx)
		return get(&a)
	}
}

// settingString reads a string setting, def when unset or invalid.
func (s *Service) settingString(ctx context.Context, key, def string) string {
	v, err := store.GetSetting[string](ctx, s.st, key)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("invalid setting, using the default", slog.String("key", key), slog.Any("error", err))
		}
		return def
	}
	return v
}
