package parser

import (
	"strings"
)

// This file holds the pre-tokenisation passes: path, extension, junk and group
// stripping, plus the tokenizer and small byte helpers. All functions return
// sub-slices of their input and do not allocate unless stated.

// stripPath keeps the last path element of a Unix or Windows path.
func stripPath(s string) string {
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		return s[i+1:]
	}
	return s
}

// containerExts are stripped from the end of the name and reported as Container.
var containerExts = []string{"mkv", "mp4", "avi", "iso", "m2ts", "wmv", "m4v", "mov", "mpg", "mpeg", "webm", "ts"}

// noiseExts are stripped without being reported.
var noiseExts = []string{"nzb", "torrent", "par2", "nfo", "sfv"}

// stripExtension removes a known file extension. ".ts" is only an extension when
// written in lower case after a group ("-GRP.ts"); "TS" otherwise means telesync.
func stripExtension(s string) (rest, container string) {
	i := strings.LastIndexByte(s, '.')
	if i <= 0 || len(s)-i > 6 {
		return s, ""
	}
	ext := s[i+1:]
	for _, e := range noiseExts {
		if strings.EqualFold(ext, e) {
			return stripExtension(s[:i])
		}
	}
	for _, e := range containerExts {
		if !strings.EqualFold(ext, e) {
			continue
		}
		if e == "ts" && (ext != "ts" || !strings.Contains(s[:i], "-")) {
			return s, ""
		}
		return s[:i], e
	}
	return s, ""
}

// stripLeadingJunk removes tracker prefixes such as "www.site.org - ".
func stripLeadingJunk(s string) string {
	if len(s) > 4 && strings.EqualFold(s[:4], "www.") {
		if i := strings.Index(s, " - "); i > 0 {
			return strings.TrimSpace(s[i+3:])
		}
	}
	return s
}

// isObfuscated detects Usenet obfuscated names (random hex or base62 strings) so
// that they yield an empty release instead of a garbage title.
func isObfuscated(s string) bool {
	if len(s) < 12 {
		return false
	}
	var hasDigit, hasLetter, hasSep, allHex bool
	allHex = true
	transitions := 0
	prevDigit := isDigit(s[0])
	for i := range len(s) {
		c := s[i]
		d := isDigit(c)
		switch {
		case d:
			hasDigit = true
		case isLetter(c):
			hasLetter = true
		case c != '-':
			hasSep = true
		}
		if !d && !isHexLetter(c) && c != '-' {
			allHex = false
		}
		if i > 0 && (d || isLetter(c)) && d != prevDigit {
			transitions++
		}
		if d || isLetter(c) {
			prevDigit = d
		}
	}
	if hasSep || !hasDigit {
		return false
	}
	if allHex && len(s) >= 16 {
		return true
	}
	return hasLetter && !strings.Contains(s, "-") && transitions >= 4
}

// stripLeadingGroup removes an anime-style leading "[Group]" and returns it.
func stripLeadingGroup(s string) (rest, group string) {
	if len(s) < 3 || s[0] != '[' {
		return s, ""
	}
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return s, ""
	}
	content := strings.TrimSpace(s[1:end])
	rest = strings.TrimLeft(s[end+1:], " ._-")
	if rest == "" {
		return s, ""
	}
	switch {
	case isHash(content), isJunkSite(content):
		return rest, ""
	case content != "" && !containsTag(content):
		return rest, content
	}
	return s, ""
}

// stripTrailingBrackets removes trailing "[...]" blocks that hold a CRC hash, a
// tracker tag ([rarbg]) or a group ([YTS.MX]). It stops at the first block holding
// release tags ([1080p]).
func stripTrailingBrackets(s string) (rest, group, hash string) {
	for {
		s = strings.TrimRight(s, " ._-")
		if !strings.HasSuffix(s, "]") {
			return s, group, hash
		}
		open := strings.LastIndexByte(s, '[')
		if open < 0 {
			return s, group, hash
		}
		content := strings.TrimSpace(s[open+1 : len(s)-1])
		switch {
		case isHash(content):
			if hash == "" {
				hash = content
			}
		case isJunkSite(content):
		case group == "" && looksLikeGroup(content):
			group = content
		default:
			return s, group, hash
		}
		s = s[:open]
	}
}

