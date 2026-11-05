package parser_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// fieldCase is one input name and the expected value of a single field family.
type fieldCase[T any] struct {
	in   string
	want T
}

// runField parses every case and compares the projected field with want.
func runField[T any](t *testing.T, cases []fieldCase[T], get func(parser.ParsedRelease) T) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := get(parser.Parse(tc.in))
			if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Parse(%q) mismatch (-want +got):\n%s", tc.in, diff)
			}
		})
	}
}

type titleYear struct {
	Title     string
	Year      int
	Alternate []string
}

func TestParse_TitleYear(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[titleYear]{
		{"The.Dark.Knight.2008.1080p.BluRay.H264-PRiSTiNE", titleYear{Title: "The Dark Knight", Year: 2008}},
		{"2001.A.Space.Odyssey.1968.2160p.UHD.BluRay.H265-WOU", titleYear{Title: "2001 A Space Odyssey", Year: 1968}},
		{"Blade.Runner.2049.2017.2160p.UHD.BluRay.H265-WOU", titleYear{Title: "Blade Runner 2049", Year: 2017}},
		{"1917.2019.1080p.BluRay.x264-SPARKS", titleYear{Title: "1917", Year: 2019}},
		{"1917.1080p.BluRay.x264-GROUP", titleYear{Title: "1917"}},
		{"Toy.Story.5.2026.MULTi.COMPLETE.BLURAY-MONUMENT", titleYear{Title: "Toy Story 5", Year: 2026}},
		{"Inception.German.2010.AC3.BDRip.x264.iNTERNAL-VideoStar", titleYear{Title: "Inception", Year: 2010}},
		{"Blade.Runner.2049.3D.2017.German.DL.1080p.BluRay.x264.RERIP-BluRay3D", titleYear{Title: "Blade Runner 2049", Year: 2017}},
		{"Le.fabuleux.destin.dAmelie.Poulain.BDRip.x264.2001.SP.SUB-USL", titleYear{Title: "Le fabuleux destin dAmelie Poulain", Year: 2001}},
		{"Brotherhood.of.the.Wolf.Proper.DVDRiP.DivX-COLLiSiON", titleYear{Title: "Brotherhood of the Wolf"}},
		{"The Matrix (1999) [1080p] [BluRay] [5.1] [YTS.MX]", titleYear{Title: "The Matrix", Year: 1999}},
		{"Mission: Impossible - Fallout (2018) 2160p UHD BluRay x265-GROUP", titleYear{Title: "Mission: Impossible - Fallout", Year: 2018}},
		{"Spider-Man.No.Way.Home.2021.1080p.WEB-DL.DDP5.1.H.264-GROUP", titleYear{Title: "Spider-Man No Way Home", Year: 2021}},
		{"Terrell.and.T.O.2026.2160p.WEB.h265-GRACE", titleYear{Title: "Terrell and T.O", Year: 2026}},
		{"The.Man.from.U.N.C.L.E.2015.1080p.BluRay.x264-SPARKS", titleYear{Title: "The Man from U.N.C.L.E", Year: 2015}},
		{"Le.Fabuleux.Destin.d.Amelie.Poulain.AKA.Amelie.2001.1080p.BluRay.x264-GROUP",
			titleYear{Title: "Le Fabuleux Destin d Amelie Poulain", Year: 2001, Alternate: []string{"Amelie"}}},
		{"/mnt/media/incoming/Dune.2021.2160p.WEB-DL.DDP5.1.Atmos.DV.H.265-GROUP.mkv", titleYear{Title: "Dune", Year: 2021}},
		{`C:\Downloads\Dune.2021.1080p.BluRay.x264-GROUP.mkv`, titleYear{Title: "Dune", Year: 2021}},
		{"the_dark_knight_2008_720p_bluray_x264-group", titleYear{Title: "the dark knight", Year: 2008}},
		{"IMAX.Space.Station.2002.2160p.UHD.BluRay.H265-GAZPROM", titleYear{Title: "IMAX Space Station", Year: 2002}},
		{"Charlottes.Web.2006.1080p.BluRay.x264-GROUP", titleYear{Title: "Charlottes Web", Year: 2006}},
		{"Mad.Max.Fury.Road.2015.MULTi.COMPLETE.BLURAY-FLAME", titleYear{Title: "Mad Max Fury Road", Year: 2015}},
		{"[Judas] Your Name (2016) [BD 1080p HEVC x265 10bit][Dual-Audio][Multi-Subs]", titleYear{Title: "Your Name", Year: 2016}},
		{"Movie.2019", titleYear{Title: "Movie", Year: 2019}},
		{"Blade.Runner.Final.Cut.1982.GERMAN.DL.DV.HDR.2160p.WEB.H265-SunDry", titleYear{Title: "Blade Runner", Year: 1982}},
		{"Terminator.2.Judgment.Day.The.Ultimate.Edition.1991.NTSC.2DISC.COMPLETE.DVDR-25LiGHTERS", titleYear{Title: "Terminator 2 Judgment Day", Year: 1991}},
		{"The.Final.Cut.2004.1080p.BluRay.x264-GROUP", titleYear{Title: "The Final Cut", Year: 2004}},
		{"Les.Miserables.in.Concert.The.25th.Anniversary.2010.1080p.BluRay.H264-MiSERABLE", titleYear{Title: "Les Miserables in Concert The 25th Anniversary", Year: 2010}},
		{"Blade.Runner.2049.GERMAN.DL.2160p.UHD.BluRay.x265-ENDSTATiON", titleYear{Title: "Blade Runner", Year: 2049}}, // documented ambiguity
		{"The.Good.German.2006.1080p.BluRay.x264-GROUP", titleYear{Title: "The Good", Year: 2006}},                     // documented limitation
		{"", titleYear{}},
	}, func(p parser.ParsedRelease) titleYear {
		return titleYear{Title: p.Title, Year: p.Year, Alternate: p.AlternateTitles}
	})
}

