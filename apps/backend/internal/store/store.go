// Package store owns the backend SQLite database: the authority for desired
// state, identities, allocation history, sessions, and operations.
//
// Driver: modernc.org/sqlite (pure Go, CGO_ENABLED=0). This keeps linux/amd64
// and linux/arm64 cross-builds free of a C toolchain. The trade-off is lower
// throughput than the CGO driver, which is irrelevant at control-plane scale.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"

	_ "modernc.org/sqlite"
)

// ApplicationID marks a Bento state database ("BNT1"). Files without it are
// refused.
const ApplicationID = 0x424E5431

// SchemaVersion is the only schema Open accepts. Every other version is
// refused without modifying the file. Schema changes after a release need a
// new version and an in-place migration in Open.
const SchemaVersion = 2

// TransferFormatVersion versions the serialized export manifest.
const TransferFormatVersion = 1

var ErrUnsupportedState = errors.New("unsupported state database")

type Store struct {
	db   *sql.DB
	path string
}

// Inspect reports the application id and schema version of an existing
// database without writing to it (immutable read-only open).
func Inspect(path string) (appID int64, version int64, err error) {
	if _, err := os.Stat(path); err != nil {
		return 0, 0, err
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()
	if err := db.QueryRow("PRAGMA application_id").Scan(&appID); err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrUnsupportedState, err)
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrUnsupportedState, err)
	}
	return appID, version, nil
}

// CheckCompatible refuses foreign, old, or future state with guidance. It
// accepts only SchemaVersion and never writes.
func CheckCompatible(path string) error {
	appID, version, err := Inspect(path)
	if err != nil {
		return err
	}
	if appID != ApplicationID {
		return fmt.Errorf(
			"%w: %s is not a Bento state database (application_id=%#x); it was left untouched",
			ErrUnsupportedState,
			path,
			appID,
		)
	}
	if version < SchemaVersion {
		return fmt.Errorf("%w: schema version %d is older than supported %d", ErrUnsupportedState, version, SchemaVersion)
	}
	if version > SchemaVersion {
		return fmt.Errorf(
			"%w: schema version %d was written by a newer Bento (supported %d)",
			ErrUnsupportedState,
			version,
			SchemaVersion,
		)
	}
	return nil
}

func dsn(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_txlock", "immediate")
	u.RawQuery = q.Encode()
	return u.String()
}

