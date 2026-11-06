package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/importer"
)

// File operations on library movies: rename to the naming templates, move to
// another root folder, delete the file. Every file goes through internal/fsx;
// deleted files go to the recycle bin.

type moveBody struct {
	RootFolderID int64 `json:"rootFolderId" minimum:"1"`
	MoveFiles    bool  `json:"moveFiles" doc:"Move the files into a folder named with the template (refused if it exists); false only changes where Gonema looks"`
}

type moveInput struct {
	IDParam
	Preview bool `query:"preview" doc:"Only return the planned operations"`
	Body    moveBody
}

type bulkMoveInput struct {
	Preview bool `query:"preview" doc:"Only return the planned operations"`
	Body    struct {
		IDs          []int64 `json:"ids" minItems:"1" maxItems:"1000"`
		RootFolderID int64   `json:"rootFolderId" minimum:"1"`
		MoveFiles    bool    `json:"moveFiles"`
	}
}

type bulkRenameInput struct {
	Preview bool `query:"preview" doc:"Only return the planned operations"`
	Body    struct {
		IDs []int64 `json:"ids" minItems:"1" maxItems:"1000"`
	}
}

// BulkFileResult is the outcome of a bulk rename or move.
type BulkFileResult struct {
	Results []MoveOutcome `json:"results" doc:"One per movie, in request order"`
}

func (r *importRoutes) fileOps() {
	tags := []string{"Movie files"}
	huma.Register(r.s.api, huma.Operation{
		OperationID: "move-movie", Method: http.MethodPost, Path: "/movies/{id}/move", Summary: "Move a movie to another root folder",
		Description: "With moveFiles, every file of the movie folder moves into a folder named with the folder template " +
			"(a rename on one file system; across file systems each file is copied, verified, then its source removed). " +
			"Refused (409) when the destination folder exists. Written to history (file_renamed). preview=true only plans.",
		Tags: tags,
	}, func(ctx context.Context, in *moveInput) (*struct{ Body importer.Relocation }, error) {
		if in.Preview {
			p, err := r.svc.PlanMove(ctx, in.ID, in.Body.RootFolderID, in.Body.MoveFiles)
			if err != nil {
				return nil, r.problem(ctx, err, false)
			}
			return &struct{ Body importer.Relocation }{importer.Relocation{Plan: p, Done: []importer.FileOp{}}}, nil
		}
		rel, err := r.svc.Move(ctx, in.ID, in.Body.RootFolderID, in.Body.MoveFiles)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importer.Relocation }{rel}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "move-movies", Method: http.MethodPost, Path: "/movies/move", Summary: "Move several movies",
		Description: "Each movie as POST /movies/{id}/move; a failure is reported for its movie and the others go on.",
		Tags:        tags,
	}, func(ctx context.Context, in *bulkMoveInput) (*struct{ Body BulkFileResult }, error) {
		out := BulkFileResult{Results: []MoveOutcome{}}
		for _, id := range in.Body.IDs {
			out.Results = append(out.Results, r.s.moveOne(ctx, id, in.Body.RootFolderID, in.Body.MoveFiles, in.Preview))
			if ctx.Err() != nil {
				break
			}
		}
		return &struct{ Body BulkFileResult }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "preview-movie-rename", Method: http.MethodGet, Path: "/movies/{id}/rename/preview",
		Summary:     "Preview the rename of a movie",
		Description: "Current and new names with the naming templates: folder, file and its subtitles. problems lists what would block it.",
		Tags:        tags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body importer.Plan }, error) {
		p, err := r.svc.PlanRename(ctx, in.ID)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importer.Plan }{p}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "rename-movie", Method: http.MethodPost, Path: "/movies/{id}/rename", Summary: "Rename a movie to the templates",
		Description: "Renames the folder (same root folder), the file and its subtitles; refused (409) when a new name is taken. " +
			"Written to history (file_renamed); media servers are asked to rescan.",
		Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body importer.Relocation }, error) {
		rel, err := r.svc.Rename(ctx, in.ID)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importer.Relocation }{rel}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "rename-movies", Method: http.MethodPost, Path: "/movies/rename", Summary: "Rename several movies",
		Description: "Each movie as POST /movies/{id}/rename; preview=true only plans.", Tags: tags,
	}, func(ctx context.Context, in *bulkRenameInput) (*struct{ Body BulkFileResult }, error) {
		out := BulkFileResult{Results: []MoveOutcome{}}
		for _, id := range in.Body.IDs {
			o := MoveOutcome{MovieID: id}
			var rel importer.Relocation
			var err error
			if in.Preview {
				var p importer.Plan
				if p, err = r.svc.PlanRename(ctx, id); err == nil {
					rel = importer.Relocation{Plan: p, Done: []importer.FileOp{}}
					if len(p.Problems) > 0 {
						o.Error = p.Problems[0]
					}
				}
			} else {
				rel, err = r.svc.Rename(ctx, id)
			}
			if err != nil {
				o.Error = errText(err)
				if o.Error == "" {
					o.Error = r.problem(ctx, err, false).Error()
				}
			}
			if rel.MovieID != 0 {
				o.Relocation = &rel
			}
			o.OK = o.Error == ""
			out.Results = append(out.Results, o)
			if ctx.Err() != nil {
				break
			}
		}
		return &struct{ Body BulkFileResult }{out}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "delete-movie-file", Method: http.MethodDelete, Path: "/movies/{id}/file", Summary: "Delete the file of a movie",
		Description: "Moves the file and its subtitles to the recycle bin (never a permanent deletion) and forgets it: the movie is missing again. " +
			"Requires confirm=true (422 otherwise). Written to history (file_deleted).",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		IDParam
		Confirm bool `query:"confirm" doc:"Required: the user confirmed the deletion"`
	}) (*struct{ Body importer.Deletion }, error) {
		d, err := r.svc.DeleteFile(ctx, in.ID, in.Confirm)
		if err != nil {
			return nil, r.problem(ctx, err, false)
		}
		return &struct{ Body importer.Deletion }{d}, nil
	})
}

// fileProblem maps the errors of file operations outside the import routes.
func (s *Server) fileProblem(ctx context.Context, err error) error {
	return (&importRoutes{s: s, svc: s.files}).problem(ctx, err, false)
}
