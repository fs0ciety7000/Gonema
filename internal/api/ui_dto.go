package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Image sizes of the UI (see imagecache for the sizes kept per kind).
const (
	gridPosterSize   = "w342"
	detailPosterSize = "w780"
	backdropSize     = "w1280"
	profileSize      = "w185"
)

// imageURL is the API path of a cached TMDB image, with the base URL: usable
// as is in an <img src>. nil without an image.
func (s *Server) imageURL(kind imagecache.Kind, size string, tmdbPath *string) *string {
	if tmdbPath == nil || *tmdbPath == "" {
		return nil
	}
	u := s.d.BaseURL + Prefix + "/images/" + string(kind) + "/" + size + *tmdbPath
	return &u
}

func (s *Server) imageURLOf(kind imagecache.Kind, size, tmdbPath string) *string {
	if tmdbPath == "" {
		return nil
	}
	return s.imageURL(kind, size, &tmdbPath)
}

// stringList is a JSON array of strings stored as text by the database (checked
// there with json_type = 'array'): it is written as is, without decoding.
type stringList string

// MarshalJSON writes the stored array.
func (l stringList) MarshalJSON() ([]byte, error) {
	if l == "" {
		return []byte("[]"), nil
	}
	return []byte(l), nil
}

// UnmarshalJSON keeps the array as is (clients and tests decode responses).
func (l *stringList) UnmarshalJSON(b []byte) error {
	*l = stringList(b)
	return nil
}

// Strings decodes the list.
func (l stringList) Strings() []string {
	var out []string
	if json.Unmarshal([]byte(l), &out) != nil || out == nil {
		return []string{}
	}
	return out
}

// Schema describes stringList in the OpenAPI document.
func (stringList) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeArray, Items: &huma.Schema{Type: huma.TypeString}}
}

// ratingsJSON is movies.ratings ({tmdb:{value,votes}, imdb:{value,votes}}),
// written as stored.
type ratingsJSON string

// MarshalJSON writes the stored object.
func (r ratingsJSON) MarshalJSON() ([]byte, error) {
	if r == "" {
		return []byte("{}"), nil
	}
	return []byte(r), nil
}

// UnmarshalJSON keeps the object as is.
func (r *ratingsJSON) UnmarshalJSON(b []byte) error {
	*r = ratingsJSON(b)
	return nil
}

func ratingSchema() *huma.Schema {
	return &huma.Schema{
		Type: huma.TypeObject, Nullable: true,
		Properties: map[string]*huma.Schema{
			"value": {Type: huma.TypeNumber, Description: "Average out of 10"},
			"votes": {Type: huma.TypeInteger},
		},
		Required: []string{"value", "votes"},
	}
}

// Schema describes ratingsJSON in the OpenAPI document.
func (ratingsJSON) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeObject, Properties: map[string]*huma.Schema{
		"tmdb": ratingSchema(), "imdb": ratingSchema(),
	}}
}

// DownloadProgress is the state of the active download of a movie.
type DownloadProgress struct {
	ID         int64   `json:"id" doc:"Queue entry id"`
	Status     string  `json:"status" enum:"queued,downloading,paused,completed,importing,warning"`
	Progress   float64 `json:"progress" doc:"0 to 1"`
	SpeedBps   int64   `json:"speedBps"`
	EtaSeconds *int64  `json:"etaSeconds"`
}