// junkSuffixes are appended by indexers or posting tools after the real group.
var junkSuffixes = []string{
	"obfuscated", "scrambled", "asrequested", "alternativetorequested", "postbot", "xpost", "rakuten",
	"whiterev", "buymore", "chamele0n", "nzbgeek", "rp", "sample",
}

// stripJunkSuffixes removes "-Obfuscated", "-postbot"... (repeatedly).
func stripJunkSuffixes(s string) string {
	for {
		s = strings.TrimRight(s, " ._")
		i := strings.LastIndexByte(s, '-')
		if i <= 0 {
			return s
		}
		suffix := s[i+1:]
		found := false
		for _, j := range junkSuffixes {
			if strings.EqualFold(suffix, j) {
				found = true
				break
			}
		}
		if !found {
			return s
		}
		s = s[:i]
	}
}

// junkSites are tracker or indexer tags found in brackets.
var junkSites = []string{"rarbg", "eztv", "ettv", "tgx", "rartv", "ethd", "publichd", "1337x", "eztv.re", "rarbg.com", "ion10"}

func isJunkSite(c string) bool {
	if len(c) > 4 && strings.EqualFold(c[:4], "www.") {
		return true
	}
	for _, j := range junkSites {
		if strings.EqualFold(c, j) {
			return true
		}
	}
	for _, tld := range [...]string{".com", ".org", ".net", ".to", ".cc", ".io"} {
		if len(c) > len(tld) && strings.EqualFold(c[len(c)-len(tld):], tld) {
			return true
		}
	}
	return false
}

// isHash reports whether c is an 8-digit hexadecimal CRC32.
func isHash(c string) bool {
	if len(c) != 8 {
		return false
	}
	for i := range 8 {
		if !isDigit(c[i]) && !isHexLetter(c[i]) {
			return false
		}
	}
	return true
}

// looksLikeGroup accepts a bracketed group name such as "YTS.MX" or "HDO".
func looksLikeGroup(c string) bool {
	if c == "" || len(c) > 32 || strings.ContainsAny(c, " ,+[]()") {
		return false
	}
	hasLetter := false
	for i := range len(c) {
		if isLetter(c[i]) {
			hasLetter = true
			break
		}
	}
	return hasLetter && !containsTag(c)
}

// containsTag reports whether any word of c is a known release tag or a year.
func containsTag(c string) bool {
	var buf [64]byte
	start := 0
	for i := 0; i <= len(c); i++ {
		if i < len(c) && !isSep(c[i]) {
			continue
		}
		w := c[start:i]
		start = i + 1
		if w == "" || len(w) > len(buf) {
			continue
		}
		up := buf[:len(w)]
		for k := range len(w) {
			up[k] = upper(w[k])
		}
		if _, ok := singles[string(up)]; ok {
			return true
		}
		if _, ok := compoundFirst[string(up)]; ok {
			return true
		}
		if isYear(string(up)) || allDigits(string(up)) {
			return true
		}
	}
	return false
}

// splitGroup splits "Name-GROUP" into "Name" and "GROUP". The group is the text
// after the last hyphen outside brackets, when it is a single word that is not a
// release tag (WEB-DL, DTS-HD) and not glued with spaces (" - " is a title dash).
func splitGroup(s string) (rest, group string) {
	s = strings.TrimRight(s, " ._-")
	idx, depth := -1, 0
	for i := range len(s) {
		switch s[i] {
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			if depth > 0 {
				depth--
			}
		case '-':
			if depth == 0 {
				idx = i
			}
		}
	}
	if idx <= 0 || idx == len(s)-1 || s[idx-1] == ' ' {
		return s, ""
	}
	cand := s[idx+1:]
	if strings.ContainsAny(cand, " .[](){}") || allDigits(cand) || isGroupTail(cand) {
		return s, ""
	}
	// Reject compound tags split by the hyphen ("Blu-ray", "DTS-X").
	ws := idx
	for ws > 0 && !isSep(s[ws-1]) {
		ws--
	}
	var a, b [32]byte
	if prev := s[ws:idx]; prev != "" && len(prev) <= len(a) && len(cand) <= len(b) {
		for k := range len(prev) {
			a[k] = upper(prev[k])
		}
		for k := range len(cand) {
			b[k] = upper(cand[k])
		}
		if _, ok := compounds2[[2]string{string(a[:len(prev)]), string(b[:len(cand)])}]; ok {
			return s, ""
		}
	}
	return s[:idx], cand
}

