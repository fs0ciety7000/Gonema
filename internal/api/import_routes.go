package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/jobs"

	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/naming"
)

// RegisterImportRoutes adds the import endpoints: import settings, manual
// import, existing library import, recycle bin and naming templates. Call it
// once, after New and before serving.
func (s *Server) RegisterImportRoutes(svc *importer.Service) {
	r := &importRoutes{s: s, svc: svc}
	s.files = svc
	r.settings()
	r.manual()
	r.library()
	r.recycle()
	r.naming()
	r.fileOps()
}

type importRoutes struct {
	s   *Server
	svc *importer.Service
}

// problem maps importer errors; the others go through Server.problem.
func (r *importRoutes) problem(ctx context.Context, err error, upstream bool) error {
	msg := errText(err)
	switch {
	case msg != "" && (errors.Is(err, importer.ErrInvalid) || errors.Is(err, importer.ErrOutsideRoots) || errors.Is(err, importer.ErrConfirmRequired)):
		return huma.Error422UnprocessableEntity(msg)
	case msg != "" && errors.Is(err, importer.ErrConflict):
		return huma.Error409Conflict(msg)
	case msg != "" && errors.Is(err, importer.ErrNotFound):
		return huma.Error404NotFound(msg)
	case msg != "" && errors.Is(err, importer.ErrFileSystem):
		r.s.log.Error("import file operation failed", slog.String("request_id", RequestID(ctx)), slog.Any("error", err))
		return huma.Error500InternalServerError(msg + " (request id " + RequestID(ctx) + ")")
	}
	return r.s.problem(ctx, err, upstream)
}

func errText(err error) string {
	var um userMessage
	if errors.As(err, &um) {
		return um.UserMessage()
	}
	return ""
}

// ----------------------------------------------------------------- settings

type importSettingsBody struct {
	Mode                 string   `json:"mode" enum:"move,copy" doc:"Usenet downloads and torrents no longer seeding"`
	TorrentMode          string   `json:"torrentMode" enum:"hardlink,copy" doc:"Torrents still seeding (hardlink falls back to a copy across file systems)"`
	AllowedRoots         []string `json:"allowedRoots" maxItems:"50" doc:"Folders imports may read from, as seen by Gonema; remote path mappings are allowed too"`
	RecycleBin           string   `json:"recycleBin" maxLength:"4096" doc:"Single recycle bin; empty for a .gonema-recycle folder in each root folder"`
	RecycleRetentionDays int      `json:"recycleRetentionDays" minimum:"1" maximum:"3650" doc:"Default age of the files a purge deletes"`
	ImportSubtitles      bool     `json:"importSubtitles"`
}

func settingsBody(set *importer.Settings) importSettingsBody {
	return importSettingsBody{
		Mode: string(set.Mode), TorrentMode: string(set.TorrentMode), AllowedRoots: set.AllowedRoots,
		RecycleBin: set.RecycleBin, RecycleRetentionDays: set.RecycleRetentionDays, ImportSubtitles: set.ImportSubtitles,
	}
}

func (r *importRoutes) settings() {
	tags := []string{"Import"}
	huma.Register(r.s.api, huma.Operation{
		OperationID: "get-import-settings", Method: http.MethodGet, Path: "/import/settings",
		Summary: "Import settings", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body importSettingsBody }, error) {
		set, err := r.svc.Settings(ctx)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importSettingsBody }{settingsBody(&set)}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "put-import-settings", Method: http.MethodPut, Path: "/import/settings",
		Summary: "Change the import settings", Tags: tags,
	}, func(ctx context.Context, in *struct{ Body importSettingsBody }) (*struct{ Body importSettingsBody }, error) {
		b := in.Body
		set, err := r.svc.SetSettings(ctx, importer.Settings{
			Mode: fsx.Mode(b.Mode), TorrentMode: fsx.Mode(b.TorrentMode), AllowedRoots: b.AllowedRoots,
			RecycleBin: b.RecycleBin, RecycleRetentionDays: b.RecycleRetentionDays, ImportSubtitles: b.ImportSubtitles,
		})
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importSettingsBody }{settingsBody(&set)}, nil
	})
}

// ----------------------------------------------------------------- manual import

type scanInput struct {
	Body struct {
		Path string `json:"path" minLength:"1" maxLength:"4096" doc:"Folder or file inside an allowed import folder"`
	}
}

type manualInput struct {
	Body struct {
		Selections []importer.Selection `json:"selections" minItems:"1" maxItems:"100"`
	}
}

