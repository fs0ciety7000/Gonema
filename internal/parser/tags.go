package parser

// This file holds the tag dictionaries. They are built once at package
// initialisation, never mutated afterwards, and only read by Parse: safe for
// concurrent use without locking.

// tagFlags qualify a tag.
type tagFlags uint32

const (
	// fStrong marks technical tags that can only appear after the title
	// (resolution, source, codec, audio...). The first strong tag ends the title.
	fStrong tagFlags = 1 << iota
	// fAmbig marks strong tags that are also common words (WEB, TS, CAM, 4K...):
	// they end the title only when the next token is a tag too.
	fAmbig
	// fStrip marks weak tags (languages, INTERNAL, PROPER, 3D...) that are removed
	// from the end of a title, e.g. "Inception.German.2010" -> "Inception".
	fStrip
	fRawCodec // AVC, HEVC, VC-1, MPEG-2: untouched disc video
	fEncode   // x264, x265, XviD...: an encode, never a remux or a full disc
	fAtmos
	fMulti
	f3D
	fUHD // UHD: implies 2160p when no explicit resolution
	f4K  // 4K: same, but ambiguous
	fComplete
	fReal
	fSubs
	fHardsub
	fEditionWord // EDITION, VERSION, CUT: extends an edition run
	fChannels
	fISO
	// fStripLong marks multi-word editions ("Final.Cut", "Ultimate.Edition") that are
	// stripped from the end of a title only when two title words remain, so that
	// "Blade.Runner.Final.Cut.1982" -> "Blade Runner" but "The.Final.Cut.2004" stays.
	fStripLong
	fExtras // BONUS, EXTRAS, FEATURETTE(S), MAKING.OF: bonus material only
	fSample // SAMPLE: a sample only
)

// srcBits accumulates source evidence; the final Source is resolved by priority.
type srcBits uint32

const (
	sCam srcBits = 1 << iota
	sTelesync
	sTelesyncAmbig // bare "TS": only used when nothing better is present
	sTelecine
	sScreener
	sDVD
	sDVDR
	sRegional
	sHDTV
	sRawHD
	sWebRip
	sWebDL
	sBluRay
	sRemux
	sBRDisk
)

// tag is the effect of one matched token sequence.
type tag struct {
	flags  tagFlags
	src    srcBits
	ed     Edition
	res    Resolution
	codec  Codec
	depth  uint8
	hdr    HDRFormat
	dvProf uint8
	audio  AudioFormat
	ch     uint8 // channel layout major digit ('5' in 5.1), as a byte; 0 if none
	chMin  uint8 // channel layout minor digit, as a byte
	lang   Language
	stream uint8 // index+1 into streamingNames
	rev    uint8 // revision increment (PROPER, REPACK, RERIP)
	ver    uint8 // explicit version (v2, v3)
	fix    FixKind
	n      uint8 // number of tokens consumed, set by the matcher
}

// streamingNames are the canonical streaming service tags.
var streamingNames = []string{
	"AMZN", "NF", "DSNP", "ATVP", "HMAX", "MAX", "HULU", "PCOK", "PMTP", "iT", "MA", "CRAV", "STAN",
	"RKTN", "VUDU", "MUBI", "CRKL", "ROKU", "SHO", "STZ", "SKST", "NOW", "iP", "BCORE", "CC", "HBO",
}

func streamIdx(name string) uint8 {
	for i, n := range streamingNames {
		if n == name {
			return uint8(i + 1)
		}
	}
	return 0
}

