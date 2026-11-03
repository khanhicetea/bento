package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// RuntimeBuild is one built managed runtime image (see runtimeBuildsSchema).
type RuntimeBuild struct {
	ID          int64
	ImageKey    string
	ContextHash string
	Tag         string
	ImageID     string
	Manifest    map[string]string
	BuiltAt     time.Time
	Current     bool
}

// AppRuntimeBuild is the build an app runs or will run once replaced.
type AppRuntimeBuild struct {
	AppID           string
	BuildID         int64
	PreviousBuildID int64 // 0: none
	Rolling         bool
	UpdatedAt       time.Time
}

const runtimeBuildColumns = `id, image_key, context_hash, tag, image_id, manifest_json, built_at, current`

func scanRuntimeBuild(row interface{ Scan(...any) error }) (RuntimeBuild, error) {
	var b RuntimeBuild
	var manifest, builtAt string
	if err := row.Scan(&b.ID, &b.ImageKey, &b.ContextHash, &b.Tag, &b.ImageID, &manifest, &builtAt,
		&b.Current); err != nil {
		return b, err
	}
	b.Manifest = map[string]string{}
	if err := json.Unmarshal([]byte(manifest), &b.Manifest); err != nil {
		return b, err
	}
	b.BuiltAt = platform.ParseTime(builtAt)
	return b, nil
}

