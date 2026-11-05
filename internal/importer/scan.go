package importer

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// Video files Gonema imports. Disc images (ISO, IMG) and disc folders (BDMV,
// VIDEO_TS) are refused: media servers play them poorly and they cannot be
// renamed as one file.
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".wmv": true,
	".mpg": true, ".mpeg": true, ".m2ts": true, ".ts": true, ".webm": true,
}

var discExts = map[string]bool{".iso": true, ".img": true}

// subtitleExts are carried along with the movie.
var subtitleExts = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".vtt": true, ".sub": true, ".idx": true, ".sup": true,
}

// extrasDirs hold bonus material, never the movie (compared in lower case).
var extrasDirs = map[string]bool{
	"extras": true, "extra": true, "featurettes": true, "featurette": true, "behind the scenes": true,
	"deleted scenes": true, "interviews": true, "scenes": true, "shorts": true, "trailers": true,
	"sample": true, "samples": true, "other": true, "others": true, "bonus": true, "specials": true,
	"proof": true,
}

// discDirs are disc structures.
var discDirs = map[string]bool{"bdmv": true, "video_ts": true, "certificate": true}

// Scan limits: a download is a handful of files; a deep or huge tree is not one.
const (
	maxScanDepth   = 4
	maxScanEntries = 5000
	// minMovieSize: a smaller video is a sample, a trailer or a broken file.
	minMovieSize = 20 << 20
	// sampleRatio: a video smaller than largest/sampleRatio is a sample or extra.
	sampleRatio = 10
)

