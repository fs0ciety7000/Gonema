package acquire

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// BlocklistItem is a blocklist entry.
type BlocklistItem struct {
	ID           int64     `json:"id"`
	MovieID      *int64    `json:"movieId" doc:"null: the entry applies to every movie"`
	Kind         string    `json:"kind" enum:"release,group"`
	SourceTitle  string    `json:"sourceTitle"`
	ReleaseGroup *string   `json:"releaseGroup"`
	IndexerID    *int64    `json:"indexerId"`
	Protocol     *string   `json:"protocol"`
	InfoHash     *string   `json:"infoHash"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"createdAt"`
}

func blocklistView(b *db.Blocklist) BlocklistItem {
	return BlocklistItem{
		ID: b.ID, MovieID: b.MovieID, Kind: b.Kind, SourceTitle: b.SourceTitle, ReleaseGroup: b.ReleaseGroup,
		IndexerID: b.IndexerID, Protocol: b.Protocol, InfoHash: b.InfoHash, Reason: b.Reason,
		CreatedAt: store.FromMillis(b.CreatedAt),
	}
}

// BlocklistFilter selects blocklist entries; zero values disable a filter.
type BlocklistFilter struct {
	MovieID int64
	Kind    string // release or group
	Before  int64  // keyset: entries with a lower id; 0 = newest
	Limit   int64
}

// ListBlocklist returns blocklist entries, newest first.
func (s *Service) ListBlocklist(ctx context.Context, f BlocklistFilter) ([]BlocklistItem, error) {
	p := db.ListBlocklistParams{BeforeID: f.Before, Limit: f.Limit}
	if p.BeforeID <= 0 {
		p.BeforeID = math.MaxInt64
	}
	if p.Limit <= 0 {
		p.Limit = 100
	}
	if f.MovieID > 0 {
		p.MovieID = &f.MovieID
	}
	if f.Kind != "" {
		if f.Kind != string(decision.BlockRelease) && f.Kind != string(decision.BlockGroup) {
			return nil, invalid("The kind must be release or group.")
		}
		p.Kind = &f.Kind
	}
	rows, err := s.st.Read().ListBlocklist(ctx, p)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("acquire: list blocklist: %w", err))
	}
	out := make([]BlocklistItem, 0, len(rows))
	for i := range rows {
		out = append(out, blocklistView(&rows[i]))
	}
	return out, nil
}

// DeleteBlocklist removes one entry: the release may be grabbed again.
func (s *Service) DeleteBlocklist(ctx context.Context, id int64) error {
	if err := store.RequireRow(s.st.Write().DeleteBlocklist(ctx, id)); err != nil {
		return mapNotFound(err, "Blocklist entry %d does not exist.", id)
	}
	s.log.Info("blocklist entry removed", slog.Int64("blocklist_id", id))
	return nil
}

// ClearMovieBlocklist removes every entry of a movie (global entries stay).
func (s *Service) ClearMovieBlocklist(ctx context.Context, movieID int64) (int64, error) {
	n, err := s.st.Write().ClearMovieBlocklist(ctx, &movieID)
	if err != nil {
		return 0, store.MapError(fmt.Errorf("acquire: clear blocklist of movie %d: %w", movieID, err))
	}
	s.log.Info("blocklist of a movie cleared", slog.Int64("movie_id", movieID), slog.Int64("entries", n))
	return n, nil
}

// BlockGroup blocks every release of a group, for one movie (movieID > 0) or
// for all movies (movieID 0).
func (s *Service) BlockGroup(ctx context.Context, group string, movieID int64, reason string) (BlocklistItem, error) {
	group = strings.TrimSpace(group)
	if group == "" || len(group) > 100 {
		return BlocklistItem{}, invalid("The release group must be 1 to 100 characters long.")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Release group blocked by the user."
	}
	p := db.InsertBlocklistParams{
		Kind: string(decision.BlockGroup), SourceTitle: group, ReleaseGroup: &group, Reason: reason,
		CreatedAt: store.Millis(s.now()),
	}
	if movieID > 0 {
		if _, err := s.st.Read().GetMovie(ctx, movieID); err != nil {
			return BlocklistItem{}, mapNotFound(err, "Movie %d is not in the library.", movieID)
		}
		p.MovieID = &movieID
	}
	var row db.Blocklist
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		var err error
		if row, err = q.InsertBlocklist(ctx, p); err != nil {
			return fmt.Errorf("insert blocklist: %w", err)
		}
		if movieID == 0 {
			return nil
		}
		return s.appendHistory(ctx, q, historyEntry{
			movieID: movieID, event: HistoryBlocklisted, title: group,
			data: map[string]any{"reason": reason, "kind": "group", "group": group},
		})
	})
	if err != nil {
		return BlocklistItem{}, fmt.Errorf("acquire: block group %q: %w", group, err)
	}
	s.log.Info("release group blocked", slog.String("group", group), slog.Int64("movie_id", movieID))
	return blocklistView(&row), nil
}