func TestParse_CleanTitle(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[string]{
		{"The.Dark.Knight.2008.1080p.BluRay.H264-PRiSTiNE", "dark knight"},
		{"Amélie.2001.FRENCH.1080p.BluRay.x264-GROUP", "amelie"},
		{"Mission: Impossible - Fallout (2018) 2160p UHD BluRay x265-GROUP", "mission impossible fallout"},
		{"Fast & Furious 6 (2013) 1080p BluRay x264-GROUP", "fast furious 6"},
		{"Fast.and.Furious.6.2013.1080p.BluRay.x264-GROUP", "fast furious 6"},
		{"Schindler's.List.1993.1080p.BluRay.x264-GROUP", "schindlers list"},
		{"A.Quiet.Place.2018.1080p.BluRay.x264-GROUP", "quiet place"},
	}, func(p parser.ParsedRelease) string { return p.CleanTitle })
}

func TestCleanTitle(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"The Dark Knight", "dark knight"},
		{"An American Werewolf in London", "american werewolf in london"},
		{"A", "a"},
		{"The", "the"},
		{"Léon: The Professional", "leon the professional"},
		{"Æon Flux", "aeon flux"},
		{"Die Straße", "die strasse"},
		{"WALL·E", "wall e"},
		{"Crouching Tiger, Hidden Dragon", "crouching tiger hidden dragon"},
		{"Kill Bill: Vol. 1", "kill bill vol 1"},
		{"Ocean's Eleven", "oceans eleven"},
		{"Tom & Jerry", "tom jerry"},
		{"  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := parser.CleanTitle(tt.in); got != tt.want {
				t.Errorf("CleanTitle(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParse_Resolution(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[parser.Resolution]{
		{"Movie.2019.480p.WEB.x264-GROUP", parser.Resolution480p},
		{"Movie.2004.576p.BDRip.x264-HANDJOB", parser.Resolution576p},
		{"Movie.2019.720p.BluRay.x264-GROUP", parser.Resolution720p},
		{"Movie.2019.1080p.BluRay.x264-GROUP", parser.Resolution1080p},
		{"Movie.2020.1080i.HDTV.DD5.1.H.264-NOGRP", parser.Resolution1080p},
		{"Movie.2019.2160p.WEB.H265-GROUP", parser.Resolution2160p},
		{"Movie.2019.2160P.UHD.BLURAY.H265-GROUP", parser.Resolution2160p},
		{"Movie.2019.UHD.BluRay.x265-GROUP", parser.Resolution2160p},
		{"Movie.2019.COMPLETE.UHD.BLURAY-GROUP", parser.Resolution2160p},
		{"Movie 2020 WEB-DL 4K H265 10bit HDR DDP5.1 Atmos-PTerWEB", parser.Resolution2160p},
		{"Movie.2011.1080p.UHD.BluRay.DD5.1.HDR.x265-CtrlHD", parser.Resolution1080p},
		{"Movie.2018.1920x1080.Bluray", parser.Resolution1080p},
		{"4K.Movie.Title.2019.WEB.x264-GROUP", parser.ResolutionUnknown},
		{"Movie.2019.DVDRip.XviD-GROUP", parser.ResolutionUnknown},
	}, func(p parser.ParsedRelease) parser.Resolution { return p.Resolution })
}

type sourceMod struct {
	Source   parser.Source
	Modifier parser.Modifier
}