var (
	sampleName = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])sample(?:[^a-z0-9]|$)`)
	// Plex/Jellyfin local extras naming: "Movie-trailer.mkv", "x-featurette.mp4".
	extraSuffix = regexp.MustCompile(`(?i)-(?:trailer|featurette|behindthescenes|deleted|deletedscene|interview|scene|short|other|extra)$`)
	partName    = regexp.MustCompile(`(?i)(?:^|[ ._\-\[(])(?:cd|dvd|disc|disk|part|pt)[ ._-]?([1-9])(?:[ ._\-\])]|$)`)
)

// videoFile is a video found by a scan.
type videoFile struct {
	path      string
	rel       string // relative to the scanned path ("" for a file scan)
	size      int64
	parsed    parser.ParsedRelease
	rejection string // why it is not the movie; "" if it may be
}

// subFile is a subtitle found by a scan.
type subFile struct {
	path string
	rel  string
}

// scanResult is what a scan found.
type scanResult struct {
	videos []videoFile // valid first (largest first), then rejected
	subs   []subFile
	notes  []string // folders or files skipped as a whole
}

func (r *scanResult) valid() []videoFile {
	var out []videoFile
	for i := range r.videos {
		if r.videos[i].rejection == "" {
			out = append(out, r.videos[i])
		}
	}
	return out
}

// rejections lists the reasons of the rejected videos and the notes.
func (r *scanResult) rejections() []string {
	out := slices.Clone(r.notes)
	for i := range r.videos {
		if v := &r.videos[i]; v.rejection != "" {
			out = append(out, filepath.Base(v.path)+": "+v.rejection)
		}
	}
	return out
}

// scan lists the videos and subtitles of path (a file or a folder) without
// following symbolic links, and classifies the videos.
func (s *Service) scan(ctx context.Context, path string) (*scanResult, error) {
	fi, err := s.fs.Lstat(path)
	if err != nil {
		return nil, userErr(ErrNotFound, err, "%s cannot be read (missing, or no permission)", path)
	}
	res := &scanResult{}
	switch {
	case fi.Mode().IsRegular():
		ext := strings.ToLower(filepath.Ext(path))
		switch {
		case videoExts[ext]:
			res.videos = append(res.videos, videoFile{path: path, size: fi.Size()})
		case discExts[ext]:
			res.notes = append(res.notes, filepath.Base(path)+": disc images (ISO) are not supported; extract or remux the movie first")
		default:
			res.notes = append(res.notes, filepath.Base(path)+": not a video file")
		}
		// Subtitles next to a single file: "Movie.en.srt" for "Movie.mkv".
		stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if entries, err := s.fs.ReadDir(filepath.Dir(path)); err == nil {
			for _, e := range entries {
				n := e.Name()
				if e.Type().IsRegular() && subtitleExts[strings.ToLower(filepath.Ext(n))] && strings.HasPrefix(n, stem+".") {
					res.subs = append(res.subs, subFile{path: filepath.Join(filepath.Dir(path), n), rel: n})
				}
			}
		}
	case fi.IsDir():
		count := 0
		if err := s.walk(ctx, path, "", 0, &count, res); err != nil {
			return nil, err
		}
	default:
		return nil, userErr(ErrInvalid, nil, "%s is neither a file nor a folder (symbolic links are not followed)", path)
	}
	classify(res)
	return res, nil
}

func (s *Service) walk(ctx context.Context, dir, rel string, depth int, count *int, res *scanResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		if rel == "" {
			return userErr(ErrNotFound, err, "%s cannot be listed (no permission?)", dir)
		}
		res.notes = append(res.notes, rel+": cannot be listed, skipped")
		return nil
	}
	for _, e := range entries {
		*count++
		if *count > maxScanEntries {
			return userErr(ErrInvalid, nil, "%s holds more than %d files: choose the folder of one movie", dir, maxScanEntries)
		}
		name := e.Name()
		childRel := filepath.Join(rel, name)
		child := filepath.Join(dir, name)
		switch {
		case strings.HasPrefix(name, "."):
			continue // hidden, our temporary files, recycle bins
		case e.Type()&fs.ModeSymlink != 0:
			res.notes = append(res.notes, childRel+": symbolic link, not followed")
		case e.IsDir():
			lower := strings.ToLower(name)
			switch {
			case extrasDirs[lower]:
				continue
			case discDirs[lower]:
				res.notes = append(res.notes, childRel+": disc folders (BDMV, VIDEO_TS) are not supported; remux the movie first")
			case depth+1 >= maxScanDepth:
				res.notes = append(res.notes, childRel+": too deep, skipped")
			default:
				if err := s.walk(ctx, child, childRel, depth+1, count, res); err != nil {
					return err
				}
			}
		case e.Type().IsRegular():
			ext := strings.ToLower(filepath.Ext(name))
			switch {
			case videoExts[ext]:
				info, err := e.Info()
				if err != nil {
					continue // removed meanwhile
				}
				res.videos = append(res.videos, videoFile{path: child, rel: childRel, size: info.Size()})
			case subtitleExts[ext]:
				res.subs = append(res.subs, subFile{path: child, rel: childRel})
			case discExts[ext]:
				res.notes = append(res.notes, childRel+": disc images (ISO) are not supported; extract or remux the movie first")
			}
		}
	}
	return nil
}

// classify parses each video and rejects samples, extras and repair packs; the
// valid videos come first, largest first.
func classify(res *scanResult) {
	var largest int64
	for i := range res.videos {
		v := &res.videos[i]
		v.parsed = parser.Parse(filepath.Base(v.path))
		stem := strings.TrimSuffix(filepath.Base(v.path), filepath.Ext(v.path))
		switch {
		case v.parsed.Sample || sampleName.MatchString(stem):
			v.rejection = "a sample, not the movie"
		case v.parsed.Extras || extraSuffix.MatchString(stem):
			v.rejection = "bonus material (extras), not the movie"
		case v.parsed.Fix != parser.FixNone && v.parsed.Fix != parser.FixDir:
			v.rejection = "a repair pack (" + v.parsed.Fix.String() + "fix), not the movie"
		case v.size < minMovieSize:
			v.rejection = fmt.Sprintf("too small to be a movie (%s)", humanSize(v.size))
		default:
			largest = max(largest, v.size)
		}
	}
	for i := range res.videos {
		v := &res.videos[i]
		if v.rejection == "" && v.size < largest/sampleRatio {
			v.rejection = fmt.Sprintf("much smaller than the main file (%s against %s): a sample or an extra", humanSize(v.size), humanSize(largest))
		}
	}
	slices.SortStableFunc(res.videos, func(a, b videoFile) int {
		switch {
		case (a.rejection == "") != (b.rejection == ""):
			if a.rejection == "" {
				return -1
			}
			return 1
		case a.size != b.size:
			if a.size > b.size {
				return -1
			}
			return 1
		}
		return strings.Compare(a.path, b.path)
	})
}

// multiPart reports whether the valid videos are parts of one movie (CD1, CD2).
func multiPart(valid []videoFile) bool {
	if len(valid) < 2 {
		return false
	}
	parts := map[string]bool{}
	for i := range valid {
		stem := strings.TrimSuffix(filepath.Base(valid[i].path), filepath.Ext(valid[i].path))
		if m := partName.FindStringSubmatch(stem); m != nil {
			parts[m[1]] = true
		}
	}
	return len(parts) >= 2
}

// humanSize formats bytes for messages.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// informative scores how much a parsed name tells.
func informative(p *parser.ParsedRelease) int {
	n := 0
	for _, ok := range []bool{
		p.Title != "", p.Year > 0, p.Resolution != parser.ResolutionUnknown, p.Source != parser.SourceUnknown,
		p.VideoCodec != parser.CodecUnknown, p.ReleaseGroup != "", len(p.HDR) > 0, len(p.Audio) > 0,
		p.Edition != 0, p.Revision.Version > 1,
	} {
		if ok {
			n++
		}
	}
	return n
}

// named is a parsed name and where it comes from.
type named struct {
	name   string
	parsed parser.ParsedRelease
}

// bestParse picks the most informative name (earlier names win ties) and fills
// the fields it misses from the others. It returns the merged result and the
// name it is based on.
func bestParse(names []named) (merged parser.ParsedRelease, from string) {
	best := -1
	for i := range names {
		if names[i].name == "" || !names[i].parsed.IsMovie() && informative(&names[i].parsed) == 0 {
			continue
		}
		if best < 0 || informative(&names[i].parsed) > informative(&names[best].parsed) {
			best = i
		}
	}
	if best < 0 {
		return parser.ParsedRelease{Revision: parser.Revision{Version: 1}}, ""
	}
	p := names[best].parsed
	for i := range names {
		if i == best {
			continue
		}
		o := &names[i].parsed
		if p.Title == "" && o.Title != "" {
			p.Title, p.CleanTitle, p.AlternateTitles = o.Title, o.CleanTitle, o.AlternateTitles
		}
		if p.Year == 0 {
			p.Year = o.Year
		}
		if p.Resolution == parser.ResolutionUnknown {
			p.Resolution = o.Resolution
		}
		if p.Source == parser.SourceUnknown {
			p.Source, p.Modifier = o.Source, o.Modifier
		}
		if p.VideoCodec == parser.CodecUnknown {
			p.VideoCodec = o.VideoCodec
		}
		if len(p.HDR) == 0 {
			p.HDR = o.HDR
		}
		if len(p.Audio) == 0 {
			p.Audio, p.Atmos, p.Channels = o.Audio, o.Atmos, o.Channels
		}
		if p.Edition == 0 {
			p.Edition, p.EditionText = o.Edition, o.EditionText
		}
		if p.ReleaseGroup == "" {
			p.ReleaseGroup = o.ReleaseGroup
		}
		if len(p.Languages) == 0 {
			p.Languages, p.Multi = o.Languages, o.Multi
		}
		if o.Revision.Version > p.Revision.Version {
			p.Revision = o.Revision
		}
	}
	return p, names[best].name
}

// qualityID converts a stored quality, Unknown when it is not in the catalog.
func qualityID(s string) quality.ID {
	if id := quality.ID(s); id.Valid() {
		return id
	}
	return quality.Unknown
}
