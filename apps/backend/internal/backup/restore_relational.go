package backup

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// RestoreOptions connects a relational restore to the operation running it.
// Every hook is optional.
type RestoreOptions struct {
	// Phase records progress before each external effect. An error (a
	// cancellation) before the swap leaves the live database unchanged.
	Phase func(ctx context.Context, name string) error
	// Quiesce stops every client of the database. It is called only when the
	// live database must be rebuilt in place (see RestoreInPlace).
	Quiesce func(ctx context.Context) error
	// Warn reports a problem that does not fail the restore, such as a
	// staging database that could not be dropped.
	Warn func(msg string)
}

func (o RestoreOptions) phase(ctx context.Context, name string) error {
	if o.Phase == nil {
		return ctx.Err()
	}
	return o.Phase(ctx, name)
}

func (o RestoreOptions) warn(format string, args ...any) {
	if o.Warn != nil {
		o.Warn(fmt.Sprintf(format, args...))
	}
}

// How a restored database replaced the live one.
const (
	// RestoreSwapped: the artifact was restored into a staging database that
	// then replaced the live one in a single rename.
	RestoreSwapped = "swap"
	// RestoreInPlace: the staging restore succeeded, but MySQL cannot move
	// views, routines or events between schemas, so the live database was
	// dropped and the artifact replayed into it a second time with the app
	// stopped.
	RestoreInPlace = "in-place"
)

// LiveUnchangedError is a restore failure that happened before the live
// database was touched: a corrupt or truncated artifact, a statement the
// binding user may not run, or a cancellation before the swap.
type LiveUnchangedError struct{ Err error }

func (e *LiveUnchangedError) Error() string { return e.Err.Error() }
func (e *LiveUnchangedError) Unwrap() error { return e.Err }

func unchanged(err error) error {
	if err == nil {
		return nil
	}
	return &LiveUnchangedError{Err: err}
}

// stagingNames are the databases a restore of database uses on its service:
// stage receives the artifact, trash briefly holds the replaced data. App
// database names never carry domain.ReservedDatabasePrefix, so the names
// never collide with an app database.
func stagingNames(database string) (stage, trash string) {
	h := platform.SHA256Hex([]byte(database))[:16]
	return domain.ReservedDatabasePrefix + "rs_" + h, domain.ReservedDatabasePrefix + "rt_" + h
}

// RestoreRelational replaces an app-namespaced database with an artifact.
//
// The artifact is first restored into a staging database as the binding
// user. A corrupt or truncated artifact, or a statement the binding user may
// not run, fails there and leaves the live database unchanged
// (LiveUnchangedError). The staging database then replaces the live one:
// PostgreSQL renames the databases; MySQL moves every table in one RENAME
// TABLE. A MySQL database with views, routines or events cannot be moved
// that way, so it is rebuilt in place after opts.Quiesce (RestoreInPlace).
//
// Dumps are always replayed as the binding user, never as the administrator,
// so a restored view, routine, trigger or event can never run with more than
// the app's own privileges.
func (d Deps) RestoreRelational(
	ctx context.Context,
	app domain.App,
	b domain.Binding,
	database, artifactPath string,
	opts RestoreOptions,
) (string, error) {
	if err := domain.ValidateDatabaseName(database); err != nil {
		return "", unchanged(fmt.Errorf("database %q: %w", database, err))
	}
	id, svc, err := d.ServiceContainer(ctx, b.Service)
	if err != nil {
		return "", unchanged(err)
	}
	switch svc.Engine {
	case domain.EngineMySQL:
		return d.restoreMySQL(ctx, svc, id, b, database, artifactPath, opts)
	case domain.EnginePostgres:
		return d.restorePostgres(ctx, svc, id, b, database, artifactPath, opts)
	}
	return "", unchanged(errors.New("not a relational binding"))
}

// ---- MySQL ----