// InsertCurrentRuntimeBuild records a new build as the current one of its key
// and inputs, replacing the previous current build (which is kept).
func InsertCurrentRuntimeBuild(ctx context.Context, q Q, b RuntimeBuild) (int64, error) {
	manifest, err := json.Marshal(b.Manifest)
	if err != nil {
		return 0, err
	}
	if _, err := q.ExecContext(ctx, `UPDATE runtime_builds SET current = 0
	  WHERE image_key = ? AND context_hash = ? AND current = 1`, b.ImageKey, b.ContextHash); err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx, `INSERT INTO runtime_builds(image_key, context_hash, tag, image_id, manifest_json,
	  built_at, current) VALUES(?, ?, ?, ?, ?, ?, 1)`, b.ImageKey, b.ContextHash, b.Tag, b.ImageID, string(manifest),
		platform.FormatTime(b.BuiltAt))
	if isUniqueViolation(err) {
		return 0, fmt.Errorf("%w: build tag %s already exists", ErrConflict, b.Tag)
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func GetRuntimeBuild(ctx context.Context, q Q, id int64) (RuntimeBuild, error) {
	b, err := scanRuntimeBuild(q.QueryRowContext(ctx, "SELECT "+runtimeBuildColumns+" FROM runtime_builds WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// CurrentRuntimeBuild returns the current build of a key and its inputs.
func CurrentRuntimeBuild(ctx context.Context, q Q, imageKey, contextHash string) (RuntimeBuild, error) {
	b, err := scanRuntimeBuild(q.QueryRowContext(ctx, "SELECT "+runtimeBuildColumns+
		" FROM runtime_builds WHERE image_key = ? AND context_hash = ? AND current = 1", imageKey, contextHash))
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// ListRuntimeBuilds returns every recorded build, newest first.
func ListRuntimeBuilds(ctx context.Context, q Q) ([]RuntimeBuild, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+runtimeBuildColumns+" FROM runtime_builds ORDER BY built_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeBuild{}
	for rows.Next() {
		b, err := scanRuntimeBuild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteRuntimeBuildsByImage forgets the builds of a removed image. Apps
// assigned to one lose their assignment and get the current build again.
func DeleteRuntimeBuildsByImage(ctx context.Context, q Q, imageID string) error {
	_, err := q.ExecContext(ctx, "DELETE FROM runtime_builds WHERE image_id = ?", imageID)
	return err
}

const appRuntimeBuildColumns = `app_id, build_id, COALESCE(previous_build_id, 0), rolling, updated_at`

func scanAppRuntimeBuild(row interface{ Scan(...any) error }) (AppRuntimeBuild, error) {
	var a AppRuntimeBuild
	var updated string
	if err := row.Scan(&a.AppID, &a.BuildID, &a.PreviousBuildID, &a.Rolling, &updated); err != nil {
		return a, err
	}
	a.UpdatedAt = platform.ParseTime(updated)
	return a, nil
}

func GetAppRuntimeBuild(ctx context.Context, q Q, appID string) (AppRuntimeBuild, error) {
	a, err := scanAppRuntimeBuild(q.QueryRowContext(ctx, "SELECT "+appRuntimeBuildColumns+
		" FROM app_runtime_builds WHERE app_id = ?", appID))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func ListAppRuntimeBuilds(ctx context.Context, q Q) ([]AppRuntimeBuild, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+appRuntimeBuildColumns+" FROM app_runtime_builds ORDER BY app_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppRuntimeBuild{}
	for rows.Next() {
		a, err := scanAppRuntimeBuild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AssignAppRuntimeBuild points an app at a build. A change of build keeps the
// previous one for rolling back. rolling marks an assignment made by a
// rollout that a failed rollout reverts until ConfirmAppRuntimeBuild.
func AssignAppRuntimeBuild(ctx context.Context, q Q, appID string, buildID int64, rolling bool) error {
	_, err := q.ExecContext(ctx, `INSERT INTO app_runtime_builds(app_id, build_id, previous_build_id, rolling, updated_at)
	  VALUES(?, ?, NULL, ?, ?)
	  ON CONFLICT(app_id) DO UPDATE SET
	    previous_build_id = CASE WHEN build_id = excluded.build_id THEN previous_build_id ELSE build_id END,
	    build_id = excluded.build_id,
	    rolling = excluded.rolling,
	    updated_at = excluded.updated_at`, appID, buildID, rolling, now())
	return err
}

// ConfirmAppRuntimeBuild clears the rolling mark once the app's instance is
// ready on the image of its assigned build. It reports whether it did.
func ConfirmAppRuntimeBuild(ctx context.Context, q Q, appID, imageID string) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE app_runtime_builds SET rolling = 0, updated_at = ?
	  WHERE app_id = ? AND rolling = 1
	    AND build_id IN (SELECT id FROM runtime_builds WHERE image_id = ?)`, now(), appID, imageID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// HasRollingRuntimeBuilds reports whether a rollout on imageKey still has
// unconfirmed assignments.
func HasRollingRuntimeBuilds(ctx context.Context, q Q, imageKey string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_runtime_builds a JOIN runtime_builds b ON b.id = a.build_id
	  WHERE a.rolling = 1 AND b.image_key = ?`, imageKey).Scan(&n)
	return n > 0, err
}

// RevertRollingRuntimeBuilds stops a rollout on imageKey: every unconfirmed
// assignment goes back to the build it replaced (which in turn becomes the
// previous one). It returns the reverted app ids.
func RevertRollingRuntimeBuilds(ctx context.Context, q Q, imageKey string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.app_id FROM app_runtime_builds a JOIN runtime_builds b ON b.id = a.build_id
	  WHERE a.rolling = 1 AND b.image_key = ? ORDER BY a.app_id`, imageKey)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := q.ExecContext(ctx, `UPDATE app_runtime_builds SET rolling = 0, updated_at = ?,
		    build_id = COALESCE(previous_build_id, build_id),
		    previous_build_id = CASE WHEN previous_build_id IS NULL THEN NULL ELSE build_id END
		  WHERE app_id = ?`, now(), id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// ReplaceAppRuntimeBuild assigns buildID to an app only if its assignment is
// still expected (0: none), so a concurrent rollout's assignment is never
// overwritten. It reports whether it wrote.
func ReplaceAppRuntimeBuild(ctx context.Context, q Q, appID string, expected, buildID int64) (bool, error) {
	var res sql.Result
	var err error
	if expected == 0 {
		res, err = q.ExecContext(ctx, `INSERT INTO app_runtime_builds(app_id, build_id, previous_build_id, rolling,
		  updated_at) VALUES(?, ?, NULL, 0, ?) ON CONFLICT(app_id) DO NOTHING`, appID, buildID, now())
	} else {
		res, err = q.ExecContext(ctx, `UPDATE app_runtime_builds SET previous_build_id = build_id, build_id = ?,
		  rolling = 0, updated_at = ? WHERE app_id = ? AND build_id = ?`, buildID, now(), appID, expected)
	}
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
