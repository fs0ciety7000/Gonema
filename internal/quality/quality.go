package quality

import "github.com/fs0ciety7000/gonema/internal/parser"

// ID identifies a quality in the fixed catalog, e.g. "bluray-1080p". IDs are
// stable: they are stored in the database (quality_profile_items, movie_files,
// downloads, history) and must never be renamed.
type ID string

// Catalog quality IDs, from worst to best intrinsic rank.
const (
	Unknown     ID = "unknown"
	Cam         ID = "cam"
	Telesync    ID = "telesync"
	Telecine    ID = "telecine"
	Screener    ID = "screener"
	Regional    ID = "regional"
	SDTV        ID = "sdtv"
	DVD         ID = "dvd"
	DVDR        ID = "dvd-r"
	WebRip480p  ID = "webrip-480p"
	WebDL480p   ID = "webdl-480p"
	Bluray480p  ID = "bluray-480p"
	Bluray576p  ID = "bluray-576p"
	HDTV720p    ID = "hdtv-720p"
	WebRip720p  ID = "webrip-720p"
	WebDL720p   ID = "webdl-720p"
	Bluray720p  ID = "bluray-720p"
	HDTV1080p   ID = "hdtv-1080p"
	WebRip1080p ID = "webrip-1080p"
	WebDL1080p  ID = "webdl-1080p"
	Bluray1080p ID = "bluray-1080p"
	Remux1080p  ID = "remux-1080p"
	BRDisk1080p ID = "brdisk-1080p"
	RawHD       ID = "rawhd"
	HDTV2160p   ID = "hdtv-2160p"
	WebRip2160p ID = "webrip-2160p"
	WebDL2160p  ID = "webdl-2160p"
	Bluray2160p ID = "bluray-2160p"
	Remux2160p  ID = "remux-2160p"
	BRDisk2160p ID = "brdisk-2160p"
)

// Quality is one entry of the catalog.
type Quality struct {
	ID         ID                `json:"id"`
	Name       string            `json:"name"` // display name, e.g. "Bluray-1080p"
	Source     parser.Source     `json:"source"`
	Modifier   parser.Modifier   `json:"modifier"`
	Resolution parser.Resolution `json:"resolution"`
	// Rank is the intrinsic rank, higher is better. Profiles define their own
	// order; Rank only orders the catalog and the "Any" starter profile.
	Rank int `json:"rank"`
}

// catalog is read-only. Index i holds the quality with Rank i.
var catalog = [...]Quality{
	{Unknown, "Unknown", parser.SourceUnknown, parser.ModifierNone, parser.ResolutionUnknown, 0},
	{Cam, "CAM", parser.SourceCam, parser.ModifierNone, parser.ResolutionUnknown, 1},
	{Telesync, "TELESYNC", parser.SourceTelesync, parser.ModifierNone, parser.ResolutionUnknown, 2},
	{Telecine, "TELECINE", parser.SourceTelecine, parser.ModifierNone, parser.ResolutionUnknown, 3},
	{Screener, "SCREENER", parser.SourceScreener, parser.ModifierNone, parser.ResolutionUnknown, 4},
	{Regional, "REGIONAL", parser.SourceDVD, parser.ModifierRegional, parser.Resolution480p, 5},
	{SDTV, "SDTV", parser.SourceHDTV, parser.ModifierNone, parser.Resolution480p, 6},
	{DVD, "DVD", parser.SourceDVD, parser.ModifierNone, parser.Resolution480p, 7},
	{DVDR, "DVD-R", parser.SourceDVD, parser.ModifierDVDR, parser.Resolution480p, 8},
	{WebRip480p, "WEBRip-480p", parser.SourceWebRip, parser.ModifierNone, parser.Resolution480p, 9},
	{WebDL480p, "WEBDL-480p", parser.SourceWebDL, parser.ModifierNone, parser.Resolution480p, 10},
	{Bluray480p, "Bluray-480p", parser.SourceBluRay, parser.ModifierNone, parser.Resolution480p, 11},
	{Bluray576p, "Bluray-576p", parser.SourceBluRay, parser.ModifierNone, parser.Resolution576p, 12},
	{HDTV720p, "HDTV-720p", parser.SourceHDTV, parser.ModifierNone, parser.Resolution720p, 13},
	{WebRip720p, "WEBRip-720p", parser.SourceWebRip, parser.ModifierNone, parser.Resolution720p, 14},
	{WebDL720p, "WEBDL-720p", parser.SourceWebDL, parser.ModifierNone, parser.Resolution720p, 15},
	{Bluray720p, "Bluray-720p", parser.SourceBluRay, parser.ModifierNone, parser.Resolution720p, 16},
	{HDTV1080p, "HDTV-1080p", parser.SourceHDTV, parser.ModifierNone, parser.Resolution1080p, 17},
	{WebRip1080p, "WEBRip-1080p", parser.SourceWebRip, parser.ModifierNone, parser.Resolution1080p, 18},
	{WebDL1080p, "WEBDL-1080p", parser.SourceWebDL, parser.ModifierNone, parser.Resolution1080p, 19},
	{Bluray1080p, "Bluray-1080p", parser.SourceBluRay, parser.ModifierNone, parser.Resolution1080p, 20},
	{Remux1080p, "Remux-1080p", parser.SourceBluRay, parser.ModifierRemux, parser.Resolution1080p, 21},
	{BRDisk1080p, "BR-DISK-1080p", parser.SourceBluRay, parser.ModifierBRDisk, parser.Resolution1080p, 22},
	{RawHD, "Raw-HD", parser.SourceHDTV, parser.ModifierRawHD, parser.Resolution1080p, 23},
	{HDTV2160p, "HDTV-2160p", parser.SourceHDTV, parser.ModifierNone, parser.Resolution2160p, 24},
	{WebRip2160p, "WEBRip-2160p", parser.SourceWebRip, parser.ModifierNone, parser.Resolution2160p, 25},
	{WebDL2160p, "WEBDL-2160p", parser.SourceWebDL, parser.ModifierNone, parser.Resolution2160p, 26},
	{Bluray2160p, "Bluray-2160p", parser.SourceBluRay, parser.ModifierNone, parser.Resolution2160p, 27},
	{Remux2160p, "Remux-2160p", parser.SourceBluRay, parser.ModifierRemux, parser.Resolution2160p, 28},
	{BRDisk2160p, "BR-DISK-2160p", parser.SourceBluRay, parser.ModifierBRDisk, parser.Resolution2160p, 29},
}

