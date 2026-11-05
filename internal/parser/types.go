package parser

import (
	"fmt"
	"strings"
)

// ParsedRelease is everything Parse extracts from a release name.
// Unknown fields keep their zero value; Revision.Version is always at least 1.
type ParsedRelease struct {
	Title           string   `json:"title,omitempty"`
	AlternateTitles []string `json:"alternateTitles,omitempty"` // titles after an "AKA" marker
	Year            int      `json:"year,omitempty"`
	CleanTitle      string   `json:"cleanTitle,omitempty"` // see CleanTitle

	Resolution Resolution `json:"resolution,omitempty"`
	Source     Source     `json:"source,omitempty"`
	Modifier   Modifier   `json:"modifier,omitempty"`

	VideoCodec         Codec       `json:"videoCodec,omitempty"`
	BitDepth           int         `json:"bitDepth,omitempty"` // only when tagged (10bit, Hi10P...), never inferred from HDR
	HDR                []HDRFormat `json:"hdr,omitempty"`      // in order of appearance
	DolbyVisionProfile int         `json:"dvProfile,omitempty"`

	Audio    []AudioFormat `json:"audio,omitempty"` // in order of appearance
	Atmos    bool          `json:"atmos,omitempty"` // object audio on top of TrueHD or E-AC3
	Channels string        `json:"channels,omitempty"`

	Edition     Edition `json:"edition,omitempty"`
	EditionText string  `json:"editionText,omitempty"` // raw edition tokens as written in the name

	// Languages lists the languages tagged in the name, in order of appearance. An
	// untagged name yields no language: the parser never assumes English. A later stage
	// (with metadata) may infer the original language and set LanguageInferred.
	Languages        []Language `json:"languages,omitempty"`
	LanguageInferred bool       `json:"languageInferred,omitempty"`
	Multi            bool       `json:"multi,omitempty"` // MULTi, DL, DUAL, ML: several audio tracks

	HardcodedSubs    bool   `json:"hardcodedSubs,omitempty"`
	HardcodedSubsTag string `json:"hardcodedSubsTag,omitempty"`
	Subs             string `json:"subs,omitempty"` // soft subtitle tags (VOSTFR, SUBBED, MULTISUBS...)

	Revision Revision `json:"revision"`

	ReleaseGroup string `json:"releaseGroup,omitempty"`
	Container    string `json:"container,omitempty"` // mkv, mp4, avi, iso...
	Streaming    string `json:"streaming,omitempty"` // canonical service tag: AMZN, NF, DSNP...
	Hash         string `json:"hash,omitempty"`      // CRC32 in brackets, e.g. [ABCD1234]
	ThreeD       bool   `json:"threeD,omitempty"`
}

// Revision identifies re-releases of the same content.
type Revision struct {
	Version int  `json:"version"`        // 1 for an original release; +1 per PROPER/REPACK/RERIP, or vN
	Real    bool `json:"real,omitempty"` // REAL.PROPER / REAL.REPACK
}

// enumText returns the text of v from names, or an error if v is out of range.
func enumText[T ~uint8](v T, names []string) (string, error) {
	if int(v) >= len(names) || names[v] == "" {
		return "", fmt.Errorf("parser: invalid %T value %d", v, uint8(v))
	}
	return names[v], nil
}

// enumString is enumText for fmt.Stringer: out-of-range values print as T(n).
func enumString[T ~uint8](v T, names []string) string {
	s, err := enumText(v, names)
	if err != nil {
		return fmt.Sprintf("%T(%d)", v, uint8(v))
	}
	return s
}

// enumParse is the inverse of enumText.
func enumParse[T ~uint8](dst *T, b []byte, names []string) error {
	for i, n := range names {
		if n != "" && n == string(b) {
			*dst = T(i)
			return nil
		}
	}
	return fmt.Errorf("parser: unknown %T %q", *dst, b)
}

// Resolution is the vertical video resolution.
type Resolution uint8

// Resolutions. Interlaced variants map to the progressive value with the same height.
const (
	ResolutionUnknown Resolution = iota
	Resolution480p
	Resolution576p
	Resolution720p
	Resolution1080p
	Resolution2160p
)

var resolutionNames = []string{"unknown", "480p", "576p", "720p", "1080p", "2160p"}

// String returns the text form of r.
func (r Resolution) String() string { return enumString(r, resolutionNames) }