func TestParse_SourceModifier(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[sourceMod]{
		{"Movie.2019.HDCAM.x264-GROUP", sourceMod{parser.SourceCam, parser.ModifierNone}},
		{"Movie.2019.CAMRip.x264-GROUP", sourceMod{parser.SourceCam, parser.ModifierNone}},
		{"Movie.2019.HDTS.x264-GROUP", sourceMod{parser.SourceTelesync, parser.ModifierNone}},
		{"Movie.2019.TELESYNC.XviD-GROUP", sourceMod{parser.SourceTelesync, parser.ModifierNone}},
		{"Movie.2019.TS.XviD-GROUP", sourceMod{parser.SourceTelesync, parser.ModifierNone}},
		{"Movie.2019.TC.XviD-GROUP", sourceMod{parser.SourceTelecine, parser.ModifierNone}},
		{"Intouchables.2011.FRENCH.DVDSCR.XviD-INTOUCHABLES", sourceMod{parser.SourceScreener, parser.ModifierNone}},
		{"Movie.2019.BDSCR.x264-GROUP", sourceMod{parser.SourceScreener, parser.ModifierNone}},
		{"Movie.2019.WEB.SCREENER.x264-GROUP", sourceMod{parser.SourceScreener, parser.ModifierNone}},
		{"Movie.2019.DVDRip.XviD-GROUP", sourceMod{parser.SourceDVD, parser.ModifierNone}},
		{"Shoplifters.2018.NTSC.DVD9-WaLMaRT", sourceMod{parser.SourceDVD, parser.ModifierDVDR}},
		{"Blade.Runner.2049.2017.PAL.DVDR-iGNiTiON", sourceMod{parser.SourceDVD, parser.ModifierDVDR}},
		{"Movie.2019.R5.XviD-GROUP", sourceMod{parser.SourceDVD, parser.ModifierRegional}},
		{"Memory.2025.CZECH.1080p.HDTV.x265-iCZi", sourceMod{parser.SourceHDTV, parser.ModifierNone}},
		{"Movie.2019.1080i.RawHD.MPEG2-GROUP", sourceMod{parser.SourceHDTV, parser.ModifierRawHD}},
		{"Movie.2019.1080p.WEBRip.x264-GROUP", sourceMod{parser.SourceWebRip, parser.ModifierNone}},
		{"Movie.2019.1080p.WEB-DL.DDP5.1.H.264-GROUP", sourceMod{parser.SourceWebDL, parser.ModifierNone}},
		{"Movie.2019.1080p.WEB.H264-GROUP", sourceMod{parser.SourceWebDL, parser.ModifierNone}},
		{"Movie.2019.1080p.AMZN.DDP5.1.H.264-GROUP", sourceMod{parser.SourceWebDL, parser.ModifierNone}},
		{"Movie.2019.1080p.BluRay.x264-GROUP", sourceMod{parser.SourceBluRay, parser.ModifierNone}},
		{"Movie.2019.BDRip.x264-GROUP", sourceMod{parser.SourceBluRay, parser.ModifierNone}},
		{"Movie.2019.720p.BRRip.x264-GROUP", sourceMod{parser.SourceBluRay, parser.ModifierNone}},
		{"Movie.2019.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GROUP", sourceMod{parser.SourceBluRay, parser.ModifierRemux}},
		{"Movie.2019.1080p.BDRemux.AVC-GROUP", sourceMod{parser.SourceBluRay, parser.ModifierRemux}},
		{"Movie.2019.COMPLETE.BLURAY-GROUP", sourceMod{parser.SourceBluRay, parser.ModifierBRDisk}},
		{"Movie.2019.1080p.Blu-ray.AVC.DTS-HD.MA.5.1-GROUP", sourceMod{parser.SourceBluRay, parser.ModifierBRDisk}},
		{"Movie.2019.BDMV.1080p-GROUP", sourceMod{parser.SourceBluRay, parser.ModifierBRDisk}},
		{"Movie.2019.German.DL.1080p.BluRay.AVC-UNTAVC", sourceMod{parser.SourceBluRay, parser.ModifierRemux}},
		{"Movie.2019.German.DL.2160p.UHD.BluRay.HEVC-GROUP", sourceMod{parser.SourceBluRay, parser.ModifierRemux}},
		{"Movie.Title.2012.Bluray.1080p.3D.AVC.DTS-HD.MA.5.1.iso", sourceMod{parser.SourceBluRay, parser.ModifierBRDisk}},
		{"Movie.2019.1080p.x264-GROUP", sourceMod{parser.SourceUnknown, parser.ModifierNone}},
	}, func(p parser.ParsedRelease) sourceMod { return sourceMod{p.Source, p.Modifier} })
}

type codecDepth struct {
	Codec    parser.Codec
	BitDepth int
}

func TestParse_VideoCodec(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[codecDepth]{
		{"Movie.2019.1080p.BluRay.x264-GROUP", codecDepth{parser.CodecH264, 0}},
		{"Movie.2019.1080p.WEB.H.264-GROUP", codecDepth{parser.CodecH264, 0}},
		{"Movie.2019.1080p.WEB.h264-GROUP", codecDepth{parser.CodecH264, 0}},
		{"Movie.2019.1080p.BluRay.AVC-GROUP", codecDepth{parser.CodecH264, 0}},
		{"Movie.2019.2160p.BluRay.x265.10bit-GROUP", codecDepth{parser.CodecH265, 10}},
		{"Movie.2019.2160p.WEB.H.265-GROUP", codecDepth{parser.CodecH265, 0}},
		{"Movie.2019.2160p.UHD.BluRay.HEVC-GROUP", codecDepth{parser.CodecH265, 0}},
		{"Movie.2019.2160p.WEB-DL.AV1.10-bit-GROUP", codecDepth{parser.CodecAV1, 10}},
		{"Movie.2004.1080p.Blu-ray.VC-1.TrueHD.5.1-GROUP", codecDepth{parser.CodecVC1, 0}},
		{"Movie.2019.DVDRip.XviD-GROUP", codecDepth{parser.CodecXviD, 0}},
		{"Movie.FRENCH.DVDRiP.DiVX-GROUP", codecDepth{parser.CodecDivX, 0}},
		{"Movie.2009.1080i.HDTV.AAC2.0.MPEG2-GROUP", codecDepth{parser.CodecMPEG2, 0}},
		{"Movie.2018.1080p.BluRay.REMUX.MPEG-2.DTS-HD.MA.5.1-EPSiLON", codecDepth{parser.CodecMPEG2, 0}},
		{"Movie.2019.2160p.WEB-DL.VP9-GROUP", codecDepth{parser.CodecVP9, 0}},
		{"[Group] Movie (2010) [BD 1080p Hi10P FLAC]", codecDepth{parser.CodecH264, 10}},
		{"Movie.2019.1080p.WEB.x264.8bit-GROUP", codecDepth{parser.CodecH264, 8}},
		{"Movie.2019.2160p.WEB.x265.12bit-GROUP", codecDepth{parser.CodecH265, 12}},
	}, func(p parser.ParsedRelease) codecDepth { return codecDepth{p.VideoCodec, p.BitDepth} })
}

type hdr struct {
	HDR       []parser.HDRFormat
	DVProfile int
}