// MovieListItem is a movie of the library grid and table: everything both
// screens show, in one compact row.
type MovieListItem struct {
	ID                  int64             `json:"id"`
	TmdbID              int64             `json:"tmdbId"`
	ImdbID              *string           `json:"imdbId"`
	Title               string            `json:"title"`
	SortTitle           string            `json:"sortTitle"`
	Year                int64             `json:"year" doc:"0 when unknown"`
	Status              string            `json:"status" enum:"announced,in_cinemas,released,deleted" doc:"Release stage"`
	State               string            `json:"state" enum:"available,missing,downloading,upgradable" doc:"Library state from files and downloads"`
	Failed              bool              `json:"failed" doc:"The newest download failed after the current file (if any) was imported"`
	Monitored           bool              `json:"monitored"`
	RuntimeMin          int64             `json:"runtimeMin"`
	Genres              stringList        `json:"genres"`
	Ratings             ratingsJSON       `json:"ratings"`
	PosterURL           *string           `json:"posterUrl" doc:"Poster at w342; replace w342 by w780 in the path for high-density screens, or by w92 for a thumbnail"`
	DominantColor       *string           `json:"dominantColor" doc:"#rrggbb from the poster"`
	QualityProfileID    int64             `json:"qualityProfileId"`
	MinimumAvailability string            `json:"minimumAvailability" enum:"announced,in_cinemas,released"`
	RootFolderID        int64             `json:"rootFolderId"`
	Path                string            `json:"path" doc:"Movie folder, relative to the root folder"`
	CollectionID        *int64            `json:"collectionId"`
	AddedAt             time.Time         `json:"addedAt"`
	ReleaseCinema       *string           `json:"releaseCinema" doc:"YYYY-MM-DD"`
	ReleaseDigital      *string           `json:"releaseDigital"`
	ReleasePhysical     *string           `json:"releasePhysical"`
	Quality             *string           `json:"quality" doc:"Quality id of the current file; null without a file"`
	QualityName         *string           `json:"qualityName"`
	SizeOnDisk          *int64            `json:"sizeOnDisk" doc:"Bytes of the current file"`
	CustomScore         *int64            `json:"customScore" doc:"Custom score of the current file"`
	HDR                 []string          `json:"hdr" doc:"HDR formats of the current file (DV, HDR10...)"`
	Edition             *string           `json:"edition"`
	TagIDs              []int64           `json:"tagIds" doc:"Ids of the movie's tags (GET /tags has the labels)"`
	Download            *DownloadProgress `json:"download" doc:"Active download; null otherwise"`
}

func tagIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// splitHDR turns the stored "dv hdr10" into display names ["DV", "HDR10"].
func splitHDR(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Fields(strings.ToUpper(s))
}

