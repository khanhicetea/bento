package runtime

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/khanhicetea/bento/apps/backend/internal/assets"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// Service endpoints on the private data network.
const (
	MySQLPort    = 3306
	PostgresPort = 5432
	RedisHost    = "redis"
	RedisPort    = 6379
)

// SchedulerBasePath is the URL prefix minicrond serves under; the backend's
// scheduler gateway on the utils listener forwards exactly this prefix.
func SchedulerBasePath(slug string) string { return "/_bento/scheduler/a/" + slug + "/" }

// SQLiteFile is the database file path inside the container.
func SQLiteFile(b domain.Binding, slug string) string {
	return b.SQLiteContainerDir() + "/" + slug + ".db"
}

// AppContext is everything the materializer needs beyond the app itself.
type AppContext struct {
	Layout         platform.Layout
	TrustedProxies []string
	// Base passwd/group bytes read from the resolved runtime image. The app
	// entry is appended; image entries are preserved.
	ImagePasswd []byte
	ImageGroup  []byte
}

// Materialized reports content hashes of generated files. Boot-static inputs
// (fingerprinted) require recreation; frontend, pool and scheduler inputs
// have scoped reload paths.
type Materialized struct {
	RuntimeEnvHash string
	ArgvHash       string
	IdentityHash   string
	FrontendHash   string
	PoolHash       string
	SchedulerHash  string
}

type file struct {
	name string
	data []byte
	mode os.FileMode
}

// RenderAppConfig renders every per-app generated file in memory, without
// touching the filesystem. The caller validates and then promotes them.
func RenderAppConfig(app domain.App, ctx AppContext) (config []file, identity []file, m Materialized, err error) {
	code := app.ContainerCode()
	env := orderedEnv{}
	env.add("BENTO_APP_ID", app.ID)
	env.add("BENTO_APP_SLUG", app.Slug)
	env.add("BENTO_RUNTIME_KIND", string(app.Runtime.Kind))
	env.add("BENTO_HTTP_PORT", strconv.Itoa(app.HTTPPort()))
	env.add("BENTO_READY_PATH", app.ReadyPath())
	env.add("BENTO_SCHEDULER_BASE_PATH", SchedulerBasePath(app.Slug))
	env.add("BENTO_TRUSTED_PROXIES", strings.Join(ctx.TrustedProxies, ","))
	env.add("TZ", "UTC")
	if app.Runtime.HTTP != nil {
		env.add("BENTO_WORKDIR", joinPath(code, app.Runtime.HTTP.Workdir))
	} else {
		env.add("BENTO_WORKDIR", code)
	}
	userEnv := AppEnv(app)
	if len(app.Runtime.Env) > 0 {
		// Values may be sensitive: only their hash enters the fingerprint.
		env.add("BENTO_APP_ENV_HASH", platform.SHA256Hex(userEnv)[:20])
	}
	config = append(config, file{"app.env", userEnv, 0o440})
	runtimeEnv := env.bytes()
	config = append(config, file{"runtime.env", runtimeEnv, 0o440})
	m.RuntimeEnvHash = platform.SHA256Hex(runtimeEnv)

	creds := CredentialsEnv(app)
	config = append(config, file{"credentials.env", creds, 0o440})

	var argv []byte
	if app.Runtime.HTTP != nil {
		for _, a := range app.Runtime.HTTP.Argv {
			argv = append(argv, a...)
			argv = append(argv, 0)
		}
		config = append(config, file{"app.argv", argv, 0o440})
	}
	m.ArgvHash = platform.SHA256Hex(argv)

	sched, err := renderScheduler(app)
	if err != nil {
		return nil, nil, m, err
	}
	config = append(config, file{"minicrond.toml", sched, 0o440})
	m.SchedulerHash = platform.SHA256Hex(sched)

	if app.Runtime.Kind == domain.RuntimePHP && app.Runtime.PHP != nil {
		frontend, fastcgi, pool, err := renderPHP(app, ctx)
		if err != nil {
			return nil, nil, m, err
		}
		config = append(
			config,
			file{"nginx.conf", frontend, 0o440},
			file{"fastcgi.conf", fastcgi, 0o440},
			file{"php-fpm.conf", pool, 0o440},
		)
		m.FrontendHash = platform.SHA256HexConcat(frontend, fastcgi)
		m.PoolHash = platform.SHA256Hex(pool)
	}

	passwd, group := identityFiles(app, ctx.ImagePasswd, ctx.ImageGroup)
	identity = []file{{"passwd", passwd, 0o444}, {"group", group, 0o444}}
	m.IdentityHash = platform.SHA256HexConcat(passwd, group)
	return config, identity, m, nil
}

