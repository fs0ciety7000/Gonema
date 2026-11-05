package match_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/parser"
)

// library is a realistic library (titles as TMDB writes them). IDs 46 and 47
// are synthetic homonyms (same title, same year) to exercise ambiguity.
var library = []match.Entry{
	{MovieID: 1, Title: "The Dark Knight", Year: 2008, IMDbID: "tt0468569"},
	{MovieID: 2, Title: "Inception", Year: 2010},
	{MovieID: 3, Title: "Fast & Furious", Year: 2009, AlternateTitles: []string{"Fast and Furious 4"}},
	{MovieID: 4, Title: "Rocky IV", Year: 1985},
	{MovieID: 5, Title: "Rocky II", Year: 1979},
	{MovieID: 6, Title: "Dune", Year: 1984},
	{MovieID: 7, Title: "Dune", Year: 2021, AlternateTitles: []string{"Dune: Part One"}},
	{MovieID: 8, Title: "Dune: Part Two", Year: 2024},
	{MovieID: 9, Title: "Amélie", OriginalTitle: "Le Fabuleux Destin d'Amélie Poulain", Year: 2001, AlternateTitles: []string{"Amelie from Montmartre"}},
	{MovieID: 10, Title: "Léon: The Professional", OriginalTitle: "Léon", Year: 1994, AlternateTitles: []string{"Leon", "The Professional"}},
	{MovieID: 11, Title: "The Lives of Others", OriginalTitle: "Das Leben der Anderen", Year: 2006},
	{MovieID: 12, Title: "Pan's Labyrinth", OriginalTitle: "El laberinto del fauno", Year: 2006},
	{MovieID: 13, Title: "Ocean's Eleven", Year: 2001},
	{MovieID: 14, Title: "Ocean's 11", Year: 1960},
	{MovieID: 15, Title: "Spider-Man: No Way Home", Year: 2021},
	{MovieID: 16, Title: "Mission: Impossible - Dead Reckoning Part One", Year: 2023},
	{MovieID: 17, Title: "Harry Potter and the Deathly Hallows: Part 2", Year: 2011},
	{MovieID: 18, Title: "Kill Bill: Vol. 1", Year: 2003},
	{MovieID: 19, Title: "Kill Bill: Vol. 2", Year: 2004},
	{MovieID: 20, Title: "Star Wars", Year: 1977, AlternateTitles: []string{"Star Wars: Episode IV - A New Hope"}},
	{MovieID: 21, Title: "The Thing", Year: 1982},
	{MovieID: 22, Title: "The Thing", Year: 2011},
	{MovieID: 23, Title: "Halloween", Year: 1978},
	{MovieID: 24, Title: "Halloween", Year: 2018},
	{MovieID: 25, Title: "Halloween", Year: 2007},
	{MovieID: 28, Title: "Les Misérables", Year: 2012},
	{MovieID: 29, Title: "Les Misérables", Year: 2019},
	{MovieID: 30, Title: "Batman v Superman: Dawn of Justice", Year: 2016},
	{MovieID: 31, Title: "Alien vs. Predator", Year: 2004},
	{MovieID: 32, Title: "Die Hard", Year: 1988},
	{MovieID: 33, Title: "Die Hard 2", Year: 1990, AlternateTitles: []string{"Die Hard 2: Die Harder"}},
	{MovieID: 34, Title: "La La Land", Year: 2016},
	{MovieID: 35, Title: "WALL·E", Year: 2008},
	{MovieID: 36, Title: "Schindler's List", Year: 1993},
	{MovieID: 37, Title: "Crouching Tiger, Hidden Dragon", OriginalTitle: "卧虎藏龙", Year: 2000, AlternateTitles: []string{"Wo hu cang long"}},
	{MovieID: 38, Title: "Spirited Away", OriginalTitle: "千と千尋の神隠し", Year: 2001, AlternateTitles: []string{"Sen to Chihiro no Kamikakushi"}},
	{MovieID: 39, Title: "The Intouchables", OriginalTitle: "Intouchables", Year: 2011},
	{MovieID: 40, Title: "Blade Runner 2049", Year: 2017},
	{MovieID: 41, Title: "2001: A Space Odyssey", Year: 1968},
	{MovieID: 42, Title: "Se7en", Year: 1995, AlternateTitles: []string{"Seven"}},
	{MovieID: 43, Title: "Mad Max: Fury Road", Year: 2015},
	{MovieID: 44, Title: "Shrek 2", Year: 2004},
	{MovieID: 45, Title: "Toy Story 3", Year: 2010},
	{MovieID: 46, Title: "Twins", Year: 2023},
	{MovieID: 47, Title: "Twins", Year: 2023},
	{MovieID: 48, Title: "Amour", Year: 2012},
	{MovieID: 49, Title: "Downfall", OriginalTitle: "Der Untergang", Year: 2004},
	{MovieID: 50, Title: "L'Auberge espagnole", Year: 2002, AlternateTitles: []string{"The Spanish Apartment"}},
	{MovieID: 51, Title: "Mission: Impossible II", Year: 2000},
	{MovieID: 52, Title: "The Godfather Part II", Year: 1974},
	{MovieID: 53, Title: "The Godfather Part III", Year: 1990},
	{MovieID: 54, Title: "Star Wars: Episode I - The Phantom Menace", Year: 1999},
	{MovieID: 55, Title: "Fast Five", Year: 2011, AlternateTitles: []string{"Fast & Furious 5"}},
	{MovieID: 56, Title: "Avengers: Endgame", Year: 2019},
	{MovieID: 57, Title: "Pirates of the Caribbean: The Curse of the Black Pearl", Year: 2003},
	{MovieID: 58, Title: "Rock'n Roll", Year: 2017},
	{MovieID: 59, Title: "Bienvenue chez les Ch'tis", Year: 2008, AlternateTitles: []string{"Welcome to the Sticks"}},
	{MovieID: 60, Title: "The Lord of the Rings: The Return of the King", Year: 2003},
	{MovieID: 61, Title: "Project Hail Mary"}, // announced, no date yet
	{MovieID: 62, Title: "Godzilla Minus One", OriginalTitle: "ゴジラ-1.0", Year: 2023},
	{MovieID: 63, Title: "The Good, the Bad and the Ugly", OriginalTitle: "Il buono, il brutto, il cattivo", Year: 1966},
}

