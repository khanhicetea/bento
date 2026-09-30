// Package stack bootstraps and imports stack roots. These are offline
// commands: they require the backend to be stopped and take the same
// lifetime lock.
package stack

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

type InitOptions struct {
	Root     string
	Name     string
	UIDRange domain.UIDRange
	MySQL    string
	Postgres string
	Password string
}

// DetectForeign reports files that show a root holds something other than a
// Bento stack, so it is refused rather than initialized over.
func DetectForeign(root string) string {
	for _, marker := range []string{"state.db", ".env", "generated", "overlays"} {
		if _, err := os.Lstat(filepath.Join(root, marker)); err == nil {
			return marker
		}
	}
	return ""
}

// Init creates a new stack. It never touches a non-empty directory.
func Init(ctx context.Context, opts InitOptions) (store.StackIdentity, error) {
	var id store.StackIdentity
	layout, err := platform.NewLayout(opts.Root)
	if err != nil {
		return id, err
	}
	if err := runtime.ValidateStackName(opts.Name); err != nil {
		return id, err
	}
	if opts.UIDRange == (domain.UIDRange{}) {
		opts.UIDRange = domain.DefaultUIDRange()
	}
	if opts.UIDRange.First < 1000 || opts.UIDRange.Last <= opts.UIDRange.First || opts.UIDRange.Last > 2_000_000_000 {
		return id, fmt.Errorf("invalid uid range %d-%d", opts.UIDRange.First, opts.UIDRange.Last)
	}
	if m := DetectForeign(layout.Root); m != "" {
		return id, fmt.Errorf(
			"%s is not a Bento stack root (found %s); it was left untouched. Initialize an empty directory instead",
			layout.Root,
			m,
		)
	}
	empty, err := platform.DirIsEmptyOrMissing(layout.Root)
	if err != nil {
		return id, err
	}
	if !empty {
		return id, fmt.Errorf("refusing to initialize: %s is not empty", layout.Root)
	}
	if opts.Password != "" {
		if err := api.ValidatePassword(opts.Password); err != nil {
			return id, err
		}
	}
	for dir, mode := range layout.SkeletonDirs() {
		if err := platform.EnsureDir(dir, os.FileMode(mode), platform.RootOwner); err != nil {
			return id, err
		}
	}
	lock, err := platform.TryLock(layout.ControllerLock())
	if err != nil {
		return id, err
	}
	defer lock.Release()
	s, err := store.Create(layout.Database())
	if err != nil {
		return id, err
	}
	defer s.Close()
	id = store.StackIdentity{ID: platform.NewStackID(), Name: opts.Name, CreatedAt: platform.FormatTime(time.Now())}
	names := runtime.Names{StackID: id.ID, StackName: id.Name}
	services := []domain.DataService{{
		Name:    "redis",
		Engine:  domain.EngineRedis,
		Version: domain.RedisVersion,
		Image:   domain.RedisImage,
	}}
	if opts.MySQL != "" {
		img, ok := domain.MySQLVersions[opts.MySQL]
		if !ok {
			return id, fmt.Errorf("unsupported MySQL version %q", opts.MySQL)
		}
		services = append(
			services,
			domain.DataService{
				Name:    "mysql" + strings.ReplaceAll(opts.MySQL, ".", ""),
				Engine:  domain.EngineMySQL,
				Version: opts.MySQL,
				Image:   img,
			},
		)
	}
	if opts.Postgres != "" {
		img, ok := domain.PostgresVersions[opts.Postgres]
		if !ok {
			return id, fmt.Errorf("unsupported PostgreSQL version %q", opts.Postgres)
		}
		services = append(
			services,
			domain.DataService{
				Name:    "postgres" + opts.Postgres,
				Engine:  domain.EnginePostgres,
				Version: opts.Postgres,
				Image:   img,
			},
		)
	}
	err = s.Tx(ctx, func(q store.Q) error {
		for k, v := range map[string]string{
			"stack_id":   id.ID,
			"stack_name": id.Name,
			"created_at": id.CreatedAt,
			"format":     "bento-go-state",
		} {
			if err := store.SetMeta(ctx, q, k, v); err != nil {
				return err
			}
		}
		if err := store.PutSetting(ctx, q, "uid_range", opts.UIDRange); err != nil {
			return err
		}
		if err := store.PutSetting(ctx, q, "edge", domain.DefaultEdgeSettings()); err != nil {
			return err
		}
		if err := seedBackupSchedule(ctx, q); err != nil {
			return err
		}
		for _, svc := range services {
			svc.Volume = names.ServiceVolume(svc.Name)
			svc.CreatedAt = time.Now().UTC()
			if err := store.InsertService(ctx, q, svc); err != nil {
				return err
			}
			// Explicit initialization of each service's first volume runs as a
			// durable operation when the backend first starts.
			raw, _ := json.Marshal(svc)
			if _, _, err := store.InsertOperation(ctx, q, store.Operation{ID: platform.NewOperationID(), Kind: "service.create",
				TargetKind: "service", TargetID: svc.Name, Request: raw, Origin: "init"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return id, err
	}
	if opts.Password != "" {
		if err := api.SetOperatorPassword(ctx, s, opts.Password); err != nil {
			return id, err
		}
	}
	placeholder := "# rclone configuration for scheduled backup uploads (operator-owned, private).\n"
	if err := platform.AtomicWrite(
		filepath.Join(layout.RcloneDir(), "rclone.conf"),
		[]byte(placeholder),
		0o600,
		platform.RootOwner,
	); err != nil {
		return id, err
	}
	return id, nil
}

// seedBackupSchedule stores the default (disabled) backup schedule.
func seedBackupSchedule(ctx context.Context, q store.Q) error {
	d := domain.DefaultBackupSchedule()
	spec, err := json.Marshal(map[string]any{
		"scope": d.Scope, "compression": d.Compression, "retain": d.Retain, "rcloneRemote": d.RcloneRemote,
	})
	if err != nil {
		return err
	}
	now := platform.FormatTime(time.Now())
	return store.PutSchedule(ctx, q, store.Schedule{
		ID: store.BackupScheduleID, Kind: "backup", Name: d.Name, Cron: d.Cron, Enabled: d.Enabled, Spec: spec,
	}, now)
}