// Changes reports which scoped-reload scopes differ from what the running
// instance last applied (not merely what changed on this write), plus whether
// boot-static files changed on this write. It holds the applied bytes so a
// failed validation can restore them, and the rendered bytes so a successful
// reload can be recorded.
type Changes struct {
	Frontend  bool
	Pool      bool
	Scheduler bool
	Boot      bool
	previous  map[string][]byte
	applied   AppliedConfig
	rendered  map[string]AppliedScope
	layout    platform.Layout
	appID     string
	dir       string
	owner     platform.Owner
}

// Changed reports whether a scope differs from the applied state.
func (c Changes) Changed(scope string) bool {
	switch scope {
	case ScopeFrontend:
		return c.Frontend
	case ScopePool:
		return c.Pool
	case ScopeScheduler:
		return c.Scheduler
	}
	return false
}

// Restore puts back the previous bytes of the named files. For scope files
// the last applied bytes win over the bytes seen before this write.
func (c Changes) Restore(names ...string) error {
	for _, n := range names {
		prev, ok := c.previous[n]
		for _, s := range Scopes {
			if sc, found := c.applied.Scopes[s]; found {
				if b, has := sc.Files[n]; has {
					prev, ok = b, true
				}
			}
		}
		if !ok || c.dir == "" {
			continue
		}
		if prev == nil {
			_ = os.Remove(filepath.Join(c.dir, n))
			continue
		}
		if err := platform.AtomicWrite(filepath.Join(c.dir, n), prev, 0o440, c.owner); err != nil {
			return err
		}
	}
	return nil
}

// RestoreUnapplied restores every changed scope that has not been recorded
// as applied, so no validated-but-unreloaded or rejected bytes stay on disk.
func (c Changes) RestoreUnapplied(except ...string) error {
	var errs []error
	for _, s := range Scopes {
		if !c.Changed(s) || slices.Contains(except, s) {
			continue
		}
		errs = append(errs, c.Restore(ScopeFiles(s)...))
	}
	return errors.Join(errs...)
}

// MarkApplied records the rendered bytes of the given scopes as loaded by the
// running instance. Only reload and instance start paths call it.
func (c *Changes) MarkApplied(scopes ...string) error {
	if c.appID == "" || c.rendered == nil {
		return nil
	}
	if c.applied.Scopes == nil {
		c.applied.Scopes = map[string]AppliedScope{}
	}
	for _, s := range scopes {
		c.applied.Scopes[s] = c.rendered[s]
		switch s {
		case ScopeFrontend:
			c.Frontend = false
		case ScopePool:
			c.Pool = false
		case ScopeScheduler:
			c.Scheduler = false
		}
	}
	return writeApplied(c.layout.AppStateDir(c.appID), c.layout.AppAppliedConfig(c.appID), c.applied)
}

// MarkAllApplied records every scope as applied: a freshly (re)started
// instance loads all generated config at boot.
func (c *Changes) MarkAllApplied() error { return c.MarkApplied(Scopes...) }

// FrontendFiles, PoolFiles and SchedulerFiles name scoped-reload inputs.
var (
	FrontendFiles  = []string{"nginx.conf", "fastcgi.conf"}
	PoolFiles      = []string{"php-fpm.conf"}
	SchedulerFiles = []string{"minicrond.toml"}
)