func TestMatch(t *testing.T) {
	t.Parallel()
	idx := match.NewIndex(library)
	const (
		sure      = iota // first candidate is Confident
		unsure           // first candidate is not Confident, not ambiguous
		ambiguous        // every top candidate is ambiguous
	)
	tests := []struct {
		release string
		want    []int64 // candidate IDs, in order; nil = no match
		kind    int
	}{
		// Exact title and year.
		{"The.Dark.Knight.2008.1080p.BluRay.x264-GRP", []int64{1}, sure},
		{"Dark.Knight.2008.720p.BluRay.x264-GRP", []int64{1}, sure},
		{"Inception.2010.2160p.UHD.BluRay.x265-GRP", []int64{2}, sure},
		{"Mad.Max.Fury.Road.2015.1080p.BluRay.x264-GRP", []int64{43}, sure},
		{"Avengers.Endgame.2019.IMAX.2160p.DSNP.WEB-DL.DDP5.1.Atmos.HDR.H.265-GRP", []int64{56}, sure},
		{"Pirates.of.the.Caribbean.The.Curse.of.the.Black.Pearl.2003.1080p.BluRay.x264-GRP", []int64{57}, sure},
		{"The.Lord.of.the.Rings.The.Return.of.the.King.2003.EXTENDED.1080p.BluRay.x264-GRP", []int64{60}, sure},
		{"Blade.Runner.2049.2017.1080p.BluRay.x264-GRP", []int64{40}, sure},
		{"2001.A.Space.Odyssey.1968.1080p.BluRay.x264-GRP", []int64{41}, sure},
		{"The Matrix (1999) [1080p] [BluRay] [5.1] [YTS.MX]", nil, sure},
		// Year off by one; wrong year.
		{"Inception.2011.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{2}, sure},
		{"Inception.2013.1080p.WEB-DL.DDP5.1.H.264-GRP", nil, sure},
		{"Dune.2022.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{7}, sure},
		{"Dune.2023.1080p.WEB-DL.DDP5.1.H.264-GRP", nil, sure},
		{"Kill.Bill.Vol.2.2003.1080p.BluRay.x264-GRP", []int64{19}, sure},
		// & / and.
		{"Fast.and.Furious.2009.1080p.BluRay.x264-GRP", []int64{3}, sure},
		{"Fast & Furious (2009) [1080p] [BluRay] [YTS.MX]", []int64{3}, sure},
		{"Fast.and.Furious.4.2009.1080p.BluRay.x264-GRP", []int64{3}, sure},
		{"Fast.Five.2011.1080p.BluRay.x264-GRP", []int64{55}, sure},
		{"Fast.and.Furious.5.2011.1080p.BluRay.x264-GRP", []int64{55}, sure},
		// Roman numerals and sequel markers.
		{"Rocky.IV.1985.1080p.BluRay.x264-GRP", []int64{4}, sure},
		{"Rocky.4.1985.1080p.BluRay.x264-GRP", []int64{4}, sure},
		{"Rocky.II.1979.1080p.BluRay.x264-GRP", []int64{5}, sure},
		{"Shrek.2.2004.1080p.BluRay.x264-GRP", []int64{44}, sure},
		{"Shrek.II.2004.1080p.BluRay.x264-GRP", []int64{44}, sure},
		{"Toy.Story.3.2010.1080p.BluRay.x264-GRP", []int64{45}, sure},
		{"Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR.HEVC.TrueHD.Atmos.7.1-GRP", []int64{8}, sure},
		{"Dune.Part.2.2024.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{8}, sure},
		{"Dune.2.2024.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{8}, sure},
		{"Dune.Part.One.2021.1080p.BluRay.x264-GRP", []int64{7}, sure},
		{"Dune.1984.1080p.BluRay.x264-GRP", []int64{6}, sure},
		{"Dune.2021.2160p.UHD.BluRay.x265-GRP", []int64{7}, sure},
		{"Harry.Potter.and.the.Deathly.Hallows.Part.2.2011.1080p.BluRay.x264-GRP", []int64{17}, sure},
		{"Harry.Potter.And.The.Deathly.Hallows.Pt.2.2011.1080p.BluRay.x264-GRP", []int64{17}, sure},
		{"Kill.Bill.Vol.1.2003.1080p.BluRay.x264-GRP", []int64{18}, sure},
		{"Kill.Bill.Volume.2.2004.1080p.BluRay.x264-GRP", []int64{19}, sure},
		{"Mission.Impossible.Dead.Reckoning.Part.One.2023.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{16}, sure},
		{"Mission.Impossible.II.2000.1080p.BluRay.x264-GRP", []int64{51}, sure},
		{"Mission.Impossible.2.2000.1080p.BluRay.x264-GRP", []int64{51}, sure},
		{"The.Godfather.Part.II.1974.1080p.BluRay.x264-GRP", []int64{52}, sure},
		{"The.Godfather.Part.2.1974.1080p.BluRay.x264-GRP", []int64{52}, sure},
		{"The.Godfather.II.1974.1080p.BluRay.x264-GRP", []int64{52}, sure},
		{"The.Godfather.Part.III.1990.1080p.BluRay.x264-GRP", []int64{53}, sure},
		{"Star.Wars.Episode.I.The.Phantom.Menace.1999.1080p.BluRay.x264-GRP", []int64{54}, sure},
		{"Star.Wars.Episode.IV.A.New.Hope.1977.1080p.BluRay.x264-GRP", []int64{20}, sure},
		{"Star.Wars.1977.1080p.BluRay.x264-GRP", []int64{20}, sure},
		{"Die.Hard.1988.1080p.BluRay.x264-GRP", []int64{32}, sure},
		{"Die.Hard.2.1990.1080p.BluRay.x264-GRP", []int64{33}, sure},
		{"Die.Hard.2.Die.Harder.1990.1080p.BluRay.x264-GRP", []int64{33}, sure},
		// Punctuation, possessives, word splits.
		{"Spider-Man.No.Way.Home.2021.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{15}, sure},
		{"Spiderman.No.Way.Home.2021.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{15}, sure},
		{"Oceans.Eleven.2001.1080p.BluRay.x264-GRP", []int64{13}, sure},
		{"Ocean.s.Eleven.2001.1080p.BluRay.x264-GRP", []int64{13}, sure},
		{"Oceans.11.1960.1080p.BluRay.x264-GRP", []int64{14}, sure},
		{"Oceans.Eleven.1960.1080p.BluRay.x264-GRP", nil, sure}, // spelled numbers are not digits
		{"Schindlers.List.1993.1080p.BluRay.x264-GRP", []int64{36}, sure},
		{"WALL-E.2008.1080p.BluRay.x264-GRP", []int64{35}, sure},
		{"Wall.E.2008.1080p.BluRay.x264-GRP", []int64{35}, sure},
		{"Batman.v.Superman.Dawn.of.Justice.2016.1080p.BluRay.x264-GRP", []int64{30}, sure},
		{"Batman.vs.Superman.Dawn.of.Justice.2016.1080p.BluRay.x264-GRP", []int64{30}, sure},
		{"Alien.vs.Predator.2004.1080p.BluRay.x264-GRP", []int64{31}, sure},
		{"Alien.Versus.Predator.2004.1080p.BluRay.x264-GRP", []int64{31}, sure},
		{"Se7en.1995.1080p.BluRay.x264-GRP", []int64{42}, sure},
		{"Seven.1995.1080p.BluRay.x264-GRP", []int64{42}, sure},
		{"La.La.Land.2016.1080p.BluRay.x264-GRP", []int64{34}, sure},
		{"The.Good.the.Bad.and.the.Ugly.1966.1080p.BluRay.x264-GRP", []int64{63}, sure},
		{"Godzilla.Minus.One.2023.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{62}, sure},
		// Foreign titles, accents, articles, AKA.
		{"Amelie.2001.1080p.BluRay.x264-GRP", []int64{9}, sure},
		{"Le.Fabuleux.Destin.d.Amelie.Poulain.2001.FRENCH.1080p.BluRay.x264-GRP", []int64{9}, sure},
		{"Le.Fabuleux.Destin.dAmelie.Poulain.2001.FRENCH.1080p.BluRay.x264-GRP", []int64{9}, sure},
		{"Fabuleux.Destin.D.Amelie.Poulain.2001.FRENCH.1080p.BluRay.x264-GRP", []int64{9}, sure},
		{"Amelie.AKA.Le.Fabuleux.Destin.D.Amelie.Poulain.2001.1080p.BluRay.x264-GRP", []int64{9}, sure},
		{"Le.Fabuleux.Destin.AKA.Amelie.2001.1080p.BluRay.x264-GRP", []int64{9}, sure}, // only the AKA part matches
		{"Leon.1994.Extended.1080p.BluRay.x264-GRP", []int64{10}, sure},
		{"Leon.The.Professional.1994.1080p.BluRay.x264-GRP", []int64{10}, sure},
		{"The.Professional.1994.1080p.BluRay.x264-GRP", []int64{10}, sure},
		{"Das.Leben.der.Anderen.2006.GERMAN.1080p.BluRay.x264-GRP", []int64{11}, sure},
		{"The.Lives.of.Others.2006.1080p.BluRay.x264-GRP", []int64{11}, sure},
		{"El.Laberinto.Del.Fauno.2006.SPANISH.1080p.BluRay.x264-GRP", []int64{12}, sure},
		{"Pans.Labyrinth.2006.1080p.BluRay.x264-GRP", []int64{12}, sure},
		{"Wo.Hu.Cang.Long.2000.CHINESE.1080p.BluRay.x264-GRP", []int64{37}, sure},
		{"Crouching.Tiger.Hidden.Dragon.2000.1080p.BluRay.x264-GRP", []int64{37}, sure},
		{"Sen.to.Chihiro.no.Kamikakushi.2001.JAPANESE.1080p.BluRay.x264-GRP", []int64{38}, sure},
		{"Intouchables.2011.FRENCH.1080p.BluRay.x264-GRP", []int64{39}, sure},
		{"Amour.2012.FRENCH.1080p.BluRay.x264-GRP", []int64{48}, sure},
		{"Der.Untergang.2004.GERMAN.1080p.BluRay.x264-GRP", []int64{49}, sure},
		{"Untergang.2004.GERMAN.1080p.BluRay.x264-GRP", []int64{49}, sure},
		{"L.Auberge.Espagnole.2002.FRENCH.1080p.BluRay.x264-GRP", []int64{50}, sure},
		{"LAuberge.Espagnole.2002.FRENCH.1080p.BluRay.x264-GRP", []int64{50}, sure},
		{"Auberge.Espagnole.2002.FRENCH.1080p.BluRay.x264-GRP", []int64{50}, sure},
		{"Les.Miserables.2012.1080p.BluRay.x264-GRP", []int64{28}, sure},
		{"Les.Miserables.2019.FRENCH.1080p.BluRay.x264-GRP", []int64{29}, sure},
		{"Miserables.2012.1080p.BluRay.x264-GRP", []int64{28}, sure},
		{"Rock.n.Roll.2017.FRENCH.1080p.BluRay.x264-GRP", []int64{58}, sure},
		{"Bienvenue.Chez.Les.Chtis.2008.FRENCH.1080p.BluRay.x264-GRP", []int64{59}, sure},
		{"Welcome.to.the.Sticks.2008.1080p.BluRay.x264-GRP", []int64{59}, sure},
		// Remakes: the year decides; without a year nobody can.
		{"The.Thing.1982.1080p.BluRay.x264-GRP", []int64{21}, sure},
		{"The.Thing.2011.1080p.BluRay.x264-GRP", []int64{22}, sure},
		{"Halloween.1978.1080p.BluRay.x264-GRP", []int64{23}, sure},
		{"Halloween.2007.Unrated.1080p.BluRay.x264-GRP", []int64{25}, sure},
		{"Halloween.2018.1080p.BluRay.x264-GRP", []int64{24}, sure},
		{"The.Thing.1080p.BluRay.x264-GRP", []int64{21, 22}, ambiguous},
		{"Dune.1080p.BluRay.x264-GRP", []int64{6, 7}, ambiguous},
		{"Halloween.1080p.BluRay.x264-GRP", []int64{23, 24, 25}, ambiguous},
		// Same title, same year: both, low confidence.
		{"Twins.2023.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{46, 47}, ambiguous},
		// Title only when unique.
		{"Inception.1080p.BluRay.x264-GRP", []int64{2}, unsure},
		{"Project.Hail.Mary.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{61}, unsure},
		{"Project.Hail.Mary.2026.1080p.WEB-DL.DDP5.1.H.264-GRP", []int64{61}, unsure}, // movie has no year yet
		// Not in the library.
		{"Some.Unknown.Movie.2020.1080p.WEB-DL.DDP5.1.H.264-GRP", nil, sure},
		{"a8f7c6d5e4b3a29182736455463728ab.mkv", nil, sure},
	}
	for _, tt := range tests {
		t.Run(tt.release, func(t *testing.T) {
			t.Parallel()
			p := parser.Parse(tt.release)
			got := idx.Match(p)
			var ids []int64
			for _, c := range got {
				ids = append(ids, c.MovieID)
			}
			if diff := cmp.Diff(tt.want, ids); diff != "" {
				t.Fatalf("Match(%q) parsed as %q (%d), AKA %q: candidates (-want +got):\n%s\n%s",
					tt.release, p.Title, p.Year, p.AlternateTitles, diff, describe(got))
			}
			if len(got) == 0 {
				return
			}
			c := got[0]
			if c.Reason == "" || c.Confidence <= 0 || c.Confidence > 1 {
				t.Errorf("candidate %+v: want a reason and a confidence in (0, 1]", c)
			}
			switch tt.kind {
			case sure:
				if !c.Confident() {
					t.Errorf("first candidate not confident: %s", describe(got))
				}
			case unsure:
				if c.Confident() || c.Ambiguous {
					t.Errorf("first candidate should be neither confident nor ambiguous: %s", describe(got))
				}
			case ambiguous:
				for _, c := range got {
					if !c.Ambiguous || c.Confident() || c.Confidence > 0.4 {
						t.Errorf("candidate should be ambiguous with low confidence: %s", describe(got))
					}
				}
			}
		})
	}
}

func describe(cs []match.Candidate) string {
	var b strings.Builder
	for _, c := range cs {
		fmt.Fprintf(&b, "  %d %q (%d) %.2f amb=%v via %s %q: %s\n", c.MovieID, c.Title, c.Year, c.Confidence, c.Ambiguous, c.Via, c.MatchedTitle, c.Reason)
	}
	return b.String()
}

func TestMatchDetails(t *testing.T) {
	t.Parallel()
	idx := match.NewIndex(library)
	tests := []struct {
		release    string
		confidence float64
		via        match.TitleKind
		reason     []string
	}{
		{"The.Dark.Knight.2008.1080p.BluRay.x264-GRP", 1, match.TitlePrimary, []string{"title and year 2008 match"}},
		{"Inception.2011.1080p.WEB-DL.DDP5.1.H.264-GRP", 0.85, match.TitlePrimary, []string{"year off by one (release 2011, movie 2010)"}},
		{"Das.Leben.der.Anderen.2006.GERMAN.1080p.BluRay.x264-GRP", 0.97, match.TitleOriginal, []string{"original title and year", `matched "Das Leben der Anderen"`}},
		{"The.Professional.1994.1080p.BluRay.x264-GRP", 0.9, match.TitleAlternate, []string{"alternate title", `matched "The Professional"`}},
		{"Le.Fabuleux.Destin.AKA.Amelie.2001.1080p.BluRay.x264-GRP", 0.95, match.TitlePrimary, []string{"via the AKA part"}},
		{"Miserables.2012.1080p.BluRay.x264-GRP", 0.95, match.TitlePrimary, []string{"ignoring a leading article"}},
		{"Inception.1080p.BluRay.x264-GRP", 0.7, match.TitlePrimary, []string{"no year to compare; the only movie with this title"}},
		{"Twins.2023.1080p.WEB-DL.DDP5.1.H.264-GRP", 0.4, match.TitlePrimary, []string{"2 movies match equally well, choose manually"}},
		{"The.Thing.1080p.BluRay.x264-GRP", 0.4, match.TitlePrimary, []string{"2 movies share this title"}},
	}
	for _, tt := range tests {
		t.Run(tt.release, func(t *testing.T) {
			t.Parallel()
			got := idx.Match(parser.Parse(tt.release))
			if len(got) == 0 {
				t.Fatal("no candidate")
			}
			c := got[0]
			if c.Confidence != tt.confidence || c.Via != tt.via {
				t.Errorf("confidence %.2f via %s, want %.2f via %s", c.Confidence, c.Via, tt.confidence, tt.via)
			}
			for _, r := range tt.reason {
				if !strings.Contains(c.Reason, r) {
					t.Errorf("reason %q lacks %q", c.Reason, r)
				}
			}
		})
	}
}

func TestMatchIMDb(t *testing.T) {
	t.Parallel()
	idx := match.NewIndex(library)
	got := idx.MatchIMDb("TT0468569")
	if len(got) != 1 || got[0].MovieID != 1 || !got[0].Confident() {
		t.Errorf("MatchIMDb = %+v", got)
	}
	if got := idx.MatchIMDb("tt0000001"); got != nil {
		t.Errorf("unknown id = %+v", got)
	}
}

func TestIndexSkipsEmpty(t *testing.T) {
	t.Parallel()
	idx := match.NewIndex([]match.Entry{{MovieID: 1, Title: "  "}, {MovieID: 2, Title: "Heat", Year: 1995}, {MovieID: 3, Title: "?!"}})
	if idx.Len() != 2 {
		t.Errorf("Len = %d, want 2", idx.Len())
	}
	if got := idx.Match(parser.ParsedRelease{}); got != nil {
		t.Errorf("empty release matched %+v", got)
	}
	if got := idx.Match(parser.ParsedRelease{Title: "?!", Year: 2000}); got != nil {
		t.Errorf("punctuation-only title matched %+v", got)
	}
	// CleanTitle is used when Title is empty.
	if got := idx.Match(parser.ParsedRelease{CleanTitle: "heat", Year: 1995}); len(got) != 1 || got[0].MovieID != 2 {
		t.Errorf("CleanTitle fallback = %+v", got)
	}
}

func TestKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		same bool
	}{
		{"Fast & Furious", "Fast and Furious", true},
		{"Rocky IV", "Rocky 4", true},
		{"Star Wars: Episode IV - A New Hope", "Star Wars Episode 4 A New Hope", true},
		{"Dune: Part Two", "Dune 2", true},
		{"Dune: Part Two", "Dune Pt. 2", true},
		{"Kill Bill: Vol. 1", "Kill Bill Volume One", true},
		{"Star Wars: Episode I", "Star Wars Episode 1", true},
		{"The Godfather Part II", "Godfather 2", true},
		{"Les Misérables", "Miserables", true},
		{"L'Odyssée", "L.Odyssee", true},
		{"L'Odyssée", "Odyssee", true},
		{"Le Fabuleux Destin d'Amélie Poulain", "Le Fabuleux Destin d Amelie Poulain", true},
		{"Ocean's Eleven", "Oceans Eleven", true},
		{"Ocean's Eleven", "Ocean s Eleven", true},
		{"Spider-Man", "Spiderman", true},
		{"Batman v Superman", "Batman vs. Superman", true},
		{"Das Boot", "Boot", true},
		{"El Topo", "Topo", true},
		{"Æon Flux", "Aeon Flux", true},
		{"C'era una volta il West", "C era una volta il West", true},
		{"Ocean's Eleven", "Ocean's 11", false},
		{"I, Robot", "1 Robot", false},
		{"Rocky", "Rocky II", false},
		{"Dune", "Dune: Part Two", false},
		{"The Thing", "Thing 2", false},
		{"Part", "2", false},
	}
	for _, tt := range tests {
		ka, kb := match.Key(tt.a), match.Key(tt.b)
		if (ka == kb) != tt.same {
			t.Errorf("Key(%q) = %q, Key(%q) = %q; same = %v, want %v", tt.a, ka, tt.b, kb, ka == kb, tt.same)
		}
	}
	for _, s := range []string{"", " ", "!!!", "&"} {
		if k := match.Key(s); k != "" {
			t.Errorf("Key(%q) = %q, want empty", s, k)
		}
	}
	if k := match.Key("Les"); k != "les" {
		t.Errorf("Key(Les) = %q: a lone article must stay", k)
	}
}