// groupTails are tags that follow a hyphen inside a tag (WEB-DL, DTS-HD, Dual-Audio)
// and therefore cannot be a group. Any other word is accepted, even one that is
// also a weak tag: "-UNTOUCHED", "-UNRATED" and "-BluRay3D" are real group names.
var groupTails = []string{
	"DL", "HD", "X", "MA", "HRA", "RIP", "RAY", "R", "AUDIO", "SUBS", "SUB", "DUB", "BR", "ES", "TS", "TC", "CAM",
	"SBS", "OU", "DLRIP", "SCR", "SCREENER", "BIT", "AAC", "AC3", "DTS", "REMUX", "BRRIP", "BDRIP", "DVDRIP", "WEBRIP",
}

// isGroupTail reports whether cand is a tag fragment or a resolution rather than a group.
func isGroupTail(cand string) bool {
	for _, t := range groupTails {
		if strings.EqualFold(cand, t) {
			return true
		}
	}
	if len(cand) > 16 {
		return false
	}
	var buf [16]byte
	for k := range len(cand) {
		buf[k] = upper(cand[k])
	}
	t, ok := singles[string(buf[:len(cand)])]
	return ok && (t.res != ResolutionUnknown || t.codec != CodecUnknown)
}

// tokenize splits s into at most maxTokens words stored in buf and returns their
// count. Tokens share memory with s and with one upper-cased copy of s (the only
// allocation).
func tokenize(s string, buf *[maxTokens]token) int {
	if s == "" {
		return 0
	}
	u := asciiUpper(s)
	toks := buf[:0]
	prevEnd := 0
	for i := 0; i < len(s) && len(toks) < maxTokens; {
		if isSep(s[i]) {
			i++
			continue
		}
		start := i
		for i < len(s) && !isSep(s[i]) {
			i++
		}
		toks = append(toks, token{start: start, end: i, orig: s[start:i], up: u[start:i], gap: s[prevEnd:start]})
		prevEnd = i
	}
	return len(toks)
}

func isSep(c byte) bool {
	switch c {
	case '.', ' ', '_', '-', '[', ']', '(', ')', '{', '}', ',', '/', '|', '\t', '~', '*', ';':
		return true
	}
	return false
}

// asciiUpper upper-cases ASCII letters only, so byte offsets are preserved.
func asciiUpper(s string) string {
	b := make([]byte, len(s))
	for i := range len(s) {
		b[i] = upper(s[i])
	}
	return string(b)
}

func upper(c byte) byte {
	if 'a' <= c && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}

func isDigit(c byte) bool     { return '0' <= c && c <= '9' }
func isLetter(c byte) bool    { return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') }
func isAlnum(c byte) bool     { return isDigit(c) || isLetter(c) }
func isHexLetter(c byte) bool { return ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F') }

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// isYear reports whether u is a four-digit year in 1900..2100.
func isYear(u string) bool {
	if len(u) != 4 || !allDigits(u) {
		return false
	}
	y := atoi4(u)
	return y >= 1900 && y <= 2100
}

func atoi4(u string) int {
	return int(u[0]-'0')*1000 + int(u[1]-'0')*100 + int(u[2]-'0')*10 + int(u[3]-'0')
}

// atoiSmall parses at most two digits.
func atoiSmall(u string) int {
	n := 0
	for i := range min(len(u), 2) {
		n = n*10 + int(u[i]-'0')
	}
	return n
}

func appendUnique[T comparable](s []T, v T) []T {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func appendWord(dst, w string) string {
	if dst == "" {
		return w
	}
	return dst + " " + w
}

func hasLanguage(ls []Language, l Language) bool {
	for _, x := range ls {
		if x == l {
			return true
		}
	}
	return false
}