func TestParse_HDR(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[hdr]{
		{"Movie.2019.2160p.WEB.H265-GROUP", hdr{}},
		{"Movie.2019.HDR.2160p.WEB.H265-GROUP", hdr{HDR: []parser.HDRFormat{parser.HDR10}}},
		{"Movie.2019.2160p.BluRay.HDR10.x265-GROUP", hdr{HDR: []parser.HDRFormat{parser.HDR10}}},
		{"Movie.2019.2160p.WEB-DL.HDR10+.H.265-GROUP", hdr{HDR: []parser.HDRFormat{parser.HDR10Plus}}},
		{"Movie.2024.German.AC3D.DL.2160p.Hybrid.WEB.DV.HDR10Plus.HEVC-GROUP", hdr{HDR: []parser.HDRFormat{parser.DolbyVision, parser.HDR10Plus}}},
		{"Movie.2019.DV.2160p.WEB.H265-GROUP", hdr{HDR: []parser.HDRFormat{parser.DolbyVision}}},
		{"Movie.2019.GERMAN.DL.DV.HDR.2160p.WEB.H265-GROUP", hdr{HDR: []parser.HDRFormat{parser.DolbyVision, parser.HDR10}}},
		{"Movie.2019.2160p.WEB-DL.DoVi.HEVC-GROUP", hdr{HDR: []parser.HDRFormat{parser.DolbyVision}}},
		{"Movie.2019.2160p.WEB-DL.DoVi.P5.HEVC-GROUP", hdr{HDR: []parser.HDRFormat{parser.DolbyVision}, DVProfile: 5}},
		{"Movie.2019.2160p.BluRay.REMUX.DV.P7.HDR.HEVC-GROUP", hdr{HDR: []parser.HDRFormat{parser.DolbyVision, parser.HDR10}, DVProfile: 7}},
		{"Movie.2019.2160p.WEB-DL.Dolby.Vision.HEVC-GROUP", hdr{HDR: []parser.HDRFormat{parser.DolbyVision}}},
		{"Movie.2019.2160p.HDTV.HLG.HEVC-GROUP", hdr{HDR: []parser.HDRFormat{parser.HLG}}},
		{"Movie (2016) [UHDRemux 2160p SDR]", hdr{HDR: []parser.HDRFormat{parser.SDR}}},
	}, func(p parser.ParsedRelease) hdr { return hdr{p.HDR, p.DolbyVisionProfile} })
}

type audio struct {
	Audio    []parser.AudioFormat
	Atmos    bool
	Channels string
}

func TestParse_Audio(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[audio]{
		{"Movie.2019.1080p.WEB-DL.AAC2.0.H.264-GROUP", audio{[]parser.AudioFormat{parser.AudioAAC}, false, "2.0"}},
		{"Movie.2019.DVDRip.XviD.AC3-GROUP", audio{[]parser.AudioFormat{parser.AudioAC3}, false, ""}},
		{"Movie.2019.1080p.AMZN.WEB-DL.DD5.1.H.264-GROUP", audio{[]parser.AudioFormat{parser.AudioAC3}, false, "5.1"}},
		{"Movie.2019.1080p.WEB-DL.DDP5.1.H.264-GROUP", audio{[]parser.AudioFormat{parser.AudioEAC3}, false, "5.1"}},
		{"Movie 2005 1080p UHD BluRay DD+7.1 x264-LoRD", audio{[]parser.AudioFormat{parser.AudioEAC3}, false, "7.1"}},
		{"Movie.2019.2160p.WEB-DL.DDP5.1.Atmos.DV.H.265-GROUP", audio{[]parser.AudioFormat{parser.AudioEAC3}, true, "5.1"}},
		{"Movie.2019.2160p.WEB-DL.DDPA5.1.H.265-GROUP", audio{[]parser.AudioFormat{parser.AudioEAC3}, true, "5.1"}},
		{"Movie.2019.1080p.WEB-DL.EAC3.5.1.H.264-GROUP", audio{[]parser.AudioFormat{parser.AudioEAC3}, false, "5.1"}},
		{"Movie.2019.2160p.BluRay.REMUX.HEVC.TrueHD.7.1.Atmos-GROUP", audio{[]parser.AudioFormat{parser.AudioTrueHD}, true, "7.1"}},
		{"Movie.2019.1080p.BluRay.x264.DTS-GROUP", audio{[]parser.AudioFormat{parser.AudioDTS}, false, ""}},
		{"Movie.2019.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GROUP", audio{[]parser.AudioFormat{parser.AudioDTSHDMA}, false, "5.1"}},
		{"Movie.2019.1080p.BluRay.x264.DTS-HD.HRA.7.1-GROUP", audio{[]parser.AudioFormat{parser.AudioDTSHDHRA}, false, "7.1"}},
		{"2001.A.Space.Odyssey.1968.SE.DTS-HRA.DTS.MULTISUBS.1080p.BluRay.x264.HQ-TUSAHD", audio{[]parser.AudioFormat{parser.AudioDTSHDHRA, parser.AudioDTS}, false, ""}},
		{"Movie.2019.2160p.BluRay.REMUX.HEVC.DTS-X.7.1-GROUP", audio{[]parser.AudioFormat{parser.AudioDTSX}, false, "7.1"}},
		{"Movie (2019) 2160p BluRay DTS:X 7.1 HEVC-GROUP", audio{[]parser.AudioFormat{parser.AudioDTSX}, false, "7.1"}},
		{"Movie.1993.720p.BluRay.REMUX.AVC.FLAC.2.0-BLURANiUM", audio{[]parser.AudioFormat{parser.AudioFLAC}, false, "2.0"}},
		{"Movie.2008.1080i.Blu-ray.MPEG-2.LPCM2.0.ISO", audio{[]parser.AudioFormat{parser.AudioPCM}, false, "2.0"}},
		{"Movie.2018.WEB.1080p.HEVC.Opus-Netaro", audio{[]parser.AudioFormat{parser.AudioOpus}, false, ""}},
		{"Movie.2001.DVDRip.XviD.MP3-GROUP", audio{[]parser.AudioFormat{parser.AudioMP3}, false, ""}},
		{"Crouching.Tiger.Hidden.Dragon.2000.REMASTERED.CHINESE.TrueHD.Atmos.AC3.MULTISUBS.1080p.BluRay.x264.HQ-TUSAHD",
			audio{[]parser.AudioFormat{parser.AudioTrueHD, parser.AudioAC3}, true, ""}},
		{"Movie.2019.German.AC3D.DL.1080p.BluRay.x265-GROUP", audio{[]parser.AudioFormat{parser.AudioAC3}, false, ""}},
		{"Movie (2019) [1080p] [BluRay] [5.1] [YTS.MX]", audio{nil, false, "5.1"}},
		{"Movie.2019.1080p.WEB.x264.6CH-GROUP", audio{nil, false, "5.1"}},
	}, func(p parser.ParsedRelease) audio { return audio{p.Audio, p.Atmos, p.Channels} })
}

