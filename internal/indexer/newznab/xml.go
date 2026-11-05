package newznab

import (
	"bytes"
	"encoding/base32"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/indexer"
)

// Bounds applied while decoding, on top of the response body size limit.
const (
	maxItems        = 5000 // items kept from one feed
	maxAttrsPerItem = 256  // extended attributes read per item
	maxTextLen      = 2048 // longest title, GUID or URL kept
	maxCategories   = 512  // categories read from capabilities
)

// Page is one decoded search or RSS response.
type Page struct {
	Releases []indexer.Release
	// Count is the number of items in the response, including the skipped ones;
	// it drives pagination.
	Count int
	// Skipped counts items dropped for lack of a title or a download URL.
	Skipped int
	Offset  int
	// Total is the number of results announced by newznab:response, or -1.
	Total int
}

type xmlGUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr"`
}

type xmlEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// xmlAttr matches both newznab:attr and torznab:attr: the namespace is ignored
// on purpose, since some indexers declare it wrongly.
type xmlAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlItem struct {
	Title      string         `xml:"title"`
	GUID       xmlGUID        `xml:"guid"`
	Link       string         `xml:"link"`
	Comments   string         `xml:"comments"`
	PubDate    string         `xml:"pubDate"`
	Size       string         `xml:"size"`
	Categories []string       `xml:"category"`
	Enclosures []xmlEnclosure `xml:"enclosure"`
	Attrs      []xmlAttr      `xml:"attr"`
}

type xmlResponse struct {
	Offset string `xml:"offset,attr"`
	Total  string `xml:"total,attr"`
}

type xmlRSS struct {
	Channel struct {
		Response *xmlResponse `xml:"response"`
		Items    []xmlItem    `xml:"item"`
	} `xml:"channel"`
}

type xmlMode struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

type xmlCategory struct {
	ID      string        `xml:"id,attr"`
	Name    string        `xml:"name,attr"`
	Subcats []xmlCategory `xml:"subcat"`
}

type xmlCaps struct {
	Server struct {
		Version string `xml:"version,attr"`
		Title   string `xml:"title,attr"`
	} `xml:"server"`
	Limits struct {
		Max     string `xml:"max,attr"`
		Default string `xml:"default,attr"`
	} `xml:"limits"`
	Retention struct {
		Days string `xml:"days,attr"`
	} `xml:"retention"`
	Searching struct {
		Search      *xmlMode `xml:"search"`
		MovieSearch *xmlMode `xml:"movie-search"`
	} `xml:"searching"`
	Categories []xmlCategory `xml:"categories>category"`
	Tags       []struct {
		Name string `xml:"name,attr"`
	} `xml:"tags>tag"`
}

// newDecoder returns a decoder that accepts HTML named entities (a static
// table: no external entity is ever resolved) and Latin-1 encoded documents.
// encoding/xml does not process DTDs, so entity expansion attacks do not apply.
func newDecoder(body []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(body))
	d.Entity = xml.HTMLEntity
	d.CharsetReader = charsetReader
	return d
}

// charsetReader converts the few non-UTF-8 encodings seen on indexers.
func charsetReader(label string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return r, nil
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(b)+len(b)/8)
		for _, c := range b {
			out = utf8.AppendRune(out, rune(c))
		}
		return bytes.NewReader(out), nil
	}
	return nil, fmt.Errorf("unsupported encoding %q", label)
}

// root returns the first start element of the document.
func root(d *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se, nil
		}
	}
}

// notIndexer builds the error for a document that is not a Newznab answer.
func notIndexer(body []byte, err error) error {
	msg := "the response is not XML"
	if looksLikeHTML(body) {
		msg = "the response is an HTML page"
	}
	return &indexer.Error{Kind: indexer.ErrNotIndexer, Message: msg, Err: err}
}

func looksLikeHTML(body []byte) bool {
	head := bytes.ToLower(body[:min(len(body), 512)])
	return bytes.Contains(head, []byte("<html")) || bytes.Contains(head, []byte("<!doctype html"))
}

// parseError decodes a Newznab <error code=".." description=".."/> element into
// a typed error.
func parseError(se *xml.StartElement) error {
	var code int
	var desc string
	for _, a := range se.Attr {
		switch a.Name.Local {
		case "code":
			code, _ = strconv.Atoi(strings.TrimSpace(a.Value))
		case "description":
			desc = clip(strings.TrimSpace(a.Value), 300)
		}
	}
	return errorForCode(code, desc)
}