func (r *importRoutes) manual() {
	tags := []string{"Import"}
	huma.Register(r.s.api, huma.Operation{
		OperationID: "scan-import", Method: http.MethodPost, Path: "/import/scan",
		Summary: "List the video files of a folder", Tags: tags,
		Description: "Parsed name, quality, likely movie and rejection reasons of each video file. Nothing is changed on disk.",
	}, func(ctx context.Context, in *scanInput) (*struct{ Body []importer.ScanCandidate }, error) {
		out, err := r.svc.Scan(ctx, in.Body.Path)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body []importer.ScanCandidate }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "import-manual", Method: http.MethodPost, Path: "/import/manual",
		Summary: "Import chosen files", Tags: tags,
		Description: "Each file is imported for the movie (and quality) chosen. The upgrade rules apply unless replace is set; a replaced file goes to the recycle bin.",
	}, func(ctx context.Context, in *manualInput) (*struct{ Body []importer.ManualResult }, error) {
		out, err := r.svc.ImportManual(ctx, in.Body.Selections)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body []importer.ManualResult }{out}, nil
	})
}

// ----------------------------------------------------------------- library import

type libraryScanInput struct {
	Body struct {
		RootFolderID int64  `json:"rootFolderId" minimum:"1"`
		Path         string `json:"path,omitempty" maxLength:"4096" doc:"Sub-path inside the root folder (from GET /library-import/folders); empty for the whole root folder. A movie folder (a video inside) is scanned as one folder"`
	}
}

type libraryConfirmInput struct {
	Body struct {
		RootFolderID int64                       `json:"rootFolderId" minimum:"1"`
		Selections   []importer.LibrarySelection `json:"selections" minItems:"1" maxItems:"500"`
	}
}

type scanIDParam struct {
	ScanID int64 `path:"scanId" minimum:"1"`
}

