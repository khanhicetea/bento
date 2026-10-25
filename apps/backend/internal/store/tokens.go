package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// APIToken is a scoped bearer credential for automation (MCP clients,
// scripts). Only the SHA-256 of the secret is stored.
type APIToken struct {
	ID         string
	Name       string
	Scope      string
	TokenHash  string
	CreatedAt  time.Time
	ExpiresAt  time.Time // zero: never expires
	LastUsedAt time.Time // zero: never used
}

const apiTokensTable = `
CREATE TABLE api_tokens (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  scope        TEXT NOT NULL CHECK (scope IN ('read','operate','admin')),
  token_hash   TEXT NOT NULL UNIQUE,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL DEFAULT '',
  last_used_at TEXT NOT NULL DEFAULT ''
) STRICT;
`

// InsertAPIToken stores a new token. A duplicate name is a conflict.
func InsertAPIToken(ctx context.Context, q Q, t APIToken) error {
	_, err := q.ExecContext(ctx,
		"INSERT INTO api_tokens(id, name, scope, token_hash, created_at, expires_at) VALUES(?,?,?,?,?,?)",
		t.ID, t.Name, t.Scope, t.TokenHash, platform.FormatTime(t.CreatedAt), platform.FormatTime(t.ExpiresAt))
	if err != nil && isUniqueViolation(err) {
		return fmt.Errorf("%w: a token with this name already exists", ErrConflict)
	}
	return err
}

const apiTokenCols = "id, name, scope, token_hash, created_at, expires_at, last_used_at"

func scanAPIToken(row interface{ Scan(...any) error }) (APIToken, error) {
	var t APIToken
	var created, expires, used string
	if err := row.Scan(&t.ID, &t.Name, &t.Scope, &t.TokenHash, &created, &expires, &used); err != nil {
		return t, err
	}
	t.CreatedAt = platform.ParseTime(created)
	t.ExpiresAt = platform.ParseTime(expires)
	t.LastUsedAt = platform.ParseTime(used)
	return t, nil
}

// GetLiveAPIToken returns the unexpired token with this hash.
func GetLiveAPIToken(ctx context.Context, q Q, tokenHash string, at time.Time) (APIToken, error) {
	t, err := scanAPIToken(q.QueryRowContext(ctx,
		"SELECT "+apiTokenCols+" FROM api_tokens WHERE token_hash=?", tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	if !t.ExpiresAt.IsZero() && !at.Before(t.ExpiresAt) {
		return t, ErrNotFound
	}
	return t, nil
}

func ListAPITokens(ctx context.Context, q Q) ([]APIToken, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+apiTokenCols+" FROM api_tokens ORDER BY created_at, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken revokes a token by id or name.
func DeleteAPIToken(ctx context.Context, q Q, ref string) error {
	res, err := q.ExecContext(ctx, "DELETE FROM api_tokens WHERE id=? OR name=?", ref, ref)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: no such token", ErrNotFound)
	}
	return nil
}

// TouchAPIToken records use at most once a minute per token.
func TouchAPIToken(ctx context.Context, q Q, id string, at time.Time) error {
	_, err := q.ExecContext(ctx,
		"UPDATE api_tokens SET last_used_at=? WHERE id=? AND (last_used_at='' OR last_used_at<?)",
		platform.FormatTime(at), id, platform.FormatTime(at.Add(-time.Minute)))
	return err
}
