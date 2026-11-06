package naming_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fs0ciety7000/gonema/internal/naming"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

func vars(title string, year int, release string) naming.Vars {
	p := parser.Parse(release)
	return naming.Vars{Title: title, Year: year, Quality: quality.FromParsed(&p), Release: p}
}

func TestRender(t *testing.T) {
	dune := vars("Dune: Part Two", 2024, "Dune.Part.Two.2024.2160p.UHD.BluRay.DV.HDR10.TrueHD.Atmos.7.1.x265-FLUX")
	dune.TmdbID, dune.ImdbID, dune.OriginalTitle = 693134, "tt15239678", "Dune: Part Two"
	plain := vars("Some Movie", 2019, "Some.Movie.2019.1080p.WEB-DL.H264")
	bare := naming.Vars{Title: "Some Movie"} // nothing known but the title
	blade := vars("Blade Runner", 1982, "Blade.Runner.1982.Final.Cut.1080p.BluRay.DTS-HD.MA.5.1.x264-AMIABLE")
	proper := vars("Heat", 1995, "Heat.1995.PROPER.1080p.BluRay.x264-GRP")
	realProper := vars("Heat", 1995, "Heat.1995.REAL.PROPER.1080p.BluRay.x264-GRP")
	repack3 := vars("Heat", 1995, "Heat.1995.1080p.BluRay.x264.v3-GRP")
	amelie := vars("Amélie", 2001, "Amelie.2001.1080p.BluRay.x264-GRP")
	amelie.OriginalTitle = "Le Fabuleux Destin d'Amélie Poulain"
	matrix := vars("The Matrix", 1999, "The.Matrix.1999.1080p.BluRay.x264-GRP")
	quiet := vars("A Quiet Place", 2018, "A.Quiet.Place.2018.720p.WEB-DL.DD5.1.H264-GRP")
	fast := vars("Fast & Furious", 2009, "Fast.and.Furious.2009.1080p.BluRay.x264-GRP")
	ocean := vars("Ocean's Eleven", 2001, "Oceans.Eleven.2001.1080p.BluRay.x264-GRP")
	mrsmith := vars("Mr. & Mrs. Smith", 2005, "Mr.and.Mrs.Smith.2005.1080p.BluRay.x264-GRP")
	illegal := naming.Vars{Title: `What? <Is> "This" | A*Title/Or\Not: Really`, Year: 2020}
	con := naming.Vars{Title: "CON"}
	long := naming.Vars{Title: strings.Repeat("Très long titre ", 30), Year: 2020}
	hdr10p := vars("X", 2020, "X.2020.2160p.WEB-DL.HDR10Plus.DDP5.1.H265-GRP")
	hlg := vars("X", 2020, "X.2020.2160p.HDTV.HLG.H265-GRP")
	cf := plain
	cf.CustomFormats = []string{"HDR10+", "Trusted"}
	ed := vars("Aliens", 1986, "Aliens.1986.Directors.Cut.Remastered.1080p.BluRay.x264-GRP")
	noGroup := vars("Some Movie", 2019, "Some.Movie.2019.1080p.BluRay.x264")
	unknownQ := naming.Vars{Title: "X", Year: 2000, Quality: quality.Unknown}

	cases := []struct {
		name, tmpl string
		v          naming.Vars
		want       string
	}{
		{"default full", naming.DefaultFileTemplate, dune, "Dune - Part Two (2024) [Bluray-2160p][DV HDR10][TrueHD Atmos 7.1]-FLUX"},
		{"default plain web", naming.DefaultFileTemplate, plain, "Some Movie (2019) [WEBDL-1080p]"},
		{"default title only", naming.DefaultFileTemplate, bare, "Some Movie"},
		{"default edition", naming.DefaultFileTemplate, blade, "Blade Runner (1982) [Final Cut][Bluray-1080p][DTS-HD MA 5.1]-AMIABLE"},
		{"default proper", naming.DefaultFileTemplate, proper, "Heat (1995) [Bluray-1080p Proper]-GRP"},
		{"default no group", naming.DefaultFileTemplate, noGroup, "Some Movie (2019) [Bluray-1080p]"},
		{"default two editions", naming.DefaultFileTemplate, ed, "Aliens (1986) [Director's Cut Remastered][Bluray-1080p]-GRP"},
		{"default unknown quality", naming.DefaultFileTemplate, unknownQ, "X (2000)"},
		{"title", "{Movie Title}", dune, "Dune - Part Two"},
		{"title lower", "{movie title}", dune, "dune - part two"},
		{"title upper", "{MOVIE TITLE}", dune, "DUNE - PART TWO"},
		{"title dots", "{Movie.Title}.{Release.Year}", plain, "Some.Movie.2019"},
		{"title underscores", "{Movie_Title}_{Release_Year}", plain, "Some_Movie_2019"},
		{"dots empty year", "{Movie.Title}.{Release.Year}.{Quality.Title}", naming.Vars{Title: "Some Movie", Quality: quality.WebDL1080p}, "Some.Movie.WEBDL-1080p"},
		{"clean title colon", "{Movie CleanTitle}", dune, "Dune Part Two"},
		{"clean title ampersand", "{Movie CleanTitle}", fast, "Fast and Furious"},
		{"clean title apostrophe", "{Movie CleanTitle}", ocean, "Oceans Eleven"},
		{"title the", "{Movie TitleThe}", matrix, "Matrix, The"},
		{"title the a", "{Movie TitleThe}", quiet, "Quiet Place, A"},
		{"title the none", "{Movie TitleThe}", plain, "Some Movie"},
		{"title keeps dots", "{Movie Title} ({Release Year})", mrsmith, "Mr. & Mrs. Smith (2005)"},
		{"original title", "{Original Title} ({Release Year})", amelie, "Le Fabuleux Destin d'Amélie Poulain (2001)"},
		{"original title fallback", "{Original Title}", plain, "Some Movie"},
		{"empty year parens removed", "{Movie Title} ({Release Year})", bare, "Some Movie"},
		{"empty brackets removed", "{Movie Title} [{Quality Title}]", bare, "Some Movie"},
		{"quality full real", "{Movie Title} {Quality Full}", realProper, "Heat Bluray-1080p REAL Proper"},
		{"quality full v3", "{Movie Title} {Quality Full}", repack3, "Heat Bluray-1080p Proper v3"},
		{"quality title", "{Movie Title} - {Quality Title}", proper, "Heat - Bluray-1080p"},
		{"video codec", "{Movie Title} {MediaInfo VideoCodec}", dune, "Dune - Part Two h265"},
		{"video codec upper", "{Movie Title} {MEDIAINFO VIDEOCODEC}", dune, "Dune - Part Two H265"},
		{"hdr10plus", "{Movie Title} {[MediaInfo VideoDynamicRange]}", hdr10p, "X [HDR10+]"},
		{"hlg", "{Movie Title} {[MediaInfo VideoDynamicRange]}", hlg, "X [HLG]"},
		{"no hdr", "{Movie Title} {[MediaInfo VideoDynamicRange]}", plain, "Some Movie"},
		{"audio group", "{Movie Title} {[MediaInfo AudioCodec MediaInfo AudioChannels]}", quiet, "A Quiet Place [AC3 5.1]"},
		{"audio group empty", "{Movie Title} {[MediaInfo AudioCodec MediaInfo AudioChannels]}", plain, "Some Movie"},
		{"edition brackets", "{Movie Title} {[Edition Tags]}", blade, "Blade Runner [Final Cut]"},
		{"edition empty", "{Movie Title} {[Edition Tags]} ({Release Year})", plain, "Some Movie (2019)"},
		{"group dash", "{Movie Title}{-Release Group}", dune, "Dune - Part Two-FLUX"},
		{"group dash empty", "{Movie Title}{-Release Group}", noGroup, "Some Movie"},
		{"group literal dash empty", "{Movie Title} - {Release Group}", noGroup, "Some Movie"},
		{"custom formats", "{Movie Title} {[Custom Formats]}", cf, "Some Movie [HDR10+ Trusted]"},
		{"custom formats empty", "{Movie Title} {[Custom Formats]}", plain, "Some Movie"},
		{"tmdb", "{Movie Title} {[tmdb-ID]}", dune, "Dune - Part Two [tmdb-693134]"},
		{"tmdb plex braces", "{Movie Title} ({Release Year}) {{{tmdb-ID}}}", dune, "Dune - Part Two (2024) {tmdb-693134}"},
		{"tmdb empty braces", "{Movie Title} {{{tmdb-ID}}}", plain, "Some Movie"},
		{"imdb", "{Movie Title} {[imdb-ID]}", dune, "Dune - Part Two [imdb-tt15239678]"},
		{"imdb empty", "{Movie Title} {[imdb-ID]}", plain, "Some Movie"},
		{"windows illegal", "{Movie Title} ({Release Year})", illegal, "What Is 'This' ATitle-Or-Not - Really (2020)"},
		{"windows reserved", "{Movie Title}", con, "CON_"},
		{"literal text", "Film - {Movie Title}", plain, "Film - Some Movie"},
		{"leading separators trimmed", "{-Release Group} {Movie Title}", noGroup, "Some Movie"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tp, err := naming.Parse(c.tmpl)
			if err != nil {
				t.Fatalf("Parse(%q): %v", c.tmpl, err)
			}
			if got := tp.Render(&c.v); got != c.want {
				t.Errorf("Render(%q)\n got  %q\n want %q", c.tmpl, got, c.want)
			}
		})
	}
	t.Run("long name is cut", func(t *testing.T) {
		tp, _ := naming.Parse(naming.DefaultFileTemplate)
		got := tp.FileName(&long, "MKV")
		if !strings.HasSuffix(got, ".mkv") || len(got) > 255 || !utf8.ValidString(got) {
			t.Errorf("FileName = %q (%d bytes)", got, len(got))
		}
	})
}