// AppConfigDrift reports whether any rendered config file differs from the
// bytes on disk. Identity files are boot-static and covered by Fingerprint.
func AppConfigDrift(app domain.App, ctx AppContext) (bool, error) {
	config, _, m, err := RenderAppConfig(app, ctx)
	if err != nil {
		return false, err
	}
	// A scope rendered differently from what the instance applied is drift
	// even when the disk already holds the new bytes (written by a tool or
	// backup container, or left by an earlier interrupted write).
	a, err := loadApplied(ctx, app.ID)
	if err != nil {
		return false, err
	}
	for _, s := range Scopes {
		if a.Scopes[s].Hash != renderedScopeHash(m, s) {
			return true, nil
		}
	}
	cfgDir := ctx.Layout.AppConfigDir(app.ID)
	for _, f := range config {
		cur, err := os.ReadFile(filepath.Join(cfgDir, f.name))
		if err != nil || !bytes.Equal(cur, f.data) {
			return true, nil
		}
	}
	return false, nil
}

// WriteAppConfig promotes rendered files: the config directory is owned by
// root with the app's group so the app can read but never modify it.
func WriteAppConfig(app domain.App, ctx AppContext) (Materialized, error) {
	m, _, err := WriteAppConfigChanges(app, ctx)
	return m, err
}

func WriteAppConfigChanges(app domain.App, ctx AppContext) (Materialized, Changes, error) {
	ch := Changes{previous: map[string][]byte{}}
	config, identity, m, err := RenderAppConfig(app, ctx)
	if err != nil {
		return m, ch, err
	}
	appDir := ctx.Layout.AppDir(app.ID)
	if err := platform.EnsureDir(appDir, 0o711, platform.RootOwner); err != nil {
		return m, ch, err
	}
	owner := platform.Owner{UID: 0, GID: app.GID}
	cfgDir := ctx.Layout.AppConfigDir(app.ID)
	if err := platform.EnsureDir(cfgDir, 0o750, owner); err != nil {
		return m, ch, err
	}
	ch.dir, ch.owner = cfgDir, owner
	applied, err := loadApplied(ctx, app.ID)
	if err != nil {
		return m, ch, err
	}
	ch.applied, ch.layout, ch.appID = applied, ctx.Layout, app.ID
	ch.rendered = map[string]AppliedScope{}
	for _, sc := range Scopes {
		files := map[string][]byte{}
		for _, n := range ScopeFiles(sc) {
			files[n] = nil
		}
		ch.rendered[sc] = AppliedScope{Hash: renderedScopeHash(m, sc), Files: files}
	}
	for _, f := range config {
		path := filepath.Join(cfgDir, f.name)
		prev, rerr := os.ReadFile(path)
		if rerr != nil {
			prev = nil
		}
		changed, err := platform.WriteIfChanged(path, f.data, f.mode, owner)
		if err != nil {
			return m, ch, err
		}
		if !changed {
			continue
		}
		ch.previous[f.name] = prev
		if scopeOf(f.name) == "" {
			ch.Boot = true
		}
	}
	for _, f := range config {
		if sc := scopeOf(f.name); sc != "" {
			ch.rendered[sc].Files[f.name] = f.data
		}
	}
	ch.Frontend = applied.Scopes[ScopeFrontend].Hash != ch.rendered[ScopeFrontend].Hash
	ch.Pool = applied.Scopes[ScopePool].Hash != ch.rendered[ScopePool].Hash
	ch.Scheduler = applied.Scopes[ScopeScheduler].Hash != ch.rendered[ScopeScheduler].Hash
	idDir := ctx.Layout.AppIdentityDir(app.ID)
	if err := platform.EnsureDir(idDir, 0o755, platform.RootOwner); err != nil {
		return m, ch, err
	}
	for _, f := range identity {
		changed, err := platform.WriteIfChanged(filepath.Join(idDir, f.name), f.data, f.mode, platform.RootOwner)
		if err != nil {
			return m, ch, err
		}
		ch.Boot = ch.Boot || changed
	}
	return m, ch, nil
}

func scopeOf(name string) string {
	for _, s := range Scopes {
		if slices.Contains(ScopeFiles(s), name) {
			return s
		}
	}
	return ""
}

type orderedEnv struct{ buf bytes.Buffer }