func (d Deps) restoreMySQL(
	ctx context.Context,
	svc domain.DataService,
	id string,
	b domain.Binding,
	database, artifactPath string,
	opts RestoreOptions,
) (string, error) {
	stage, trash := stagingNames(database)
	if err := d.dropMySQLStaging(ctx, svc, id, b.Username, stage, trash); err != nil {
		return "", unchanged(err)
	}
	if err := opts.phase(ctx, "restore-stage"); err != nil {
		return "", unchanged(err)
	}
	var charset string
	err := d.replayMySQL(ctx, svc, id, b, stage, artifactPath, func(cs string) string {
		charset = cs
		return fmt.Sprintf("CREATE DATABASE `%s` %s;\nGRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';\n",
			stage, cs, grantPattern(stage), b.Username)
	})
	// From here on, the staging databases are dropped however the restore
	// ends; a failure to drop them is only a warning (the next restore of
	// this database drops them first).
	defer func() {
		if derr := d.dropMySQLStaging(context.WithoutCancel(ctx), svc, id, b.Username, stage, trash); derr != nil {
			opts.warn("staging databases of %s were not dropped: %v", database, derr)
		}
	}()
	if err != nil {
		return "", unchanged(err)
	}
	if err := opts.phase(ctx, "swap"); err != nil {
		return "", unchanged(err)
	}
	staged, err := d.mysqlObjects(ctx, svc, id, stage)
	if err != nil {
		return "", unchanged(err)
	}
	if movable(staged) {
		return RestoreSwapped, d.swapMySQL(ctx, svc, id, b, database, trash, stage, charset, staged)
	}

	// Views, routines and events stay bound to their schema; replay the
	// artifact into the live database itself. The staging restore proved
	// that the artifact is intact and replays as the binding user.
	if opts.Quiesce != nil {
		if err := opts.Quiesce(ctx); err != nil {
			return "", unchanged(fmt.Errorf("stop the app: %w", err))
		}
	}
	if err := opts.phase(ctx, "restore-in-place"); err != nil {
		return "", unchanged(err)
	}
	err = d.replayMySQL(ctx, svc, id, b, database, artifactPath, func(cs string) string {
		return fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;\nCREATE DATABASE `%s` %s;\nGRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';\n",
			database, database, cs, database, b.Username)
	})
	return RestoreInPlace, err
}