type edition struct {
	Edition parser.Edition
	Text    string
}

func TestParse_Edition(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[edition]{
		{"Kingdom.of.Heaven.2005.DC.1080p.BluRay.H264-REFRACTiON", edition{parser.EditionDirectors, "DC"}},
		{"Kingdom.Of.Heaven.2005.Directors.Cut.Roadshow.Version.DTS-HD.DTS.1080p.BluRay.x264.HQ-TUSAHD",
			edition{parser.EditionDirectors, "Directors.Cut.Roadshow.Version"}},
		{"Movie.2019.Director's.Cut.1080p.BluRay.x264-GROUP", edition{parser.EditionDirectors, "Director's.Cut"}},
		{"Les.Miserables.1978.EXTENDED.COMPLETE.BLURAY-INCUBO", edition{parser.EditionExtended, "EXTENDED"}},
		{"Aliens.1986.THEATRiCAL.1080p.BluRay.H264-TAPPAD", edition{parser.EditionTheatrical, "THEATRiCAL"}},
		{"Ogre.2008.UNRATED.DVDRip.XviD-DEViSE", edition{parser.EditionUnrated, "UNRATED"}},
		{"Das.Boot.1981.UNCUT.REMASTERED.2DISC.GERMAN.DL.COMPLETE.PAL.DVD9-iNRi",
			edition{parser.EditionUncut | parser.EditionRemastered, "UNCUT.REMASTERED"}},
		{"Avengers.Endgame.2019.IMAX.HDR.2160p.WEB.H265-RVKD", edition{parser.EditionIMAX, "IMAX"}},
		{"Seven.Samurai.1954.Criterion.iNTERNAL.DVDRip.XviD-FLS", edition{parser.EditionCriterion, "Criterion"}},
		{"Pulp.Fiction.1994.Special.Edition.German.DL.1080p.BluRay.AVC-SAViOURHD", edition{parser.EditionSpecial, "Special.Edition"}},
		{"Aliens.1986.SE.1080p.BluRay.H264-TAPPAD", edition{parser.EditionSpecial, "SE"}},
		{"Apocalypse.Now.1979.Final.Cut.POLISH.1080p.WEB.H264-FLAME", edition{parser.EditionFinalCut, "Final.Cut"}},
		{"Kingdom.of.Heaven.2005.ULTIMATE.EDITION.COMPLETE.BluRay-GiMCHi", edition{parser.EditionUltimate, "ULTIMATE.EDITION"}},
		{"Movie.1990.25th.Anniversary.Edition.1080p.BluRay.x264-GROUP", edition{parser.EditionAnniversary, "25th.Anniversary.Edition"}},
		{"Movie.2015.Open.Matte.1080i.HDTV.DD5.1.MPEG2", edition{parser.EditionOpenMatte, "Open.Matte"}},
		{"Movie.1999.Fan.Edit.1080p.BluRay.x264-GROUP", edition{parser.EditionFanEdit, "Fan.Edit"}},
		{"Apocalypse.Now.1979.Redux.Remastered.German.1080p.BluRay.AVC-UNTAVC", edition{parser.EditionRedux | parser.EditionRemastered, "Redux.Remastered"}},
		{"Mad.Max.Fury.Road.2015.iTALiAN.BLACK.N.CHROME.EDiTiON.720p.BluRay.x264-NTROPiC", edition{0, "BLACK.N.CHROME.EDiTiON"}},
		{"The.Final.Cut.2004.1080p.BluRay.x264-GROUP", edition{}},
		{"Unforgiven.1992.REMASTERED.25th.A.E.DTS-HD.DTS.MULTISUBS.1080p.BluRay.x264.HQ-TUSAHD", edition{parser.EditionRemastered | parser.EditionAnniversary, "REMASTERED.25th.A.E"}},
		{"Terminator.2.Judgment.Day.The.Ultimate.Edition.1991.NTSC.2DISC.COMPLETE.DVDR-25LiGHTERS", edition{parser.EditionUltimate, "Ultimate.Edition"}},
		{"Les.Miserables.in.Concert.The.25th.Anniversary.2010.1080p.BluRay.H264-MiSERABLE", edition{}},
	}, func(p parser.ParsedRelease) edition { return edition{p.Edition, p.EditionText} })
}

type langs struct {
	Languages []parser.Language
	Multi     bool
}