func TestFileNameExtension(t *testing.T) {
	tp, err := naming.Parse("{Movie Title}")
	if err != nil {
		t.Fatal(err)
	}
	v := naming.Vars{Title: "X"}
	for ext, want := range map[string]string{"mkv": "X.mkv", ".MP4": "X.mp4", "": "X", "../x": "X", "m2ts": "X.m2ts", "verylongextension": "X"} {
		if got := tp.FileName(&v, ext); got != want {
			t.Errorf("FileName(%q) = %q, want %q", ext, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"":                                   "empty",
		"   ":                                "empty",
		"{Movie Title} {Bogus}":              "unknown token {Bogus}",
		"{Movie Title} {Movie Titel}":        "unknown token {Movie Titel}",
		"{Movie Title} {Release Year":        "unclosed",
		"{Movie Title} }":                    "unmatched",
		"{Movie Title}/{Release Year}":       "cannot contain",
		`{Movie Title}\x`:                    "cannot contain",
		"{Release Year}":                     "must contain",
		"{Movie Title} {}":                   "unknown token",
		"{Movie Title} {[]}":                 "unknown token",
		"{Movie Title}\t":                    "control character",
		strings.Repeat("{Movie Title}", 100): "too long",
		"{Movie Title} ":                    "private-use",
	}
	for tmpl, want := range cases {
		err := naming.Validate(tmpl)
		if err == nil {
			t.Errorf("Validate(%q) = nil, want error containing %q", tmpl, want)
			continue
		}
		if !errors.Is(err, naming.ErrInvalid) {
			t.Errorf("Validate(%q): %v does not wrap ErrInvalid", tmpl, err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate(%q) = %q, want it to contain %q", tmpl, err, want)
		}
	}
	// Unknown tokens list the available ones.
	err := naming.Validate("{Movie Title} {Foo} {Bar}")
	if err == nil || !strings.Contains(err.Error(), "{Bar}, {Foo}") || !strings.Contains(err.Error(), "{Release Group}") {
		t.Errorf("message = %v", err)
	}
}

func TestPreviewAndTokens(t *testing.T) {
	v := naming.Example()
	got, err := naming.Preview(naming.DefaultFileTemplate, &v, "mkv")
	if err != nil || got != "Dune - Part Two (2024) [Bluray-2160p][DV HDR10][TrueHD Atmos 7.1]-FLUX.mkv" {
		t.Errorf("Preview = %q, %v", got, err)
	}
	if _, err := naming.Preview("{Nope}", &v, "mkv"); err == nil {
		t.Error("Preview accepted an unknown token")
	}
	toks := naming.Tokens()
	if len(toks) != 16 || toks[0].Name != "Movie Title" {
		t.Fatalf("Tokens = %+v", toks)
	}
	for _, tk := range toks {
		if err := naming.Validate("{Movie Title} {" + tk.Name + "}"); err != nil {
			t.Errorf("token %q does not validate: %v", tk.Name, err)
		}
	}
}

func FuzzRender(f *testing.F) {
	f.Add(naming.DefaultFileTemplate, "Dune: Part Two", "Dune.Part.Two.2024.2160p.BluRay.x265-FLUX")
	f.Add("{Movie.Title}..{{x}}", "a/b", "")
	f.Fuzz(func(t *testing.T, tmpl, title, release string) {
		tp, err := naming.Parse(tmpl)
		if err != nil {
			return
		}
		v := vars(title, 2000, release)
		name := tp.FileName(&v, "mkv")
		if name == "" || strings.ContainsAny(name, `/\:*?"<>|`) || len(name) > 255 || !utf8.ValidString(name) {
			t.Fatalf("FileName(%q, %q) = %q", tmpl, title, name)
		}
	})
}

func BenchmarkRender(b *testing.B) {
	tp, err := naming.Parse(naming.DefaultFileTemplate)
	if err != nil {
		b.Fatal(err)
	}
	v := naming.Example()
	b.ReportAllocs()
	for b.Loop() {
		_ = tp.FileName(&v, "mkv")
	}
}
