package metadata

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func d(s string) Date {
	v, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

func TestParseDate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"1999-10-15", "1999-10-15", false},
		{"2012-08-28T00:00:00.000Z", "2012-08-28", false},
		{"1999-02-30", "", true},
		{"15/10/1999", "", true},
		{"1999", "", true},
	}
	for _, tt := range tests {
		got, err := ParseDate(tt.in)
		if (err != nil) != tt.wantErr || got.String() != tt.want {
			t.Errorf("ParseDate(%q) = %q, %v; want %q, err %v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestDateCompareAndJSON(t *testing.T) {
	t.Parallel()
	a, b := d("1999-10-15"), d("2000-01-01")
	if a.Compare(b) != -1 || b.Compare(a) != 1 || a.Compare(a) != 0 {
		t.Error("Compare order")
	}
	if (Date{}).Compare(a) != 1 || a.Compare(Date{}) != -1 || (Date{}).Before(a) || a.Before(Date{}) {
		t.Error("zero date must sort last and never be before")
	}
	if got := a.AddDays(90).String(); got != "2000-01-13" {
		t.Errorf("AddDays = %s", got)
	}
	b2, err := json.Marshal(struct {
		D Date `json:"d,omitzero"`
		E Date `json:"e,omitzero"`
	}{D: a})
	if err != nil || string(b2) != `{"d":"1999-10-15"}` {
		t.Errorf("Marshal = %s, %v", b2, err)
	}
	var back struct{ D Date }
	if err := json.Unmarshal([]byte(`{"D":"1999-10-15"}`), &back); err != nil || back.D != a {
		t.Errorf("Unmarshal = %v, %v", back.D, err)
	}
}

func rd(region string, dates ...ReleaseDate) ReleaseDates {
	return ReleaseDates{Region: region, Dates: dates}
}

func rel(t ReleaseType, date, cert string) ReleaseDate {
	return ReleaseDate{Type: t, Date: d(date), Certification: cert}
}

func TestSelectReleases(t *testing.T) {
	t.Parallel()
	all := []ReleaseDates{
		rd("US",
			rel(ReleasePremiere, "2024-01-10", ""),
			rel(ReleaseTheatrical, "2024-03-01", "PG-13"),
			rel(ReleaseDigital, "2024-04-16", ""),
		),
		rd("FR",
			rel(ReleaseTheatrical, "2024-02-28", "TP"),
			rel(ReleasePhysical, "2024-07-01", ""),
		),
		rd("DE", rel(ReleaseTheatricalLimited, "2024-02-20", "12")),
		rd("JP", rel(ReleasePhysical, "2024-06-20", "G")),
		rd("GB", rel(ReleasePremiere, "2024-01-05", "")),
	}
	tests := []struct {
		name   string
		region string
		want   ReleaseSelection
	}{
		{
			name: "configured region first, per kind", region: "fr",
			want: ReleaseSelection{
				Cinema:        DatedRelease{Date: d("2024-02-28"), Region: "FR", Type: ReleaseTheatrical},
				Digital:       DatedRelease{Date: d("2024-04-16"), Region: "US", Type: ReleaseDigital}, // FR has none: US
				Physical:      DatedRelease{Date: d("2024-07-01"), Region: "FR", Type: ReleasePhysical},
				Certification: "TP", CertificationRegion: "FR",
			},
		},
		{
			name: "US fallback, then earliest", region: "",
			want: ReleaseSelection{
				Cinema:        DatedRelease{Date: d("2024-03-01"), Region: "US", Type: ReleaseTheatrical},
				Digital:       DatedRelease{Date: d("2024-04-16"), Region: "US", Type: ReleaseDigital},
				Physical:      DatedRelease{Date: d("2024-06-20"), Region: "JP", Type: ReleasePhysical}, // earliest of FR, JP
				Certification: "PG-13", CertificationRegion: "US",
			},
		},
		{
			name: "limited theatrical when no wide release", region: "DE",
			want: ReleaseSelection{
				Cinema:        DatedRelease{Date: d("2024-02-20"), Region: "DE", Type: ReleaseTheatricalLimited},
				Digital:       DatedRelease{Date: d("2024-04-16"), Region: "US", Type: ReleaseDigital},
				Physical:      DatedRelease{Date: d("2024-06-20"), Region: "JP", Type: ReleasePhysical},
				Certification: "12", CertificationRegion: "DE",
			},
		},
		{
			name: "premiere as last resort; no certification from another country", region: "GB",
			want: ReleaseSelection{
				Cinema:        DatedRelease{Date: d("2024-01-05"), Region: "GB", Type: ReleasePremiere},
				Digital:       DatedRelease{Date: d("2024-04-16"), Region: "US", Type: ReleaseDigital},
				Physical:      DatedRelease{Date: d("2024-06-20"), Region: "JP", Type: ReleasePhysical},
				Certification: "PG-13", CertificationRegion: "US",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, SelectReleases(all, tt.region)); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}

	t.Run("earliest across regions without US", func(t *testing.T) {
		t.Parallel()
		got := SelectReleases([]ReleaseDates{
			rd("IT", rel(ReleaseTheatrical, "2001-05-02", "")),
			rd("BE", rel(ReleaseTheatrical, "2001-05-02", "")),
			rd("ES", rel(ReleaseTheatrical, "2001-06-01", "")),
		}, "FR")
		want := ReleaseSelection{Cinema: DatedRelease{Date: d("2001-05-02"), Region: "BE", Type: ReleaseTheatrical}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("(-want +got):\n%s", diff)
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		if diff := cmp.Diff(ReleaseSelection{}, SelectReleases(nil, "FR")); diff != "" {
			t.Error(diff)
		}
	})
}

func TestDeriveStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 23, 30, 0, 0, time.UTC)
	cinema := func(s string) DatedRelease { return DatedRelease{Date: d(s), Region: "US", Type: ReleaseTheatrical} }
	tests := []struct {
		name       string
		sel        ReleaseSelection
		primary    Date
		want       Status
		wantReason string
	}{
		{"nothing known", ReleaseSelection{}, Date{}, StatusAnnounced, "no release date known"},
		{"future cinema", ReleaseSelection{Cinema: cinema("2026-12-18")}, Date{}, StatusAnnounced, "cinema release planned on 2026-12-18 (US, theatrical)"},
		{"cinema today", ReleaseSelection{Cinema: cinema("2026-11-05")}, Date{}, StatusInCinemas, "in cinemas since 2026-11-05"},
		{"cinema recent, home release later", ReleaseSelection{
			Cinema: cinema("2026-10-01"), Digital: DatedRelease{Date: d("2026-12-01"), Region: "US", Type: ReleaseDigital},
		}, Date{}, StatusInCinemas, "in cinemas since"},
		{"digital reached", ReleaseSelection{
			Cinema: cinema("2026-09-01"), Digital: DatedRelease{Date: d("2026-11-04"), Region: "US", Type: ReleaseDigital},
		}, Date{}, StatusReleased, "digital release on 2026-11-04"},
		{"physical reached", ReleaseSelection{
			Cinema: cinema("2026-06-01"), Physical: DatedRelease{Date: d("2026-10-04"), Region: "FR", Type: ReleasePhysical},
		}, Date{}, StatusReleased, "physical release on 2026-10-04 (FR, physical)"},
		{"old cinema only, assumed", ReleaseSelection{Cinema: cinema("1977-05-25")}, Date{}, StatusReleased, "assumed released 90 days"},
		{"cinema 89 days ago", ReleaseSelection{Cinema: cinema("2026-08-08")}, Date{}, StatusInCinemas, "in cinemas"},
		{"cinema 90 days ago", ReleaseSelection{Cinema: cinema("2026-08-07")}, Date{}, StatusReleased, "assumed"},
		{"primary date stands in", ReleaseSelection{}, d("2026-11-01"), StatusInCinemas, "in cinemas since 2026-11-01"},
		{"future digital only", ReleaseSelection{Digital: DatedRelease{Date: d("2027-01-01"), Region: "US", Type: ReleaseDigital}}, Date{}, StatusAnnounced, "no release date known"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, reason := DeriveStatus(tt.sel, tt.primary, now)
			if got != tt.want || !strings.Contains(reason, tt.wantReason) {
				t.Errorf("DeriveStatus = %s %q, want %s containing %q", got, reason, tt.want, tt.wantReason)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	nf := NewAPIError("tmdb", 404, 34, "not found", ErrNotFound)
	if !errors.Is(nf, ErrNotFound) || errors.Is(nf, ErrUnauthorized) {
		t.Error("APIError must match its kind only")
	}
	if got := nf.Error(); got != "metadata: tmdb answered HTTP 404: not found" {
		t.Errorf("Error() = %q", got)
	}
	var rl error = &RateLimitError{RetryAfter: 3 * time.Second}
	if !errors.Is(rl, ErrRateLimited) || !strings.Contains(rl.Error(), "3s") {
		t.Errorf("RateLimitError = %v", rl)
	}
}

func TestRatingsJSON(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Ratings{TMDB: &Rating{Value: 8.4, Votes: 100}})
	if err != nil || string(b) != `{"tmdb":{"value":8.4,"votes":100}}` {
		t.Errorf("Ratings JSON = %s, %v", b, err)
	}
}