// Constructors keep the dictionary below readable.
func res(r Resolution) tag           { return tag{res: r, flags: fStrong} }
func src(s srcBits) tag              { return tag{src: s, flags: fStrong} }
func ambigSrc(s srcBits) tag         { return tag{src: s, flags: fStrong | fAmbig} }
func enc(c Codec) tag                { return tag{codec: c, flags: fStrong | fEncode} }
func raw(c Codec) tag                { return tag{codec: c, flags: fStrong | fRawCodec} }
func depth(d uint8) tag              { return tag{depth: d, flags: fStrong} }
func hdr(h HDRFormat) tag            { return tag{hdr: h, flags: fStrong} }
func dv(profile uint8) tag           { return tag{hdr: DolbyVision, dvProf: profile, flags: fStrong} }
func audio(a AudioFormat) tag        { return tag{audio: a, flags: fStrong} }
func edition(e Edition) tag          { return tag{ed: e, flags: fStrip} }
func editionKeep(e Edition) tag      { return tag{ed: e} } // never stripped from titles ("25th Anniversary", "Redux")
func editionLong(e Edition) tag      { return tag{ed: e, flags: fStripLong} }
func lang(l Language) tag            { return tag{lang: l, flags: fStrip} }
func misc() tag                      { return tag{flags: fStrip} }
func strongMisc() tag                { return tag{flags: fStrong} }
func flag(f tagFlags) tag            { return tag{flags: f} }
func stream(name string) tag         { return tag{stream: streamIdx(name)} }
func revision() tag                  { return tag{rev: 1, flags: fStrip} }
func fix(k FixKind) tag              { return tag{fix: k, flags: fStrip} }
func with(t tag, f func(*tag)) tag   { f(&t); return t }
func plus(t tag, extra tagFlags) tag { t.flags |= extra; return t }
func also(t tag, s srcBits) tag      { t.src |= s; return t }

// singles maps an upper-cased token to its tag.
var singles = buildSingles()

// compounds2 and compounds3 map sequences of upper-cased tokens ("WEB","DL").
var (
	compounds2 = buildCompounds2()
	compounds3 = buildCompounds3()
)

// compoundFirst lets the matcher skip compound lookups for most tokens.
var compoundFirst = func() map[string]struct{} {
	m := make(map[string]struct{}, len(compounds2)+len(compounds3))
	for k := range compounds2 {
		m[k[0]] = struct{}{}
	}
	for k := range compounds3 {
		m[k[0]] = struct{}{}
	}
	return m
}()

