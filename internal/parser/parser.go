package parser

import (
	"strings"
)

// token is a word of the release name, delimited by separators.
type token struct {
	start, end int    // byte offsets in the working string
	orig       string // original text
	up         string // ASCII upper-case text, a slice of the upper-cased working string
	gap        string // separators between the previous token and this one
}

// maxTokens bounds the work done on pathological inputs; real names have < 40 tokens.
const maxTokens = 96

// Parse extracts structured information from a release or file name.
// It never panics and never fails: unknown fields keep their zero value.
func Parse(name string) ParsedRelease {
	p := ParsedRelease{Revision: Revision{Version: 1}}

	s := strings.TrimSpace(stripPath(name))
	s, p.Container = stripExtension(s)
	s = stripLeadingJunk(s)
	if isObfuscated(s) {
		return p
	}
	s, lead := stripLeadingGroup(s)
	s, trailGroup, hash := stripTrailingBrackets(s)
	p.Hash = hash
	s = stripJunkSuffixes(s)
	s, group := splitGroup(s)
	switch {
	case group != "":
		p.ReleaseGroup = group
	case trailGroup != "":
		p.ReleaseGroup = trailGroup
	default:
		p.ReleaseGroup = lead
	}

	// Scratch space lives in e, on the stack: Parse allocates only its outputs and
	// one upper-cased copy of the name (see BenchmarkParse).
	var e extractor
	e.s = s
	e.n = tokenize(s, &e.tokbuf)
	if e.n == 0 {
		return p
	}
	e.parse(&p)
	return p
}

// extractor holds per-call scratch state.
type extractor struct {
	s       string
	n       int // number of tokens
	tokbuf  [maxTokens]token
	matches [maxTokens]tag  // matches[i].n > 0 when a tag starts at token i
	cover   [maxTokens]int8 // cover[i] = start index of the match covering token i, or -1
}

func (e *extractor) parse(p *ParsedRelease) {
	toks := e.tokbuf[:e.n]
	n := len(toks)
	for i := 0; i < n; {
		t := matchAt(toks, i)
		if t.n == 0 {
			e.cover[i] = -1
			i++
			continue
		}
		e.matches[i] = t
		for k := i; k < i+int(t.n); k++ {
			e.cover[k] = int8(i)
		}
		i += int(t.n)
	}

	// The first strong tag (resolution, source, codec...) starts the technical part.
	qstart := n
	for i := 1; i < n; i++ {
		t := e.matches[i]
		if t.n == 0 || t.flags&fStrong == 0 {
			continue
		}
		if t.flags&fAmbig != 0 {
			next := i + int(t.n)
			if next >= n || e.matches[next].n == 0 {
				continue // "Charlottes.Web.2006": WEB followed by a non-tag is a word
			}
		}
		qstart = i
		break
	}

	// The year is the last year-like token before the technical part ("2001 A Space
	// Odyssey 1968", "Blade Runner 2049 2017"), never the first token ("1917 2019").
	// Without one, the first year-like token after it ("...BDRip.x264.2001").
	yearIdx := -1
	for i := 1; i < qstart; i++ {
		if e.cover[i] < 0 && isYear(toks[i].up) {
			yearIdx = i
		}
	}
	if yearIdx < 0 {
		for i := max(qstart, 1); i < n; i++ {
			if e.cover[i] < 0 && isYear(toks[i].up) {
				yearIdx = i
				break
			}
		}
	}
	if yearIdx >= 0 {
		p.Year = atoi4(toks[yearIdx].up)
	}

	titleEnd := qstart
	if yearIdx >= 0 && yearIdx < qstart {
		titleEnd = yearIdx
	}
	// Strip weak tags glued to the end of the title ("Inception.German.2010").
	for titleEnd > 1 {
		c := int(e.cover[titleEnd-1])
		if c < 1 {
			break
		}
		f := e.matches[c].flags
		if f&fStrip != 0 {
			titleEnd = c
			continue
		}
		if f&fStripLong == 0 || c < 2 {
			break
		}
		titleEnd = c
		if toks[c-1].up == "THE" && c-1 >= 2 { // "Terminator.2.Judgment.Day.The.Ultimate.Edition"
			titleEnd = c - 1
		}
	}
	e.setTitle(p, titleEnd)
	e.extract(p, titleEnd, yearIdx)
}

// setTitle builds Title, AlternateTitles and CleanTitle from toks[:end].
func (e *extractor) setTitle(p *ParsedRelease, end int) {
	if end <= 0 {
		return
	}
	segStart := 0
	for i := 1; i < end; i++ {
		if akaLen := e.akaAt(i, end); akaLen > 0 {
			e.appendTitle(p, segStart, i)
			segStart = i + akaLen
			i += akaLen - 1
		}
	}
	e.appendTitle(p, segStart, end)
	p.CleanTitle = CleanTitle(p.Title)
}

