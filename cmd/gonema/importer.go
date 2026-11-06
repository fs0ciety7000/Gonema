package main

import (
	"context"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/importer"
)

// importAdapter lets the acquisition service import completed downloads with
// the importer. A refusal (sample, wrong movie, not an upgrade) is Imported
// false with a nil error; an error means the import could not run.
type importAdapter struct{ imp *importer.Service }

// Import implements acquire.ImportFunc.
func (a importAdapter) Import(ctx context.Context, req acquire.ImportRequest) (acquire.ImportResult, error) {
	r, err := a.imp.Import(ctx, importer.Request{
		DownloadID: req.DownloadID, MovieID: req.MovieID, LocalPath: req.LocalPath,
		ReleaseTitle: req.ReleaseTitle, Protocol: req.Protocol, ClientSeeding: req.ClientSeeding,
	})
	if err != nil {
		return acquire.ImportResult{}, err
	}
	return acquire.ImportResult{Imported: r.Imported, Upgraded: r.Upgraded, FileID: r.FileID, Reason: r.Reason}, nil
}
