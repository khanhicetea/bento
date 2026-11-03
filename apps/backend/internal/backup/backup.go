// Package backup implements the pieces of app backup (restic): database
// dumps of app bindings (MySQL, PostgreSQL, SQLite) and their restores, the
// restic and rclone job containers, and the stack backup password.
//
// Administrator secrets never appear in argv; dumps are replayed as the
// binding user, never as the administrator.
package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/moby/moby/api/types/mount"

	"github.com/khanhicetea/bento/apps/backend/internal/dataservices"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

// Target is one binding database to back up.
type Target struct {
	App      domain.App
	Binding  domain.Binding
	Database string // relational database name; SQLite file id otherwise
}

type Deps struct {
	Engine docker.Engine
	Layout platform.Layout
	Names  runtime.Names
	Data   *dataservices.Manager
	// ServiceContainer resolves a running, ready service container.
	ServiceContainer func(ctx context.Context, service string) (string, domain.DataService, error)
	// OpenTool starts a scoped tooling container with an extra mount.
	ToolSpec func(ctx context.Context, app domain.App) (docker.ContainerSpec, error)
	Progress func(string)
}

func (d Deps) dumpRelational(ctx context.Context, t Target, w io.Writer) error {
	id, svc, err := d.ServiceContainer(ctx, t.Binding.Service)
	if err != nil {
		return err
	}
	var cmd []string
	if svc.Engine == domain.EngineMySQL {
		cmd = []string{"mysqldump", "--defaults-extra-file=/run/bento-secrets/client.cnf", "--single-transaction", "--quick",
			"--routines", "--triggers", "--events", "--hex-blob", "--no-tablespaces", "--set-gtid-purged=OFF", t.Database}
	} else {
		cmd = []string{"pg_dump", "-U", "postgres", "-d", t.Database, "--no-owner", "--no-privileges", "--format=plain"}
	}
	var stderr strings.Builder
	out := w
	var df *definerFilter
	if svc.Engine == domain.EngineMySQL {
		// The pinned mysqldump has no --skip-definer; strip DEFINER clauses
		// in-stream so a restore does not depend on the source account.
		df = &definerFilter{w: w}
		out = df
	}
	res, err := d.Engine.Exec(
		ctx,
		id,
		docker.ExecRequest{Cmd: cmd, Stdout: out, Stderr: &limitWriter{w: &stderr, n: 4096}},
	)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf(
			"%s dump of %s failed (exit %d): %s",
			svc.Engine,
			t.Database,
			res.ExitCode,
			strings.TrimSpace(stderr.String()),
		)
	}
	if df != nil {
		return df.Flush()
	}
	return nil
}

var definerClause = regexp.MustCompile("\\s*DEFINER\\s*=\\s*(?:`(?:[^`]|``)*`|'(?:[^']|'')*'|[A-Za-z0-9_.%-]+)@(?:`(?:[^`]|``)*`|'(?:[^']|'')*'|[A-Za-z0-9_.%-]+)")

// definerFilter strips DEFINER=user@host clauses from a mysqldump stream.
// Data lines (INSERT) are passed through unchanged however the stream is
// chunked; other lines are buffered to the newline and rewritten. Restores
// replay the result as the binding user, so every view, routine, trigger and
// event comes back owned by that user.
type definerFilter struct {
	w           io.Writer
	line        []byte
	passthrough bool
}

var insertPrefix = []byte("INSERT INTO ")

func (f *definerFilter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		if f.passthrough {
			i := bytes.IndexByte(p, '\n')
			chunk := p
			if i >= 0 {
				chunk = p[:i+1]
				f.passthrough = false
			}
			if _, err := f.w.Write(chunk); err != nil {
				return 0, err
			}
			p = p[len(chunk):]
			continue
		}
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			f.line = append(f.line, p...)
			p = nil
		} else {
			f.line = append(f.line, p[:i+1]...)
			p = p[i+1:]
		}
		// A data line is recognised by its first bytes, whether or not its
		// newline arrived in the same chunk, and is never rewritten.
		if bytes.HasPrefix(f.line, insertPrefix) {
			if _, err := f.w.Write(f.line); err != nil {
				return 0, err
			}
			f.line = f.line[:0]
			f.passthrough = i < 0
			continue
		}
		if i >= 0 {
			if err := f.Flush(); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

// Flush writes any buffered partial line.
func (f *definerFilter) Flush() error {
	if len(f.line) == 0 {
		return nil
	}
	_, err := f.w.Write(definerClause.ReplaceAll(f.line, nil))
	f.line = f.line[:0]
	return err
}

// limitWriter keeps at most n bytes of diagnostic output (stderr excerpts in
// memory). It always reports success so the producer is never interrupted;
// truncated records that output was dropped.
type limitWriter struct {
	w         io.Writer
	n         int
	truncated bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		l.truncated = true
	}
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	_, _ = l.w.Write(q)
	return len(p), nil
}

// dumpSQLite uses the online .backup API in a scoped tooling container with
// only the binding directory and a private staging directory mounted.
func (d Deps) dumpSQLite(ctx context.Context, t Target, w io.Writer) error {
	spec, err := d.ToolSpec(ctx, t.App)
	if err != nil {
		return err
	}
	staging := filepath.Join(d.Layout.StagingDir(), "backup-"+platform.RandomHex(6))
	if err := platform.EnsureDir(staging, 0o700, platform.Owner{UID: t.App.UID, GID: t.App.GID}); err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	// Replace the tool's mounts: SQLite dir + staging only (no home, no backups).
	var mounts []mount.Mount
	for _, m := range spec.HostConfig.Mounts {
		if m.Target == t.Binding.SQLiteContainerDir() || m.Target == "/etc/passwd" || m.Target == "/etc/group" {
			mounts = append(mounts, m)
		}
	}
	mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: staging, Target: "/bento-backup-out"})
	spec.HostConfig.Mounts = mounts
	spec.Config.Labels[runtime.LabelRole] = string(runtime.RoleBackup)
	spec.Config.Entrypoint = []string{"sleep"}
	spec.Config.Cmd = []string{"3600"}
	spec.Config.WorkingDir = "/"
	spec.Name = d.Names.BackupContainer("sqlite-" + platform.RandomHex(5))
	spec.Networking = nil
	spec.HostConfig.NetworkMode = "none"
	id, err := d.Engine.Create(ctx, spec)
	if err != nil {
		return err
	}
	defer d.Engine.Remove(context.WithoutCancel(ctx), id)
	if err := d.Engine.Start(ctx, id); err != nil {
		return err
	}
	src := t.Binding.SQLiteContainerDir() + "/" + t.App.Slug + ".db"
	res, err := d.Engine.Exec(ctx, id, docker.ExecRequest{
		User: fmt.Sprintf("%d:%d", t.App.UID, t.App.GID),
		Cmd:  []string{"sqlite3", "-bail", "-cmd", ".timeout 30000", src, ".backup '/bento-backup-out/snapshot.db'"},
	})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("sqlite backup failed: %s", strings.TrimSpace(string(res.Stderr)))
	}
	snap := filepath.Join(staging, "snapshot.db")
	info, err := os.Lstat(snap)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("sqlite backup produced no snapshot")
	}
	f, err := os.Open(snap)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}