// akaAt reports the number of tokens of an "AKA" / "a.k.a" marker at i, if any
// title token follows it.
func (e *extractor) akaAt(i, end int) int {
	t := e.tokbuf[:e.n]
	if t[i].up == "AKA" && i+1 < end {
		return 1
	}
	if i+3 < end && t[i].up == "A" && t[i+1].up == "K" && t[i+2].up == "A" && t[i+1].gap == "." && t[i+2].gap == "." {
		return 3
	}
	return 0
}

func (e *extractor) appendTitle(p *ParsedRelease, from, to int) {
	if from >= to {
		return
	}
	var sb strings.Builder
	sb.Grow(e.tokbuf[to-1].end - e.tokbuf[from].start)
	for i := from; i < to; i++ {
		t := e.tokbuf[i]
		if i > from {
			sb.WriteString(joinGap(e.tokbuf[i-1], t, e.s))
		}
		sb.WriteString(e.s[t.start:t.end])
	}
	if p.Title == "" {
		p.Title = sb.String()
		return
	}
	p.AlternateTitles = append(p.AlternateTitles, sb.String())
}

// joinGap renders the separators between two title tokens: hyphens inside words
// and dotted acronyms (U.N.C.L.E) are kept, everything else becomes one space.
func joinGap(prev, cur token, s string) string {
	switch cur.gap {
	case "-":
		return "-"
	case " - ":
		return " - "
	case ". ":
		return ". "
	case ".":
		if prev.end-prev.start == 1 && cur.end-cur.start == 1 && isAlnum(s[prev.start]) && isAlnum(s[cur.start]) {
			return "."
		}
	}
	return " "
}

// extract applies every tag found after the title.
func (e *extractor) extract(p *ParsedRelease, from, yearIdx int) {
	var (
		srcs        srcBits
		flags       tagFlags
		hasEncode   bool
		hasRaw      bool
		res         Resolution
		revInc      int
		explicitVer int
		runs        [4][2]int
		nRuns       int
		runStart    = -1
		runEnd      = -1
		unkStart    = -1
		unkEnd      = -1
	)
	flushRun := func() {
		if runStart >= 0 && nRuns < len(runs) {
			runs[nRuns] = [2]int{runStart, runEnd}
			nRuns++
		}
		runStart, runEnd = -1, -1
	}
	n := e.n
	for i := from; i < n; {
		if i == yearIdx {
			i++
			continue
		}
		t := e.matches[i]
		if t.n == 0 {
			if unkStart < 0 || unkEnd != i-1 {
				unkStart = i
			}
			unkEnd = i
			i++
			continue
		}
		last := i + int(t.n) - 1
		raw := e.s[e.tokbuf[i].start:e.tokbuf[last].end]

		if t.ed != 0 || t.flags&fEditionWord != 0 {
			start := i
			if t.flags&fEditionWord != 0 && unkStart >= 0 && unkEnd == i-1 {
				start = unkStart
			}
			switch {
			case runStart >= 0 && runEnd >= start-1:
				runEnd = last
			case t.ed != 0 || start < i:
				flushRun()
				runStart, runEnd = start, last
			}
			p.Edition |= t.ed
		}
		unkStart, unkEnd = -1, -1

		srcs |= t.src
		flags |= t.flags
		if t.res != ResolutionUnknown && res == ResolutionUnknown {
			res = t.res
		}
		if t.codec != CodecUnknown {
			if p.VideoCodec == CodecUnknown {
				p.VideoCodec = t.codec
			}
			hasEncode = hasEncode || t.flags&fEncode != 0
			hasRaw = hasRaw || t.flags&fRawCodec != 0
		}
		if t.depth != 0 && p.BitDepth == 0 {
			p.BitDepth = int(t.depth)
		}
		if t.hdr != 0 {
			p.HDR = appendUnique(p.HDR, t.hdr)
		}
		if t.dvProf != 0 && p.DolbyVisionProfile == 0 {
			p.DolbyVisionProfile = int(t.dvProf)
		}
		if t.audio != 0 {
			p.Audio = appendUnique(p.Audio, t.audio)
		}
		if t.ch != 0 && t.chMin != 0 && p.Channels == "" {
			p.Channels = string([]byte{t.ch, '.', t.chMin})
		}
		if t.lang != 0 {
			p.Languages = appendUnique(p.Languages, t.lang)
		}
		if t.stream != 0 && p.Streaming == "" {
			p.Streaming = streamingNames[t.stream-1]
		}
		if t.flags&fHardsub != 0 && !p.HardcodedSubs {
			p.HardcodedSubs = true
			p.HardcodedSubsTag = raw
		}
		if t.flags&fSubs != 0 {
			p.Subs = appendWord(p.Subs, raw)
		}
		revInc += int(t.rev)
		if int(t.ver) > explicitVer {
			explicitVer = int(t.ver)
		}
		i = last + 1
	}
	flushRun()

	p.Atmos = flags&fAtmos != 0
	p.Multi = flags&fMulti != 0
	p.ThreeD = flags&f3D != 0
	p.Revision.Real = flags&fReal != 0
	p.Revision.Version = max(1+revInc, explicitVer)
	if flags&fISO != 0 && p.Container == "" {
		p.Container = "iso"
	}

	if res == ResolutionUnknown && flags&(fUHD|f4K) != 0 {
		res = Resolution2160p
	}
	p.Resolution = res
	p.Source, p.Modifier = resolveSource(srcs, flags, hasEncode, hasRaw, p)

	for r := range nRuns {
		p.EditionText = appendWord(p.EditionText, e.s[e.tokbuf[runs[r][0]].start:e.tokbuf[runs[r][1]].end])
	}
}