// replayMySQL runs prepare (given the dump's charset clause) as the
// administrator, then streams the artifact into db as the binding user with
// DEFINER clauses removed, so every restored object belongs to that user.
func (d Deps) replayMySQL(
	ctx context.Context,
	svc domain.DataService,
	id string,
	b domain.Binding,
	db, artifactPath string,
	prepare func(charsetClause string) string,
) error {
	src, err := Decompress(artifactPath)
	if err != nil {
		return err
	}
	defer src.Close()
	in := &checkedReader{r: src}
	peek := bufio.NewReaderSize(in, dumpPeekBytes)
	// A short dump returns fewer bytes and io.EOF; the charset sniff only
	// needs whatever is available.
	head, _ := peek.Peek(dumpPeekBytes)
	if err := in.Err(); err != nil {
		return fmt.Errorf("read artifact: %w", err)
	}
	if _, err := d.Data.SQL(ctx, svc, id, "", prepare(mysqlCharsetClause(head))); err != nil {
		return err
	}
	cnf, remove, err := d.Data.AppClientFile(svc.Name, b.Username, b.Password)
	if err != nil {
		return err
	}
	defer remove()
	stream, finish := stripDefiners(peek)
	var stderr strings.Builder
	res, err := d.Engine.Exec(ctx, id, docker.ExecRequest{
		Cmd: []string{"mysql", "--defaults-extra-file=" + cnf, db}, Stdin: stream,
		Stdout: io.Discard, Stderr: &limitWriter{w: &stderr, n: 4096},
	})
	finish()
	// A read error ends stdin early; the client may then exit 0 after
	// replaying a prefix of the dump, so it is checked first.
	if rerr := in.Err(); rerr != nil {
		return fmt.Errorf("read artifact: %w", rerr)
	}
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("mysql restore failed: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// stripDefiners filters r through definerFilter. finish releases the
// filtering goroutine and waits for it; call it once the reader is no
// longer used.
func stripDefiners(r io.Reader) (io.Reader, func()) {
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f := &definerFilter{w: pw}
		_, err := io.Copy(f, r)
		if err == nil {
			err = f.Flush()
		}
		pw.CloseWithError(err)
	}()
	return pr, func() {
		_ = pr.Close()
		<-done
	}
}

// swapMySQL moves the live database's tables to trash and the staged tables
// into the live database in one RENAME TABLE, which MySQL applies
// atomically. Triggers block moving a table to another schema and views
// share the table namespace, so the live database's triggers and views are
// dropped first; its routines and events are dropped after the swap. All of
// them are replaced by the artifact's (it has none, or the in-place path
// would have run).
func (d Deps) swapMySQL(
	ctx context.Context,
	svc domain.DataService,
	id string,
	b domain.Binding,
	database, trash, stage, charset string,
	staged []dbObject,
) error {
	exists, err := d.Data.DatabaseExists(ctx, svc, id, database)
	if err != nil {
		return unchanged(err)
	}
	var live []dbObject
	if exists {
		if live, err = d.mysqlObjects(ctx, svc, id, database); err != nil {
			return unchanged(err)
		}
	}
	var sql strings.Builder
	if exists {
		fmt.Fprintf(&sql, "ALTER DATABASE `%s` %s;\n", database, charset)
	} else {
		fmt.Fprintf(&sql, "CREATE DATABASE `%s` %s;\nGRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';\n",
			database, charset, database, b.Username)
	}
	fmt.Fprintf(&sql, "CREATE DATABASE `%s`;\nGRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';\n",
		trash, grantPattern(trash), b.Username)
	// An open transaction of a running app holds metadata locks; wait for it
	// a bounded time instead of the server's default of a year.
	sql.WriteString("SET SESSION lock_wait_timeout = 60;\n")
	for _, o := range live {
		if o.kind == "TRIGGER" || o.kind == "VIEW" {
			fmt.Fprintf(&sql, "DROP %s `%s`.%s;\n", o.kind, database, quoteIdent(o.name))
		}
	}
	var moves []string
	for _, o := range live {
		if o.kind == "TABLE" {
			moves = append(moves, fmt.Sprintf("`%s`.%s TO `%s`.%s", database, quoteIdent(o.name), trash, quoteIdent(o.name)))
		}
	}
	for _, o := range staged {
		moves = append(moves, fmt.Sprintf("`%s`.%s TO `%s`.%s", stage, quoteIdent(o.name), database, quoteIdent(o.name)))
	}
	if len(moves) > 0 {
		fmt.Fprintf(&sql, "RENAME TABLE %s;\n", strings.Join(moves, ", "))
	}
	for _, o := range live {
		if o.kind == "PROCEDURE" || o.kind == "FUNCTION" || o.kind == "EVENT" {
			fmt.Fprintf(&sql, "DROP %s `%s`.%s;\n", o.kind, database, quoteIdent(o.name))
		}
	}
	if _, err := d.Data.SQL(ctx, svc, id, "", sql.String()); err != nil {
		return fmt.Errorf("swap into %s: %w", database, err)
	}
	return nil
}

// dropMySQLStaging drops leftover staging databases of user. A staging name
// that exists without this user's grant is not ours and is refused.
func (d Deps) dropMySQLStaging(ctx context.Context, svc domain.DataService, id, user string, names ...string) error {
	for _, n := range names {
		exists, err := d.Data.DatabaseExists(ctx, svc, id, n)
		if err != nil {
			return err
		}
		if exists {
			owned, err := d.Data.SQL(ctx, svc, id, "", fmt.Sprintf(
				"SELECT COUNT(*) FROM mysql.db WHERE User = '%s' AND Host = '%%' AND Db = '%s';\n",
				user, grantPattern(n)))
			if err != nil {
				return err
			}
			if owned == "0" {
				return fmt.Errorf("database %s exists but was not staged for %s; refusing to drop it", n, user)
			}
		}
		if _, err := d.Data.SQL(ctx, svc, id, "", fmt.Sprintf(
			"DROP DATABASE IF EXISTS `%s`;\nREVOKE IF EXISTS ALL PRIVILEGES ON `%s`.* FROM '%s'@'%%';\n",
			n, grantPattern(n), user)); err != nil {
			return err
		}
	}
	return nil
}

// grantPattern escapes "_" in a database name: in a database-level GRANT an
// unescaped "_" matches any character, which would let the user create and
// use other databases matching the pattern.
func grantPattern(name string) string { return strings.ReplaceAll(name, "_", `\_`) }

type dbObject struct {
	kind string // TABLE, VIEW, PROCEDURE, FUNCTION, TRIGGER, EVENT
	name string
}

// movable reports whether RENAME TABLE can move every object of a database:
// only base tables move between schemas.
func movable(objs []dbObject) bool {
	for _, o := range objs {
		if o.kind != "TABLE" {
			return false
		}
	}
	return true
}

// mysqlObjects lists the schema objects of db. Names come back hex-encoded
// so that any byte an app put in a name survives batch output.
func (d Deps) mysqlObjects(ctx context.Context, svc domain.DataService, id, db string) ([]dbObject, error) {
	q := strings.ReplaceAll(`SELECT 'TABLE', HEX(TABLE_NAME) FROM information_schema.TABLES WHERE TABLE_SCHEMA = '@' AND TABLE_TYPE = 'BASE TABLE'
UNION ALL SELECT 'VIEW', HEX(TABLE_NAME) FROM information_schema.VIEWS WHERE TABLE_SCHEMA = '@'
UNION ALL SELECT ROUTINE_TYPE, HEX(ROUTINE_NAME) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = '@'
UNION ALL SELECT 'TRIGGER', HEX(TRIGGER_NAME) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = '@'
UNION ALL SELECT 'EVENT', HEX(EVENT_NAME) FROM information_schema.EVENTS WHERE EVENT_SCHEMA = '@';
`, "@", db)
	out, err := d.Data.SQL(ctx, svc, id, "", q)
	if err != nil {
		return nil, err
	}
	var objs []dbObject
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		kind, hexName, ok := strings.Cut(line, "\t")
		name, herr := hex.DecodeString(hexName)
		if !ok || herr != nil || len(name) == 0 {
			return nil, fmt.Errorf("unexpected schema listing line %q", line)
		}
		objs = append(objs, dbObject{kind: kind, name: string(name)})
	}
	return objs, nil
}