func TestParse_Languages(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[langs]{
		{"Movie.2019.1080p.BluRay.x264-GROUP", langs{}},
		{"Movie.2019.FRENCH.1080p.BluRay.x264-GROUP", langs{[]parser.Language{parser.LanguageFrench}, false}},
		{"Movie.2019.TRUEFRENCH.1080p.BluRay.x264-GROUP", langs{[]parser.Language{parser.LanguageFrench}, false}},
		{"Movie.2019.MULTi.TRUEFRENCH.1080p.BluRay.x264-GROUP", langs{[]parser.Language{parser.LanguageFrench}, true}},
		{"Movie.2019.MULTi.VFF.1080p.BluRay.x264-GROUP", langs{[]parser.Language{parser.LanguageFrench}, true}},
		{"Movie.2019.VFQ.1080p.WEB.x264-GROUP", langs{[]parser.Language{parser.LanguageFrenchCanadian}, false}},
		{"Movie.2019.German.DL.1080p.BluRay.x264-GROUP", langs{[]parser.Language{parser.LanguageGerman}, true}},
		{"Movie.2019.iTALiAN.1080p.BluRay.x264-GROUP", langs{[]parser.Language{parser.LanguageItalian}, false}},
		{"Movie.2019.SPANiSH.1080p.WEB.h264-GROUP", langs{[]parser.Language{parser.LanguageSpanish}, false}},
		{"Top.Gun.Maverick.2022.LATiN.SPANiSH.BDRip.x264-CEBRAY", langs{[]parser.Language{parser.LanguageLatino}, false}},
		{"Movie.2019.LATINO.1080p.WEB.x264-GROUP", langs{[]parser.Language{parser.LanguageLatino}, false}},
		{"Train.To.Busan.2.2020.KOREAN.1080p.BLURAY.x264-HOA", langs{[]parser.Language{parser.LanguageKorean}, false}},
		{"Seven.Samurai.1954.JAPANESE.2160p.UHD.BluRay.H265-MOONCAKE", langs{[]parser.Language{parser.LanguageJapanese}, false}},
		{"Movie.2019.HINDI.1080p.WEB.x264-GROUP", langs{[]parser.Language{parser.LanguageHindi}, false}},
		{"Inception.2010.RU.DUAL.2160p.UHD.BluRay.x265-RUHD", langs{[]parser.Language{parser.LanguageRussian}, true}},
		{"Toy.Story.5.2026.NORDIC.COMPLETE.BLURAY-BDTOGO", langs{[]parser.Language{parser.LanguageNordic}, false}},
		{"Ik.Zal.Zien.2025.DUTCH.1080p.WEB.H264-MULTiKABEL", langs{[]parser.Language{parser.LanguageDutch}, false}},
		{"Hereditary.2018.PL.DUAL.1080p.BluRay.x264-FLAME", langs{[]parser.Language{parser.LanguagePolish}, true}},
		{"Deadpool.and.Wolverine.2024.PLDUB.BDRip.x264-FLAME", langs{[]parser.Language{parser.LanguagePolish}, false}},
		{"Memory.2025.CZECH.1080p.HDTV.x265-iCZi", langs{[]parser.Language{parser.LanguageCzech}, false}},
		{"Spirited.Away.2001.JAPANESE.NORDIC.DTS-HD.DTS.1080p.BluRay.x264.HQ-TUSAHD", langs{[]parser.Language{parser.LanguageJapanese, parser.LanguageNordic}, false}},
		{"Crouching.Tiger.Hidden.Dragon.2000.REMASTERED.CHINESE.TrueHD.Atmos.AC3.MULTISUBS.1080p.BluRay.x264.HQ-TUSAHD", langs{[]parser.Language{parser.LanguageChinese}, false}},
		{"Movie.2019.VOSTFR.1080p.WEB.x264-GROUP", langs{}},
		{"La.haine.1995.BONUS.GERMAN.SUBBED.COMPLETE.BLURAY-iFPD", langs{}},
		{"Intouchables.2011.CUSTOM.PT.SUBS.PAL.DVDR-HRAKi", langs{}},
	}, func(p parser.ParsedRelease) langs { return langs{p.Languages, p.Multi} })
}

type subs struct {
	Hardcoded bool
	HCTag     string
	Subs      string
}

func TestParse_Subs(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[subs]{
		{"Movie.2019.1080p.BluRay.x264-GROUP", subs{}},
		{"Movie Title 2017 HC 720p HDRiP DD5 1 x264-LEGi0N", subs{true, "HC", ""}},
		{"Movie.2016.1080p.KORSUB.WEBRip.x264.AAC2.0-GROUP", subs{true, "KORSUB", ""}},
		{"Movie.2016.1080p.KORSUBS.WEBRip.x264.AAC2.0-GROUP", subs{true, "KORSUBS", ""}},
		{"Movie.2019.1080p.HCSUBS.WEB.x264-GROUP", subs{true, "HCSUBS", ""}},
		{"Movie.2019.Hardsub.720p.WEB.x264-GROUP", subs{true, "Hardsub", ""}},
		{"Godzilla.Minus.One.2023.SUBFRENCH.1080p.BluRay.x264-UKDHD", subs{true, "SUBFRENCH", ""}},
		{"Movie.2019.VOSTFR.1080p.WEB.x264-GROUP", subs{false, "", "VOSTFR"}},
		{"Lagaan.Es.war.einmal.in.Indien.2001.German.SUBBED.DVDRip.x264-GMA", subs{false, "", "German.SUBBED"}},
		{"Your.Name.Engraved.Herein.2020.FESTiVAL.SUBBED.1080p.BluRay.x264-NOELLE", subs{false, "", "SUBBED"}},
		{"2001.A.Space.Odyssey.1968.SE.DTS-HRA.DTS.MULTISUBS.1080p.BluRay.x264.HQ-TUSAHD", subs{false, "", "MULTISUBS"}},
	}, func(p parser.ParsedRelease) subs { return subs{p.HardcodedSubs, p.HardcodedSubsTag, p.Subs} })
}