// errorForCode maps a Newznab error code (and description, for indexers that
// signal limits with a generic code) to a typed error.
func errorForCode(code int, desc string) *indexer.Error {
	kind := indexer.ErrServer
	low := strings.ToLower(desc)
	switch {
	case code == 429 || strings.Contains(low, "limit reached") || strings.Contains(low, "request limit"):
		kind = indexer.ErrRateLimited
	case code == 100:
		kind = indexer.ErrUnauthorized
	case code == 101:
		kind = indexer.ErrSuspended
	case code == 102:
		kind = indexer.ErrForbidden
	case code >= 103 && code <= 199:
		kind = indexer.ErrUnauthorized
	case code == 200 || code == 201:
		kind = indexer.ErrBadRequest
	case code == 202 || code == 203:
		kind = indexer.ErrUnsupported
	case code == 910:
		kind = indexer.ErrDisabled
	}
	return &indexer.Error{Kind: kind, Code: code, Message: desc}
}

// ParseFeed decodes a search or RSS response. Releases are stamped with info.
// A Newznab <error> answer is returned as an *indexer.Error.
func ParseFeed(body []byte, info indexer.Info) (Page, error) {
	d := newDecoder(body)
	se, err := root(d)
	if err != nil {
		return Page{}, notIndexer(body, err)
	}
	switch se.Name.Local {
	case "error":
		return Page{}, parseError(&se)
	case "rss":
	default:
		return Page{}, notIndexer(body, fmt.Errorf("unexpected root element <%s>", se.Name.Local))
	}
	var doc xmlRSS
	if err := d.DecodeElement(&doc, &se); err != nil {
		return Page{}, &indexer.Error{Kind: indexer.ErrBadResponse, Message: "malformed RSS document", Err: err}
	}
	items := doc.Channel.Items
	if len(items) > maxItems {
		items = items[:maxItems]
	}
	p := Page{Count: len(doc.Channel.Items), Total: -1, Releases: make([]indexer.Release, 0, len(items))}
	if r := doc.Channel.Response; r != nil {
		p.Offset = atoiNonNeg(r.Offset, 0)
		p.Total = atoiNonNeg(r.Total, -1)
	}
	for i := range items {
		rel, ok := toRelease(&items[i], info)
		if !ok {
			p.Skipped++
			continue
		}
		p.Releases = append(p.Releases, rel)
	}
	return p, nil
}

// ParseCaps decodes a t=caps response.
func ParseCaps(body []byte) (indexer.Caps, error) {
	d := newDecoder(body)
	se, err := root(d)
	if err != nil {
		return indexer.Caps{}, notIndexer(body, err)
	}
	switch se.Name.Local {
	case "error":
		return indexer.Caps{}, parseError(&se)
	case "caps":
	default:
		return indexer.Caps{}, notIndexer(body, fmt.Errorf("unexpected root element <%s>", se.Name.Local))
	}
	var x xmlCaps
	if err := d.DecodeElement(&x, &se); err != nil {
		return indexer.Caps{}, &indexer.Error{Kind: indexer.ErrBadResponse, Message: "malformed capabilities document", Err: err}
	}
	c := indexer.Caps{
		ServerTitle:   clip(strings.TrimSpace(x.Server.Title), 200),
		Version:       clip(strings.TrimSpace(x.Server.Version), 50),
		LimitMax:      atoiNonNeg(x.Limits.Max, 0),
		LimitDefault:  atoiNonNeg(x.Limits.Default, 0),
		RetentionDays: atoiNonNeg(x.Retention.Days, 0),
		// Defaults from the spec (revision 1.1) when supportedParams is absent.
		Search:      mode(x.Searching.Search, "q", true),
		MovieSearch: mode(x.Searching.MovieSearch, "q,imdbid", false),
	}
	n := 0 // categories kept, parents and subcategories
	for i := range x.Categories {
		if n >= maxCategories {
			break
		}
		cat, ok := category(&x.Categories[i])
		if !ok {
			continue
		}
		n++
		for j := range x.Categories[i].Subcats {
			if n >= maxCategories {
				break
			}
			if s, ok := category(&x.Categories[i].Subcats[j]); ok {
				cat.Subcats = append(cat.Subcats, s)
				n++
			}
		}
		c.Categories = append(c.Categories, cat)
	}
	for _, t := range x.Tags {
		if name := strings.ToLower(strings.TrimSpace(t.Name)); name != "" && len(c.Tags) < 100 {
			c.Tags = append(c.Tags, clip(name, 50))
		}
	}
	return c, nil
}

// mode decodes a searching element. A missing element is unavailable, except
// for search, which every Newznab server must support (absent defaults to
// available when absentAvailable).
func mode(m *xmlMode, defaultParams string, absentAvailable bool) indexer.SearchMode {
	if m == nil {
		if !absentAvailable {
			return indexer.SearchMode{}
		}
		return indexer.SearchMode{Available: true, Params: splitParams(defaultParams)}
	}
	params := m.SupportedParams
	if strings.TrimSpace(params) == "" {
		params = defaultParams
	}
	return indexer.SearchMode{Available: yes(m.Available), Params: splitParams(params)}
}