// resolveSource turns the accumulated evidence into a Source and a Modifier.
func resolveSource(s srcBits, flags tagFlags, hasEncode, hasRaw bool, p *ParsedRelease) (Source, Modifier) {
	var src Source
	switch {
	case s&sCam != 0:
		src = SourceCam
	case s&sTelesync != 0:
		src = SourceTelesync
	case s&sTelecine != 0:
		src = SourceTelecine
	case s&sScreener != 0:
		src = SourceScreener
	case s&(sBluRay|sRemux|sBRDisk) != 0:
		src = SourceBluRay
	case s&sWebRip != 0:
		src = SourceWebRip
	case s&sWebDL != 0:
		src = SourceWebDL
	case s&sHDTV != 0:
		src = SourceHDTV
	case s&sDVD != 0:
		src = SourceDVD
	case s&sTelesyncAmbig != 0:
		src = SourceTelesync
	case p.Streaming != "":
		src = SourceWebDL
	}

	mod := ModifierNone
	switch src {
	case SourceBluRay:
		isoOrComplete := p.Container == "iso" || (flags&fComplete != 0 && !hasEncode)
		switch {
		case s&sRemux != 0:
			mod = ModifierRemux
		case s&sBRDisk != 0 || isoOrComplete:
			mod = ModifierBRDisk
		case hasRaw && !hasEncode:
			// "BluRay.AVC" is a remux in German scene naming and a full disc elsewhere.
			if hasLanguage(p.Languages, LanguageGerman) {
				mod = ModifierRemux
			} else {
				mod = ModifierBRDisk
			}
		}
	case SourceDVD:
		switch {
		case s&sDVDR != 0 || p.Container == "iso":
			mod = ModifierDVDR
		case s&sRegional != 0:
			mod = ModifierRegional
		}
	case SourceHDTV:
		if s&sRawHD != 0 {
			mod = ModifierRawHD
		}
	}
	return src, mod
}

// matchAt returns the tag starting at token i (n == 0 when none).
func matchAt(toks []token, i int) tag {
	a := toks[i].up
	n := len(toks)
	if _, ok := compoundFirst[a]; ok {
		if i+2 < n {
			if t, ok := compounds3[[3]string{a, toks[i+1].up, toks[i+2].up}]; ok {
				t.n = 3
				return t
			}
			if base, d, ok := splitChannelDigit(toks[i+2].up); ok {
				if t, ok := compounds3[[3]string{a, toks[i+1].up, base}]; ok && isAudioTag(t) {
					return gluedChannels(toks, i+2, t, d, 2)
				}
			}
		}
		if i+1 < n {
			if t, ok := compounds2[[2]string{a, toks[i+1].up}]; ok {
				t.n = 2
				return t
			}
			if base, d, ok := splitChannelDigit(toks[i+1].up); ok {
				if t, ok := compounds2[[2]string{a, base}]; ok && isAudioTag(t) {
					return gluedChannels(toks, i+1, t, d, 1)
				}
			}
		}
	}
	if a == "IT" || a == "IP" {
		// iTunes and BBC iPlayer are only recognised with their exact casing.
		if o := toks[i].orig; o == "iT" || o == "iP" {
			return tag{stream: streamIdx(o), n: 1}
		}
		return tag{}
	}
	if t, ok := singles[a]; ok {
		t.n = 1
		if t.lang != 0 && i+1 < n {
			// "GERMAN.SUBBED", "PT.SUBS": subtitles in that language, not the audio.
			switch toks[i+1].up {
			case "SUBBED", "SUBS", "SUB":
				return tag{flags: fSubs | fStrip, n: 2}
			}
		}
		return channelsAfter(toks, i, t)
	}
	if base, d, ok := splitChannelDigit(a); ok {
		if t, ok := singles[base]; ok && isAudioTag(t) {
			return gluedChannels(toks, i, t, d, 0)
		}
	}
	return dynamicTag(toks, i)
}

