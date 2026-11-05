package parser_test

import (
	"encoding"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

type textEnum interface {
	fmt.Stringer
	encoding.TextMarshaler
}

// roundTrip marshals v to text, checks it equals wantText and String(), and unmarshals it back.
func roundTrip[T textEnum](t *testing.T, v T, wantText string, unmarshal func([]byte) (T, error)) {
	t.Helper()
	b, err := v.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText(%v): %v", v, err)
	}
	if string(b) != wantText {
		t.Errorf("MarshalText(%#v) = %q, want %q", v, b, wantText)
	}
	if v.String() != wantText {
		t.Errorf("String() = %q, want %q", v.String(), wantText)
	}
	back, err := unmarshal(b)
	if err != nil {
		t.Fatalf("UnmarshalText(%q): %v", b, err)
	}
	if !cmp.Equal(back, v) {
		t.Errorf("round trip %q: got %v, want %v", b, back, v)
	}
}

func unmarshalAs[T any, P interface {
	*T
	encoding.TextUnmarshaler
}](b []byte) (T, error) {
	var v T
	err := P(&v).UnmarshalText(b)
	return v, err
}

func TestEnumText(t *testing.T) {
	t.Parallel()
	t.Run("Resolution", func(t *testing.T) {
		for v, s := range map[parser.Resolution]string{
			parser.ResolutionUnknown: "unknown", parser.Resolution480p: "480p", parser.Resolution576p: "576p",
			parser.Resolution720p: "720p", parser.Resolution1080p: "1080p", parser.Resolution2160p: "2160p",
		} {
			roundTrip(t, v, s, unmarshalAs[parser.Resolution])
		}
	})
	t.Run("Source", func(t *testing.T) {
		for v, s := range map[parser.Source]string{
			parser.SourceUnknown: "unknown", parser.SourceCam: "cam", parser.SourceTelesync: "telesync",
			parser.SourceTelecine: "telecine", parser.SourceScreener: "screener", parser.SourceDVD: "dvd",
			parser.SourceHDTV: "hdtv", parser.SourceWebRip: "webrip", parser.SourceWebDL: "webdl", parser.SourceBluRay: "bluray",
		} {
			roundTrip(t, v, s, unmarshalAs[parser.Source])
		}
	})
	t.Run("Modifier", func(t *testing.T) {
		for v, s := range map[parser.Modifier]string{
			parser.ModifierNone: "none", parser.ModifierRemux: "remux", parser.ModifierBRDisk: "brdisk",
			parser.ModifierDVDR: "dvdr", parser.ModifierRegional: "regional", parser.ModifierRawHD: "rawhd",
		} {
			roundTrip(t, v, s, unmarshalAs[parser.Modifier])
		}
	})
	t.Run("Codec", func(t *testing.T) {
		for v, s := range map[parser.Codec]string{
			parser.CodecUnknown: "unknown", parser.CodecH264: "h264", parser.CodecH265: "h265", parser.CodecAV1: "av1",
			parser.CodecVC1: "vc1", parser.CodecXviD: "xvid", parser.CodecDivX: "divx", parser.CodecMPEG2: "mpeg2", parser.CodecVP9: "vp9",
		} {
			roundTrip(t, v, s, unmarshalAs[parser.Codec])
		}
	})
	t.Run("HDRFormat", func(t *testing.T) {
		for v, s := range map[parser.HDRFormat]string{
			parser.HDR10: "hdr10", parser.HDR10Plus: "hdr10+", parser.DolbyVision: "dv", parser.HLG: "hlg", parser.SDR: "sdr",
		} {
			roundTrip(t, v, s, unmarshalAs[parser.HDRFormat])
		}
	})
	t.Run("AudioFormat", func(t *testing.T) {
		for v, s := range map[parser.AudioFormat]string{
			parser.AudioAAC: "aac", parser.AudioAC3: "ac3", parser.AudioEAC3: "eac3", parser.AudioTrueHD: "truehd",
			parser.AudioDTS: "dts", parser.AudioDTSHD: "dts-hd", parser.AudioDTSHDMA: "dts-hd-ma", parser.AudioDTSHDHRA: "dts-hd-hra",
			parser.AudioDTSX: "dts-x", parser.AudioFLAC: "flac", parser.AudioPCM: "pcm", parser.AudioOpus: "opus", parser.AudioMP3: "mp3",
		} {
			roundTrip(t, v, s, unmarshalAs[parser.AudioFormat])
		}
	})
	t.Run("Language", func(t *testing.T) {
		for v, s := range map[parser.Language]string{
			parser.LanguageEnglish: "en", parser.LanguageFrench: "fr", parser.LanguageFrenchCanadian: "fr-CA",
			parser.LanguageGerman: "de", parser.LanguageLatino: "es-419", parser.LanguageNordic: "nordic",
		} {
			roundTrip(t, v, s, unmarshalAs[parser.Language])
		}
	})
	t.Run("Edition", func(t *testing.T) {
		roundTrip(t, parser.Edition(0), "", unmarshalAs[parser.Edition])
		roundTrip(t, parser.EditionDirectors, "directors", unmarshalAs[parser.Edition])
		roundTrip(t, parser.EditionUncut|parser.EditionRemastered, "uncut,remastered", unmarshalAs[parser.Edition])
		roundTrip(t, parser.EditionFinalCut|parser.EditionIMAX, "imax,final-cut", unmarshalAs[parser.Edition])
	})
}

func TestEnumText_Invalid(t *testing.T) {
	t.Parallel()
	targets := map[string]encoding.TextUnmarshaler{
		"Resolution": new(parser.Resolution), "Source": new(parser.Source), "Modifier": new(parser.Modifier),
		"Codec": new(parser.Codec), "HDRFormat": new(parser.HDRFormat), "AudioFormat": new(parser.AudioFormat),
		"Language": new(parser.Language), "Edition": new(parser.Edition),
	}
	for name, u := range targets {
		if err := u.UnmarshalText([]byte("no-such-value")); err == nil {
			t.Errorf("%s.UnmarshalText(invalid) returned nil error", name)
		}
	}
	if _, err := parser.HDRFormat(250).MarshalText(); err == nil {
		t.Error("HDRFormat(250).MarshalText() returned nil error")
	}
}

func TestParsedRelease_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	in := parser.Parse("Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR.HEVC.TrueHD.Atmos.7.1-GROUP")
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out parser.ParsedRelease
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal(%s): %v", b, err)
	}
	if diff := cmp.Diff(in, out); diff != "" {
		t.Errorf("JSON round trip mismatch (-in +out):\n%s\njson: %s", diff, b)
	}
}
