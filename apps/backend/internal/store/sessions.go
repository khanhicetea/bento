package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

type Session struct {
	TokenHash string
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func InsertSession(ctx context.Context, q Q, s Session) error {
	_, err := q.ExecContext(ctx, "INSERT INTO sessions(token_hash, csrf_token, created_at, expires_at) VALUES(?,?,?,?)",
		s.TokenHash, s.CSRFToken, platform.FormatTime(s.CreatedAt), platform.FormatTime(s.ExpiresAt))
	return err
}

// GetLiveSession returns an unexpired, unrevoked session.
func GetLiveSession(ctx context.Context, q Q, tokenHash string, at time.Time) (Session, error) {
	var s Session
	var created, expires string
	err := q.QueryRowContext(
		ctx,
		"SELECT token_hash, csrf_token, created_at, expires_at FROM sessions WHERE token_hash=? AND revoked=0",
		tokenHash,
	).
		Scan(&s.TokenHash, &s.CSRFToken, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	s.CreatedAt = platform.ParseTime(created)
	s.ExpiresAt = platform.ParseTime(expires)
	if !at.Before(s.ExpiresAt) {
		return s, ErrNotFound
	}
	return s, nil
}

func RevokeSession(ctx context.Context, q Q, tokenHash string) error {
	_, err := q.ExecContext(ctx, "UPDATE sessions SET revoked=1 WHERE token_hash=?", tokenHash)
	return err
}

func RevokeAllSessions(ctx context.Context, q Q) error {
	_, err := q.ExecContext(ctx, "UPDATE sessions SET revoked=1")
	return err
}

func PruneSessions(ctx context.Context, q Q, at time.Time) error {
	_, err := q.ExecContext(
		ctx,
		"DELETE FROM sessions WHERE expires_at < ? OR revoked=1",
		platform.FormatTime(at.Add(-24*time.Hour)),
	)
	return err
}
