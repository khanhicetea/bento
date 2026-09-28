// Package backup implements logical backups and restores for app bindings
// (MySQL, PostgreSQL, SQLite), retention, and scoped rclone upload.
//
// Guarantees: one batch at a time (file lock); every artifact is written to a
// private partial and published atomically only if the dump succeeded and is
// non-empty; retention runs only after the whole batch succeeds; uploads run
// in an ephemeral container with read-only access to exactly the new
// artifacts; administrator secrets never appear in argv.
package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
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

// Artifact is one published backup file.
type Artifact struct {
	Path      string        `json:"path"` // relative to the backups dir
	AppSlug   string        `json:"appSlug"`
	Engine    domain.Engine `json:"engine"`
	Database  string        `json:"database"`
	SizeBytes int64         `json:"sizeBytes"`
	CreatedAt time.Time     `json:"createdAt"`
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

func ext(compression string) (string, error) {
	switch compression {
	case "", "zstd":
		return ".zst", nil
	case "gzip":
		return ".gz", nil
	case "none":
		return "", nil
	}
	return "", fmt.Errorf("compression must be zstd, gzip, or none")
}

type compressor struct {
	io.Writer
	close func() error
}

func newCompressor(w io.Writer, compression string) (*compressor, error) {
	switch compression {
	case "", "zstd":
		z, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			return nil, err
		}
		return &compressor{Writer: z, close: z.Close}, nil
	case "gzip":
		g := gzip.NewWriter(w)
		return &compressor{Writer: g, close: g.Close}, nil
	}
	return &compressor{Writer: w, close: func() error { return nil }}, nil
}

// Decompress opens an artifact for reading by extension.
func Decompress(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasSuffix(path, ".zst"):
		z, err := zstd.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return struct {
			io.Reader
			io.Closer
		}{z, closerFunc(func() error { z.Close(); return f.Close() })}, nil
	case strings.HasSuffix(path, ".gz"):
		g, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return struct {
			io.Reader
			io.Closer
		}{g, closerFunc(func() error { g.Close(); return f.Close() })}, nil
	}
	return f, nil
}

type closerFunc func() error

func (c closerFunc) Close() error { return c() }

var safeName = regexp.MustCompile(`[^a-z0-9_.-]`)

// Artifact timestamp layouts. New artifacts use millisecond resolution;
// the legacy one-second layout is still parsed for existing files.
const (
	stampLayout       = "20060102T150405.000Z"
	legacyStampLayout = "20060102T150405Z"
)

var artifactPattern = regexp.MustCompile(`^(mysql|postgres|sqlite)-(.+)-(\d{8}T\d{6}(?:\.\d{3})?Z)\.(sql|db)(\.zst|\.gz)?$`)

func parseStamp(s string) time.Time {
	if ts, err := time.Parse(stampLayout, s); err == nil {
		return ts
	}
	ts, _ := time.Parse(legacyStampLayout, s)
	return ts
}

