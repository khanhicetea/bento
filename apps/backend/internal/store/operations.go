package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

type OpState string

const (
	OpQueued      OpState = "queued"
	OpRunning     OpState = "running"
	OpSucceeded   OpState = "succeeded"
	OpFailed      OpState = "failed"
	OpCancelled   OpState = "cancelled"
	OpInterrupted OpState = "interrupted"
)

func (s OpState) Terminal() bool {
	return s == OpSucceeded || s == OpFailed || s == OpCancelled || s == OpInterrupted
}

// Operation is a durable record of one accepted mutation.
type Operation struct {
	ID               string
	Kind             string
	TargetKind       string
	TargetID         string
	State            OpState
	Phase            string
	TargetGeneration int64
	IdempotencyKey   string
	Request          json.RawMessage
	Result           json.RawMessage
	ErrorCode        string
	ErrorMessage     string
	Guidance         string
	CancelRequested  bool
	Origin           string
	CreatedAt        string
	StartedAt        string
	FinishedAt       string
}

type OpEvent struct {
	Seq     int
	At      string
	Level   string
	Message string
}

const opColumns = `id, kind, target_kind, target_id, state, phase, target_generation, COALESCE(idempotency_key,''),
	request_json, result_json, error_code, error_message, guidance, cancel_requested, origin, created_at,
	COALESCE(started_at,''), COALESCE(finished_at,'')`

func scanOp(row interface{ Scan(...any) error }) (Operation, error) {
	var o Operation
	var req, res string
	var cancel int
	err := row.Scan(&o.ID, &o.Kind, &o.TargetKind, &o.TargetID, &o.State, &o.Phase, &o.TargetGeneration, &o.IdempotencyKey,
		&req, &res, &o.ErrorCode, &o.ErrorMessage, &o.Guidance, &cancel, &o.Origin, &o.CreatedAt, &o.StartedAt, &o.FinishedAt)
	o.Request = json.RawMessage(req)
	o.Result = json.RawMessage(res)
	o.CancelRequested = cancel == 1
	return o, err
}