func quoteIdent(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }

// ---- PostgreSQL ----

func (d Deps) restorePostgres(
	ctx context.Context,
	svc domain.DataService,
	id string,
	b domain.Binding,
	database, artifactPath string,
	opts RestoreOptions,
) (string, error) {
	stage, trash := stagingNames(database)
	if err := d.dropPostgresStaging(ctx, svc, id, b.Username, stage, trash); err != nil {
		return "", unchanged(err)
	}
	if err := opts.phase(ctx, "restore-stage"); err != nil {
		return "", unchanged(err)
	}
	swapped := false
	defer func() {
		names := []string{trash}
		if !swapped {
			names = append(names, stage)
		}
		if derr := d.dropPostgresStaging(context.WithoutCancel(ctx), svc, id, b.Username, names...); derr != nil {
			opts.warn("staging databases of %s were not dropped: %v", database, derr)
		}
	}()
	if err := d.replayPostgres(ctx, svc, id, b, stage, artifactPath); err != nil {
		return "", unchanged(err)
	}
	if err := opts.phase(ctx, "swap"); err != nil {
		return "", unchanged(err)
	}
	exists, err := d.Data.DatabaseExists(ctx, svc, id, database)
	if err != nil {
		return "", unchanged(err)
	}
	if !exists {
		if _, err := d.Data.SQL(ctx, svc, id, "", fmt.Sprintf("ALTER DATABASE \"%s\" RENAME TO \"%s\";\n", stage, database)); err != nil {
			return "", unchanged(err)
		}
		swapped = true
		return RestoreSwapped, nil
	}
	// Renaming a database needs it to have no sessions: refuse new ones,
	// end the app's, then swap both names in one transaction (RENAME waits
	// a few seconds for terminated sessions to exit).
	swap := fmt.Sprintf(
		"ALTER DATABASE \"%s\" WITH ALLOW_CONNECTIONS false;\n"+
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();\n"+
			"BEGIN;\nALTER DATABASE \"%s\" RENAME TO \"%s\";\nALTER DATABASE \"%s\" RENAME TO \"%s\";\nCOMMIT;\n",
		database, database, database, trash, stage, database)
	if _, err := d.Data.SQL(ctx, svc, id, "", swap); err != nil {
		reopen := fmt.Sprintf("ALTER DATABASE \"%s\" WITH ALLOW_CONNECTIONS true;\n", database)
		if _, rerr := d.Data.SQL(context.WithoutCancel(ctx), svc, id, "", reopen); rerr != nil {
			return "", fmt.Errorf("swap into %s: %w; connections to it stay refused: %v", database, err, rerr)
		}
		return "", unchanged(fmt.Errorf("swap into %s: %w", database, err))
	}
	swapped = true
	return RestoreSwapped, nil
}