// Create initializes a new baseline database. The file must not exist.
func Create(path string) (*Store, error) {
	switch _, err := os.Lstat(path); {
	case err == nil:
		return nil, fmt.Errorf("refusing to initialize: %s already exists", path)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err := tx.Exec(schema); err != nil {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf(
		"PRAGMA application_id = %d; PRAGMA user_version = %d;",
		ApplicationID,
		SchemaVersion,
	)); err != nil {
		tx.Rollback()
		db.Close()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

// Open opens an existing compatible database. Incompatible files are refused
// before any write.
func Open(path string) (*Store, error) {
	if err := CheckCompatible(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	// A single connection serializes writers; readers are short-lived. The
	// backend never holds a transaction open across Docker calls.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	var check string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		db.Close()
		return nil, fmt.Errorf("%w: integrity check failed (%s %v); refusing to continue", ErrUnsupportedState, check, err)
	}
	return &Store{db: db, path: path}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Path() string { return s.path }

// Q is the subset of *sql.DB / *sql.Tx used by repositories.
type Q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) DB() Q { return s.db }

// Tx runs fn in an immediate transaction. Callers must not perform Docker or
// other slow external effects inside fn.
func (s *Store) Tx(ctx context.Context, fn func(q Q) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// SnapshotTo writes a consistent online copy of the database (VACUUM INTO).
func (s *Store) SnapshotTo(ctx context.Context, dest string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dest)
	return err
}

const schema = `
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;

CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
) STRICT;

-- Allocation ledger: never deleted, never reclaimed. state: allocated (reserved
-- during provisioning), active, retired (app removed), burned (failed provision).
CREATE TABLE uid_ledger (
  uid          INTEGER PRIMARY KEY,
  app_id       TEXT NOT NULL,
  slug         TEXT NOT NULL,
  state        TEXT NOT NULL CHECK (state IN ('allocated','active','retired','burned')),
  allocated_at TEXT NOT NULL,
  retired_at   TEXT
) STRICT;

CREATE TABLE apps (
  id                     TEXT PRIMARY KEY,
  slug                   TEXT NOT NULL UNIQUE,
  uid                    INTEGER NOT NULL UNIQUE REFERENCES uid_ledger(uid),
  gid                    INTEGER NOT NULL,
  home_path              TEXT,
  runtime_json           TEXT NOT NULL,
  resources_json         TEXT NOT NULL,
  desired_runtime        TEXT NOT NULL CHECK (desired_runtime IN ('stopped','running')),
  ingress                TEXT NOT NULL CHECK (ingress IN ('managed','external','none')),
  publication            TEXT NOT NULL CHECK (publication IN ('unpublished','published')),
  access_log             INTEGER NOT NULL DEFAULT 0 CHECK (access_log IN (0, 1)),
  valkey                 INTEGER NOT NULL DEFAULT 0 CHECK (valkey IN (0, 1)),
  minicrond              INTEGER NOT NULL DEFAULT 1 CHECK (minicrond IN (0, 1)),
  config_generation      INTEGER NOT NULL DEFAULT 1,
  credentials_generation INTEGER NOT NULL DEFAULT 1,
  provisioned            INTEGER NOT NULL DEFAULT 0,
  created_at             TEXT NOT NULL,
  updated_at             TEXT NOT NULL,
  CHECK (uid = gid),
  CHECK (publication = 'unpublished' OR ingress = 'managed')
) STRICT;

-- Ingress hosts. Each name points at exactly one target: an app (app_id),
-- external upstreams (upstreams_json), or another host name (redirect_to).
-- position orders an app's hosts; the lowest is its display host.
CREATE TABLE hosts (
  name           TEXT PRIMARY KEY,
  target_kind    TEXT NOT NULL CHECK (target_kind IN ('app','upstream','redirect')),
  app_id         TEXT REFERENCES apps(id) ON DELETE CASCADE,
  upstreams_json TEXT NOT NULL DEFAULT '[]',
  redirect_to    TEXT NOT NULL DEFAULT '',
  route_json     TEXT NOT NULL,
  enabled        INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  position       INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  CHECK ((target_kind = 'app') = (app_id IS NOT NULL)),
  CHECK (target_kind <> 'redirect' OR redirect_to <> '')
) STRICT;
CREATE INDEX hosts_app ON hosts(app_id);

CREATE TABLE data_services (
  name          TEXT PRIMARY KEY,
  engine        TEXT NOT NULL CHECK (engine IN ('mysql','postgres')),
  version       TEXT NOT NULL,
  image         TEXT NOT NULL,
  volume        TEXT NOT NULL UNIQUE,
  initialized   INTEGER NOT NULL DEFAULT 0,
  settings_json TEXT NOT NULL DEFAULT '{}',
  created_at    TEXT NOT NULL
) STRICT;

-- Add-only bindings: rows are never deleted while the app exists.
CREATE TABLE bindings (
  id             TEXT PRIMARY KEY,
  app_id         TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  engine         TEXT NOT NULL CHECK (engine IN ('mysql','postgres','sqlite')),
  service        TEXT,
  username       TEXT,
  password       TEXT,
  sqlite_file_id TEXT UNIQUE,
  vacuum_json    TEXT,
  position       INTEGER NOT NULL,
  created_at     TEXT NOT NULL,
  UNIQUE (app_id, service)
) STRICT;

CREATE TABLE binding_databases (
  binding_id TEXT NOT NULL REFERENCES bindings(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (binding_id, name)
) STRICT;

CREATE TABLE operations (
  id               TEXT PRIMARY KEY,
  kind             TEXT NOT NULL,
  target_kind      TEXT NOT NULL,
  target_id        TEXT NOT NULL,
  state            TEXT NOT NULL CHECK (state IN ('queued','running','succeeded','failed','cancelled','interrupted')),
  phase            TEXT NOT NULL DEFAULT '',
  target_generation INTEGER NOT NULL DEFAULT 0,
  idempotency_key  TEXT UNIQUE,
  request_json     TEXT NOT NULL,
  result_json      TEXT NOT NULL DEFAULT '{}',
  error_code       TEXT NOT NULL DEFAULT '',
  error_message    TEXT NOT NULL DEFAULT '',
  guidance         TEXT NOT NULL DEFAULT '',
  cancel_requested INTEGER NOT NULL DEFAULT 0,
  origin           TEXT NOT NULL DEFAULT 'api',
  created_at       TEXT NOT NULL,
  started_at       TEXT,
  finished_at      TEXT
) STRICT;
CREATE INDEX operations_state ON operations(state, created_at);
CREATE INDEX operations_target ON operations(target_kind, target_id, created_at);

CREATE TABLE operation_events (
  operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
  seq          INTEGER NOT NULL,
  at           TEXT NOT NULL,
  level        TEXT NOT NULL,
  message      TEXT NOT NULL,
  PRIMARY KEY (operation_id, seq)
) STRICT;

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  csrf_token TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  revoked    INTEGER NOT NULL DEFAULT 0
) STRICT;

-- Retired app incarnations and their retained durable artifacts. Rows are
-- kept after prune for history; pruned_at records permanent deletion.
CREATE TABLE retired_apps (
  app_id         TEXT PRIMARY KEY,
  slug           TEXT NOT NULL,
  uid            INTEGER NOT NULL,
  retired_at     TEXT NOT NULL,
  artifacts_json TEXT NOT NULL,
  pruned_at      TEXT
) STRICT;

CREATE TABLE backup_runs (
  id            TEXT PRIMARY KEY,
  trigger       TEXT NOT NULL,
  state         TEXT NOT NULL,
  started_at    TEXT NOT NULL,
  finished_at   TEXT,
  artifacts_json TEXT NOT NULL DEFAULT '[]',
  upload_state  TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE schedules (
  id           TEXT PRIMARY KEY,
  kind         TEXT NOT NULL,
  name         TEXT NOT NULL DEFAULT '',
  cron         TEXT NOT NULL,
  enabled      INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  spec_json    TEXT NOT NULL DEFAULT '{}',
  last_slot    TEXT NOT NULL DEFAULT '',
  last_run_at  TEXT NOT NULL DEFAULT '',
  last_op_id   TEXT NOT NULL DEFAULT '',
  last_state   TEXT NOT NULL DEFAULT '',
  missed_count INTEGER NOT NULL DEFAULT 0,
  revision     INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
) STRICT;

-- Scoped bearer credentials for automation. Only the SHA-256 of the secret
-- is stored.
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