// publish atomically links a verified, non-empty partial to its final name.
// It refuses to overwrite an existing artifact (link fails with EEXIST,
// unlike rename which silently replaces).
func publish(partial, final string) (int64, error) {
	info, err := os.Stat(partial)
	if err != nil {
		return 0, err
	}
	if info.Size() == 0 {
		os.Remove(partial)
		return 0, errors.New("dump produced an empty artifact; not published")
	}
	if err := os.Link(partial, final); err != nil {
		os.Remove(partial)
		if errors.Is(err, os.ErrExist) {
			return 0, fmt.Errorf("artifact %s already exists; refusing to overwrite", filepath.Base(final))
		}
		return 0, err
	}
	if err := os.Remove(partial); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (d Deps) appDir(slug string) (string, error) {
	dir := filepath.Join(d.Layout.BackupsDir(), slug)
	if err := platform.NoSymlinkBetween(d.Layout.BackupsDir(), dir); err != nil {
		return "", err
	}
	return dir, platform.EnsureDir(dir, 0o700, platform.RootOwner)
}

// Dump writes one target's artifact.
func (d Deps) Dump(ctx context.Context, t Target, compression string) (Artifact, error) {
	e, err := ext(compression)
	if err != nil {
		return Artifact{}, err
	}
	dir, err := d.appDir(t.App.Slug)
	if err != nil {
		return Artifact{}, err
	}
	now := time.Now().UTC()
	stamp := now.Format(stampLayout)
	var final string
	switch t.Binding.Engine {
	case domain.EngineMySQL, domain.EnginePostgres:
		final = filepath.Join(dir, fmt.Sprintf("%s-%s-%s.sql%s", t.Binding.Engine, safeName.ReplaceAllString(t.Database, "_"), stamp, e))
	case domain.EngineSQLite:
		final = filepath.Join(dir, fmt.Sprintf("sqlite-%s-%s.db%s", safeName.ReplaceAllString(t.Binding.SQLiteFileID, "_"), stamp, e))
	}
	partial := filepath.Join(dir, ".partial-"+platform.RandomHex(6))
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Artifact{}, err
	}
	cleanup := func() { f.Close(); os.Remove(partial) }
	comp, err := newCompressor(f, compression)
	if err != nil {
		cleanup()
		return Artifact{}, err
	}
	switch t.Binding.Engine {
	case domain.EngineMySQL, domain.EnginePostgres:
		err = d.dumpRelational(ctx, t, comp)
	case domain.EngineSQLite:
		err = d.dumpSQLite(ctx, t, comp)
	}
	if err == nil {
		err = comp.close()
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		cleanup()
		return Artifact{}, err
	}
	if err := f.Close(); err != nil {
		os.Remove(partial)
		return Artifact{}, err
	}
	size, err := publish(partial, final)
	if err != nil {
		return Artifact{}, err
	}
	rel, _ := filepath.Rel(d.Layout.BackupsDir(), final)
	return Artifact{Path: rel, AppSlug: t.App.Slug, Engine: t.Binding.Engine, Database: t.Database, SizeBytes: size, CreatedAt: now}, nil
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
	res, err := d.Engine.Exec(ctx, id, docker.ExecRequest{Cmd: cmd, Stdout: out, Stderr: &limitWriter{w: &stderr, n: 4096}})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s dump of %s failed (exit %d): %s", svc.Engine, t.Database, res.ExitCode, strings.TrimSpace(stderr.String()))
	}
	if df != nil {
		return df.Flush()
	}
	return nil
}

var definerClause = regexp.MustCompile("\\s*DEFINER\\s*=\\s*(?:`(?:[^`]|``)*`|'(?:[^']|'')*'|[A-Za-z0-9_.%-]+)@(?:`(?:[^`]|``)*`|'(?:[^']|'')*'|[A-Za-z0-9_.%-]+)")

// definerFilter strips DEFINER=user@host clauses from a mysqldump stream.
// Data lines (INSERT) are passed through unbuffered; other lines are
// buffered to the newline and rewritten.
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
		if len(f.line) >= len(insertPrefix) && bytes.HasPrefix(f.line, insertPrefix) && (i < 0) {
			if _, err := f.w.Write(f.line); err != nil {
				return 0, err
			}
			f.line = f.line[:0]
			f.passthrough = true
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

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
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
		return fmt.Errorf("sqlite backup produced no snapshot")
	}
	f, err := os.Open(snap)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// ListArtifacts enumerates published artifacts (partials excluded).
