package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// openRaw opens a store file the way Open does, without migrating, so a test
// can run migrate against a synthetic target version.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func newStateFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bento.db")
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := PutSetting(t.Context(), s.DB(), "probe", map[string]string{"v": "before"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	return path
}

func userVersion(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func stateCopies(t *testing.T, path string) []string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), StateBackupsDir, "*"))
	return matches
}

func TestMigrateUpgradesAfterCopyingState(t *testing.T) {
	path := newStateFile(t)
	db := openRaw(t, path)
	ctx := t.Context()
	steps := map[int64]migration{SchemaVersion: func(tx *sql.Tx) error {
		_, err := tx.Exec(`ALTER TABLE settings ADD COLUMN note TEXT NOT NULL DEFAULT 'migrated'`)
		return err
	}}
	if err := migrate(ctx, db, path, SchemaVersion+1, steps); err != nil {
		t.Fatal(err)
	}
	if v := userVersion(t, db); v != SchemaVersion+1 {
		t.Fatalf("user_version %d, want %d", v, SchemaVersion+1)
	}
	var note string
	if err := db.QueryRow("SELECT note FROM settings WHERE key = 'probe'").Scan(&note); err != nil || note != "migrated" {
		t.Fatalf("migrated column: %q %v", note, err)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign keys must be back on after migrating, got %d %v", fk, err)
	}
	// The copy holds the pre-migration schema and data, privately.
	copies := stateCopies(t, path)
	if len(copies) != 1 || !strings.Contains(filepath.Base(copies[0]), fmt.Sprintf(".schema-%d.", SchemaVersion)) {
		t.Fatalf("want one schema-%d copy, got %v", SchemaVersion, copies)
	}
	if info, _ := os.Stat(copies[0]); info.Mode().Perm() != 0o600 {
		t.Fatalf("copy mode %v, want 0600", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Dir(copies[0])); info.Mode().Perm() != 0o700 {
		t.Fatalf("copy dir mode %v, want 0700", info.Mode().Perm())
	}
	if err := CheckCompatible(copies[0]); err != nil {
		t.Fatalf("copy must be an openable schema-%d state: %v", SchemaVersion, err)
	}
	cp := openRaw(t, copies[0])
	if v := userVersion(t, cp); v != SchemaVersion {
		t.Fatalf("copy user_version %d", v)
	}
	if err := cp.QueryRow("SELECT note FROM settings").Scan(&note); err == nil {
		t.Fatal("copy must predate the migration")
	}
}

func TestMigrateFailureLeavesStateUnchanged(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		stage string
		step  func(*sql.Tx) error
	}{
		"step error": {fmt.Sprintf("step %d to %d", SchemaVersion, SchemaVersion+1), func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE settings SET value_json = '{"v":"half"}'`); err != nil {
				return err
			}
			return errors.New("cannot map row")
		}},
		// Foreign keys are off during the step, so the violation surfaces at
		// the foreign_key_check before commit.
		"foreign key violation": {"foreign key check", func(tx *sql.Tx) error {
			_, err := tx.Exec(`DELETE FROM uid_ledger`)
			return err
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := newStateFile(t)
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Tx(ctx, func(q Q) error {
				uid, err := AllocateUID(ctx, q, domain.UIDRange{First: 20000, Last: 29999}, nil, "a1", "shop")
				if err != nil {
					return err
				}
				return InsertApp(ctx, q, domain.App{ID: "a1", Slug: "shop", UID: uid, GID: uid,
					DesiredRuntime: domain.DesiredStopped, Ingress: domain.IngressNone, Publication: domain.Unpublished,
					CreatedAt: time.Now(), UpdatedAt: time.Now()})
			}); err != nil {
				t.Fatal(err)
			}
			s.Close()
			db := openRaw(t, path)
			err = migrate(ctx, db, path, SchemaVersion+1, map[int64]migration{SchemaVersion: tc.step})
			if err == nil || !strings.Contains(err.Error(), tc.stage) {
				t.Fatalf("want failure at %q, got %v", tc.stage, err)
			}
			copies := stateCopies(t, path)
			if len(copies) != 1 || !strings.Contains(err.Error(), copies[0]) {
				t.Fatalf("error must name the pre-migration copy %v: %v", copies, err)
			}
			if v := userVersion(t, db); v != SchemaVersion {
				t.Fatalf("user_version %d after failed migration", v)
			}
			var value string
			db.QueryRow("SELECT value_json FROM settings WHERE key = 'probe'").Scan(&value)
			var ledger int
			db.QueryRow("SELECT count(*) FROM uid_ledger").Scan(&ledger)
			if !strings.Contains(value, "before") || ledger != 1 {
				t.Fatalf("failed migration was not rolled back: %q ledger=%d", value, ledger)
			}
		})
	}
}

func TestMigrateRefusesGapsAndUnsupportedVersionsWithoutWriting(t *testing.T) {
	ctx := t.Context()
	path := newStateFile(t)
	db := openRaw(t, path)
	// No step registered for the current version.
	if err := migrate(ctx, db, path, SchemaVersion+1, nil); !errors.Is(err, ErrUnsupportedState) {
		t.Fatalf("missing step: %v", err)
	}
	// A target below the file's version is a downgrade.
	if err := migrate(ctx, db, path, SchemaVersion-1, nil); !errors.Is(err, ErrUnsupportedState) {
		t.Fatalf("downgrade: %v", err)
	}
	if v := userVersion(t, db); v != SchemaVersion {
		t.Fatalf("user_version changed to %d", v)
	}
	if copies := stateCopies(t, path); len(copies) != 0 {
		t.Fatalf("a refused migration must not copy state: %v", copies)
	}
}

func TestOpenCurrentSchemaDoesNotCopyState(t *testing.T) {
	path := newStateFile(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), StateBackupsDir)); !os.IsNotExist(err) {
		t.Fatalf("opening the current schema must not create %s: %v", StateBackupsDir, err)
	}
}

func TestSupportedVersionsStartAtBaseline(t *testing.T) {
	got := SupportedVersions()
	if len(got) == 0 || got[0] != MigratableFromVersion || got[len(got)-1] != SchemaVersion {
		t.Fatalf("supported versions %v", got)
	}
	for v := MigratableFromVersion; v < SchemaVersion; v++ {
		if migrations[int64(v)] == nil {
			t.Fatalf("schema %d has no migration to %d", v, v+1)
		}
	}
	dir := t.TempDir()
	for _, v := range []int{MigratableFromVersion - 1, SchemaVersion + 1} {
		p := filepath.Join(dir, "v.db")
		os.Remove(p)
		makeForeignDB(t, p, ApplicationID, v)
		if err := CheckCompatible(p); !errors.Is(err, ErrUnsupportedState) {
			t.Fatalf("version %d must be refused: %v", v, err)
		}
	}
}

// TestCopyFileSeesLiveWALWithoutWriting pins the snapshot an upgrade plan
// works on: it includes commits still in the WAL of a serving backend, and
// never writes the source.
func TestCopyFileSeesLiveWALWithoutWriting(t *testing.T) {
	ctx := t.Context()
	path := newStateFile(t)
	live, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := PutSetting(ctx, live.DB(), "probe", map[string]string{"v": "in-wal"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "copy.db")
	appID, version, err := CopyFile(ctx, path, dest)
	if err != nil {
		t.Fatal(err)
	}
	if appID != ApplicationID || version != SchemaVersion {
		t.Fatalf("copy reports app id %#x version %d", appID, version)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("copying must not write the source database")
	}
	cp, err := Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	var got map[string]string
	if _, err := GetSetting(ctx, cp.DB(), "probe", &got); err != nil || got["v"] != "in-wal" {
		t.Fatalf("copy misses the live commit: %v %v", got, err)
	}
	if info, _ := os.Stat(dest); info.Mode().Perm() != 0o600 {
		t.Fatalf("copy mode %v, want 0600", info.Mode().Perm())
	}
	if _, _, err := CopyFile(ctx, path, dest); err == nil {
		t.Fatal("an existing destination must be refused")
	}
	// A stopped backend leaves no WAL or shared-memory file behind.
	live.Close()
	if _, _, err := CopyFile(ctx, path, filepath.Join(t.TempDir(), "stopped.db")); err != nil {
		t.Fatalf("copy of a stopped backend's state: %v", err)
	}
	for _, side := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + side); err == nil {
			t.Fatalf("copying a stopped backend's state left %s behind", filepath.Base(path+side))
		}
	}
}