// MarshalText implements encoding.TextMarshaler.
func (r Resolution) MarshalText() ([]byte, error) { return marshal(enumText(r, resolutionNames)) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (r *Resolution) UnmarshalText(b []byte) error { return enumParse(r, b, resolutionNames) }

// Source is where the video comes from.
type Source uint8

// Sources, roughly from worst to best.
const (
	SourceUnknown Source = iota
	SourceCam
	SourceTelesync
	SourceTelecine
	SourceScreener
	SourceDVD
	SourceHDTV
	SourceWebRip
	SourceWebDL
	SourceBluRay
)

var sourceNames = []string{"unknown", "cam", "telesync", "telecine", "screener", "dvd", "hdtv", "webrip", "webdl", "bluray"}

// String returns the text form of s.
func (s Source) String() string { return enumString(s, sourceNames) }

// MarshalText implements encoding.TextMarshaler.
func (s Source) MarshalText() ([]byte, error) { return marshal(enumText(s, sourceNames)) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *Source) UnmarshalText(b []byte) error { return enumParse(s, b, sourceNames) }

// Modifier refines a Source.
type Modifier uint8

// Modifiers.
const (
	ModifierNone     Modifier = iota
	ModifierRemux             // untouched streams remuxed from a disc
	ModifierBRDisk            // full Blu-ray disc (BDMV, ISO, COMPLETE.BLURAY)
	ModifierDVDR              // full DVD (DVDR, DVD5, DVD9)
	ModifierRegional          // R5/R6 regional DVD
	ModifierRawHD             // raw HDTV transport stream
)

var modifierNames = []string{"none", "remux", "brdisk", "dvdr", "regional", "rawhd"}

// String returns the text form of m.
func (m Modifier) String() string { return enumString(m, modifierNames) }

// MarshalText implements encoding.TextMarshaler.
func (m Modifier) MarshalText() ([]byte, error) { return marshal(enumText(m, modifierNames)) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *Modifier) UnmarshalText(b []byte) error { return enumParse(m, b, modifierNames) }

// Codec is the video codec. x264, H.264 and AVC are all CodecH264; x265, H.265 and HEVC are CodecH265.
type Codec uint8

// Video codecs.
const (
	CodecUnknown Codec = iota
	CodecH264
	CodecH265
	CodecAV1
	CodecVC1
	CodecXviD
	CodecDivX
	CodecMPEG2
	CodecVP9
)

var codecNames = []string{"unknown", "h264", "h265", "av1", "vc1", "xvid", "divx", "mpeg2", "vp9"}

// String returns the text form of c.
func (c Codec) String() string { return enumString(c, codecNames) }

// MarshalText implements encoding.TextMarshaler.
func (c Codec) MarshalText() ([]byte, error) { return marshal(enumText(c, codecNames)) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (c *Codec) UnmarshalText(b []byte) error { return enumParse(c, b, codecNames) }

// HDRFormat is a dynamic range format. A bare "HDR" tag is reported as HDR10, the
// mandatory base layer of every HDR Blu-ray and streaming release.
type HDRFormat uint8

// HDR formats. The zero value is invalid and never produced.
const (
	HDR10 HDRFormat = iota + 1
	HDR10Plus
	DolbyVision
	HLG
	SDR // explicitly tagged SDR (e.g. an SDR grade of a UHD source)
)

var hdrNames = []string{"", "hdr10", "hdr10+", "dv", "hlg", "sdr"}

// String returns the text form of h.
func (h HDRFormat) String() string { return enumString(h, hdrNames) }

// MarshalText implements encoding.TextMarshaler.
func (h HDRFormat) MarshalText() ([]byte, error) { return marshal(enumText(h, hdrNames)) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (h *HDRFormat) UnmarshalText(b []byte) error { return enumParse(h, b, hdrNames) }

// AudioFormat is an audio codec. Atmos is a separate flag (ParsedRelease.Atmos).
type AudioFormat uint8

// Audio formats. The zero value is invalid and never produced.
const (
	AudioAAC      AudioFormat = iota + 1
	AudioAC3                  // Dolby Digital (AC3, DD)
	AudioEAC3                 // Dolby Digital Plus (EAC3, DDP, DD+)
	AudioTrueHD               // Dolby TrueHD
	AudioDTS                  // DTS core
	AudioDTSHD                // DTS-HD without MA/HRA qualifier
	AudioDTSHDMA              // DTS-HD Master Audio
	AudioDTSHDHRA             // DTS-HD High Resolution Audio
	AudioDTSX                 // DTS:X
	AudioFLAC
	AudioPCM // PCM and LPCM
	AudioOpus
	AudioMP3
)

var audioNames = []string{"", "aac", "ac3", "eac3", "truehd", "dts", "dts-hd", "dts-hd-ma", "dts-hd-hra", "dts-x", "flac", "pcm", "opus", "mp3"}

// String returns the text form of a.
func (a AudioFormat) String() string { return enumString(a, audioNames) }

// MarshalText implements encoding.TextMarshaler.
func (a AudioFormat) MarshalText() ([]byte, error) { return marshal(enumText(a, audioNames)) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (a *AudioFormat) UnmarshalText(b []byte) error { return enumParse(a, b, audioNames) }

// Language is a language tag found in a release name. Text values are BCP 47 codes,
// except LanguageNordic ("nordic"), a scene tag for Nordic multi-language releases.
type Language uint8

// Languages. The zero value is invalid and never produced.
const (
	LanguageEnglish Language = iota + 1
	LanguageFrench
	LanguageFrenchCanadian
	LanguageGerman
	LanguageItalian
	LanguageSpanish
	LanguageLatino
	LanguagePortuguese
	LanguageBrazilian
	LanguageDutch
	LanguageFlemish
	LanguagePolish
	LanguageRussian
	LanguageUkrainian
	LanguageCzech
	LanguageSlovak
	LanguageHungarian
	LanguageRomanian
	LanguageBulgarian
	LanguageGreek
	LanguageTurkish
	LanguageDanish
	LanguageSwedish
	LanguageNorwegian
	LanguageFinnish
	LanguageIcelandic
	LanguageEstonian
	LanguageLithuanian
	LanguageLatvian
	LanguageNordic
	LanguageJapanese
	LanguageKorean
	LanguageChinese
	LanguageHindi
	LanguageTamil
	LanguageTelugu
	LanguageThai
	LanguageVietnamese
	LanguageArabic
	LanguageHebrew
	LanguagePersian
	LanguageCatalan
)

var languageNames = []string{
	"", "en", "fr", "fr-CA", "de", "it", "es", "es-419", "pt", "pt-BR", "nl", "nl-BE", "pl", "ru", "uk", "cs", "sk",
	"hu", "ro", "bg", "el", "tr", "da", "sv", "no", "fi", "is", "et", "lt", "lv", "nordic", "ja", "ko", "zh", "hi",
	"ta", "te", "th", "vi", "ar", "he", "fa", "ca",
}

// String returns the text form of l.
func (l Language) String() string { return enumString(l, languageNames) }

// MarshalText implements encoding.TextMarshaler.
func (l Language) MarshalText() ([]byte, error) { return marshal(enumText(l, languageNames)) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (l *Language) UnmarshalText(b []byte) error { return enumParse(l, b, languageNames) }

// Edition is a set of edition flags.
type Edition uint32

// Edition flags.
const (
	EditionDirectors Edition = 1 << iota
	EditionExtended
	EditionTheatrical
	EditionUnrated
	EditionUncut
	EditionRemastered
	EditionIMAX
	EditionCriterion
	EditionSpecial
	EditionFinalCut
	EditionUltimate
	EditionAnniversary
	EditionOpenMatte
	EditionFanEdit
	EditionRedux
	EditionCollectors
)

var editionNames = []string{
	"directors", "extended", "theatrical", "unrated", "uncut", "remastered", "imax", "criterion",
	"special", "final-cut", "ultimate", "anniversary", "open-matte", "fan-edit", "redux", "collectors",
}

// Has reports whether all flags in f are set.
func (e Edition) Has(f Edition) bool { return e&f == f }

// String returns the comma-separated flag names in bit order ("" for none).
func (e Edition) String() string {
	if e == 0 {
		return ""
	}
	var sb strings.Builder
	for i, n := range editionNames {
		if e&(1<<i) == 0 {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(n)
	}
	if rest := e &^ (1<<len(editionNames) - 1); rest != 0 {
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "0x%x", uint32(rest))
	}
	return sb.String()
}

// MarshalText implements encoding.TextMarshaler.
func (e Edition) MarshalText() ([]byte, error) {
	if e>>len(editionNames) != 0 {
		return nil, fmt.Errorf("parser: invalid Edition bits 0x%x", uint32(e))
	}
	return []byte(e.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (e *Edition) UnmarshalText(b []byte) error {
	var out Edition
	for part := range strings.SplitSeq(string(b), ",") {
		if part == "" {
			continue
		}
		found := false
		for i, n := range editionNames {
			if n == part {
				out |= 1 << i
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("parser: unknown Edition %q", part)
		}
	}
	*e = out
	return nil
}

func marshal(s string, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}