func (e *orderedEnv) add(k, v string) {
	v = strings.NewReplacer("\n", "", "\r", "").Replace(v)
	fmt.Fprintf(&e.buf, "%s=%s\n", k, v)
}

func (e *orderedEnv) bytes() []byte { return e.buf.Bytes() }

func joinPath(base, rel string) string {
	if rel == "" {
		return base
	}
	return path.Join(base, rel)
}

// AppEnv renders the operator-defined environment. It is loaded before
// runtime.env and credentials.env so Bento-managed keys always win.
func AppEnv(app domain.App) []byte {
	env := orderedEnv{}
	env.buf.WriteString("# Generated by Bento from app settings.\n")
	for _, e := range app.Runtime.Env {
		env.add(e.Key, e.Value)
	}
	return env.bytes()
}

// CredentialsEnv renders protected connection metadata for all bindings. The
// first binding is the conventional DB_* connection.
func CredentialsEnv(app domain.App) []byte {
	env := orderedEnv{}
	env.buf.WriteString("# Generated by Bento. Protected app credentials; never commit or print.\n")
	for i, b := range app.Bindings {
		prefix := fmt.Sprintf("BENTO_DB_%d_", i)
		conn, host, port, db, user, pass := bindingFields(b, app.Slug)
		env.add(prefix+"ENGINE", string(b.Engine))
		env.add(prefix+"CONNECTION", conn)
		if host != "" {
			env.add(prefix+"HOST", host)
			env.add(prefix+"PORT", strconv.Itoa(port))
			env.add(prefix+"USERNAME", user)
			env.add(prefix+"PASSWORD", pass)
		}
		env.add(prefix+"DATABASE", db)
		if i == 0 {
			env.add("DB_CONNECTION", conn)
			if host != "" {
				env.add("DB_HOST", host)
				env.add("DB_PORT", strconv.Itoa(port))
				env.add("DB_USERNAME", user)
				env.add("DB_PASSWORD", pass)
			}
			env.add("DB_DATABASE", db)
		}
	}
	env.add("BENTO_DB_COUNT", strconv.Itoa(len(app.Bindings)))
	if app.Redis.Username != "" {
		env.add("REDIS_HOST", RedisHost)
		env.add("REDIS_PORT", strconv.Itoa(RedisPort))
		env.add("REDIS_USERNAME", app.Redis.Username)
		env.add("REDIS_PASSWORD", app.Redis.Password)
		env.add("REDIS_PREFIX", app.Redis.Prefix)
		env.add("BENTO_REDIS_PREFIX", app.Redis.Prefix)
	}
	return env.bytes()
}

func bindingFields(b domain.Binding, slug string) (conn, host string, port int, db, user, pass string) {
	var first string
	if len(b.Databases) > 0 {
		first = b.Databases[0]
	}
	switch b.Engine {
	case domain.EngineMySQL:
		return "mysql", b.Service, MySQLPort, first, b.Username, b.Password
	case domain.EnginePostgres:
		return "pgsql", b.Service, PostgresPort, first, b.Username, b.Password
	default:
		return "sqlite", "", 0, SQLiteFile(b, slug), "", ""
	}
}

type schedJob struct {
	Name     string
	Schedule string
	Argv     []string
	Timeout  int
}

// Reserved internal task names; minicrond refuses registry collisions.
const internalTaskPrefix = "bento-internal-"

func renderScheduler(app domain.App) ([]byte, error) {
	var jobs []schedJob
	for _, b := range app.Bindings {
		if b.Engine != domain.EngineSQLite || b.Vacuum == nil {
			continue
		}
		v := b.Vacuum
		jobs = append(jobs, schedJob{
			Name:     internalTaskPrefix + "sqlite-vacuum-" + strings.ReplaceAll(b.SQLiteFileID, "_", "-"),
			Schedule: fmt.Sprintf("%d %d * * %d", v.Minute, v.Hour, v.DayOfWeek),
			Argv:     []string{"sqlite3", "-cmd", ".timeout 30000", SQLiteFile(b, app.Slug), "VACUUM;"},
			Timeout:  3600,
		})
	}
	slices.SortFunc(jobs, func(a, b schedJob) int { return strings.Compare(a.Name, b.Name) })
	return assets.Render("minicrond.toml.tmpl", map[string]any{"Slug": app.Slug, "AppID": app.ID, "Jobs": jobs})
}