func buildSingles() map[string]tag {
	m := map[string]tag{
		// Resolution.
		"480P": res(Resolution480p), "480I": res(Resolution480p), "576P": res(Resolution576p), "576I": res(Resolution576p),
		"720P": res(Resolution720p), "1080P": res(Resolution1080p), "1080I": res(Resolution1080p), "2160P": res(Resolution2160p),
		"848X480": res(Resolution480p), "720X480": res(Resolution480p), "720X576": res(Resolution576p),
		"1280X720": res(Resolution720p), "1920X1080": res(Resolution1080p), "3840X2160": res(Resolution2160p),
		"UHD": flag(fStrong | fUHD), "4K": flag(fStrong | fAmbig | f4K),

		// Sources.
		"CAM": ambigSrc(sCam), "HDCAM": src(sCam), "CAMRIP": src(sCam), "HQCAM": src(sCam), "NEWCAM": src(sCam), "HDCAMRIP": src(sCam),
		"TS": ambigSrc(sTelesyncAmbig), "TELESYNC": src(sTelesync), "HDTS": src(sTelesync), "TSRIP": src(sTelesync), "HDTSRIP": src(sTelesync),
		"TC": ambigSrc(sTelecine), "TELECINE": src(sTelecine), "HDTC": src(sTelecine),
		"SCR": ambigSrc(sScreener), "SCREENER": ambigSrc(sScreener), "DVDSCR": src(sScreener), "DVDSCREENER": src(sScreener),
		"BDSCR": src(sScreener), "WEBSCR": src(sScreener), "WEBSCREENER": src(sScreener),
		"DVD": ambigSrc(sDVD), "DVDRIP": src(sDVD), "DVDIVX": with(src(sDVD), func(t *tag) { t.codec = CodecDivX }),
		"DVDR": src(sDVD | sDVDR), "DVD5": src(sDVD | sDVDR), "DVD9": src(sDVD | sDVDR),
		"R5": src(sDVD | sRegional), "R6": src(sDVD | sRegional),
		"HDTV": src(sHDTV), "PDTV": src(sHDTV), "SDTV": src(sHDTV), "TVRIP": src(sHDTV), "DSR": src(sHDTV), "DSRIP": src(sHDTV),
		"SATRIP": src(sHDTV), "DTHRIP": src(sHDTV), "DVBRIP": src(sHDTV), "HDTVRIP": src(sHDTV), "UHDTV": plus(src(sHDTV), fUHD),
		"RAWHD":  src(sHDTV | sRawHD),
		"WEBRIP": src(sWebRip), "WEBMUX": src(sWebRip), "WEBCAP": src(sWebRip), "HDRIP": src(sWebRip), "WEBDLRIP": src(sWebRip),
		"WEBDL": src(sWebDL), "WEB": ambigSrc(sWebDL), "WEBHD": src(sWebDL),
		"BLURAY": src(sBluRay), "BLUERAY": src(sBluRay), "BDRIP": src(sBluRay), "BRRIP": src(sBluRay), "BD": ambigSrc(sBluRay),
		"BD5": src(sBluRay), "BD9": src(sBluRay), "BDLIGHT": src(sBluRay), "MBLURAY": src(sBluRay), "BDMUX": src(sBluRay),
		"HDDVD": src(sBluRay), "HDDVDRIP": src(sBluRay), "UHDBD": plus(src(sBluRay), fUHD), "UHDBDRIP": plus(src(sBluRay), fUHD),
		"BLURAY3D": plus(src(sBluRay), f3D),
		"BD25":     src(sBluRay | sBRDisk), "BD50": src(sBluRay | sBRDisk), "BD66": src(sBluRay | sBRDisk), "BD100": src(sBluRay | sBRDisk),
		"BDMV": src(sBluRay | sBRDisk), "BDISO": src(sBluRay | sBRDisk),
		"REMUX": src(sRemux), "BDREMUX": src(sBluRay | sRemux), "UHDREMUX": plus(src(sBluRay|sRemux), fUHD),
		"ISO":      flag(fISO | fStrong),
		"COMPLETE": flag(fComplete | fStrip),

		// Video codecs and bit depth.
		"X264": enc(CodecH264), "H264": enc(CodecH264), "AVC": raw(CodecH264),
		"X265": enc(CodecH265), "H265": enc(CodecH265), "HEVC": raw(CodecH265),
		"AV1": enc(CodecAV1), "VC1": raw(CodecVC1), "XVID": enc(CodecXviD), "DIVX": enc(CodecDivX),
		"MPEG2": raw(CodecMPEG2), "VP9": enc(CodecVP9), "MVC": plus(raw(CodecH264), f3D),
		"HI10P": with(enc(CodecH264), func(t *tag) { t.depth = 10 }), "HI10": with(enc(CodecH264), func(t *tag) { t.depth = 10 }),
		"8BIT": depth(8), "10BIT": depth(10), "12BIT": depth(12), "10BITS": depth(10), "8BITS": depth(8),
		"SVCD": strongMisc(), "VCD": strongMisc(), "WMVHD": strongMisc(),

		// HDR.
		"HDR": hdr(HDR10), "HDR10": hdr(HDR10), "HDR10+": hdr(HDR10Plus), "HDR10PLUS": hdr(HDR10Plus), "HDR10P": hdr(HDR10Plus),
		"DV": dv(0), "DOVI": dv(0), "DOLBYVISION": dv(0), "DVP5": dv(5), "DVP7": dv(7), "DVP8": dv(8),
		"HLG": hdr(HLG), "SDR": hdr(SDR),

		// Audio.
		"AAC": audio(AudioAAC), "AC3": audio(AudioAC3), "DD": audio(AudioAC3), "AC3D": audio(AudioAC3),
		"DD+": audio(AudioEAC3), "DDP": audio(AudioEAC3), "EAC3": audio(AudioEAC3), "EAC3D": audio(AudioEAC3),
		"DDPA":   plus(audio(AudioEAC3), fAtmos),
		"TRUEHD": audio(AudioTrueHD), "ATMOS": flag(fAtmos | fStrong),
		"DTS": audio(AudioDTS), "DTSD": audio(AudioDTS), "DTSHD": audio(AudioDTSHD), "DTSMA": audio(AudioDTSHDMA),
		"DTSHDMA": audio(AudioDTSHDMA), "DTSHRA": audio(AudioDTSHDHRA), "DTSX": audio(AudioDTSX), "DTS:X": audio(AudioDTSX),
		"FLAC": audio(AudioFLAC), "PCM": audio(AudioPCM), "LPCM": audio(AudioPCM), "OPUS": audio(AudioOpus), "MP3": audio(AudioMP3),
		"2CH": with(flag(fChannels|fStrong), func(t *tag) { t.ch, t.chMin = '2', '0' }),
		"6CH": with(flag(fChannels|fStrong), func(t *tag) { t.ch, t.chMin = '5', '1' }),
		"8CH": with(flag(fChannels|fStrong), func(t *tag) { t.ch, t.chMin = '7', '1' }),

		// Editions.
		"DC": edition(EditionDirectors), "DIRECTORS": edition(EditionDirectors), "EXTENDED": edition(EditionExtended),
		"THEATRICAL": edition(EditionTheatrical), "UNRATED": edition(EditionUnrated), "UNCUT": edition(EditionUncut),
		"REMASTERED": edition(EditionRemastered), "REMASTER": edition(EditionRemastered), "IMAX": edition(EditionIMAX),
		"CRITERION": edition(EditionCriterion), "SE": edition(EditionSpecial), "ULTIMATE": editionLong(EditionUltimate),
		"ANNIVERSARY": editionKeep(EditionAnniversary), "FANEDIT": edition(EditionFanEdit), "REDUX": editionKeep(EditionRedux),
		"CE": edition(EditionCollectors), "OPENMATTE": edition(EditionOpenMatte),
		"EDITION": flag(fEditionWord), "VERSION": flag(fEditionWord), "CUT": flag(fEditionWord),

		// Languages. Two-letter codes are limited to those used as tags by the scene
		// and unlikely to clash with tags (no DE, IT, ES: "iT" is iTunes).
		"ENGLISH": lang(LanguageEnglish), "ENG": lang(LanguageEnglish),
		"FRENCH": lang(LanguageFrench), "TRUEFRENCH": lang(LanguageFrench), "VFF": lang(LanguageFrench), "VF": lang(LanguageFrench),
		"VF2": lang(LanguageFrench), "VFI": lang(LanguageFrench), "VOF": lang(LanguageFrench), "FR": lang(LanguageFrench),
		"FRA": lang(LanguageFrench), "FRE": lang(LanguageFrench), "VFQ": lang(LanguageFrenchCanadian),
		"GERMAN": lang(LanguageGerman), "GER": lang(LanguageGerman), "SWISSGERMAN": lang(LanguageGerman),
		"ITALIAN": lang(LanguageItalian), "ITA": lang(LanguageItalian),
		"SPANISH": lang(LanguageSpanish), "CASTELLANO": lang(LanguageSpanish), "SPA": lang(LanguageSpanish), "ESP": lang(LanguageSpanish),
		"LATINO": lang(LanguageLatino), "LATIN": lang(LanguageLatino),
		"PORTUGUESE": lang(LanguagePortuguese), "PT": lang(LanguagePortuguese), "BRAZILIAN": lang(LanguageBrazilian), "DUBLADO": lang(LanguageBrazilian),
		"DUTCH": lang(LanguageDutch), "NL": lang(LanguageDutch), "FLEMISH": lang(LanguageFlemish),
		"POLISH": lang(LanguagePolish), "PL": lang(LanguagePolish), "PLDUB": lang(LanguagePolish), "DUBPL": lang(LanguagePolish),
		"LEKPL": lang(LanguagePolish), "PLLEK": lang(LanguagePolish),
		"RUSSIAN": lang(LanguageRussian), "RUS": lang(LanguageRussian), "RU": lang(LanguageRussian),
		"UKRAINIAN": lang(LanguageUkrainian), "UKR": lang(LanguageUkrainian),
		"CZECH": lang(LanguageCzech), "CZ": lang(LanguageCzech), "SLOVAK": lang(LanguageSlovak),
		"HUNGARIAN": lang(LanguageHungarian), "HUN": lang(LanguageHungarian), "ROMANIAN": lang(LanguageRomanian),
		"BULGARIAN": lang(LanguageBulgarian), "GREEK": lang(LanguageGreek), "TURKISH": lang(LanguageTurkish),
		"DANISH": lang(LanguageDanish), "SWEDISH": lang(LanguageSwedish), "SWE": lang(LanguageSwedish),
		"NORWEGIAN": lang(LanguageNorwegian), "FINNISH": lang(LanguageFinnish), "ICELANDIC": lang(LanguageIcelandic),
		"ESTONIAN": lang(LanguageEstonian), "LITHUANIAN": lang(LanguageLithuanian), "LATVIAN": lang(LanguageLatvian),
		"NORDIC":   lang(LanguageNordic),
		"JAPANESE": lang(LanguageJapanese), "JAP": lang(LanguageJapanese), "JPN": lang(LanguageJapanese),
		"KOREAN": lang(LanguageKorean), "KOR": lang(LanguageKorean),
		"CHINESE": lang(LanguageChinese), "MANDARIN": lang(LanguageChinese), "CANTONESE": lang(LanguageChinese),
		"HINDI": lang(LanguageHindi), "TAMIL": lang(LanguageTamil), "TELUGU": lang(LanguageTelugu),
		"THAI": lang(LanguageThai), "VIETNAMESE": lang(LanguageVietnamese), "VIE": lang(LanguageVietnamese),
		"ARABIC": lang(LanguageArabic), "HEBREW": lang(LanguageHebrew), "PERSIAN": lang(LanguagePersian), "FARSI": lang(LanguagePersian),
		"CATALAN": lang(LanguageCatalan),
		"MULTI":   flag(fMulti | fStrip), "DL": flag(fMulti | fStrip), "DUAL": flag(fMulti | fStrip), "ML": flag(fMulti | fStrip),

		// Subtitles. Hardcoded: HC, HARDSUB, SUBFRENCH (French scene rule) and the
		// <language>SUB tags of the scene (KORSUB, SWESUB...). Soft: the rest.
		"HC": flag(fHardsub | fStrip), "HCSUB": flag(fHardsub | fStrip), "HCSUBS": flag(fHardsub | fStrip),
		"HARDSUB": flag(fHardsub | fStrip), "HARDSUBS": flag(fHardsub | fStrip), "HARDCODED": flag(fHardsub | fStrip),
		"SUBFRENCH": flag(fHardsub | fStrip), "KORSUB": flag(fHardsub | fStrip), "KORSUBS": flag(fHardsub | fStrip),
		"ENGSUB": flag(fHardsub | fStrip), "ENGSUBS": flag(fHardsub | fStrip), "SWESUB": flag(fHardsub | fStrip),
		"FINSUB": flag(fHardsub | fStrip), "NORSUB": flag(fHardsub | fStrip), "DANSUB": flag(fHardsub | fStrip),
		"VOSTFR": flag(fSubs | fStrip), "SUBBED": flag(fSubs | fStrip), "MULTISUBS": flag(fSubs | fStrip), "MULTISUB": flag(fSubs | fStrip),
		"SUBS": flag(fSubs | fStrip), "SUB": flag(fSubs | fStrip), "SOFTSUBS": flag(fSubs | fStrip), "ESUBS": flag(fSubs | fStrip),
		"PLSUB": flag(fSubs | fStrip), "SUBPL": flag(fSubs | fStrip), "NLSUBS": flag(fSubs | fStrip),

		// Revisions.
		"PROPER": revision(), "REPACK": revision(), "RERIP": revision(),

		// 3D.
		"3D": flag(f3D | fStrip), "HSBS": flag(f3D | fStrip), "SBS": flag(f3D | fStrip), "HOU": flag(f3D | fStrip),

		// Scene noise: recognised so that they end/strip a title, no field.
		"INTERNAL": misc(), "INT": misc(), "LIMITED": misc(), "STV": misc(), "FESTIVAL": misc(), "READNFO": misc(),
		"PAL": misc(), "NTSC": misc(), "SECAM": misc(), "WS": misc(), "FS": misc(), "DUBBED": misc(), "DUB": misc(),
		"CUSTOM": misc(), "SUBPACK": misc(), "HQ": misc(), "HYBRID": misc(),
		"2DISC": misc(), "3DISC": misc(), "4DISC": misc(), "RETAIL": misc(), "UNTOUCHED": misc(), "HD": misc(),

		// Fixes and non-movie content (see ParsedRelease.IsMovie). The fixes, BONUS and
		// EXTRAS were already stripped from titles; FEATURETTE(S) and SAMPLE are new
		// and only read after the title, so they never change it.
		"DIRFIX": fix(FixDir), "NFOFIX": fix(FixNFO), "SAMPLEFIX": fix(FixSample), "SUBFIX": fix(FixSub),
		"SYNCFIX": fix(FixSync), "PROOFFIX": fix(FixProof),
		"BONUS": flag(fExtras | fStrip), "EXTRAS": flag(fExtras | fStrip),
		"FEATURETTE": flag(fExtras), "FEATURETTES": flag(fExtras), "SAMPLE": flag(fSample),
	}
	// Streaming services: never strip (MAX, NOW, STAN are words), only read after the title.
	for _, s := range streamingNames {
		if s == "iT" || s == "iP" {
			continue // case-sensitive, handled by the matcher
		}
		m[s] = stream(s)
	}
	m["NETFLIX"] = stream("NF")
	m["DSNY"] = stream("DSNP")
	m["DSNYP"] = stream("DSNP")
	m["AMAZON"] = stream("AMZN")
	m["ITUNES"] = stream("iT")
	m["ITUNESHD"] = also(stream("iT"), sWebDL)
	return m
}

