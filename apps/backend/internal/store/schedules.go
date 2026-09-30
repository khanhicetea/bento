package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// BackupScheduleID is the schedule that carries the stack-wide backup
// settings.
const BackupScheduleID = "backup-default"

// Schedule is one durable, operator-editable scheduled job. The backend
// scheduler submits an operation of Kind for each due slot; Spec is the
// kind-specific configuration and never holds secrets.
type Schedule struct {
	ID      string
	Kind    string
	Name    string
	Cron    string
	Enabled bool
	Spec    json.RawMessage
	// Bookkeeping written only by the scheduler (and reset on config edits).
	LastSlot  string
	LastRunAt string
	LastOpID  string
	LastState string
	Missed    int
	// Revision increments on every configuration edit.
	Revision  int64
	CreatedAt string
	UpdatedAt string
}

// DecodeSpec unmarshals Spec into out; an empty spec leaves out unchanged.
func (s Schedule) DecodeSpec(out any) error {
	if len(s.Spec) == 0 {
		return nil
	}
	return json.Unmarshal(s.Spec, out)
}

const scheduleColumns = `id, kind, name, cron, enabled, spec_json, last_slot, last_run_at, last_op_id, last_state,
	missed_count, revision, created_at, updated_at`

func scanSchedule(row interface{ Scan(...any) error }) (Schedule, error) {
	var s Schedule
	var spec string
	err := row.Scan(&s.ID, &s.Kind, &s.Name, &s.Cron, &s.Enabled, &spec, &s.LastSlot, &s.LastRunAt, &s.LastOpID,
		&s.LastState, &s.Missed, &s.Revision, &s.CreatedAt, &s.UpdatedAt)
	s.Spec = json.RawMessage(spec)
	return s, err
}

func GetSchedule(ctx context.Context, q Q, id string) (Schedule, error) {
	s, err := scanSchedule(q.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// ListSchedules returns every schedule ordered by id; enabledOnly filters.
func ListSchedules(ctx context.Context, q Q, enabledOnly bool) ([]Schedule, error) {
	query := "SELECT " + scheduleColumns + " FROM schedules"
	if enabledOnly {
		query += " WHERE enabled = 1"
	}
	rows, err := q.QueryContext(ctx, query+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PutSchedule inserts or replaces a schedule's configuration and resets its
// bookkeeping to lastSlot, so an edit never triggers catch-up runs.
func PutSchedule(ctx context.Context, q Q, s Schedule, now string) error {
	spec := string(s.Spec)
	if spec == "" {
		spec = "{}"
	}
	_, err := q.ExecContext(ctx, `INSERT INTO schedules(id, kind, name, cron, enabled, spec_json, last_slot, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, name = excluded.name, cron = excluded.cron,
			enabled = excluded.enabled, spec_json = excluded.spec_json, last_slot = excluded.last_slot,
			revision = schedules.revision + 1, updated_at = excluded.updated_at`,
		s.ID, s.Kind, s.Name, s.Cron, s.Enabled, spec, s.LastSlot, now, now)
	return err
}

// SaveScheduleState writes scheduler bookkeeping only if the configuration
// is unchanged since it was read (revision), so a concurrent edit wins.
func SaveScheduleState(ctx context.Context, q Q, s Schedule) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE schedules SET last_slot = ?, last_run_at = ?, last_op_id = ?, last_state = ?,
		missed_count = ? WHERE id = ? AND revision = ?`,
		s.LastSlot, s.LastRunAt, s.LastOpID, s.LastState, s.Missed, s.ID, s.Revision)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