// replayPostgres creates db owned by the binding user with the provisioning
// lockdown and streams the artifact into it as that user. psql connects as
// the user itself (local socket), not as the superuser with SET ROLE, so a
// RESET ROLE in a dump cannot regain superuser rights.
func (d Deps) replayPostgres(
	ctx context.Context,
	svc domain.DataService,
	id string,
	b domain.Binding,
	db, artifactPath string,
) error {
	if _, err := d.Data.SQL(ctx, svc, id, "", fmt.Sprintf("CREATE DATABASE \"%s\" OWNER \"%s\";\n", db, b.Username)); err != nil {
		return err
	}
	lockdown := fmt.Sprintf(
		"REVOKE ALL ON DATABASE \"%s\" FROM PUBLIC;\n"+
			"GRANT CONNECT, TEMPORARY ON DATABASE \"%s\" TO \"%s\";\n"+
			"REVOKE ALL ON SCHEMA public FROM PUBLIC;\n"+
			"ALTER SCHEMA public OWNER TO \"%s\";\n",
		db, db, b.Username, b.Username)
	if _, err := d.Data.SQL(ctx, svc, id, db, lockdown); err != nil {
		return err
	}
	src, err := Decompress(artifactPath)
	if err != nil {
		return err
	}
	defer src.Close()
	in := &checkedReader{r: src}
	var stderr strings.Builder
	res, err := d.Engine.Exec(ctx, id, docker.ExecRequest{
		Cmd:   []string{"psql", "-U", b.Username, "-d", db, "-X", "-q", "-v", "ON_ERROR_STOP=1"},
		Stdin: in, Stdout: io.Discard, Stderr: &limitWriter{w: &stderr, n: 4096},
	})
	if rerr := in.Err(); rerr != nil {
		return fmt.Errorf("read artifact: %w", rerr)
	}
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("postgres restore failed: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// dropPostgresStaging drops leftover staging databases of user. A staging
// name owned by anyone else is not ours and is refused.
func (d Deps) dropPostgresStaging(ctx context.Context, svc domain.DataService, id, user string, names ...string) error {
	for _, n := range names {
		owner, err := d.Data.SQL(ctx, svc, id, "", fmt.Sprintf(
			"SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = '%s';\n", n))
		if err != nil {
			return err
		}
		if owner == "" {
			continue
		}
		if owner != user {
			return fmt.Errorf("database %s exists but is owned by %s, not %s; refusing to drop it", n, owner, user)
		}
		if _, err := d.Data.SQL(ctx, svc, id, "", fmt.Sprintf("DROP DATABASE IF EXISTS \"%s\" WITH (FORCE);\n", n)); err != nil {
			return err
		}
	}
	return nil
}

// checkedReader records the first read error other than io.EOF. The
// replaying client sees such an error only as an early end of input.
type checkedReader struct {
	r   io.Reader
	mu  sync.Mutex
	err error
}

func (c *checkedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		c.mu.Lock()
		if c.err == nil {
			c.err = err
		}
		c.mu.Unlock()
	}
	return n, err
}

func (c *checkedReader) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
