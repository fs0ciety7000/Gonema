package acquire

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/events"
)

func TestAcquisitionSettingsValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(a *AcquisitionSettings)
		want string // substring of the message; "" for valid
	}{
		{"defaults", func(*AcquisitionSettings) {}, ""},
		{"bounds", func(a *AcquisitionSettings) {
			a.UsenetRetentionDays, a.UsenetMinimumAgeMin, a.MaxAttemptsPerDay = 10000, 1440, 50
			a.RSSIntervalMin, a.SearchMissingIntervalHours, a.PreferredProtocol = 120, 168, ProtocolTorrent
		}, ""},
		{"negative retention", func(a *AcquisitionSettings) { a.UsenetRetentionDays = -1 }, "Usenet retention must be between 0 (unlimited) and 10000 days"},
		{"minimum age too long", func(a *AcquisitionSettings) { a.UsenetMinimumAgeMin = 1441 }, "minimum age must be between 0 and 1440 minutes"},
		{"unknown protocol", func(a *AcquisitionSettings) { a.PreferredProtocol = "ftp" }, `preferred protocol must be usenet, torrent or none, not "ftp"`},
		{"no attempt", func(a *AcquisitionSettings) { a.MaxAttemptsPerDay = 0 }, "Attempts per day must be between 1 and 50 (default 6)"},
		{"RSS too often", func(a *AcquisitionSettings) { a.RSSIntervalMin = 5 }, "RSS interval must be between 10 and 120 minutes"},
		{"missing search too often", func(a *AcquisitionSettings) { a.SearchMissingIntervalHours = 1 }, "every 6 to 168 hours"},
		{"several at once", func(a *AcquisitionSettings) { a.RSSIntervalMin, a.MaxAttemptsPerDay = 1, 99 }, "Attempts per day must be between 1 and 50 (default 6). The RSS interval"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := DefaultAcquisitionSettings()
			tt.edit(&a)
			err := a.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("no error, want %q", tt.want)
			case tt.want != "" && (!strings.Contains(err.Error(), tt.want) || !errors.Is(err, ErrInvalid)):
				t.Fatalf("error %q (invalid %v), want it to contain %q", err, errors.Is(err, ErrInvalid), tt.want)
			}
		})
	}
}

func TestAcquisitionSettingsRoundTrip(t *testing.T) {
	e := newEnv(t)
	if got := e.svc.AcquisitionSettings(e.ctx); got != DefaultAcquisitionSettings() {
		t.Errorf("defaults = %+v", got)
	}
	sub := e.bus.Subscribe(8, func(ev *events.Event) bool { return ev.Type == events.SettingsChanged })
	defer sub.Close()

	in := AcquisitionSettings{
		UsenetRetentionDays: 4000, UsenetMinimumAgeMin: 30, PreferredProtocol: " Usenet ", MaxAttemptsPerDay: 3,
		BlocklistGroupOnFailure: true, RSSIntervalMin: 20, SearchMissingIntervalHours: 48,
	}
	out, err := e.svc.SetAcquisitionSettings(e.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	want := in
	want.PreferredProtocol = ProtocolUsenet
	if diff := cmp.Diff(want, out); diff != "" {
		t.Errorf("saved (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(want, e.svc.AcquisitionSettings(e.ctx)); diff != "" {
		t.Errorf("read back (-want +got):\n%s", diff)
	}
	select {
	case <-sub.C():
	case <-time.After(5 * time.Second):
		t.Error("no settings.changed event")
	}
	// The existing per-key settings stay the source of the failure handling.
	if !e.svc.settingBool(e.ctx, SettingBlocklistGroup) || e.svc.settingInt(e.ctx, SettingMaxAttempts, 0) != 3 {
		t.Error("failure settings not stored under their keys")
	}

	// A refused update changes nothing.
	bad := want
	bad.RSSIntervalMin = 1
	if _, err := e.svc.SetAcquisitionSettings(e.ctx, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid update = %v", err)
	}
	if got := e.svc.AcquisitionSettings(e.ctx); got.RSSIntervalMin != 20 {
		t.Errorf("RSS interval = %d after a refused update", got.RSSIntervalMin)
	}

	// Out-of-range values stored by hand are clamped, an unknown protocol ignored.
	for key, v := range map[string]any{SettingRSSInterval: 1, SettingPreferredProtocol: "ftp", SettingUsenetRetention: -5} {
		if err := setSetting(e, key, v); err != nil {
			t.Fatal(err)
		}
	}
	got := e.svc.AcquisitionSettings(e.ctx)
	if got.RSSIntervalMin != 10 || got.PreferredProtocol != ProtocolNone || got.UsenetRetentionDays != 0 {
		t.Errorf("clamped = %+v", got)
	}
}

// TestAcquisitionSettingsReachDecisions: automatic, RSS and interactive
// searches build their decision options from the settings.
func TestAcquisitionSettingsReachDecisions(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	if _, err := e.svc.SetAcquisitionSettings(e.ctx, AcquisitionSettings{
		UsenetRetentionDays: 3000, UsenetMinimumAgeMin: 45, PreferredProtocol: ProtocolTorrent, MaxAttemptsPerDay: 6,
		RSSIntervalMin: 15, SearchMissingIntervalHours: 24,
	}); err != nil {
		t.Fatal(err)
	}
	want := decision.Options{UsenetRetentionDays: 3000, UsenetMinimumAge: 45 * time.Minute, PreferredProtocol: decision.Torrent}

	auto := e.svc.searchOptions(e.ctx, false)
	if diff := cmp.Diff(want, auto); diff != "" {
		t.Errorf("automatic (-want +got):\n%s", diff)
	}
	interactive := e.svc.searchOptions(e.ctx, true)
	wantI := want
	wantI.Interactive = true
	if diff := cmp.Diff(wantI, interactive); diff != "" {
		t.Errorf("interactive (-want +got):\n%s", diff)
	}
	sh, err := e.svc.loadShared(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, sh.opts); diff != "" {
		t.Errorf("RSS (-want +got):\n%s", diff)
	}
	mc, err := e.svc.loadMovie(e.ctx, movie.ID, sh, sh.opts)
	if err != nil {
		t.Fatal(err)
	}
	if o := mc.cfg.Options; o.UsenetRetentionDays != 3000 || o.PreferredProtocol != decision.Torrent || o.Now.IsZero() {
		t.Errorf("movie options = %+v", o)
	}
	none := DefaultAcquisitionSettings()
	if o := none.decisionOptions(false); o.PreferredProtocol != "" {
		t.Errorf("none = %q, want no preference", o.PreferredProtocol)
	}
}
