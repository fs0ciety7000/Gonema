package health

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/store"
)

// SettingHealth is the settings key of Settings (a JSON object).
const SettingHealth = "health.settings"

// ErrInvalid is matched by rejected settings; the message says why.
var ErrInvalid = errors.New("health: invalid settings")

// InvalidError is a rejected setting, in plain language.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// UserMessage returns the plain-language message (shown by the API as is).
func (e *InvalidError) UserMessage() string { return e.Msg }

// Is makes InvalidError match ErrInvalid.
func (e *InvalidError) Is(target error) bool { return target == ErrInvalid }

// Settings tune the thresholds of the checks.
type Settings struct {
	// A root folder is low on space below LowSpacePercent of its disk OR below
	// LowSpaceGB, whichever comes first. 0 disables that threshold.
	LowSpacePercent int `json:"lowSpacePercent" doc:"Warn when a root folder has less than this share of its disk free (0 to 50; 0 disables)"`
	LowSpaceGB      int `json:"lowSpaceGb" doc:"Warn when a root folder has less than this many GB free (0 to 100000; 0 disables)"`
	// RecycleBinMaxGB: a recycle bin larger than this is reported (info).
	RecycleBinMaxGB int `json:"recycleBinMaxGb" doc:"Mention the recycle bin once it holds more than this many GB (1 to 100000)"`
}

// DefaultSettings returns the thresholds used when none are stored.
func DefaultSettings() Settings {
	return Settings{LowSpacePercent: 5, LowSpaceGB: 50, RecycleBinMaxGB: 100}
}

// Validate reports every out-of-range value in one message.
func (s *Settings) Validate() error {
	var problems []string
	if s.LowSpacePercent < 0 || s.LowSpacePercent > 50 {
		problems = append(problems, "The low space share must be between 0 (off) and 50 percent.")
	}
	if s.LowSpaceGB < 0 || s.LowSpaceGB > 100000 {
		problems = append(problems, "The low space amount must be between 0 (off) and 100000 GB.")
	}
	if s.RecycleBinMaxGB < 1 || s.RecycleBinMaxGB > 100000 {
		problems = append(problems, "The recycle bin size must be between 1 and 100000 GB.")
	}
	if len(problems) == 0 {
		return nil
	}
	return &InvalidError{Msg: strings.Join(problems, " ")}
}

// SettingsStore reads and writes the health settings.
type SettingsStore struct{ St *store.Store }

// Get returns the stored settings; the defaults when unset or unreadable.
func (s SettingsStore) Get(ctx context.Context) Settings {
	set, err := store.GetSetting[Settings](ctx, s.St, SettingHealth)
	if err != nil || set.Validate() != nil {
		return DefaultSettings()
	}
	return set
}

// Set validates and stores the settings.
func (s SettingsStore) Set(ctx context.Context, set Settings) (Settings, error) {
	if err := set.Validate(); err != nil {
		return Settings{}, err
	}
	if err := store.SetSetting(ctx, s.St, SettingHealth, set); err != nil {
		return Settings{}, fmt.Errorf("health: save settings: %w", err)
	}
	return set, nil
}