// splitChannelDigit splits "DDP5" into "DDP" and '5': a channel count glued to
// an audio tag ("DDP5.1", "AAC2.0", "MA5.1").
func splitChannelDigit(u string) (base string, d byte, ok bool) {
	if len(u) < 2 {
		return "", 0, false
	}
	d = u[len(u)-1]
	if !isChannelMajor(d) || isDigit(u[len(u)-2]) {
		return "", 0, false
	}
	return u[:len(u)-1], d, true
}

func isAudioTag(t tag) bool { return t.audio != 0 || t.flags&fAtmos != 0 }

// gluedChannels completes an audio tag whose last token (at i) carried the channel
// major digit d; the minor digit is the next token. prior is the number of tokens
// of the tag before i.
func gluedChannels(toks []token, i int, t tag, d byte, prior uint8) tag {
	t.n = prior + 1
	if i+1 < len(toks) && isChannelMinorTok(toks[i+1]) {
		t.ch, t.chMin = d, toks[i+1].up[0]
		t.n++
	}
	return t
}

// channelsAfter extends an audio tag with a following "5.1"-style channel layout.
func channelsAfter(toks []token, i int, t tag) tag {
	if t.audio == 0 && t.flags&fAtmos == 0 {
		return t
	}
	j := i + int(t.n)
	if j+1 < len(toks) && isChannelMajorTok(toks[j]) && isChannelMinorTok(toks[j+1]) {
		t.ch, t.chMin = toks[j].up[0], toks[j+1].up[0]
		t.n += 2
	}
	return t
}

// dynamicTag matches tag families that are easier to test than to enumerate.
func dynamicTag(toks []token, i int) tag {
	u := toks[i].up
	n := len(toks)
	switch {
	case isChannelMajorTok(toks[i]) && i+1 < n && isChannelMinorTok(toks[i+1]):
		return tag{flags: fChannels | fStrong, ch: u[0], chMin: toks[i+1].up[0], n: 2}
	case len(u) >= 2 && u[0] == 'V' && allDigits(u[1:]) && len(u) <= 3:
		return tag{ver: uint8(atoiSmall(u[1:])), flags: fStrip, n: 1}
	case isOrdinal(u) && i+1 < n && toks[i+1].up == "ANNIVERSARY":
		t := editionKeep(EditionAnniversary)
		t.n = 2
		if i+2 < n && toks[i+2].up == "EDITION" {
			t.n = 3
		}
		return t
	case isOrdinal(u) && i+2 < n && toks[i+1].up == "A" && toks[i+2].up == "E":
		t := editionKeep(EditionAnniversary) // "25th.A.E": anniversary edition
		t.n = 3
		return t
	}
	for _, r := range [...]string{"PROPER", "REPACK", "RERIP"} {
		if len(u) == len(r)+1 && strings.HasPrefix(u, r) && isDigit(u[len(r)]) {
			return tag{rev: u[len(r)] - '0', flags: fStrip, n: 1}
		}
	}
	if (strings.HasPrefix(u, "DISC") || strings.HasPrefix(u, "CD")) && len(u) <= 6 && isDigit(u[len(u)-1]) {
		return tag{flags: fStrip, n: 1}
	}
	return tag{}
}

func isChannelMajor(b byte) bool { return b == '1' || b == '2' || b == '5' || b == '6' || b == '7' }

func isChannelMajorTok(t token) bool { return len(t.up) == 1 && isChannelMajor(t.up[0]) }

func isChannelMinorTok(t token) bool {
	return len(t.up) == 1 && (t.up[0] == '0' || t.up[0] == '1') && (t.gap == "." || t.gap == " ")
}

func isOrdinal(u string) bool {
	if len(u) < 3 {
		return false
	}
	suf := u[len(u)-2:]
	if suf != "TH" && suf != "ST" && suf != "ND" && suf != "RD" {
		return false
	}
	return allDigits(u[:len(u)-2])
}