func splitParams(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && len(p) <= 32 && !slices.Contains(out, p) && len(out) < 64 {
			out = append(out, p)
		}
	}
	return out
}

func yes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "true", "1":
		return true
	}
	return false
}

func category(x *xmlCategory) (indexer.Category, bool) {
	id, err := strconv.Atoi(strings.TrimSpace(x.ID))
	if err != nil || id < 0 {
		return indexer.Category{}, false
	}
	return indexer.Category{ID: id, Name: clip(strings.TrimSpace(x.Name), 100)}, true
}

// toRelease maps an RSS item. ok is false when the item has no title or no
// download URL.
func toRelease(it *xmlItem, info indexer.Info) (indexer.Release, bool) {
	r := indexer.Release{
		Title:                cleanText(it.Title),
		IndexerID:            info.ID,
		IndexerName:          info.Name,
		IndexerPriority:      info.Priority,
		Protocol:             info.Protocol,
		Seeders:              -1,
		Leechers:             -1,
		Grabs:                -1,
		DownloadVolumeFactor: 1,
		UploadVolumeFactor:   1,
	}
	if r.Title == "" {
		return r, false
	}
	var (
		peers               = -1
		usenetDate          time.Time
		attrGUID, enclosure string
		enclosureLen        int64
		haveDVF, haveUVF    bool
		dvf, uvf            float64
		haveAttrSize        bool
		haveCategoryAttr    bool
		tags                []string
	)
	for i, a := range it.Attrs {
		if i >= maxAttrsPerItem {
			break
		}
		v := strings.TrimSpace(a.Value)
		switch strings.ToLower(strings.TrimSpace(a.Name)) {
		case "size":
			if n, ok := parseInt(v); ok && n > 0 {
				r.Size, haveAttrSize = n, true
			}
		case "category":
			haveCategoryAttr = true
			if n, ok := parseInt(v); ok && n >= 0 && n <= math.MaxInt32 && !slices.Contains(r.Categories, int(n)) {
				r.Categories = append(r.Categories, int(n))
			}
		case "guid":
			attrGUID = cleanText(v)
		case "seeders":
			r.Seeders = count(v)
		case "leechers":
			r.Leechers = count(v)
		case "peers":
			peers = count(v)
		case "grabs":
			r.Grabs = count(v)
		case "infohash":
			if h := normalizeHash(v); h != "" {
				r.InfoHash = h
			}
		case "magneturl":
			if isMagnet(v) {
				r.MagnetURL = clip(v, maxTextLen)
			}
		case "downloadvolumefactor":
			dvf, haveDVF = parseFactor(v)
		case "uploadvolumefactor":
			uvf, haveUVF = parseFactor(v)
		case "minimumratio":
			if f, ok := parseFactor(v); ok {
				r.MinimumRatio = f
			}
		case "minimumseedtime":
			if n, ok := parseInt(v); ok && n > 0 && n < 10*365*24*3600 {
				r.MinimumSeedTime = time.Duration(n) * time.Second
			}
		case "usenetdate":
			usenetDate = parseDate(v)
		case "imdb", "imdbid":
			r.IMDbID = indexer.NormalizeIMDb(v)
		case "tmdbid", "tmdb":
			if n, ok := parseInt(v); ok && n > 0 {
				r.TMDbID = n
			}
		case "tag":
			if t := strings.ToLower(clip(v, 50)); t != "" && !slices.Contains(tags, t) && len(tags) < 32 {
				tags = append(tags, t)
			}
		}
	}

	// Download URL: the enclosure (NZB or .torrent), else the link, else magnet.
	for _, e := range it.Enclosures {
		if u := cleanText(e.URL); isDownloadURL(u) {
			enclosure = u
			enclosureLen, _ = parseInt(strings.TrimSpace(e.Length))
			break
		}
	}
	link := cleanText(it.Link)
	switch {
	case enclosure != "":
		r.DownloadURL = enclosure
	case isDownloadURL(link):
		r.DownloadURL = link
	case r.MagnetURL != "":
		r.DownloadURL = r.MagnetURL
	}
	if isMagnet(r.DownloadURL) && r.MagnetURL == "" {
		r.MagnetURL = r.DownloadURL
	}
	if r.DownloadURL == "" {
		return r, false
	}
	if r.InfoHash == "" && r.MagnetURL != "" {
		r.InfoHash = magnetHash(r.MagnetURL)
	}

	if !haveAttrSize {
		if n, ok := parseInt(strings.TrimSpace(it.Size)); ok && n > 0 {
			r.Size = n
		} else if enclosureLen > 0 {
			r.Size = enclosureLen
		}
	}
	if !haveCategoryAttr {
		for _, c := range it.Categories {
			if n, ok := parseInt(strings.TrimSpace(c)); ok && n >= 0 && n <= math.MaxInt32 && !slices.Contains(r.Categories, int(n)) {
				r.Categories = append(r.Categories, int(n))
			}
		}
	}

	r.GUID = cleanText(it.GUID.Value)
	if r.GUID == "" {
		r.GUID = attrGUID
	}
	if r.GUID == "" {
		r.GUID = r.DownloadURL
	}
	if c := cleanText(it.Comments); isHTTP(c) {
		r.InfoURL = c
	} else if isHTTP(r.GUID) && r.GUID != r.DownloadURL {
		r.InfoURL = r.GUID
	}

	r.PublishDate = parseDate(it.PubDate)
	if !usenetDate.IsZero() && r.Protocol == decision.Usenet {
		r.PublishDate = usenetDate // the post date is what retention counts
	}

	if peers >= 0 && r.Leechers < 0 && r.Seeders >= 0 && peers >= r.Seeders {
		r.Leechers = peers - r.Seeders
	}
	if haveDVF {
		r.DownloadVolumeFactor = dvf
	}
	if haveUVF {
		r.UploadVolumeFactor = uvf
	}
	switch {
	case haveDVF && dvf == 0:
		r.Flags = append(r.Flags, indexer.FlagFreeleech)
	case haveDVF && dvf < 1:
		r.Flags = append(r.Flags, indexer.FlagHalfleech)
	}
	if haveUVF && uvf >= 2 {
		r.Flags = append(r.Flags, indexer.FlagDoubleUpload)
	}
	for _, t := range tags {
		if !slices.Contains(r.Flags, t) {
			r.Flags = append(r.Flags, t)
		}
	}
	return r, true
}

