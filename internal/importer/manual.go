package importer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// maxManualSelections bounds one manual import request.
const maxManualSelections = 100

// ScanCandidate is a video file found by Scan, with a best guess of its movie.
type ScanCandidate struct {
	Path    string               `json:"path"`
	Name    string               `json:"name"` // relative to the scanned folder
	Size    int64                `json:"size"`
	Quality quality.ID           `json:"quality"`
	Parsed  parser.ParsedRelease `json:"parsed"`
	// Movie is the most likely library movie (nil when none matches). Use it
	// without asking only when Movie.Confident().
	Movie        *match.Candidate  `json:"movie,omitempty"`
	Alternatives []match.Candidate `json:"alternatives,omitempty"`
	// Rejections say why the file would not be imported automatically. The user
	// may still import it.
	Rejections []string `json:"rejections,omitempty"`
}

// Scan lists the video files of dir (a folder or a file inside an allowed
// folder) with their parsed name, quality, likely movie and rejection reasons.
// Nothing is changed on disk.
func (s *Service) Scan(ctx context.Context, dir string) ([]ScanCandidate, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	src, err := s.checkSource(ctx, &set, dir)
	if err != nil {
		return nil, err
	}
	res, err := s.scan(ctx, src)
	if err != nil {
		return nil, err
	}
	multi := multiPart(res.valid())
	out := make([]ScanCandidate, 0, len(res.videos))
	for i := range res.videos {
		v := &res.videos[i]
		names := []named{{filepath.Base(v.path), v.parsed}}
		if parent := filepath.Dir(v.path); parent != filepath.Dir(src) || v.rel != "" {
			names = append(names, named{filepath.Base(parent), parser.Parse(filepath.Base(parent))})
		}
		parsed, _ := bestParse(names)
		c := ScanCandidate{Path: v.path, Name: v.rel, Size: v.size, Parsed: parsed, Quality: quality.FromParsed(&parsed)}
		if c.Name == "" {
			c.Name = filepath.Base(v.path)
		}
		if v.rejection != "" {
			c.Rejections = append(c.Rejections, v.rejection)
		}
		if multi && v.rejection == "" {
			c.Rejections = append(c.Rejections, "part of a multi-part movie (CD1, CD2...): not supported")
		}
		if c.Quality == quality.Unknown {
			c.Rejections = append(c.Rejections, "the quality cannot be told from the name: choose it")
		}
		if s.matcher != nil && parsed.Title != "" {
			if cands := s.matcher.MatchRelease(parsed); len(cands) > 0 {
				c.Movie = &cands[0]
				c.Alternatives = cands[1:min(len(cands), 5)]
			}
		}
		if c.Movie == nil {
			c.Rejections = append(c.Rejections, "no library movie matches the name: choose the movie")
		}
		out = append(out, c)
	}
	return out, nil
}

// Selection is a file the user chose to import manually.
type Selection struct {
	Path    string     `json:"path"`
	MovieID int64      `json:"movieId"`
	Quality quality.ID `json:"quality,omitempty"` // overrides the quality read from the name
	Mode    fsx.Mode   `json:"mode,omitempty"`    // hardlink, copy or move; default: the import mode of the settings
	// Replace replaces the current file even when this one is not an upgrade
	// (the current file goes to the recycle bin).
	Replace bool `json:"replace,omitempty"`
}

// ManualResult is the outcome of one selection. Error is set when the import
// could not run; Result.Reason explains a refusal.
type ManualResult struct {
	Path string `json:"path"`
	Result
	Error string `json:"error,omitempty"`
}

// ImportManual imports the files chosen by the user, each for the movie and
// (optionally) the quality they picked. The title is not checked against the
// movie (the user chose it); the upgrade rules apply unless Replace is set.
// Selections run in order; one failure does not stop the others.
func (s *Service) ImportManual(ctx context.Context, sels []Selection) ([]ManualResult, error) {
	if len(sels) == 0 {
		return nil, userErr(ErrInvalid, nil, "choose at least one file to import")
	}
	if len(sels) > maxManualSelections {
		return nil, userErr(ErrInvalid, nil, "import at most %d files at once", maxManualSelections)
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ManualResult, 0, len(sels))
	for i := range sels {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		r, err := s.importOne(ctx, &set, &sels[i])
		mr := ManualResult{Path: sels[i].Path, Result: r}
		if err != nil {
			var ue *Error
			if errors.As(err, &ue) {
				mr.Error = ue.UserMessage()
			} else {
				s.log.Error("manual import failed", slog.String("path", sels[i].Path), slog.Any("error", err))
				mr.Error = "the import failed on the server; the logs have the details"
			}
		}
		out = append(out, mr)
	}
	return out, nil
}

func (s *Service) importOne(ctx context.Context, set *Settings, sel *Selection) (Result, error) {
	if sel.MovieID <= 0 {
		return Result{}, userErr(ErrInvalid, nil, "choose the movie of %s", filepath.Base(sel.Path))
	}
	if sel.Quality != "" && !sel.Quality.Valid() {
		return Result{}, userErr(ErrInvalid, nil, "%q is not a known quality", sel.Quality)
	}
	mode := set.Mode
	if sel.Mode != "" {
		if !sel.Mode.Valid() {
			return Result{}, userErr(ErrInvalid, nil, "the mode must be hardlink, copy or move")
		}
		mode = sel.Mode
	}
	src, err := s.checkSource(ctx, set, sel.Path)
	if err != nil {
		return Result{}, err
	}
	fi, err := s.fs.Lstat(src)
	if err != nil || !fi.Mode().IsRegular() || !videoExts[strings.ToLower(filepath.Ext(src))] {
		return Result{}, userErr(ErrInvalid, nil, "%s is not a video file", sel.Path)
	}
	unlock := s.lockMovie(sel.MovieID)
	defer unlock()
	t, err := s.loadTarget(ctx, sel.MovieID)
	if err != nil {
		return Result{}, err
	}
	res, err := s.scan(ctx, src)
	if err != nil {
		return Result{}, err
	}
	parent := filepath.Dir(src)
	parsed, from := bestParse([]named{
		{filepath.Base(src), parser.Parse(filepath.Base(src))},
		{filepath.Base(parent), parser.Parse(filepath.Base(parent))},
	})
	q := quality.FromParsed(&parsed)
	if sel.Quality != "" {
		q = sel.Quality
	}
	if q == quality.Unknown {
		return Result{Reason: fmt.Sprintf("The quality of %s cannot be told from its name: choose it", filepath.Base(src))}, nil
	}
	p := &plan{
		t: t, src: src, size: fi.Size(), parsed: parsed, scene: strings.TrimSuffix(from, filepath.Ext(from)),
		quality: q, mode: mode, anyQuality: true, replace: sel.Replace, source: "manual",
	}
	if set.ImportSubtitles {
		p.subs = res.subs
	}
	p.scoreIt()
	if why := p.evaluate(); why != "" {
		return Result{Reason: why}, nil
	}
	return s.execute(ctx, set, p)
}