// InsertOperation persists an accepted operation. If idempotency_key already
// exists the existing operation is returned with existed=true and nothing is
// inserted, so a retried submission cannot duplicate work.
func InsertOperation(ctx context.Context, q Q, o Operation) (Operation, bool, error) {
	if o.IdempotencyKey != "" {
		existing, err := FindByIdempotencyKey(ctx, q, o.IdempotencyKey)
		if err == nil {
			if existing.Kind != o.Kind || existing.TargetID != o.TargetID {
				return existing, true, fmt.Errorf("%w: idempotency key was used for a different operation", ErrConflict)
			}
			return existing, true, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return o, false, err
		}
	}
	if len(o.Request) == 0 {
		o.Request = json.RawMessage("{}")
	}
	if o.Origin == "" {
		o.Origin = "api"
	}
	o.State = OpQueued
	o.CreatedAt = now()
	_, err := q.ExecContext(ctx, `INSERT INTO operations(id, kind, target_kind, target_id, state, phase, target_generation, idempotency_key,
		request_json, origin, created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.Kind, o.TargetKind, o.TargetID, o.State, o.Phase, o.TargetGeneration, nullable(o.IdempotencyKey), string(o.Request), o.Origin, o.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return o, false, fmt.Errorf("%w: duplicate operation", ErrConflict)
	}
	return o, false, err
}

func FindByIdempotencyKey(ctx context.Context, q Q, key string) (Operation, error) {
	o, err := scanOp(q.QueryRowContext(ctx, "SELECT "+opColumns+" FROM operations WHERE idempotency_key = ?", key))
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

func GetOperation(ctx context.Context, q Q, id string) (Operation, error) {
	o, err := scanOp(q.QueryRowContext(ctx, "SELECT "+opColumns+" FROM operations WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

type OpFilter struct {
	TargetID string
	States   []OpState
	Limit    int
}

func ListOperations(ctx context.Context, q Q, f OpFilter) ([]Operation, error) {
	query := "SELECT " + opColumns + " FROM operations WHERE 1=1"
	var args []any
	if f.TargetID != "" {
		query += " AND target_id = ?"
		args = append(args, f.TargetID)
	}
	if len(f.States) > 0 {
		query += " AND state IN (" + strings.TrimSuffix(strings.Repeat("?,", len(f.States)), ",") + ")"
		for _, s := range f.States {
			args = append(args, s)
		}
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, f.Limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Operation
	for rows.Next() {
		o, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// NextQueued returns the oldest queued operation. Insertion order (rowid) is
// the FIFO order; created_at has millisecond resolution and ids are random, so
// neither can break ties between operations accepted in the same instant.
func NextQueued(ctx context.Context, q Q) (Operation, error) {
	o, err := scanOp(q.QueryRowContext(ctx, "SELECT "+opColumns+" FROM operations WHERE state = 'queued' ORDER BY rowid LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// ActiveForTarget reports whether a queued or running operation reserves target.
func ActiveForTarget(ctx context.Context, q Q, targetID string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM operations WHERE target_id = ? AND state IN ('queued','running')", targetID).Scan(&n)
	return n > 0, err
}

func MarkRunning(ctx context.Context, q Q, id string) error {
	_, err := q.ExecContext(ctx, "UPDATE operations SET state='running', started_at=? WHERE id=? AND state='queued'", now(), id)
	return err
}

func SetPhase(ctx context.Context, q Q, id, phase string) error {
	_, err := q.ExecContext(ctx, "UPDATE operations SET phase=? WHERE id=?", phase, id)
	return err
}

func FinishOperation(ctx context.Context, q Q, id string, state OpState, result json.RawMessage, code, message, guidance string) error {
	if len(result) == 0 {
		result = json.RawMessage("{}")
	}
	_, err := q.ExecContext(ctx, `UPDATE operations SET state=?, result_json=?, error_code=?, error_message=?, guidance=?, finished_at=? WHERE id=?`,
		state, string(result), code, truncate(message, 2000), truncate(guidance, 2000), now(), id)
	return err
}

func RequestCancel(ctx context.Context, q Q, id string) (Operation, error) {
	o, err := GetOperation(ctx, q, id)
	if err != nil {
		return o, err
	}
	switch o.State {
	case OpQueued:
		if err := FinishOperation(ctx, q, id, OpCancelled, nil, "cancelled", "cancelled before start", ""); err != nil {
			return o, err
		}
	case OpRunning:
		if _, err := q.ExecContext(ctx, "UPDATE operations SET cancel_requested=1 WHERE id=?", id); err != nil {
			return o, err
		}
	default:
		return o, fmt.Errorf("%w: operation already %s", ErrConflict, o.State)
	}
	return GetOperation(ctx, q, id)
}

func CancelRequested(ctx context.Context, q Q, id string) bool {
	var n int
	_ = q.QueryRowContext(ctx, "SELECT cancel_requested FROM operations WHERE id=?", id).Scan(&n)
	return n == 1
}

// maxEventsPerOperation bounds the journal of one operation. When exceeded,
// the oldest events are dropped and replaced by a single truncation marker so
// the most recent events (including the final error) are always kept.
const maxEventsPerOperation = 200

// TruncatedEventMessage prefixes the marker that replaces dropped events.
const TruncatedEventMessage = "earlier events truncated"

func AppendEvent(ctx context.Context, q Q, id, level, message string) error {
	var seq int
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM operation_events WHERE operation_id=?", id).Scan(&seq); err != nil {
		return err
	}
	next := seq + 1
	at := now()
	if _, err := q.ExecContext(ctx, "INSERT INTO operation_events(operation_id, seq, at, level, message) VALUES(?,?,?,?,?)",
		id, next, at, level, truncate(message, 1000)); err != nil {
		return err
	}
	if next <= maxEventsPerOperation {
		return nil
	}
	// Keep the newest maxEventsPerOperation-1 events plus one marker that
	// occupies the slot just below them. Seqs stay monotonic for cursors.
	marker := next - maxEventsPerOperation + 1
	if _, err := q.ExecContext(ctx, "DELETE FROM operation_events WHERE operation_id=? AND seq <= ?", id, marker); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, "INSERT INTO operation_events(operation_id, seq, at, level, message) VALUES(?,?,?,?,?)",
		id, marker, at, "warn", fmt.Sprintf("%s (%d dropped)", TruncatedEventMessage, marker))
	return err
}

func ListEvents(ctx context.Context, q Q, id string, afterSeq int) ([]OpEvent, error) {
	rows, err := q.QueryContext(ctx, "SELECT seq, at, level, message FROM operation_events WHERE operation_id=? AND seq > ? ORDER BY seq", id, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpEvent
	for rows.Next() {
		var e OpEvent
		if err := rows.Scan(&e.Seq, &e.At, &e.Level, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InterruptRunning marks operations left running by a crashed backend as
// interrupted. They are never replayed automatically.
func InterruptRunning(ctx context.Context, q Q) ([]Operation, error) {
	ops, err := ListOperations(ctx, q, OpFilter{States: []OpState{OpRunning}, Limit: 500})
	if err != nil {
		return nil, err
	}
	for _, o := range ops {
		if err := FinishOperation(ctx, q, o.ID, OpInterrupted, nil, "interrupted",
			fmt.Sprintf("backend stopped during phase %q", o.Phase),
			"Inspect the target status; intent is preserved and reconciliation converges safe state. Resubmit the operation explicitly if still required."); err != nil {
			return nil, err
		}
	}
	return ops, nil
}

// truncate limits s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// DefaultRetention is how long finished operations and backup runs are kept.
const DefaultRetention = 30 * 24 * time.Hour

// PruneHistory deletes terminal operations (with their events) and finished
// backup runs that finished before now-retention. Queued and running
// operations and unfinished backup runs are never pruned.
func PruneHistory(ctx context.Context, q Q, retention time.Duration) (ops, runs int64, err error) {
	cutoff := platform.FormatTime(time.Now().Add(-retention))
	const terminal = "state IN ('succeeded','failed','cancelled','interrupted') AND finished_at IS NOT NULL AND finished_at < ?"
	if _, err = q.ExecContext(ctx, "DELETE FROM operation_events WHERE operation_id IN (SELECT id FROM operations WHERE "+terminal+")", cutoff); err != nil {
		return
	}
	res, err := q.ExecContext(ctx, "DELETE FROM operations WHERE "+terminal, cutoff)
	if err != nil {
		return
	}
	ops, _ = res.RowsAffected()
	res, err = q.ExecContext(ctx, "DELETE FROM backup_runs WHERE finished_at IS NOT NULL AND finished_at < ?", cutoff)
	if err != nil {
		return
	}
	runs, _ = res.RowsAffected()
	return ops, runs, nil
}