// cleanText trims s, drops control characters and invalid UTF-8, and clips it.
func cleanText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	clean := true
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c == 0x7f || c >= utf8.RuneSelf {
			clean = false
			break
		}
	}
	if !clean {
		s = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f || r == utf8.RuneError {
				if r == '\t' || r == '\n' || r == '\r' {
					return ' '
				}
				return -1
			}
			return r
		}, strings.ToValidUTF8(s, ""))
		s = strings.TrimSpace(s)
	}
	return clip(s, maxTextLen)
}

// clip cuts s to at most n bytes without splitting a rune.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func parseInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func atoiNonNeg(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return def
	}
	return n
}

// count parses a non-negative counter; -1 if invalid.
func count(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// parseFactor parses a Torznab volume factor or ratio. The spec asks clients
// to reject negative or extreme values.
func parseFactor(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil || math.IsNaN(f) || f < 0 || f > 100 {
		return 0, false
	}
	return f, true
}

func isHTTP(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

func isMagnet(s string) bool { return strings.HasPrefix(strings.ToLower(s), "magnet:?") }

func isDownloadURL(s string) bool {
	if !isHTTP(s) && !isMagnet(s) {
		return false
	}
	_, err := url.Parse(s)
	return err == nil
}

// normalizeHash returns a lower-case 40-character hexadecimal info hash, or ""
// when s is not one. Base32 hashes (32 characters) are converted.
func normalizeHash(s string) string {
	s = strings.TrimSpace(s)
	switch len(s) {
	case 40:
		s = strings.ToLower(s)
		if strings.Trim(s, "0123456789abcdef") == "" {
			return s
		}
	case 32:
		if b, err := base32.StdEncoding.DecodeString(strings.ToUpper(s)); err == nil && len(b) == 20 {
			const hex = "0123456789abcdef"
			out := make([]byte, 0, 40)
			for _, c := range b {
				out = append(out, hex[c>>4], hex[c&0xf])
			}
			return string(out)
		}
	}
	return ""
}

// magnetHash extracts the BitTorrent v1 info hash of a magnet URI.
func magnetHash(magnet string) string {
	u, err := url.Parse(magnet)
	if err != nil {
		return ""
	}
	for _, xt := range u.Query()["xt"] {
		if len(xt) > 9 && strings.EqualFold(xt[:9], "urn:btih:") {
			if h := normalizeHash(xt[9:]); h != "" {
				return h
			}
		}
	}
	return ""
}

var dateLayouts = [...]string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700",
	"02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04 -0700",
	time.RFC3339,
	"2006-01-02 15:04:05",
}

// parseDate parses an RSS (RFC 822/1123) date and a few common deviations;
// zero when s is empty or not a date. The result is in UTC.
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64 {
		return time.Time{}
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
