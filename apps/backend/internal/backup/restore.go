package backup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// RestoreRelational replaces an app-namespaced database with an artifact.
// Not object-level atomic: a failure may leave a partial destination.
func (d Deps) RestoreRelational(ctx context.Context, app domain.App, b domain.Binding, database, artifactPath string) error {
	id, svc, err := d.ServiceContainer(ctx, b.Service)
	if err != nil {
		return err
	}
	src, err := Decompress(artifactPath)
	if err != nil {
		return err
	}
	defer src.Close()
	var stderr strings.Builder
	switch svc.Engine {
	case domain.EngineMySQL:
		peek := bufio.NewReaderSize(src, dumpPeekBytes)
		// A short dump returns fewer bytes and io.EOF; the charset sniff only
		// needs whatever is available.
		head, _ := peek.Peek(dumpPeekBytes)
		reset := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;\nCREATE DATABASE `%s` %s;\nGRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';\n",
			database, database, mysqlCharsetClause(head), database, b.Username)
		if _, err := d.Data.SQL(ctx, svc, id, "", reset); err != nil {
			return err
		}
		res, err := d.Engine.Exec(ctx, id, docker.ExecRequest{
			Cmd: []string{"mysql", "--defaults-extra-file=/run/bento-secrets/client.cnf", database}, Stdin: peek,
			Stdout: io.Discard, Stderr: &limitWriter{w: &stderr, n: 4096},
		})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("mysql restore failed: %s", strings.TrimSpace(stderr.String()))
		}
	case domain.EnginePostgres:
		reset := fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();\n"+
			"DROP DATABASE IF EXISTS \"%s\";\nCREATE DATABASE \"%s\" OWNER \"%s\";\n", database, database, database, b.Username)
		if _, err := d.Data.SQL(ctx, svc, id, "", reset); err != nil {
			return err
		}
		if _, err := d.Data.SQL(ctx, svc, id, database, fmt.Sprintf(
			"REVOKE ALL ON DATABASE \"%s\" FROM PUBLIC;\nREVOKE ALL ON SCHEMA public FROM PUBLIC;\nALTER SCHEMA public OWNER TO \"%s\";\n", database, b.Username)); err != nil {
			return err
		}
		// Objects are created as the app role so ownership stays app-scoped.
		stream := io.MultiReader(strings.NewReader(fmt.Sprintf("SET ROLE \"%s\";\n", b.Username)), src)
		res, err := d.Engine.Exec(ctx, id, docker.ExecRequest{
			Cmd:   []string{"psql", "-U", "postgres", "-d", database, "-X", "-q", "-v", "ON_ERROR_STOP=1"},
			Stdin: stream, Stdout: io.Discard, Stderr: &limitWriter{w: &stderr, n: 4096},
		})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("postgres restore failed: %s", strings.TrimSpace(stderr.String()))
		}
	default:
		return errors.New("not a relational binding")
	}
	return nil
}

// RestoreSQLite replaces the binding's database file. The app must be
// stopped so no writer holds the file.
func (d Deps) RestoreSQLite(app domain.App, b domain.Binding, artifactPath string) error {
	dir := d.Layout.SQLiteFileDir(b.SQLiteFileID)
	if err := platform.NoSymlinkBetween(d.Layout.SQLiteDir(), dir); err != nil {
		return err
	}
	src, err := Decompress(artifactPath)
	if err != nil {
		return err
	}
	defer src.Close()
	partial := filepath.Join(dir, ".restore-"+platform.RandomHex(6))
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	head := make([]byte, 16)
	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		_ = f.Close()
		_ = os.Remove(partial)
		return fmt.Errorf("read artifact: %w", err)
	}
	if n < 16 || string(head[:15]) != "SQLite format 3" {
		_ = f.Close()
		_ = os.Remove(partial)
		return errors.New("artifact is not a SQLite database")
	}
	// Any write, copy, sync, or close failure aborts before the rename so a
	// truncated database is never published.
	_, err = f.Write(head)
	if err == nil {
		_, err = io.Copy(f, src)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(partial)
		return err
	}
	if err := os.Lchown(partial, app.UID, app.GID); err != nil {
		_ = os.Remove(partial)
		return err
	}
	target := filepath.Join(dir, app.Slug+".db")
	// Stale journal files of the replaced database must not be replayed
	// against the restored file; they may or may not exist.
	for _, sfx := range []string{"-wal", "-shm"} {
		if err := os.Remove(target + sfx); err != nil && !errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(partial)
			return fmt.Errorf("remove stale journal: %w", err)
		}
	}
	return os.Rename(partial, target)
}

// dumpPeekBytes is how much of a MySQL dump is inspected for its charset.
const dumpPeekBytes = 64 << 10

var (
	reCreateDBCharset = regexp.MustCompile(`(?i)CREATE DATABASE[^;]*?CHARACTER SET\s*=?\s*([a-z0-9_]+)(?:[^;]*?COLLATE\s*=?\s*([a-z0-9_]+))?`)
	reSetNames        = regexp.MustCompile(`(?i)SET NAMES\s+'?([a-z0-9_]+)'?(?:\s+COLLATE\s+'?([a-z0-9_]+)'?)?`)
)

// mysqlCharsetClause returns the CHARACTER SET/COLLATE clause for a restored
// database: the dump's CREATE DATABASE line wins, then its first SET NAMES,
// then the Bento default. Only identifier characters are ever accepted.
func mysqlCharsetClause(head []byte) string {
	for _, re := range []*regexp.Regexp{reCreateDBCharset, reSetNames} {
		m := re.FindSubmatch(head)
		if m == nil {
			continue
		}
		cs, coll := strings.ToLower(string(m[1])), strings.ToLower(string(m[2]))
		if cs == "binary" {
			break
		}
		if coll == "" && cs == domain.MySQLDefaultCharset {
			coll = domain.MySQLDefaultCollation
		}
		if coll == "" {
			return "CHARACTER SET " + cs
		}
		return "CHARACTER SET " + cs + " COLLATE " + coll
	}
	return "CHARACTER SET " + domain.MySQLDefaultCharset + " COLLATE " + domain.MySQLDefaultCollation
}