func renderPHP(app domain.App, ctx AppContext) (frontend, fastcgi, pool []byte, err error) {
	p := app.Runtime.PHP
	home := app.ContainerHome()
	code := app.ContainerCode()
	docRoot := joinPath(code, p.DocumentRoot)
	symlinkFrom := code
	if p.ReleaseSymlink != "" {
		// Components up to and including the release symlink are not checked;
		// everything below it (the rest of the document root) is.
		symlinkFrom = path.Join(code, p.ReleaseSymlink)
		rel := strings.TrimPrefix(p.DocumentRoot, p.ReleaseSymlink)
		if p.DocumentRoot != p.ReleaseSymlink && !strings.HasPrefix(p.DocumentRoot, p.ReleaseSymlink+"/") {
			return nil, nil, nil, fmt.Errorf("document root must be inside the release symlink %q", p.ReleaseSymlink)
		}
		docRoot = path.Join(symlinkFrom, strings.TrimPrefix(rel, "/"))
	}
	profile := domain.PoolProfiles[p.Pool]
	data := map[string]any{
		"Slug": app.Slug, "AppID": app.ID, "UID": app.UID, "Home": home,
		"Port": domain.PHPFrontendPort, "DocumentRoot": docRoot, "SymlinkFrom": symlinkFrom,
		"Routing": p.Routing, "UploadLimitMB": p.UploadLimitMB, "AccessLog": app.Route.AccessLog,
		"TrustedProxies": ctx.TrustedProxies, "Workers": 1, "Pool": profile,
		"OpenBasedir": openBasedir(app),
	}
	if frontend, err = assets.Render("app-nginx.conf.tmpl", data); err != nil {
		return nil, nil, nil, err
	}
	if fastcgi, err = assets.Render("app-fastcgi.conf.tmpl", data); err != nil {
		return nil, nil, nil, err
	}
	if pool, err = assets.Render("php-fpm.conf.tmpl", data); err != nil {
		return nil, nil, nil, err
	}
	return frontend, fastcgi, pool, nil
}

func openBasedir(app domain.App) string {
	parts := []string{app.ContainerHome(), "/tmp", "/usr/local/lib/php"}
	for _, b := range app.Bindings {
		if b.Engine == domain.EngineSQLite {
			parts = append(parts, b.SQLiteContainerDir())
		}
	}
	return strings.Join(parts, ":")
}

// identityFiles appends the app user/group to the image's own databases so
// shells and tools resolve a name without mutating /etc/passwd at runtime.
func identityFiles(app domain.App, basePasswd, baseGroup []byte) ([]byte, []byte) {
	name := app.Slug
	if colonFileHasName(basePasswd, name) || colonFileHasName(baseGroup, name) {
		name = "app" + strconv.Itoa(app.UID)
	}
	passwd := withTrailingNewline(stripID(basePasswd, app.UID))
	passwd = append(
		passwd,
		fmt.Sprintf("%s:x:%d:%d:Bento app %s:%s:/bin/bash\n", name, app.UID, app.GID, app.Slug, app.ContainerHome())...,
	)
	group := withTrailingNewline(stripID(baseGroup, app.GID))
	group = append(group, fmt.Sprintf("%s:x:%d:\n", name, app.GID)...)
	return passwd, group
}

func colonFileHasName(data []byte, name string) bool {
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, name+":") {
			return true
		}
	}
	return false
}

// stripID removes image entries that already use the app's numeric id so the
// app identity resolves unambiguously.
func stripID(data []byte, id int) []byte {
	var out []string
	want := strconv.Itoa(id)
	for line := range strings.SplitSeq(strings.TrimRight(string(data), "\n"), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 3 && f[2] == want {
			continue
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return []byte(strings.Join(out, "\n"))
}

func withTrailingNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return b
}
