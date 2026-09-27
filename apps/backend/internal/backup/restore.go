package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		reset := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;\nCREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;\nGRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';\n",
			database, database, database, b.Username)
		if _, err := d.Data.SQL(ctx, svc, id, "", reset); err != nil {
			return err
		}
		res, err := d.Engine.Exec(ctx, id, docker.ExecRequest{
			Cmd: []string{"mysql", "--defaults-extra-file=/run/bento-secrets/client.cnf", database}, Stdin: src,
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
		return fmt.Errorf("not a relational binding")
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
	n, _ := io.ReadFull(src, head)
	if n < 16 || string(head[:15]) != "SQLite format 3" {
		f.Close()
		os.Remove(partial)
		return fmt.Errorf("artifact is not a SQLite database")
	}
	if _, err := f.Write(head); err == nil {
		_, err = io.Copy(f, src)
	}
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		os.Remove(partial)
		return err
	}
	if err := os.Lchown(partial, app.UID, app.GID); err != nil {
		os.Remove(partial)
		return err
	}
	target := filepath.Join(dir, app.Slug+".db")
	for _, sfx := range []string{"-wal", "-shm"} {
		_ = os.Remove(target + sfx)
	}
	return os.Rename(partial, target)
}