func TestParse_Revision(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[parser.Revision]{
		{"Movie.2019.1080p.BluRay.x264-GROUP", parser.Revision{Version: 1}},
		{"Movie.2019.PROPER.1080p.BluRay.x264-GROUP", parser.Revision{Version: 2}},
		{"Movie.2019.REPACK.1080p.BluRay.x264-GROUP", parser.Revision{Version: 2}},
		{"Movie.2019.RERIP.1080p.BluRay.x264-GROUP", parser.Revision{Version: 2}},
		{"Movie.2019.REPACK2.1080p.BluRay.x264-GROUP", parser.Revision{Version: 3}},
		{"Movie.2019.PROPER.REPACK.1080p.BluRay.x264-GROUP", parser.Revision{Version: 3}},
		{"Blade.Runner.2049.2017.German.720p.BluRay.x264.REAL.RERiP-SPiCY", parser.Revision{Version: 2, Real: true}},
		{"Movie.2019.REAL.PROPER.1080p.BluRay.x264-GROUP", parser.Revision{Version: 2, Real: true}},
		{"Movie.2019.720p.SUBBED.HDRip.V2.XViD-26k", parser.Revision{Version: 2}},
		{"[Group] Movie (2019) v3 [1080p]", parser.Revision{Version: 3}},
		{"Oppenheimer.The.Real.Story.2023.1080p.WEB.h264-EDITH", parser.Revision{Version: 1}},
	}, func(p parser.ParsedRelease) parser.Revision { return p.Revision })
}

type misc struct {
	Group     string
	Container string
	Streaming string
	Hash      string
	ThreeD    bool
}

func TestParse_GroupContainerStreaming(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[misc]{
		{"Movie.2019.1080p.BluRay.x264-GROUP", misc{Group: "GROUP"}},
		{"Movie.2019.1080p.BluRay.x264-GROUP.mkv", misc{Group: "GROUP", Container: "mkv"}},
		{"Movie.2019.1080p.BluRay.x264-GROUP[rarbg]", misc{Group: "GROUP"}},
		{"Movie.2019.1080p.BluRay.x264-GROUP-Obfuscated", misc{Group: "GROUP"}},
		{"Movie.2019.1080p.BluRay.x264-GROUP-Scrambled.mkv", misc{Group: "GROUP", Container: "mkv"}},
		{"Movie.2019.1080p.BluRay.x264-GROUP-AsRequested", misc{Group: "GROUP"}},
		{"Movie.2019.1080p.BluRay.x264-GROUP-postbot", misc{Group: "GROUP"}},
		{"Movie.2019.1080p.BluRay.x264-GROUP-xpost", misc{Group: "GROUP"}},
		{"Movie.2019.1080p.WEB-DL.DDP5.1.H.264-GROUP-Rakuten", misc{Group: "GROUP"}},
		{"Movie.2019.1080p.WEB-DL", misc{}},
		{"Movie.2019.1080p.BluRay.DTS-HD", misc{}},
		{"Movie 2019 1080p BluRay x264 - GROUP", misc{}},
		{"Movie.2019.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTG", misc{Group: "NTG", Streaming: "AMZN"}},
		{"Movie.2019.2160p.NF.WEB-DL.DDP5.1.HDR.HEVC-GROUP", misc{Group: "GROUP", Streaming: "NF"}},
		{"Movie.2019.2160p.DSNP.WEB-DL.DDP5.1.HDR.HEVC-GROUP", misc{Group: "GROUP", Streaming: "DSNP"}},
		{"Movie.2019.2160p.ATVP.WEB-DL.DDP5.1.HDR.HEVC-GROUP", misc{Group: "GROUP", Streaming: "ATVP"}},
		{"Movie.2019.1080p.HMAX.WEB-DL.DD5.1.H.264-GROUP", misc{Group: "GROUP", Streaming: "HMAX"}},
		{"Movie.2019.1080p.iT.WEB-DL.DD5.1.H.264-GROUP", misc{Group: "GROUP", Streaming: "iT"}},
		{"Movie.2019.1080p.MA.WEB-DL.DDP5.1.H.264-GROUP", misc{Group: "GROUP", Streaming: "MA"}},
		{"Mad.Max.Fury.Road.2015.1080p.BluRay.x264-GROUP", misc{Group: "GROUP"}},
		{"[SubsPlease] Movie (2019) (1080p) [ABCD1234].mkv", misc{Group: "SubsPlease", Container: "mkv", Hash: "ABCD1234"}},
		{"Movie.2019.1080p.BluRay.x264-GROUP.mp4", misc{Group: "GROUP", Container: "mp4"}},
		{"Movie.2019.DVDRip.XviD-GROUP.avi", misc{Group: "GROUP", Container: "avi"}},
		{"Movie.Title.1996.Bluray.ISO", misc{Container: "iso"}},
		{"Mad.Max.Fury.Road.2015.3D.PL.DUAL.1080p.BluRay.x264-FLAME", misc{Group: "FLAME", ThreeD: true}},
		{"Movie.2019.1080p.BluRay.Half-SBS.x264-GROUP", misc{Group: "GROUP", ThreeD: true}},
		{"The Matrix (1999) [1080p] [BluRay] [5.1] [YTS.MX]", misc{Group: "YTS.MX"}},
		{"Shawshank_Redemption-DVDRip-PRiVCD", misc{Group: "PRiVCD"}},
		{"Memoirs.Of.A.Geisha.2005.COMPLETE.BLURAY-UNTOUCHED", misc{Group: "UNTOUCHED"}},
		{"Mulan.II.FRENCH.STV.DVDRip.XViD-UNRATED", misc{Group: "UNRATED"}},
		{"Pulp.Fiction.1994.NORDIC.1080P.BLURAY.H264-HD_Leaks", misc{Group: "HD_Leaks"}},
		{"Blade.Runner.2049.3D.2017.German.DL.1080p.BluRay.x264.RERIP-BluRay3D", misc{Group: "BluRay3D", ThreeD: true}},
		{"Movie.2019.1080p.Blu-ray", misc{}},
		{"Movie.2019.1080p.BluRay.DTS-X", misc{}},
	}, func(p parser.ParsedRelease) misc {
		return misc{p.ReleaseGroup, p.Container, p.Streaming, p.Hash, p.ThreeD}
	})
}

