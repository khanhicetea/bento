package stack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/transfer"
)

// exportedStack is a source stack written out in export format: a Redis
// service, one app with a home sidecar, an allocated UID, a session, and
// settings that import must neutralize.
type exportedStack struct {
	dir     string
	stackID string
	app     domain.App
}

func writeExport(t *testing.T) exportedStack {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	ctx := t.Context()
	src := filepath.Join(t.TempDir(), "src")
	id, err := Init(ctx, InitOptions{Root: src, Name: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	layout := platform.Layout{Root: src}
	s, err := store.Open(layout.Database())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := domain.App{ID: "a1b2c3", Slug: "shop", DesiredRuntime: domain.DesiredRunning, Publication: domain.Published, Ingress: domain.IngressManaged,
		Runtime:   domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24"}},
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err = s.Tx(ctx, func(q store.Q) error {
		uid, err := store.AllocateUID(ctx, q, domain.DefaultUIDRange(), platform.NoHostIDs{}, app.ID, app.Slug)
		if err != nil {
			return err
		}
		app.UID, app.GID = uid, uid
		if err := store.InsertApp(ctx, q, app); err != nil {
			return err
		}
		if err := store.PutSetting(ctx, q, "edge", map[string]any{"enabled": true, "httpPort": 80}); err != nil {
			return err
		}
		if err := store.PutSchedule(ctx, q, store.Schedule{ID: store.BackupScheduleID, Kind: "backup", Cron: "0 3 * * *",
			Enabled: true, LastSlot: "2026-01-01T00:00:00.000Z"}, "2026-01-01T00:00:00.000Z"); err != nil {
			return err
		}
		if err := store.PutSetting(ctx, q, "network", map[string]any{"apps": "10.200.0.0/24"}); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, "INSERT INTO sessions(token_hash, csrf_token, created_at, expires_at) VALUES('h','c','x','y')")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sc, _ := json.Marshal(operations.HomeSidecar{StackID: id.ID, AppID: app.ID, Slug: app.Slug, UID: app.UID, GID: app.GID})
	if err := platform.EnsureDir(layout.AppHome(app.Slug), 0o750, platform.RootOwner); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.HomeSidecar(app.Slug), sc, 0o444); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "export")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SnapshotTo(ctx, filepath.Join(dest, "state.db")); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dest, "stack.tar.zst"))
	if err != nil {
		t.Fatal(err)
	}
	if err := transfer.ArchiveRoot(src, f, operations.RootSkip); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.WriteFile(filepath.Join(dest, "volume-redis.tar"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m := transfer.Manifest{
		Format: transfer.FormatName, Version: transfer.FormatVersion, SchemaVersion: store.SchemaVersion,
		StackID: id.ID, StackName: id.Name, Arch: "amd64", StateFile: "state.db", RootArchive: "stack.tar.zst",
		Services: []transfer.ServiceEntry{{Name: "redis", Engine: "redis", Image: domain.RedisImage,
			VolumeFile: "volume-redis.tar", SourceVolume: "bento-prod-redis-data"}},
	}
	if err := transfer.WriteManifest(dest, m); err != nil {
		t.Fatal(err)
	}
	return exportedStack{dir: dest, stackID: id.ID, app: app}
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func openImported(t *testing.T, root string) *store.Store {
	t.Helper()
	s, err := store.Open(platform.Layout{Root: root}.Database())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestImportNeutralizesStateAndRestoresVolumes(t *testing.T) {
	exp := writeExport(t)
	ctx := t.Context()
	root := filepath.Join(t.TempDir(), "clone")
	fake := docker.NewFake()
	rng := &domain.UIDRange{First: 50000, Last: 59999}
	if err := Import(ctx, fake, discard, ImportOptions{Root: root, From: exp.dir, Name: "clone", NewUIDRange: rng}); err != nil {
		t.Fatal(err)
	}
	s := openImported(t, root)
	db := s.DB()
	ident, err := store.GetStackIdentity(ctx, db)
	if err != nil || ident.ID == exp.stackID || ident.Name != "clone" {
		t.Fatalf("identity %+v %v", ident, err)
	}
	if from, _ := store.GetMeta(ctx, db, "imported_from"); from != exp.stackID {
		t.Fatalf("imported_from %q", from)
	}
	if hw, _ := store.GetMeta(ctx, db, "uid_highwater"); hw != "49999" {
		t.Fatalf("uid_highwater %q", hw)
	}
	app, err := store.GetApp(ctx, db, exp.app.ID)
	if err != nil || app.DesiredRuntime != domain.DesiredStopped || app.Publication != domain.Unpublished {
		t.Fatalf("imported app must be stopped and unpublished: %+v %v", app, err)
	}
	ops, _ := store.ListOperations(ctx, db, store.OpFilter{})
	for _, o := range ops {
		if o.State != store.OpCancelled || o.ErrorCode != "imported" {
			t.Fatalf("pending operation replayable after import: %+v", o)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sessions").Scan(&n); err != nil || n != 0 {
		t.Fatalf("sessions survived import: %d %v", n, err)
	}
	if ok, _ := store.GetSetting(ctx, db, "network", &map[string]any{}); ok {
		t.Fatal("network plan survived import")
	}
	if sc, err := store.GetSchedule(ctx, db, store.BackupScheduleID); err != nil || sc.Enabled || sc.LastSlot != "" {
		t.Fatalf("schedule survived import enabled: %+v %v", sc, err)
	}
	for key, want := range map[string]string{"edge": "false", "tunnel": "false"} {
		var v map[string]any
		if _, err := store.GetSetting(ctx, db, key, &v); err != nil || v["enabled"] != (want == "true") {
			t.Fatalf("%s setting %v %v", key, v, err)
		}
	}
	names := runtime.Names{StackID: ident.ID, StackName: "clone"}
	vol := names.ServiceVolume("redis")
	svc, err := store.GetService(ctx, db, "redis")
	if err != nil || svc.Volume != vol {
		t.Fatalf("service volume %q %v", svc.Volume, err)
	}
	if v, ok := fake.Volumes[vol]; !ok || v.Labels[runtime.LabelStackID] != ident.ID || v.Labels[runtime.LabelService] != "redis" {
		t.Fatalf("volume %s not created with ownership labels: %+v", vol, v)
	}
	if fake.CallCount("PullImage "+domain.RedisImage) != 1 || fake.CallCount("Create") != 1 ||
		fake.CallCount("Start") != 1 || fake.CallCount("Remove") != 1 || len(fake.Containers) != 0 {
		t.Fatalf("restore job calls %v", fake.Calls)
	}
	raw, err := os.ReadFile(platform.Layout{Root: root}.HomeSidecar("shop"))
	if err != nil {
		t.Fatal(err)
	}
	var sc operations.HomeSidecar
	if err := json.Unmarshal(raw, &sc); err != nil || sc.StackID != ident.ID || sc.AppID != exp.app.ID {
		t.Fatalf("sidecar not re-stamped: %+v %v", sc, err)
	}
	lock, err := platform.TryLock(platform.Layout{Root: root}.ControllerLock())
	if err != nil {
		t.Fatalf("import must release the controller lock: %v", err)
	}
	lock.Release()
}

func TestImportFailureRemovesOnlyWhatItCreated(t *testing.T) {
	cases := map[string]struct {
		prepare     func(t *testing.T, exp exportedStack, fake *docker.Fake, opts *ImportOptions)
		wrap        func(*docker.Fake) docker.Engine
		rootExisted bool
		wantErr     string
		// volumes that must remain in the fake after the failure.
		keepVolumes int
	}{
		"restore job fails, new root": {
			prepare: func(_ *testing.T, _ exportedStack, fake *docker.Fake, _ *ImportOptions) {
				fake.FailOn = map[string]error{"Create": errors.New("boom")}
			},
			wantErr: "boom",
		},
		"restore job fails, existing empty root": {
			prepare: func(_ *testing.T, _ exportedStack, fake *docker.Fake, _ *ImportOptions) {
				fake.FailOn = map[string]error{"Create": errors.New("boom")}
			},
			rootExisted: true,
			wantErr:     "boom",
		},
		"volume already exists": {
			prepare: func(t *testing.T, _ exportedStack, fake *docker.Fake, _ *ImportOptions) {
				fake.VolumeCreate(t.Context(), "unrelated", nil)
			},
			wrap:        func(f *docker.Fake) docker.Engine { return volumesExist{f} },
			wantErr:     "already exists; choose a different stack name",
			keepVolumes: 1,
		},
		"uid range overlaps ledger": {
			prepare: func(_ *testing.T, exp exportedStack, _ *docker.Fake, opts *ImportOptions) {
				opts.NewUIDRange = &domain.UIDRange{First: exp.app.UID, Last: exp.app.UID + 10}
			},
			wantErr: "overlaps allocated uid",
		},
		"sidecar does not match": {
			prepare: func(t *testing.T, exp exportedStack, _ *docker.Fake, _ *ImportOptions) {
				rewriteArchivedSidecar(t, exp)
			},
			wantErr: "does not match its recorded identity",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			exp := writeExport(t)
			root := filepath.Join(t.TempDir(), "clone")
			if tc.rootExisted {
				if err := os.Mkdir(root, 0o711); err != nil {
					t.Fatal(err)
				}
			}
			fake := docker.NewFake()
			opts := ImportOptions{Root: root, From: exp.dir}
			tc.prepare(t, exp, fake, &opts)
			var engine docker.Engine = fake
			if tc.wrap != nil {
				engine = tc.wrap(fake)
			}
			err := Import(t.Context(), engine, discard, opts)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want %q, got %v", tc.wantErr, err)
			}
			if len(fake.Volumes) != tc.keepVolumes {
				t.Fatalf("volumes after failed import: %v", fake.Volumes)
			}
			entries, statErr := os.ReadDir(root)
			switch {
			case tc.rootExisted && (statErr != nil || len(entries) != 0):
				t.Fatalf("existing root must be emptied, not removed: %v %v", entries, statErr)
			case !tc.rootExisted && !errors.Is(statErr, os.ErrNotExist):
				t.Fatalf("root created by the import must be removed: %v", statErr)
			}
		})
	}
}

// volumesExist reports every volume as already present, so the planned
// service volume name (derived from the random new stack id) collides.
type volumesExist struct{ *docker.Fake }

func (volumesExist) VolumeInspect(_ context.Context, name string) (*docker.VolumeInfo, error) {
	return &docker.VolumeInfo{Name: name}, nil
}

// rewriteArchivedSidecar re-archives the export with a sidecar naming another
// app, so adoption must refuse it.
func rewriteArchivedSidecar(t *testing.T, exp exportedStack) {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "unpacked")
	if err := os.Mkdir(tmp, 0o711); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(exp.dir, "stack.tar.zst"))
	if err != nil {
		t.Fatal(err)
	}
	if err := transfer.ExtractRoot(f, tmp); err != nil {
		t.Fatal(err)
	}
	f.Close()
	sc, _ := json.Marshal(operations.HomeSidecar{AppID: "other", UID: exp.app.UID})
	path := platform.Layout{Root: tmp}.HomeSidecar(exp.app.Slug)
	os.Chmod(path, 0o644)
	if err := os.WriteFile(path, sc, 0o444); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(exp.dir, "stack.tar.zst"))
	if err != nil {
		t.Fatal(err)
	}
	if err := transfer.ArchiveRoot(tmp, out, nil); err != nil {
		t.Fatal(err)
	}
	out.Close()
}

func TestImportRefusesNonEmptyRootUntouched(t *testing.T) {
	exp := writeExport(t)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o600)
	before := treeHash(t, root)
	err := Import(context.Background(), docker.NewFake(), discard, ImportOptions{Root: root, From: exp.dir})
	if err == nil || !strings.Contains(err.Error(), "is not empty") {
		t.Fatalf("want refusal, got %v", err)
	}
	if treeHash(t, root) != before {
		t.Fatal("refusal modified the root")
	}
}
