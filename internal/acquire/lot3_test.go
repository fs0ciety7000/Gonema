package acquire

import (
	"errors"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// TestProfileOverridesInDecisions checks that the size limits and the
// preferred protocol of a profile reach the decision engine.
func TestProfileOverridesInDecisions(t *testing.T) {
	e := newEnv(t)
	m := e.addMovie() // Fight Club, 139 min, HD-1080p
	const title = "Fight.Club.1999.1080p.BluRay.x264-GRP"
	size := int64(10 << 30) // 73.7 MB/min: within the defaults of Bluray-1080p

	tests := []struct {
		name     string
		sizes    quality.Sizes
		protocol string
		accepted bool
		code     decision.Code
	}{
		{"defaults", nil, "", true, ""},
		{"profile max below the size", quality.Sizes{quality.Bluray1080p: {MinMBPerMin: 1, PreferredMBPerMin: 2, MaxMBPerMin: 30}}, "torrent", false, decision.CodeSizeTooLarge},
		{"profile min above the size", quality.Sizes{quality.Bluray1080p: {MinMBPerMin: 100, PreferredMBPerMin: 120, MaxMBPerMin: 0}}, "usenet", false, decision.CodeSizeTooSmall},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := e.lib.GetQualityProfile(e.ctx, e.hdID)
			if err != nil {
				t.Fatal(err)
			}
			in := library.ProfileInput{
				Name: p.Name, Items: p.Profile.Items, Cutoff: p.Profile.Cutoff, UpgradeAllowed: p.Profile.UpgradeAllowed,
				MinScore: p.Profile.MinScore, CutoffScore: p.Profile.CutoffScore, MinUpgradeStep: p.Profile.MinUpgradeStep,
				Languages: p.Profile.Languages, PreferredProtocol: tt.protocol, Sizes: tt.sizes,
			}
			if in.Sizes == nil {
				in.Sizes = quality.Sizes{}
			}
			if _, _, err := e.lib.UpdateQualityProfile(e.ctx, e.hdID, in); err != nil {
				t.Fatal(err)
			}
			mc, err := e.svc.loadMovie(e.ctx, m.ID, nil, e.svc.searchOptions(e.ctx, true))
			if err != nil {
				t.Fatal(err)
			}
			if got := string(mc.cfg.Options.PreferredProtocol); tt.protocol != "" && got != tt.protocol {
				t.Errorf("preferred protocol = %q, want %q", got, tt.protocol)
			}
			pv, err := e.svc.PreviewRelease(e.ctx, PreviewRequest{Title: title, MovieID: m.ID, Size: size, Protocol: decision.Torrent})
			if err != nil {
				t.Fatal(err)
			}
			d := pv.Decision
			if d == nil || d.Accepted != tt.accepted || pv.Quality != quality.Bluray1080p || pv.Profile != quality.ProfileHD1080p {
				t.Fatalf("preview = %+v", pv)
			}
			if tt.code != "" && (len(d.Rejections) == 0 || d.Rejections[0].Code != tt.code) {
				t.Errorf("rejections = %+v, want %s", d.Rejections, tt.code)
			}
		})
	}
}

func TestPreviewReleaseErrors(t *testing.T) {
	e := newEnv(t)
	m := e.addMovie()
	tests := []struct {
		name string
		req  PreviewRequest
		kind error
	}{
		{"empty title", PreviewRequest{Title: " ", ProfileID: e.hdID}, ErrInvalid},
		{"no profile nor movie", PreviewRequest{Title: "x"}, ErrInvalid},
		{"negative size", PreviewRequest{Title: "x", ProfileID: e.hdID, Size: -1}, ErrInvalid},
		{"bad protocol", PreviewRequest{Title: "x", ProfileID: e.hdID, Protocol: "ftp"}, ErrInvalid},
		{"unknown movie", PreviewRequest{Title: "x", MovieID: 9999}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.PreviewRelease(e.ctx, tt.req)
			if err == nil || (tt.kind != nil && !errors.Is(err, tt.kind)) {
				t.Errorf("err = %v, want %v", err, tt.kind)
			}
		})
	}
	// Score and cutoff of a UHD remux in the starter Ultra-HD profile scoring
	// Dolby Vision.
	uhd, err := e.st.Read().GetQualityProfileByName(e.ctx, quality.ProfileUltraHD)
	if err != nil {
		t.Fatal(err)
	}
	pv, err := e.svc.PreviewRelease(e.ctx, PreviewRequest{
		Title: "Fight.Club.1999.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1-GRP", ProfileID: uhd.ID, MovieID: m.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Quality != quality.Remux2160p || pv.ProfileID != uhd.ID || pv.Score.Total <= 0 || len(pv.Score.Rules) == 0 || pv.ParseMicros <= 0 {
		t.Errorf("preview = %+v", pv)
	}
}