func buildCompounds2() map[[2]string]tag {
	m := map[[2]string]tag{
		{"WEB", "DL"}: src(sWebDL), {"WEB", "RIP"}: src(sWebRip), {"WEB", "DLRIP"}: src(sWebRip), {"WEB", "HD"}: src(sWebDL),
		{"WEB", "SCR"}: src(sScreener), {"WEB", "SCREENER"}: src(sScreener), {"DVD", "SCR"}: src(sScreener),
		{"DVD", "RIP"}: src(sDVD), {"DVD", "R"}: src(sDVD | sDVDR), {"BD", "RIP"}: src(sBluRay), {"BR", "RIP"}: src(sBluRay),
		{"BLU", "RAY"}: src(sBluRay), {"BD", "REMUX"}: src(sBluRay | sRemux), {"UHD", "REMUX"}: plus(src(sBluRay|sRemux), fUHD),
		{"HD", "DVD"}: src(sBluRay), {"HD", "CAM"}: src(sCam), {"HD", "TS"}: src(sTelesync), {"HD", "TC"}: src(sTelecine),
		{"CAM", "RIP"}: src(sCam), {"RAW", "HD"}: src(sHDTV | sRawHD), {"HD", "RIP"}: src(sWebRip), {"BD", "50"}: src(sBluRay | sBRDisk),
		{"BD", "25"}: src(sBluRay | sBRDisk), {"HDTV", "RIP"}: src(sHDTV),
		{"H", "264"}: enc(CodecH264), {"H", "265"}: enc(CodecH265), {"X", "264"}: enc(CodecH264), {"X", "265"}: enc(CodecH265),
		{"VC", "1"}: raw(CodecVC1), {"MPEG", "2"}: raw(CodecMPEG2),
		{"8", "BIT"}: depth(8), {"10", "BIT"}: depth(10), {"12", "BIT"}: depth(12),
		{"DOLBY", "VISION"}: dv(0), {"HDR10", "PLUS"}: hdr(HDR10Plus), {"DV", "P5"}: dv(5), {"DV", "P7"}: dv(7), {"DV", "P8"}: dv(8),
		{"DOVI", "P5"}: dv(5), {"DOVI", "P7"}: dv(7), {"DOVI", "P8"}: dv(8),
		{"E", "AC3"}: audio(AudioEAC3), {"DD", "PLUS"}: audio(AudioEAC3), {"DOLBY", "DIGITAL"}: audio(AudioAC3),
		{"DOLBY", "ATMOS"}: flag(fAtmos | fStrong), {"TRUE", "HD"}: audio(AudioTrueHD), {"HE", "AAC"}: audio(AudioAAC),
		{"DTS", "HD"}: audio(AudioDTSHD), {"DTS", "MA"}: audio(AudioDTSHDMA), {"DTS", "HRA"}: audio(AudioDTSHDHRA),
		{"DTS", "X"}: audio(AudioDTSX), {"DTS", "ES"}: audio(AudioDTS), {"DTSHD", "MA"}: audio(AudioDTSHDMA),
		{"DIRECTORS", "CUT"}: edition(EditionDirectors), {"DIRECTOR'S", "CUT"}: edition(EditionDirectors),
		{"DIRECTORS", "EDITION"}: edition(EditionDirectors), {"EXTENDED", "CUT"}: edition(EditionExtended),
		{"EXTENDED", "EDITION"}: edition(EditionExtended), {"THEATRICAL", "CUT"}: edition(EditionTheatrical),
		{"FINAL", "CUT"}: editionLong(EditionFinalCut), {"SPECIAL", "EDITION"}: editionLong(EditionSpecial),
		{"ULTIMATE", "EDITION"}: editionLong(EditionUltimate), {"ULTIMATE", "CUT"}: editionLong(EditionUltimate),
		{"ANNIVERSARY", "EDITION"}: editionKeep(EditionAnniversary), {"OPEN", "MATTE"}: editionLong(EditionOpenMatte),
		{"FAN", "EDIT"}: editionLong(EditionFanEdit), {"CRITERION", "COLLECTION"}: edition(EditionCriterion),
		{"COLLECTORS", "EDITION"}: editionLong(EditionCollectors), {"COLLECTOR'S", "EDITION"}: editionLong(EditionCollectors),
		{"IMAX", "ENHANCED"}: edition(EditionIMAX), {"IMAX", "EDITION"}: edition(EditionIMAX),
		{"4K", "REMASTER"}: edition(EditionRemastered), {"4K", "REMASTERED"}: edition(EditionRemastered),
		{"LATIN", "SPANISH"}: lang(LanguageLatino), {"PT", "BR"}: lang(LanguageBrazilian), {"PL", "DUB"}: lang(LanguagePolish),
		{"DUAL", "AUDIO"}: flag(fMulti | fStrip), {"MULTI", "AUDIO"}: flag(fMulti | fStrip),
		{"MULTI", "SUBS"}: flag(fSubs | fStrip), {"MULTI", "SUB"}: flag(fSubs | fStrip), {"SOFT", "SUBS"}: flag(fSubs | fStrip),
		{"HC", "SUBS"}: flag(fHardsub | fStrip), {"HARD", "SUB"}: flag(fHardsub | fStrip), {"HARD", "SUBS"}: flag(fHardsub | fStrip),
		{"REAL", "PROPER"}: plus(revision(), fReal), {"REAL", "REPACK"}: plus(revision(), fReal), {"REAL", "RERIP"}: plus(revision(), fReal),
		{"READ", "NFO"}: misc(), {"H", "SBS"}: flag(f3D | fStrip), {"HALF", "SBS"}: flag(f3D | fStrip), {"H", "OU"}: flag(f3D | fStrip),
		{"HALF", "OU"}: flag(f3D | fStrip), {"FULL", "SBS"}: flag(f3D | fStrip),
		{"MAKING", "OF"}: flag(fExtras), {"DIR", "FIX"}: fix(FixDir), {"NFO", "FIX"}: fix(FixNFO),
		{"SAMPLE", "FIX"}: fix(FixSample), {"SUB", "FIX"}: fix(FixSub), {"SYNC", "FIX"}: fix(FixSync),
		{"PROOF", "FIX"}: fix(FixProof),
	}
	return m
}

func buildCompounds3() map[[3]string]tag {
	return map[[3]string]tag{
		{"DTS", "HD", "MA"}: audio(AudioDTSHDMA), {"DTS", "HD", "HRA"}: audio(AudioDTSHDHRA), {"DTS", "HD", "HR"}: audio(AudioDTSHDHRA),
		{"DTS", "HR", "MA"}: audio(AudioDTSHDMA), {"WEB", "DL", "RIP"}: src(sWebRip), {"BLU", "RAY", "REMUX"}: src(sBluRay | sRemux),
		{"DIRECTOR", "S", "CUT"}: edition(EditionDirectors), {"COLLECTOR", "S", "EDITION"}: editionLong(EditionCollectors),
		{"LATIN", "AMERICAN", "SPANISH"}: lang(LanguageLatino), {"HDR", "10", "PLUS"}: hdr(HDR10Plus),
	}
}
