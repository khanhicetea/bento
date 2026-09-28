package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Create(filepath.Join(t.TempDir(), "bento.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// makeForeignDB writes a SQLite database the way another program would.
func makeForeignDB(t *testing.T, path string, appID, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"CREATE TABLE state (id INTEGER PRIMARY KEY, json TEXT)", "INSERT INTO state VALUES (1, '{}')"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA application_id = " + strconv.Itoa(appID) + "; PRAGMA user_version = " + strconv.Itoa(version)); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesForeignOldFutureAndCorruptStateWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]func(path string){
		"foreign database":  func(p string) { makeForeignDB(t, p, 0, 3) },
		"older schema":      func(p string) { makeForeignDB(t, p, ApplicationID, 0) },
		"future schema":     func(p string) { makeForeignDB(t, p, ApplicationID, SchemaVersion+1) },
		"not sqlite at all": func(p string) { os.WriteFile(p, []byte("definitely not a database file, just text"), 0o600) },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".db")
			mk(path)
			before := fileHash(t, path)
			if _, err := Open(path); err == nil {
				t.Fatal("expected refusal")
			}
			if after := fileHash(t, path); after != before {
				t.Fatal("refusal modified the file")
			}
			for _, sfx := range []string{"-wal", "-shm", "-journal"} {
				if _, err := os.Stat(path + sfx); err == nil {
					t.Fatalf("refusal created %s", sfx)
				}
			}
		})
	}
}

func TestCreateRefusesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bento.db")
	os.WriteFile(path, []byte("x"), 0o600)
	if _, err := Create(path); err == nil {
		t.Fatal("expected refusal to overwrite")
	}
}

func TestOpenCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bento.db")
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state db mode %v, want 0600", info.Mode().Perm())
	}
}

type fakeHost map[int]bool

func (f fakeHost) Taken(id int) bool { return f[id] }

func TestAllocatorNeverReusesAndSkipsHostCollisions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	rng := domain.UIDRange{First: 10000, Last: 10005}
	host := fakeHost{10001: true}
	var got []int
	for i := 0; i < 3; i++ {
		err := s.Tx(ctx, func(q Q) error {
			uid, err := AllocateUID(ctx, q, rng, host, "app"+strconv.Itoa(i), "s"+strconv.Itoa(i))
			got = append(got, uid)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if got[0] != 10000 || got[1] != 10002 || got[2] != 10003 {
		t.Fatalf("allocations %v", got)
	}
	// Retiring does not make an id reusable.
	if err := SetLedgerState(ctx, s.DB(), 10003, "retired"); err != nil {
		t.Fatal(err)
	}
	var next int
	s.Tx(ctx, func(q Q) error {
		var err error
		next, err = AllocateUID(ctx, q, rng, host, "app9", "again")
		return err
	})
	if next != 10004 {
		t.Fatalf("expected 10004 after retirement, got %d", next)
	}
	// A failed provisioning transaction that rolls back does not burn, but a
	// committed burned allocation is never reclaimed.
	s.Tx(ctx, func(q Q) error {
		_, err := AllocateUID(ctx, q, rng, host, "x", "x")
		if err != nil {
			return err
		}
		return errors.New("rollback")
	})
	s.Tx(ctx, func(q Q) error {
		uid, err := AllocateUID(ctx, q, rng, host, "y", "y")
		if uid != 10005 {
			t.Errorf("rolled-back allocation should leave 10005 available, got %d", uid)
		}
		return err
	})
	err := s.Tx(ctx, func(q Q) error {
		_, err := AllocateUID(ctx, q, rng, host, "z", "z")
		return err
	})
	if !errors.Is(err, ErrUIDExhausted) {
		t.Fatalf("expected exhaustion, got %v", err)
	}
	ledger, _ := ListLedger(ctx, s.DB())
	if len(ledger) != 5 {
		t.Fatalf("ledger rows %d", len(ledger))
	}
}

func TestAllocatorConcurrentUnique(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	rng := domain.UIDRange{First: 20000, Last: 29999}
	var mu sync.Mutex
	seen := map[int]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.Tx(ctx, func(q Q) error {
				uid, err := AllocateUID(ctx, q, rng, nil, "a"+strconv.Itoa(i), "s"+strconv.Itoa(i))
				if err == nil {
					mu.Lock()
					if seen[uid] {
						t.Errorf("duplicate uid %d", uid)
					}
					seen[uid] = true
					mu.Unlock()
				}
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(seen) != 25 {
		t.Fatalf("allocated %d unique uids", len(seen))
	}
}

func TestOperationIdempotencyAndInterruption(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	op := Operation{ID: platform.NewOperationID(), Kind: "app.start", TargetKind: "app", TargetID: "a1", IdempotencyKey: "key-12345678"}
	first, existed, err := InsertOperation(ctx, s.DB(), op)
	if err != nil || existed {
		t.Fatal(err, existed)
	}
	op.ID = platform.NewOperationID()
	again, existed, err := InsertOperation(ctx, s.DB(), op)
	if err != nil || !existed || again.ID != first.ID {
		t.Fatalf("idempotent resubmission returned %v %v %v", again.ID, existed, err)
	}
	op.Kind = "app.stop"
	if _, _, err := InsertOperation(ctx, s.DB(), op); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused key for another kind must conflict, got %v", err)
	}
	if ok, err := MarkRunning(ctx, s.DB(), first.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	interrupted, err := InterruptRunning(ctx, s.DB())
	if err != nil || len(interrupted) != 1 {
		t.Fatal(err, len(interrupted))
	}
	got, _ := GetOperation(ctx, s.DB(), first.ID)
	if got.State != OpInterrupted || got.Guidance == "" {
		t.Fatalf("state %s guidance %q", got.State, got.Guidance)
	}
	if _, err := NextQueued(ctx, s.DB()); !errors.Is(err, ErrNotFound) {
		t.Fatal("interrupted operation must not be requeued")
	}
}

// M1: an operation cancelled after NextQueued read it must not be claimed.
func TestCancelledQueuedOperationIsNotClaimed(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	op, _, err := InsertOperation(ctx, s.DB(), Operation{ID: platform.NewOperationID(), Kind: "app.start", TargetKind: "app", TargetID: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := NextQueued(ctx, s.DB())
	if err != nil || next.ID != op.ID {
		t.Fatal(err)
	}
	if _, err := RequestCancel(ctx, s.DB(), op.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := MarkRunning(ctx, s.DB(), next.ID); err != nil || ok {
		t.Fatalf("claimed a cancelled operation: %v %v", ok, err)
	}
	if got, _ := GetOperation(ctx, s.DB(), op.ID); got.State != OpCancelled {
		t.Fatalf("state %s", got.State)
	}
}

func TestDomainOwnershipUnique(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	links := []domain.DomainLink{{Name: "a.example.com", Primary: true}}
	if err := ReplaceDomains(ctx, s.DB(), "app", "a1", links); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceDomains(ctx, s.DB(), "proxy", "p1", links); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if err := ReplaceDomains(ctx, s.DB(), "app", "a2", []domain.DomainLink{{Name: "b.example.com"}}); err == nil {
		t.Fatal("expected exactly-one-primary refusal")
	}
}