func ListArtifacts(backupsDir string) ([]Artifact, error) {
	var out []Artifact
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, app := range entries {
		if !app.IsDir() || strings.HasPrefix(app.Name(), ".") {
			continue
		}
		files, err := os.ReadDir(filepath.Join(backupsDir, app.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			m := artifactPattern.FindStringSubmatch(f.Name())
			if m == nil || !f.Type().IsRegular() {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			ts := parseStamp(m[3])
			out = append(out, Artifact{Path: app.Name() + "/" + f.Name(), AppSlug: app.Name(), Engine: domain.Engine(m[1]),
				Database: m[2], SizeBytes: info.Size(), CreatedAt: ts})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// RetentionKey identifies the (app, engine, database) series an artifact
// belongs to for retention.
func RetentionKey(a Artifact) string {
	return a.AppSlug + "/" + string(a.Engine) + "/" + a.Database
}

// Retain keeps the newest keep artifacts per (app, engine, database) and
// deletes older ones. Call only after a fully successful batch.
func Retain(backupsDir string, keep int) ([]string, error) {
	return RetainKeys(backupsDir, keep, nil)
}

// RetainKeys applies Retain only to the series named in keys (see
// RetentionKey). A nil keys map means every series; an empty map means none.
// Use it after a partial batch so series whose newest dump failed keep their
// older artifacts.
func RetainKeys(backupsDir string, keep int, keys map[string]bool) ([]string, error) {
	if keep < 1 || (keys != nil && len(keys) == 0) {
		return nil, nil
	}
	arts, err := ListArtifacts(backupsDir)
	if err != nil {
		return nil, err
	}
	seen := map[string]int{}
	var removed []string
	for _, a := range arts {
		key := RetentionKey(a)
		if keys != nil && !keys[key] {
			continue
		}
		seen[key]++
		if seen[key] > keep {
			if err := os.Remove(filepath.Join(backupsDir, a.Path)); err != nil {
				return removed, err
			}
			removed = append(removed, a.Path)
		}
	}
	return removed, nil
}

// ResolveArtifact validates a client-supplied artifact path: contained in
// the backups directory, no symlinks, an existing regular file.
func ResolveArtifact(backupsDir, rel string) (string, error) {
	clean, err := platform.ContainedPath(backupsDir, rel)
	if err != nil {
		return "", err
	}
	if err := platform.NoSymlinkBetween(backupsDir, clean); err != nil {
		return "", err
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("artifact not found")
	}
	if strings.HasPrefix(filepath.Base(clean), ".") || !artifactPattern.MatchString(filepath.Base(clean)) {
		return "", fmt.Errorf("artifact not found")
	}
	return clean, nil
}

// Upload copies exactly the given artifacts with rclone in an ephemeral
// container: private config and the selected files mounted read-only.
func (d Deps) Upload(ctx context.Context, remote string, artifacts []Artifact) error {
	if remote == "" || len(artifacts) == 0 {
		return nil
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+:[A-Za-z0-9_./-]*$`).MatchString(remote) {
		return fmt.Errorf("invalid rclone remote %q", remote)
	}
	conf := filepath.Join(d.Layout.RcloneDir(), "rclone.conf")
	if _, err := os.Stat(conf); err != nil {
		return fmt.Errorf("rclone config %s is missing", conf)
	}
	if _, ok, err := d.Engine.ImageID(ctx, domain.RcloneImage); err != nil {
		return err
	} else if !ok {
		if err := d.Engine.PullImage(ctx, domain.RcloneImage, nil); err != nil {
			return err
		}
	}
	mounts := []mount.Mount{{Type: mount.TypeBind, Source: d.Layout.RcloneDir(), Target: "/config/rclone", ReadOnly: true}}
	for _, a := range artifacts {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: filepath.Join(d.Layout.BackupsDir(), a.Path), Target: "/upload/" + a.Path, ReadOnly: true})
	}
	opID := "rclone-" + platform.RandomHex(5)
	spec := docker.ContainerSpec{
		Name: d.Names.BackupContainer(opID),
		Config: &container.Config{Image: domain.RcloneImage, Cmd: []string{"copy", "/upload", remote, "--config", "/config/rclone/rclone.conf", "--no-traverse"},
			Labels: d.Names.Labels(runtime.RoleBackup, map[string]string{runtime.LabelOperation: opID})},
		HostConfig: &container.HostConfig{Mounts: mounts, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"},
			LogConfig: container.LogConfig{Type: "local", Config: map[string]string{"max-size": "5m", "max-file": "1"}}},
	}
	id, err := d.Engine.Create(ctx, spec)
	if err != nil {
		return err
	}
	defer d.Engine.Remove(context.WithoutCancel(ctx), id)
	if err := d.Engine.Start(ctx, id); err != nil {
		return err
	}
	code, err := d.Engine.Wait(ctx, id)
	if err != nil {
		return err
	}
	if code != 0 {
		rc, tty, lerr := d.Engine.Logs(ctx, id, "20", false, "")
		tail := ""
		if lerr == nil {
			var b strings.Builder
			if tty {
				_, _ = io.Copy(&limitWriter{w: &b, n: 4096}, rc)
			} else {
				_, _ = stdcopy.StdCopy(&limitWriter{w: &b, n: 4096}, &limitWriter{w: &b, n: 4096}, rc)
			}
			rc.Close()
			tail = b.String()
		}
		return fmt.Errorf("rclone exited %d: %s", code, strings.TrimSpace(tail))
	}
	return nil
}
