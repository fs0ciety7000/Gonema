package store

import (
	"context"
	"errors"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// TestMigration7Auth checks migration 00007: the account survives a down and
// up; sessions and API keys are recreated empty; the constraints hold.
func TestMigration7Auth(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	u, err := s.Write().CreateUser(ctx, db.CreateUserParams{Username: "Admin", PasswordHash: "$argon2id$v=19$m=64,t=1,p=1$x$y", CreatedAt: 1, PasswordChangedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	hash := make([]byte, 32)
	if _, err := s.Write().CreateSession(ctx, db.CreateSessionParams{TokenHash: hash, UserID: u.ID, CreatedAt: 1, LastSeenAt: 1, ExpiresAt: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write().CreateSession(ctx, db.CreateSessionParams{TokenHash: hash, UserID: u.ID, CreatedAt: 1, LastSeenAt: 1, ExpiresAt: 2}); !errors.Is(MapError(err), ErrConflict) {
		t.Errorf("duplicate token hash: %v", err)
	}
	if _, err := s.Write().CreateSession(ctx, db.CreateSessionParams{TokenHash: []byte("short"), UserID: u.ID, CreatedAt: 1, LastSeenAt: 1, ExpiresAt: 2}); !errors.Is(MapError(err), ErrInvalid) {
		t.Errorf("short token hash: %v", err)
	}
	if _, err := s.Write().CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: u.ID, KeyHash: hash, Last4: "abcd", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	other := append([]byte{1}, hash[1:]...)
	if _, err := s.Write().CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: u.ID, KeyHash: other, Last4: "abcd", CreatedAt: 1}); !errors.Is(MapError(err), ErrConflict) {
		t.Errorf("second key of a user: %v", err)
	}
	if got, err := s.Read().GetUserByUsername(ctx, "admin"); err != nil || got.ID != u.ID {
		t.Errorf("case-insensitive username: %v", err)
	}

	if err := s.MigrateDownTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil || n != 1 {
		t.Errorf("users after down = %d %v", n, err)
	}
	if err := s.exec(t, `INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES ('x', ?, 2, 1)`, u.ID); err != nil {
		t.Errorf("version 6 sessions: %v", err)
	}
	if err := s.MigrateUp(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil || n != 0 {
		t.Errorf("sessions after up = %d %v", n, err)
	}
	if c, err := s.Read().CountUsers(ctx); err != nil || c != 1 {
		t.Errorf("users after up = %d %v", c, err)
	}
}
