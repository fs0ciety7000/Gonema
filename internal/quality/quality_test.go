package quality_test

import (
	"testing"

	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

func TestAll(t *testing.T) {
	t.Parallel()
	all := quality.All()
	if len(all) != 30 {
		t.Fatalf("catalog has %d qualities, want 30", len(all))
	}
	seen := map[quality.ID]bool{}
	for i, q := range all {
		if q.Rank != i {
			t.Errorf("%s: rank %d at index %d", q.ID, q.Rank, i)
		}
		if seen[q.ID] {
			t.Errorf("duplicate ID %s", q.ID)
		}
		seen[q.ID] = true
		if q.Name == "" {
			t.Errorf("%s has no name", q.ID)
		}
	}
	all[0].Name = "mutated"
	if quality.All()[0].Name == "mutated" {
		t.Error("All returned the catalog itself, not a copy")
	}
}

func TestLookup(t *testing.T) {
	t.Parallel()
	q, ok := quality.Lookup(quality.Bluray1080p)
	if !ok || q.Name != "Bluray-1080p" || q.Resolution != parser.Resolution1080p || q.Source != parser.SourceBluRay {
		t.Errorf("Lookup(bluray-1080p) = %+v, %v", q, ok)
	}
	if _, ok := quality.Lookup("bluray-4320p"); ok {
		t.Error("Lookup of an unknown ID succeeded")
	}
	if got := quality.ID("nope").Name(); got != "nope" {
		t.Errorf("Name of unknown ID = %q, want the ID", got)
	}
	if !quality.Remux2160p.Valid() || quality.ID("").Valid() {
		t.Error("Valid is wrong")
	}
}

func TestFromParsed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want quality.ID
	}{
		{"Alien.Romulus.2024.1080p.CAM.x264-GRP", quality.Cam},
		{"Alien.Romulus.2024.TELESYNC.x264-GRP", quality.Telesync},
		{"Alien.Romulus.2024.TELECINE.x264-GRP", quality.Telecine},
		{"Alien.Romulus.2024.DVDSCR.x264-GRP", quality.Screener},
		{"Alien.Romulus.2024.R5.XviD-GRP", quality.Regional},
		{"Alien.Romulus.2024.DVDRip.XviD-GRP", quality.DVD},
		{"Alien.Romulus.GERMAN.2024.DL.PAL.DVDR.DIRFIX-Goodboy", quality.DVDR},
		{"Alien.Romulus.2024.HDTV.x264-GRP", quality.SDTV},
		{"Alien.Romulus.2024.CZECH.720p.HDTV.x264-iCZi", quality.HDTV720p},
		{"Alien.Romulus.2024.CZECH.1080p.HDTV.x265-iCZi", quality.HDTV1080p},
		{"Alien.Romulus.2024.2160p.HDTV.x265-GRP", quality.HDTV2160p},
		{"Alien.Romulus.2024.WEBRip.x264-GRP", quality.WebRip480p},
		{"Alien.Romulus.2024.720p.WEBRip.x264-GRP", quality.WebRip720p},
		{"Alien.Romulus.2024.1080p.WEBRip.x264-GRP", quality.WebRip1080p},
		{"Alien.Romulus.2024.2160p.WEBRip.x265-GRP", quality.WebRip2160p},
		{"Alien.Romulus.2024.POLISH.WEB.H264-FLAME", quality.WebDL480p},
		{"Alien.Romulus.2024.POLISH.720p.WEB.H264-FLAME", quality.WebDL720p},
		{"Alien.Romulus.2024.POLISH.1080p.WEB.H264-FLAME", quality.WebDL1080p},
		{"Alien.Romulus.2024.POLISH.2160p.WEB.H265-FLAME", quality.WebDL2160p},
		{"Alien.Romulus.2024.TRUEFRENCH.BDRip.x264-Ulysse", quality.Bluray480p},
		{"Alien.Romulus.2024.576p.BluRay.x264-GRP", quality.Bluray576p},
		{"Alien.Romulus.2024.TRUEFRENCH.720p.BluRay.x264-Ulysse", quality.Bluray720p},
		{"Alien.Romulus.2024.1080p.BluRay.x264-GRP", quality.Bluray1080p},
		{"Alien.Romulus.2024.MULTi.TRUEFRENCH.2160p.UHD.BluRay.x265-SESKAPiLE", quality.Bluray2160p},
		{"Alien.Romulus.2024.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP", quality.Remux1080p},
		{"Alien.Romulus.2024.2160p.UHD.BluRay.REMUX.HDR.HEVC.Atmos-GRP", quality.Remux2160p},
		{"Alien.Romulus.2024.MULTi.COMPLETE.BLURAY-GMA", quality.BRDisk1080p},
		{"Alien.Romulus.2024.2160p.COMPLETE.UHD.BLURAY-GRP", quality.BRDisk2160p},
		{"Alien.Romulus.2024.1080p.x264-GRP", quality.Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := parser.Parse(tt.name)
			if got := quality.FromParsed(&p); got != tt.want {
				t.Errorf("FromParsed = %s, want %s (source %s, modifier %s, resolution %s)",
					got, tt.want, p.Source, p.Modifier, p.Resolution)
			}
		})
	}
}

// TestFromParsed_structs covers combinations that release names rarely produce.
func TestFromParsed_structs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   parser.ParsedRelease
		want quality.ID
	}{
		{"raw hd", parser.ParsedRelease{Source: parser.SourceHDTV, Modifier: parser.ModifierRawHD, Resolution: parser.Resolution1080p}, quality.RawHD},
		{"576p hdtv is sdtv", parser.ParsedRelease{Source: parser.SourceHDTV, Resolution: parser.Resolution576p}, quality.SDTV},
		{"remux without resolution", parser.ParsedRelease{Source: parser.SourceBluRay, Modifier: parser.ModifierRemux}, quality.Remux1080p},
		{"480p bluray", parser.ParsedRelease{Source: parser.SourceBluRay, Resolution: parser.Resolution480p}, quality.Bluray480p},
		{"unknown source with resolution", parser.ParsedRelease{Resolution: parser.Resolution2160p}, quality.Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := quality.FromParsed(&tt.in); got != tt.want {
				t.Errorf("FromParsed = %s, want %s", got, tt.want)
			}
		})
	}
}