type content struct {
	Title   string
	Fix     parser.FixKind
	Extras  bool
	Sample  bool
	IsMovie bool
}

// TestParse_Content covers fix packs, bonus discs and samples: flagged without
// changing the title, and only DIRFIX is still the movie.
func TestParse_Content(t *testing.T) {
	t.Parallel()
	runField(t, []fieldCase[content]{
		{"Movie.2019.1080p.BluRay.x264-GRP", content{Title: "Movie", IsMovie: true}},
		{"Movie.2019.1080p.BluRay.x264.DIRFIX-GRP", content{Title: "Movie", Fix: parser.FixDir, IsMovie: true}},
		{"Movie.2019.1080p.BluRay.x264.DIR.FIX-GRP", content{Title: "Movie", Fix: parser.FixDir, IsMovie: true}},
		{"Movie.2019.1080p.BluRay.x264.NFOFIX-GRP", content{Title: "Movie", Fix: parser.FixNFO}},
		{"Movie.2019.1080p.BluRay.x264.SAMPLEFiX-GRP", content{Title: "Movie", Fix: parser.FixSample}},
		{"Movie.2019.1080p.BluRay.x264.Sample.Fix-GRP", content{Title: "Movie", Fix: parser.FixSample}},
		{"Movie.2019.1080p.BluRay.x264.SUBFIX-GRP", content{Title: "Movie", Fix: parser.FixSub}},
		{"Movie.2019.1080p.BluRay.x264.SYNCFIX-GRP", content{Title: "Movie", Fix: parser.FixSync}},
		{"Movie.2019.1080p.BluRay.x264.PROOFFIX-GRP", content{Title: "Movie", Fix: parser.FixProof}},
		{"Movie.2019.NFOFIX.DIRFIX.1080p.BluRay.x264-GRP", content{Title: "Movie", Fix: parser.FixNFO}}, // first fix wins
		{"Movie.2019.BONUS.COMPLETE.BLURAY-GRP", content{Title: "Movie", Extras: true}},
		{"Movie.2019.EXTRAS.1080p.BluRay.x264-GRP", content{Title: "Movie", Extras: true}},
		{"Movie.2019.Featurettes.1080p.WEB.H264-GRP", content{Title: "Movie", Extras: true}},
		{"Movie.2019.Making.Of.1080p.WEB.H264-GRP", content{Title: "Movie", Extras: true}},
		{"Movie.2019.SAMPLE.1080p.BluRay.x264-GRP", content{Title: "Movie", Sample: true}},
		{"Apocalypse.Now.1979.BONUS.Hearts.of.Darkness.MULTI.COMPLETE.BLURAY.DiRFiX-UNTOUCHED",
			content{Title: "Apocalypse Now", Fix: parser.FixDir, Extras: true}},
		// In the title, these words are title words.
		{"The.Making.Of.Fanny.and.Alexander.1984.1080p.BluRay.x264-GRP", content{Title: "The Making Of Fanny and Alexander", IsMovie: true}},
		{"Free.Sample.2017.1080p.WEB.H264-GRP", content{Title: "Free Sample", IsMovie: true}},
		{"Featurette.2020.1080p.WEB.H264-GRP", content{Title: "Featurette", IsMovie: true}},
	}, func(p parser.ParsedRelease) content {
		return content{p.Title, p.Fix, p.Extras, p.Sample, p.IsMovie()}
	})
}

func TestParse_Obfuscated(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"a8f7c6d5e4b3a29182736455463728ab",
		"a8f7c6d5e4b3a29182736455463728ab.mkv",
		"Xk9Qz3LmP0wRt7Yb2Nc5",
		"/downloads/complete/5f2e8a9c1b7d4e3f6a0b",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			got := parser.Parse(in)
			if got.Title != "" || got.Year != 0 || got.ReleaseGroup != "" || got.Source != parser.SourceUnknown {
				t.Errorf("Parse(%q) should yield an empty release, got title=%q year=%d group=%q source=%v",
					in, got.Title, got.Year, got.ReleaseGroup, got.Source)
			}
		})
	}
}

func TestParse_CaseInsensitive(t *testing.T) {
	t.Parallel()
	upper := parser.Parse("THE.DARK.KNIGHT.2008.1080P.BLURAY.X264.DTS-HD.MA.5.1-GROUP")
	lower := parser.Parse("the.dark.knight.2008.1080p.bluray.x264.dts-hd.ma.5.1-group")
	opts := cmp.Options{
		cmpopts.IgnoreFields(parser.ParsedRelease{}, "Title", "ReleaseGroup"),
	}
	if diff := cmp.Diff(upper, lower, opts); diff != "" {
		t.Errorf("upper/lower case parse differ (-upper +lower):\n%s", diff)
	}
	if upper.CleanTitle != "dark knight" {
		t.Errorf("CleanTitle = %q, want %q", upper.CleanTitle, "dark knight")
	}
}

func TestParse_Deterministic(t *testing.T) {
	t.Parallel()
	const in = "Dune.Part.Two.2024.MULTi.TRUEFRENCH.2160p.UHD.BluRay.x265-SESKAPiLE"
	first := parser.Parse(in)
	for range 100 {
		if diff := cmp.Diff(first, parser.Parse(in)); diff != "" {
			t.Fatalf("Parse is not deterministic:\n%s", diff)
		}
	}
}