func (r *importRoutes) library() {
	tags := []string{"Import"}
	r.svc.SetImageURL(func(p string) *string { return r.s.imageURLOf(imagecache.Poster, gridPosterSize, p) })
	huma.Register(r.s.api, huma.Operation{
		OperationID: "scan-library-import", Method: http.MethodPost, Path: "/library-import/scan",
		Summary: "Find unmanaged movie folders (waits for the result)", Tags: tags,
		Description: "Folders of a root folder (or of a sub-path) that hold a movie Gonema does not manage yet, with the TMDB movies they may be. " +
			"Nothing is changed. For a large library use POST /library-import/scans: a background job that streams its progress.",
	}, func(ctx context.Context, in *libraryScanInput) (*struct{ Body []importer.LibraryFolder }, error) {
		out, err := r.svc.ScanLibraryPath(ctx, in.Body.RootFolderID, in.Body.Path, nil)
		if err != nil {
			return nil, r.problem(ctx, err, true)
		}
		return &struct{ Body []importer.LibraryFolder }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "start-library-scan", Method: http.MethodPost, Path: "/library-import/scans",
		Summary: "Scan existing movie folders in the background", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues a scan job of a root folder or of a sub-path. It goes on when the page is left. Each described folder streams " +
			"as a libraryimport.folder event {scanId, done, total, folder}; libraryimport.progress and libraryimport.finished carry the scan. " +
			"Results stay for a later review (the last 20 scans): GET /library-import/scans/{scanId}. DELETE cancels it (the folders found are kept). " +
			"Nothing is changed on disk.",
	}, func(ctx context.Context, in *libraryScanInput) (*struct{ Body importer.LibraryScan }, error) {
		sc, err := r.svc.StartLibraryScan(ctx, in.Body.RootFolderID, in.Body.Path)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importer.LibraryScan }{sc}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "list-library-scans", Method: http.MethodGet, Path: "/library-import/scans",
		Summary: "Recent library scans", Description: "Newest first, without their folders.", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []importer.LibraryScan }, error) {
		out, err := r.svc.LibraryScans(ctx)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body []importer.LibraryScan }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "get-library-scan", Method: http.MethodGet, Path: "/library-import/scans/{scanId}",
		Summary: "A library scan with its folders", Tags: tags,
		Description: "Folders described so far while it runs; all of them once done.",
	}, func(ctx context.Context, in *scanIDParam) (*struct{ Body importer.LibraryScan }, error) {
		out, err := r.svc.LibraryScanByID(ctx, in.ScanID)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importer.LibraryScan }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "cancel-library-scan", Method: http.MethodDelete, Path: "/library-import/scans/{scanId}",
		Summary: "Cancel a library scan", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Stops the scan job; the folders described so far are kept (libraryimport.finished with status cancelled). 409 when it already ended.",
	}, func(ctx context.Context, in *scanIDParam) (*struct{ Body importer.LibraryScan }, error) {
		sc, err := r.svc.LibraryScanByID(ctx, in.ScanID)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		if sc.JobID == nil || (sc.Status != importer.ScanPending && sc.Status != importer.ScanRunning) {
			return nil, huma.Error409Conflict("This scan already ended.")
		}
		if err := r.s.d.Jobs.Cancel(ctx, *sc.JobID); err != nil {
			if errors.Is(err, jobs.ErrNotCancellable) {
				return nil, huma.Error409Conflict("This scan already ended.")
			}
			return nil, r.problem(ctx, err, false)
		}
		if sc, err = r.svc.LibraryScanByID(ctx, in.ScanID); err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importer.LibraryScan }{sc}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "list-library-import-folders", Method: http.MethodGet, Path: "/library-import/folders",
		Summary: "Folders of a root folder, for the folder picker", Tags: tags,
		Description: "Sub-folders of a root folder or of path inside it (hidden folders excluded); movie tells a folder that holds a video directly.",
	}, func(ctx context.Context, in *struct {
		RootFolderID int64  `query:"rootFolderId" required:"true" minimum:"1"`
		Path         string `query:"path" maxLength:"4096" doc:"Relative to the root folder; empty for the root folder"`
	}) (*struct{ Body []importer.Subfolder }, error) {
		out, err := r.svc.Subfolders(ctx, in.RootFolderID, in.Path)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body []importer.Subfolder }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "library-import-candidates", Method: http.MethodPost, Path: "/library-import/candidates",
		Summary: "Search TMDB for a folder (change match)", Tags: tags,
		Description: "Searches TMDB (a title, tmdb:<id> or an IMDb id) and scores each result against the folder and file names: " +
			"confidence and reason are relative to this folder, as in the scan. Nothing is changed.",
	}, func(ctx context.Context, in *struct {
		Body struct {
			RootFolderID int64  `json:"rootFolderId" minimum:"1"`
			Folder       string `json:"folder" minLength:"1" maxLength:"4096" doc:"Folder as listed by the scan"`
			Query        string `json:"query" minLength:"1" maxLength:"200"`
			Year         int    `json:"year,omitempty" minimum:"0" maximum:"2200"`
		}
	}) (*struct{ Body []importer.LibraryCandidate }, error) {
		b := in.Body
		out, err := r.svc.MatchCandidates(ctx, b.RootFolderID, b.Folder, b.Query, b.Year)
		if err != nil {
			return nil, r.problem(ctx, err, true)
		}
		return &struct{ Body []importer.LibraryCandidate }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "confirm-library-import", Method: http.MethodPost, Path: "/library-import/confirm",
		Summary: "Adopt movie folders", Tags: tags,
		Description: "Adds each movie to the library, pointed at its existing folder, and records its file where it is (mode keep, the default). " +
			"Mode move then renames the folder and file to the naming templates inside the root folder: a move, never a deletion; when a " +
			"step fails the files already moved are put back and the movie keeps its folder. Monitored movies are upgraded later up to " +
			"the profile cutoff; searchOnAdd searches right after the import. Each file is written to history. The summary counts the outcomes " +
			"and lists the collections the import completed.",
	}, func(ctx context.Context, in *libraryConfirmInput) (*struct{ Body importer.LibraryImport }, error) {
		out, err := r.svc.ImportLibrary(ctx, in.Body.RootFolderID, in.Body.Selections)
		if err != nil {
			return nil, r.problem(ctx, err, true)
		}
		return &struct{ Body importer.LibraryImport }{out}, nil
	})
}

// ----------------------------------------------------------------- recycle bin

type restoreInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"512" doc:"Item name, as listed"`
	}
}

type purgeInput struct {
	Confirm       bool `query:"confirm" doc:"Must be true: purged files are deleted for good"`
	OlderThanDays int  `query:"olderThanDays" minimum:"-1" maximum:"36500" default:"-1" doc:"Delete files recycled more than this many days ago; -1 = the retention of the settings (30 days by default), 0 = all"`
}

type purgeOutput struct {
	Body struct {
		Purged []importer.RecycleEntry `json:"purged"`
	}
}

func (r *importRoutes) recycle() {
	tags := []string{"Recycle bin"}
	huma.Register(r.s.api, huma.Operation{
		OperationID: "list-recycle-bin", Method: http.MethodGet, Path: "/recycle-bin",
		Summary: "Files in the recycle bin", Tags: tags,
		Description: "Files replaced by upgrades, with where they came from. They stay until a purge is requested.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []importer.RecycleEntry }, error) {
		out, err := r.svc.ListRecycle(ctx)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body []importer.RecycleEntry }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "restore-recycled", Method: http.MethodPost, Path: "/recycle-bin/restore",
		Summary: "Put a file back where it was", Tags: tags,
	}, func(ctx context.Context, in *restoreInput) (*struct {
		Body struct {
			Path string `json:"path"`
		}
	}, error,
	) {
		p, err := r.svc.RestoreRecycled(ctx, in.Body.Name)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		out := &struct {
			Body struct {
				Path string `json:"path"`
			}
		}{}
		out.Body.Path = p
		return out, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "purge-recycle-bin", Method: http.MethodPost, Path: "/recycle-bin/purge",
		Summary: "Delete recycled files for good", Tags: tags,
		Description: "Refused unless confirm=true. Never done automatically. Each deleted file is written to history.",
	}, func(ctx context.Context, in *purgeInput) (*purgeOutput, error) {
		purged, err := r.svc.PurgeRecycle(ctx, in.OlderThanDays, in.Confirm)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		out := &purgeOutput{}
		out.Body.Purged = purged
		return out, nil
	})
}

// ----------------------------------------------------------------- naming

// NamingTemplates are the folder and file name templates.
type NamingTemplates struct {
	FolderTemplate string `json:"folderTemplate" minLength:"1" maxLength:"1024"`
	FileTemplate   string `json:"fileTemplate" minLength:"1" maxLength:"1024"`
	RenameFiles    bool   `json:"renameFiles" doc:"false keeps the original file names"`
}

// NamingSettingsView is the naming settings with what the editor needs.
type NamingSettingsView struct {
	NamingTemplates
	Defaults NamingTemplates `json:"defaults"`
	Tokens   []naming.Token  `json:"tokens" doc:"Tokens of the file template"`
}

type previewInput struct {
	Body struct {
		NamingTemplates
		MovieID int64 `json:"movieId,omitempty" minimum:"0" doc:"Library movie to preview with; 0 = a built-in example (flagged example)"`
	}
}

func namingOf(n *importer.NamingSettings) NamingTemplates {
	return NamingTemplates{FolderTemplate: n.FolderTemplate, FileTemplate: n.FileTemplate, RenameFiles: n.RenameFiles}
}

func (r *importRoutes) naming() {
	tags := []string{"Settings"}
	view := func(n *importer.NamingSettings) NamingSettingsView {
		d := importer.DefaultNaming()
		return NamingSettingsView{NamingTemplates: namingOf(n), Defaults: namingOf(&d), Tokens: naming.Tokens()}
	}
	huma.Register(r.s.api, huma.Operation{
		OperationID: "get-naming", Method: http.MethodGet, Path: "/settings/naming",
		Summary: "Folder and file name templates", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body NamingSettingsView }, error) {
		n, err := r.svc.Naming(ctx)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body NamingSettingsView }{view(&n)}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "put-naming", Method: http.MethodPut, Path: "/settings/naming",
		Summary: "Change the name templates", Tags: tags,
		Description: "Applies to movies added and files imported from now on; existing files are not renamed.",
	}, func(ctx context.Context, in *struct{ Body NamingTemplates }) (*struct{ Body NamingSettingsView }, error) {
		n, err := r.svc.SetNaming(ctx, importer.NamingSettings{
			FolderTemplate: in.Body.FolderTemplate, FileTemplate: in.Body.FileTemplate, RenameFiles: in.Body.RenameFiles,
		})
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body NamingSettingsView }{view(&n)}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "preview-naming", Method: http.MethodPost, Path: "/settings/naming/preview",
		Summary: "Preview name templates", Tags: tags,
		Description: "Renders the templates for a library movie, or a built-in example. Invalid templates get a 422 listing every problem.",
	}, func(ctx context.Context, in *previewInput) (*struct{ Body importer.NamingPreview }, error) {
		b := in.Body
		p, err := r.svc.PreviewNaming(ctx, importer.NamingSettings{
			FolderTemplate: b.FolderTemplate, FileTemplate: b.FileTemplate, RenameFiles: b.RenameFiles,
		}, b.MovieID)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importer.NamingPreview }{p}, nil
	})
}