// All returns the catalog ordered by intrinsic rank, worst first. The slice is a copy.
func All() []Quality {
	out := make([]Quality, len(catalog))
	copy(out, catalog[:])
	return out
}

// Lookup returns the catalog entry for id.
func Lookup(id ID) (Quality, bool) {
	for i := range catalog {
		if catalog[i].ID == id {
			return catalog[i], true
		}
	}
	return Quality{}, false
}

// Name returns the display name of id, or the ID itself when it is not in the catalog.
func (id ID) Name() string {
	if q, ok := Lookup(id); ok {
		return q.Name
	}
	return string(id)
}

// Valid reports whether id is in the catalog.
func (id ID) Valid() bool {
	_, ok := Lookup(id)
	return ok
}

// byResolution picks the quality for a resolution among the 480p, 720p, 1080p and
// 2160p variants of one source. An unknown, 480p or 576p resolution maps to sd
// (scene names without a resolution tag are SD encodes: "BDRip.x264", "WEB.H264").
func byResolution(r parser.Resolution, sd, hd720, hd1080, uhd ID) ID {
	switch r {
	case parser.Resolution2160p:
		return uhd
	case parser.Resolution1080p:
		return hd1080
	case parser.Resolution720p:
		return hd720
	default:
		return sd
	}
}

// FromParsed maps a parsed release to its catalog quality. See the package
// documentation for the mapping rules. It returns Unknown when the source is unknown.
func FromParsed(p *parser.ParsedRelease) ID {
	switch p.Source {
	case parser.SourceCam:
		return Cam
	case parser.SourceTelesync:
		return Telesync
	case parser.SourceTelecine:
		return Telecine
	case parser.SourceScreener:
		return Screener
	case parser.SourceDVD:
		switch p.Modifier {
		case parser.ModifierDVDR:
			return DVDR
		case parser.ModifierRegional:
			return Regional
		default:
			return DVD
		}
	case parser.SourceHDTV:
		if p.Modifier == parser.ModifierRawHD {
			return RawHD
		}
		return byResolution(p.Resolution, SDTV, HDTV720p, HDTV1080p, HDTV2160p)
	case parser.SourceWebRip:
		return byResolution(p.Resolution, WebRip480p, WebRip720p, WebRip1080p, WebRip2160p)
	case parser.SourceWebDL:
		return byResolution(p.Resolution, WebDL480p, WebDL720p, WebDL1080p, WebDL2160p)
	case parser.SourceBluRay:
		switch p.Modifier {
		case parser.ModifierRemux:
			if p.Resolution == parser.Resolution2160p {
				return Remux2160p
			}
			return Remux1080p
		case parser.ModifierBRDisk:
			if p.Resolution == parser.Resolution2160p {
				return BRDisk2160p
			}
			return BRDisk1080p
		}
		if p.Resolution == parser.Resolution576p {
			return Bluray576p
		}
		return byResolution(p.Resolution, Bluray480p, Bluray720p, Bluray1080p, Bluray2160p)
	default:
		return Unknown
	}
}
