package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
	err := q.QueryRowContext(ctx, "SELECT token_hash, csrf_token, created_at, expires_at FROM sessions WHERE token_hash=? AND revoked=0", tokenHash).
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
	_, err := q.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ? OR revoked=1", platform.FormatTime(at.Add(-24*time.Hour)))
	return err
}

// ---- backup runs ----

type BackupRun struct {
	ID          string
	Trigger     string
	State       string
	StartedAt   string
	FinishedAt  string
	Artifacts   []string
	UploadState string
	Error       string
}

func InsertBackupRun(ctx context.Context, q Q, r BackupRun) error {
	_, err := q.ExecContext(ctx, "INSERT INTO backup_runs(id, trigger, state, started_at) VALUES(?,?,?,?)", r.ID, r.Trigger, r.State, r.StartedAt)
	return err
}

func FinishBackupRun(ctx context.Context, q Q, r BackupRun) error {
	arts, _ := json.Marshal(r.Artifacts)
	_, err := q.ExecContext(ctx, "UPDATE backup_runs SET state=?, finished_at=?, artifacts_json=?, upload_state=?, error_message=? WHERE id=?",
		r.State, now(), string(arts), r.UploadState, truncate(r.Error, 2000), r.ID)
	return err
}

func ListBackupRuns(ctx context.Context, q Q, limit int) ([]BackupRun, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, trigger, state, started_at, COALESCE(finished_at,''), artifacts_json, upload_state, error_message
		FROM backup_runs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupRun
	for rows.Next() {
		var r BackupRun
		var arts string
		if err := rows.Scan(&r.ID, &r.Trigger, &r.State, &r.StartedAt, &r.FinishedAt, &arts, &r.UploadState, &r.Error); err != nil {
			return nil, err
		}
		// Artifacts are display-only history; an unreadable list shows as empty
		// rather than hiding every run.
		_ = json.Unmarshal([]byte(arts), &r.Artifacts)
		out = append(out, r)
	}
	return out, rows.Err()
}