func qualityName(id *string) *string {
	if id == nil {
		return nil
	}
	n := quality.ID(*id).Name()
	return &n
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func emptyToNil(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

func downloadOf(it *acquire.QueueItem) *DownloadProgress {
	return &DownloadProgress{ID: it.ID, Status: it.Status, Progress: it.Progress, SpeedBps: it.SpeedBps, EtaSeconds: it.EtaSeconds}
}

func (s *Server) movieListItemOf(m *library.MovieSummary, live map[int64]*acquire.QueueItem) MovieListItem {
	it := MovieListItem{
		ID: m.ID, TmdbID: m.TmdbID, ImdbID: m.ImdbID, Title: m.Title, SortTitle: m.SortTitle, Year: m.Year,
		Status: m.Status, State: m.State, Failed: m.LastDownloadFailed, Monitored: m.Monitored,
		RuntimeMin: m.RuntimeMin, Genres: stringList(m.Genres), Ratings: ratingsJSON(m.Ratings),
		PosterURL: s.imageURL(imagecache.Poster, gridPosterSize, m.PosterPath), DominantColor: m.DominantColor,
		QualityProfileID: m.QualityProfileID, MinimumAvailability: m.MinimumAvailability,
		RootFolderID: m.RootFolderID, Path: m.Path, CollectionID: m.CollectionID, AddedAt: ts(m.AddedAt),
		ReleaseCinema: m.ReleaseCinema, ReleaseDigital: m.ReleaseDigital, ReleasePhysical: m.ReleasePhysical,
		Quality: m.FileQuality, QualityName: qualityName(m.FileQuality), SizeOnDisk: m.FileSize,
		CustomScore: m.FileScore, HDR: splitHDR(m.FileHdr), Edition: emptyToNil(m.FileEdition), TagIDs: tagIDs(m.TagIDs),
	}
	if m.State == library.StateDownloading {
		if q, ok := live[m.ID]; ok {
			it.Download = downloadOf(q)
		} else {
			it.Download = &DownloadProgress{Status: "downloading", Progress: m.DownloadProgress}
		}
	}
	return it
}

// MovieColumns is the whole filtered and sorted library in columns: entry i of
// every array describes the same movie. Columns compress far better than rows
// and let a client virtualise 10,000 movies after one request.
type MovieColumns struct {
	Total         int               `json:"total"`
	ID            []int64           `json:"id"`
	Title         []string          `json:"title"`
	Year          []int64           `json:"year"`
	Status        []string          `json:"status" doc:"Release stage"`
	State         []string          `json:"state"`
	Failed        []bool            `json:"failed"`
	Monitored     []bool            `json:"monitored"`
	PosterURL     []*string         `json:"posterUrl" doc:"w342; replace w342 by w92 in the path for a table thumbnail"`
	DominantColor []*string         `json:"dominantColor"`
	Quality       []*string         `json:"quality"`
	QualityNames  map[string]string `json:"qualityNames" doc:"Display name of each quality id present"`
	SizeOnDisk    []*int64          `json:"sizeOnDisk"`
	CustomScore   []*int64          `json:"customScore"`
	HDR           []string          `json:"hdr" doc:"Space-separated HDR formats (DV HDR10); empty for SDR or no file"`
	Progress      []*float64        `json:"progress" doc:"Download progress 0 to 1; null when not downloading"`
	AddedAt       []int64           `json:"addedAt" doc:"Unix milliseconds"`
	RootFolderID  []int64           `json:"rootFolderId"`
	Path          []string          `json:"path"`
	Genres        []stringList      `json:"genres"`
	RuntimeMin    []int64           `json:"runtimeMin"`
	RatingTMDB    []*float64        `json:"ratingTmdb" doc:"TMDB average out of 10"`

	SortTitle           []string  `json:"sortTitle"`
	QualityProfileID    []int64   `json:"qualityProfileId"`
	MinimumAvailability []string  `json:"minimumAvailability"`
	ReleaseCinema       []*string `json:"releaseCinema" doc:"YYYY-MM-DD"`
	ReleaseDigital      []*string `json:"releaseDigital"`
	ReleasePhysical     []*string `json:"releasePhysical"`
	TagIDs              [][]int64 `json:"tagIds" doc:"Ids of each movie's tags"`
}

func (s *Server) movieColumnsOf(rows []*library.MovieSummary, live map[int64]*acquire.QueueItem) MovieColumns {
	n := len(rows)
	c := MovieColumns{
		Total: n, ID: make([]int64, n), Title: make([]string, n), Year: make([]int64, n), Status: make([]string, n),
		State: make([]string, n), Failed: make([]bool, n), Monitored: make([]bool, n), PosterURL: make([]*string, n),
		DominantColor: make([]*string, n), Quality: make([]*string, n), QualityNames: map[string]string{},
		SizeOnDisk: make([]*int64, n), CustomScore: make([]*int64, n), HDR: make([]string, n),
		Progress: make([]*float64, n), AddedAt: make([]int64, n), RootFolderID: make([]int64, n),
		Path: make([]string, n), Genres: make([]stringList, n), RuntimeMin: make([]int64, n),
		RatingTMDB: make([]*float64, n), SortTitle: make([]string, n), QualityProfileID: make([]int64, n),
		MinimumAvailability: make([]string, n), ReleaseCinema: make([]*string, n), ReleaseDigital: make([]*string, n),
		ReleasePhysical: make([]*string, n), TagIDs: make([][]int64, n),
	}
	for i, m := range rows {
		c.ID[i], c.Title[i], c.Year[i], c.Status[i], c.State[i] = m.ID, m.Title, m.Year, m.Status, m.State
		c.Failed[i], c.Monitored[i] = m.LastDownloadFailed, m.Monitored
		c.PosterURL[i] = s.imageURL(imagecache.Poster, gridPosterSize, m.PosterPath)
		c.DominantColor[i], c.Quality[i] = m.DominantColor, m.FileQuality
		if m.FileQuality != nil {
			if _, ok := c.QualityNames[*m.FileQuality]; !ok {
				c.QualityNames[*m.FileQuality] = quality.ID(*m.FileQuality).Name()
			}
		}
		c.SizeOnDisk[i], c.CustomScore[i], c.HDR[i] = m.FileSize, m.FileScore, strings.ToUpper(m.FileHdr)
		if m.State == library.StateDownloading {
			p := m.DownloadProgress
			if q, ok := live[m.ID]; ok {
				p = q.Progress
			}
			c.Progress[i] = &p
		}
		c.AddedAt[i], c.RootFolderID[i], c.Path[i] = m.AddedAt, m.RootFolderID, m.Path
		c.Genres[i], c.RuntimeMin[i] = stringList(m.Genres), m.RuntimeMin
		if m.RatingTmdb >= 0 {
			c.RatingTMDB[i] = &m.RatingTmdb
		}
		c.SortTitle[i], c.QualityProfileID[i], c.MinimumAvailability[i] = m.SortTitle, m.QualityProfileID, m.MinimumAvailability
		c.ReleaseCinema[i], c.ReleaseDigital[i], c.ReleasePhysical[i] = m.ReleaseCinema, m.ReleaseDigital, m.ReleasePhysical
		c.TagIDs[i] = tagIDs(m.TagIDs)
	}
	return c
}

// ----------------------------------------------------------------- detail

// Person is a cast or crew member of a movie.
type Person struct {
	TmdbID     int     `json:"tmdbId"`
	Name       string  `json:"name"`
	Character  string  `json:"character,omitempty"`
	Job        string  `json:"job,omitempty"`
	Department string  `json:"department,omitempty"`
	ProfileURL *string `json:"profileUrl" doc:"Portrait at w185"`
}

// Video is a trailer or teaser hosted on YouTube.
type Video struct {
	Name        string `json:"name"`
	YouTubeKey  string `json:"youtubeKey" doc:"Embed with https://www.youtube-nocookie.com/embed/{youtubeKey}"`
	Type        string `json:"type" enum:"trailer,teaser"`
	Official    bool   `json:"official"`
	Language    string `json:"language,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
}

// CollectionPart is a library movie of a collection.
type CollectionPart struct {
	ID            int64   `json:"id"`
	TmdbID        int64   `json:"tmdbId"`
	Title         string  `json:"title"`
	Year          int64   `json:"year"`
	State         string  `json:"state"`
	Monitored     bool    `json:"monitored"`
	PosterURL     *string `json:"posterUrl"`
	DominantColor *string `json:"dominantColor"`
}

// CollectionSummary is the collection of a movie, with its movies in the library.
// The complete list of parts (TMDB) is at GET /collections/{id}.
type CollectionSummary struct {
	ID        int64            `json:"id"`
	TmdbID    int64            `json:"tmdbId"`
	Name      string           `json:"name"`
	Monitored bool             `json:"monitored"`
	PosterURL *string          `json:"posterUrl"`
	InLibrary []CollectionPart `json:"inLibrary" doc:"Movies of the collection in the library, oldest first"`
}

// FileMedia is the technical description of a file, from its release name.
type FileMedia struct {
	Resolution         string   `json:"resolution,omitempty"`
	Source             string   `json:"source,omitempty"`
	Modifier           string   `json:"modifier,omitempty"`
	VideoCodec         string   `json:"videoCodec,omitempty"`
	BitDepth           int      `json:"bitDepth,omitempty"`
	HDR                []string `json:"hdr"`
	DolbyVisionProfile int      `json:"dvProfile,omitempty"`
	Audio              []string `json:"audio"`
	Atmos              bool     `json:"atmos"`
	Channels           string   `json:"channels,omitempty"`
	Streaming          string   `json:"streaming,omitempty"`
	Container          string   `json:"container,omitempty"`
	ThreeD             bool     `json:"threeD"`
}

// MovieFile is the current file of a movie.
type MovieFile struct {
	ID           int64      `json:"id"`
	RelativePath string     `json:"relativePath" doc:"Relative to the movie folder"`
	FullPath     string     `json:"fullPath"`
	Size         int64      `json:"size"`
	Quality      string     `json:"quality"`
	QualityName  string     `json:"qualityName"`
	Revision     int64      `json:"revision"`
	Edition      string     `json:"edition"`
	Languages    stringList `json:"languages"`
	ReleaseGroup string     `json:"releaseGroup"`
	SceneName    string     `json:"sceneName"`
	CustomScore  int64      `json:"customScore" doc:"Score given at import"`
	ImportedAt   time.Time  `json:"importedAt"`
	Media        FileMedia  `json:"media"`
	// ScoreBreakdown is the score with today's rules and profile: it differs
	// from customScore when the rules changed since the import.
	ScoreBreakdown ScoreBreakdown `json:"scoreBreakdown"`
}

// ScoreBreakdown is a custom score with the contribution of each matched rule.
type ScoreBreakdown struct {
	Total int         `json:"total"`
	Rules []ScoreRule `json:"rules" doc:"Matched rules in rule order; score 0 when the profile does not score the rule"`
}

// ScoreRule is the contribution of one scoring rule.
type ScoreRule struct {
	Rule  string `json:"rule"`
	Score int    `json:"score"`
}

func breakdownOf(b *quality.Breakdown) ScoreBreakdown {
	out := ScoreBreakdown{Rules: []ScoreRule{}}
	if b == nil {
		return out
	}
	out.Total = b.Total
	for _, r := range b.Rules {
		out.Rules = append(out.Rules, ScoreRule{Rule: r.Rule, Score: r.Score})
	}
	return out
}

// ReleaseRegions are the regions (ISO 3166-1 alpha-2, e.g. US) the release
// dates and certification were taken from; null when unknown.
type ReleaseRegions struct {
	Cinema        *string `json:"cinema"`
	Digital       *string `json:"digital"`
	Physical      *string `json:"physical"`
	Certification *string `json:"certification"`
}

// Studio is a production company.
type Studio struct {
	TmdbID  int    `json:"tmdbId"`
	Name    string `json:"name"`
	Country string `json:"country,omitempty" doc:"ISO 3166-1 alpha-2"`
}

// MovieDetail is everything the movie page shows.
type MovieDetail struct {
	Movie
	PosterURL          *string            `json:"posterUrl" doc:"Poster at w342"`
	PosterURLLarge     *string            `json:"posterUrlLarge" doc:"Poster at w780"`
	BackdropURL        *string            `json:"backdropUrl" doc:"Backdrop at w1280"`
	Tagline            string             `json:"tagline"`
	Homepage           string             `json:"homepage"`
	Keywords           []string           `json:"keywords"`
	StatusReason       string             `json:"statusReason" doc:"Why the release stage is what it is"`
	ReleaseRegions     ReleaseRegions     `json:"releaseRegions" doc:"Filled at the next metadata refresh for movies added before this field existed"`
	Studios            []Studio           `json:"studios" doc:"Production companies, TMDB order"`
	Cast               []Person           `json:"cast" doc:"Billing order"`
	Crew               []Person           `json:"crew"`
	Videos             []Video            `json:"videos" doc:"Preferred first"`
	Collection         *CollectionSummary `json:"collection"`
	File               *MovieFile         `json:"file"`
	QualityProfileName string             `json:"qualityProfileName"`
	RootFolderPath     string             `json:"rootFolderPath"`
	Failed             bool               `json:"failed" doc:"The newest download failed after the current file (if any)"`
	Download           *DownloadProgress  `json:"download"`
	History            []HistoryEntry     `json:"history" doc:"The 20 newest entries; more at /movies/{id}/history"`
}

type extraDoc struct {
	metadata.Extra
	StatusReason   string                  `json:"statusReason"`
	ReleaseRegions *library.ReleaseRegions `json:"releaseRegions"`
}

func (s *Server) movieDetailOf(d *library.MovieDetails, live map[int64]*acquire.QueueItem) MovieDetail {
	m := &d.Movie
	out := MovieDetail{
		Movie:          movieDetailsOf(d),
		PosterURL:      s.imageURL(imagecache.Poster, gridPosterSize, m.PosterPath),
		PosterURLLarge: s.imageURL(imagecache.Poster, detailPosterSize, m.PosterPath),
		BackdropURL:    s.imageURL(imagecache.Backdrop, backdropSize, m.BackdropPath),
		Keywords:       []string{}, Cast: []Person{}, Crew: []Person{}, Videos: []Video{}, History: []HistoryEntry{},
		Studios:            []Studio{},
		QualityProfileName: d.ProfileName, RootFolderPath: d.RootFolderPath,
	}
	var x extraDoc
	if err := json.Unmarshal([]byte(m.Extra), &x); err == nil {
		out.Tagline, out.Homepage, out.StatusReason = x.Tagline, x.Homepage, x.StatusReason
		if x.Keywords != nil {
			out.Keywords = x.Keywords
		}
		for _, c := range x.Cast {
			out.Cast = append(out.Cast, s.personOf(&c))
		}
		for _, c := range x.Crew {
			out.Crew = append(out.Crew, s.personOf(&c))
		}
		if r := x.ReleaseRegions; r != nil {
			out.ReleaseRegions = ReleaseRegions{
				Cinema: optStr(r.Cinema), Digital: optStr(r.Digital), Physical: optStr(r.Physical), Certification: optStr(r.Certification),
			}
		}
		for _, c := range x.Companies {
			out.Studios = append(out.Studios, Studio{TmdbID: c.TMDBID, Name: c.Name, Country: c.Country})
		}
		for _, v := range x.Videos {
			out.Videos = append(out.Videos, Video{
				Name: v.Name, YouTubeKey: v.Key, Type: string(v.Type), Official: v.Official,
				Language: v.Language, PublishedAt: v.PublishedAt,
			})
		}
	}
	if c := d.Collection; c != nil {
		cs := &CollectionSummary{
			ID: c.ID, TmdbID: c.TmdbID, Name: c.Name, Monitored: c.Monitored,
			PosterURL: s.imageURL(imagecache.Poster, gridPosterSize, c.PosterPath), InLibrary: []CollectionPart{},
		}
		for _, p := range d.CollectionMovies {
			cs.InLibrary = append(cs.InLibrary, CollectionPart{
				ID: p.ID, TmdbID: p.TmdbID, Title: p.Title, Year: p.Year, State: p.State, Monitored: p.Monitored,
				PosterURL: s.imageURL(imagecache.Poster, gridPosterSize, p.PosterPath), DominantColor: p.DominantColor,
			})
		}
		out.Collection = cs
	}
	if f := d.File; f != nil {
		out.File = &MovieFile{
			ID: f.ID, RelativePath: f.RelativePath, FullPath: filepath.Join(d.RootFolderPath, m.Path, f.RelativePath),
			Size: f.Size, Quality: f.Quality, QualityName: quality.ID(f.Quality).Name(), Revision: f.Revision,
			Edition: f.Edition, Languages: stringList(f.Languages), ReleaseGroup: f.ReleaseGroup, SceneName: f.SceneName,
			CustomScore: f.CustomScore, ImportedAt: ts(f.ImportedAt), Media: fileMediaOf(f),
			ScoreBreakdown: breakdownOf(d.FileScore),
		}
	}
	for i := range d.History {
		out.History = append(out.History, historyOf(&d.History[i]))
	}
	out.Failed = d.LastDownloadFailed
	if q, ok := live[m.ID]; ok {
		out.Download = downloadOf(q)
	}
	return out
}

func (s *Server) personOf(c *metadata.Credit) Person {
	return Person{
		TmdbID: c.Person.TMDBID, Name: c.Person.Name, Character: c.Character, Job: c.Job, Department: c.Department,
		ProfileURL: s.imageURLOf(imagecache.Profile, profileSize, c.Person.ProfilePath),
	}
}

// fileMediaOf reads the technical fields of a file from its parsed release.
func fileMediaOf(f *db.MovieFile) FileMedia {
	var p struct {
		Resolution         string   `json:"resolution"`
		Source             string   `json:"source"`
		Modifier           string   `json:"modifier"`
		VideoCodec         string   `json:"videoCodec"`
		BitDepth           int      `json:"bitDepth"`
		HDR                []string `json:"hdr"`
		DolbyVisionProfile int      `json:"dvProfile"`
		Audio              []string `json:"audio"`
		Atmos              bool     `json:"atmos"`
		Channels           string   `json:"channels"`
		Streaming          string   `json:"streaming"`
		Container          string   `json:"container"`
		ThreeD             bool     `json:"threeD"`
	}
	_ = json.Unmarshal([]byte(f.Parsed), &p) // stored by the importer; a bad value shows as unknown
	out := FileMedia(p)
	if out.HDR == nil {
		out.HDR = []string{}
	}
	for i, h := range out.HDR {
		out.HDR[i] = strings.ToUpper(h) // display names, as in the lists
	}
	if out.Audio == nil {
		out.Audio = []string{}
	}
	return out
}
