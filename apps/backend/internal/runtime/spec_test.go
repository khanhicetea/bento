package runtime

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/assets"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

func testApp() domain.App {
	return domain.App{
		ID: "aabc123", Slug: "shop", UID: 10000, GID: 10000,
		Runtime:   domain.Runtime{Kind: domain.RuntimePHP, PHP: &domain.PHPRuntime{Version: "8.4", DocumentRoot: "public", Routing: "front-controller", Mode: "standard", UploadLimitMB: 64}},
		Resources: domain.Resources{MemoryMB: 512, CPUMillis: 1000, PIDs: 256},
		Redis:     domain.RedisIdentity{Mode: "acl", Prefix: "shop:", Username: "app-aabc123", Password: "redissecret123"},
		Bindings: []domain.Binding{
			{ID: "b1", Engine: domain.EngineMySQL, Service: "mysql84", Username: "uaabc123", Password: "dbsecret12345", Databases: []string{"shop"}},
			{ID: "b2", Engine: domain.EngineSQLite, SQLiteFileID: "shop_0123456789", Vacuum: &domain.VacuumSlot{DayOfWeek: 1, Hour: 2, Minute: 3}},
		},
		CredentialsGeneration: 1,
	}
}

func inputs(t *testing.T, app domain.App) AppInputs {
	t.Helper()
	ctx := AppContext{Layout: platform.Layout{Root: "/srv/stack"}, TrustedProxies: []string{"10.200.0.2"},
		ImagePasswd: []byte("root:x:0:0::/root:/bin/bash\n"), ImageGroup: []byte("root:x:0:\n")}
	_, _, m, err := RenderAppConfig(app, ctx)
	if err != nil {
		t.Fatal(err)
	}
	return AppInputs{App: app, Names: Names{StackID: "s1", StackName: "prod"}, Layout: ctx.Layout, ImageID: "sha256:img", Materialized: m}
}

func TestPersistentSpecSecurityInvariants(t *testing.T) {
	spec, gen := AppContainerSpec(inputs(t, testApp()), true)
	h, c := spec.HostConfig, spec.Config
	if c.User != "10000:10000" || c.WorkingDir != "/home/shop/app" || !h.ReadonlyRootfs || len(h.CapDrop) != 1 || h.CapDrop[0] != "ALL" || len(h.CapAdd) != 0 || h.Privileged {
		t.Fatalf("identity/privilege invariants violated: %+v", h)
	}
	if len(h.PortBindings) != 0 || h.NetworkMode != "" && h.NetworkMode != "bridge" {
		t.Fatal("app must not publish host ports or use host networking")
	}
	if !strings.Contains(strings.Join(h.SecurityOpt, ","), "no-new-privileges:true") {
		t.Fatal("no-new-privileges missing")
	}
	if h.Resources.Memory != 512<<20 || h.Resources.NanoCPUs != 1e9 || h.Resources.PidsLimit == nil || *h.Resources.PidsLimit != 256 {
		t.Fatal("resource limits not set through Engine API fields")
	}
	if h.LogConfig.Type != "local" || h.LogConfig.Config["max-size"] == "" {
		t.Fatal("logs must be bounded")
	}
	if !strings.Contains(h.Tmpfs["/run"], "uid=10000") || strings.Contains(h.Tmpfs["/run"], "noexec") {
		t.Fatal("/run must be app-owned and executable for s6")
	}
	for _, m := range h.Mounts {
		switch {
		case strings.Contains(m.Source, "docker.sock"):
			t.Fatal("docker socket mounted")
		case strings.HasPrefix(m.Source, "/srv/stack/homes/") && m.Source != "/srv/stack/homes/shop":
			t.Fatal("sibling home mounted")
		case m.Source == "/srv/stack" || strings.Contains(m.Source, "backups") || strings.Contains(m.Source, "secrets"):
			t.Fatalf("broad or sensitive mount %s", m.Source)
		case m.Target == "/etc/bento" && !m.ReadOnly:
			t.Fatal("config must be read-only")
		}
	}
	if c.Labels[LabelGeneration] != gen || c.Labels[LabelAppID] != "aabc123" || c.Labels[LabelRole] != "runtime" {
		t.Fatal("labels")
	}
	for _, e := range c.Env {
		if strings.Contains(e, "secret") {
			t.Fatalf("secret in env %s", e)
		}
	}
	for k, v := range c.Labels {
		if strings.Contains(v, "secret") {
			t.Fatalf("secret in label %s", k)
		}
	}
	if _, ok := spec.Networking.EndpointsConfig["bento-prod-apps"]; !ok {
		t.Fatal("app network missing")
	}
	if spec.Networking.EndpointsConfig["bento-prod-apps"].Aliases[0] != "app-aabc123" {
		t.Fatal("stable alias missing")
	}
	stopped, _ := AppContainerSpec(inputs(t, testApp()), false)
	if stopped.HostConfig.RestartPolicy.Name != "no" {
		t.Fatal("stopped intent must disable restart policy")
	}
}

func TestToolSpecStartsNoDaemons(t *testing.T) {
	spec := ToolContainerSpec(inputs(t, testApp()), "op1", time.Hour)
	if spec.Config.Entrypoint[0] != "/usr/local/bin/bento-exec" || spec.Config.Labels[LabelRole] != "tool" || spec.Config.WorkingDir != "/home/shop/app" {
		t.Fatal("tool must use the exec entrypoint, never /init")
	}
	if spec.HostConfig.RestartPolicy.Name != "no" || !spec.HostConfig.ReadonlyRootfs {
		t.Fatal("tool must not restart and must be read-only")
	}
}

func TestFingerprintStableAndScoped(t *testing.T) {
	a := testApp()
	g1 := Fingerprint(inputs(t, a))
	if g1 != Fingerprint(inputs(t, a)) {
		t.Fatal("fingerprint not deterministic")
	}
	// Frontend-only changes use a scoped reload, not recreation.
	b := testApp()
	b.Runtime.PHP.Routing = "legacy"
	b.Route.AccessLog = true
	if Fingerprint(inputs(t, b)) != g1 {
		t.Fatal("frontend change must not change the boot fingerprint")
	}
	for name, mut := range map[string]func(*domain.App){
		"resources":   func(a *domain.App) { a.Resources.MemoryMB = 256 },
		"credentials": func(a *domain.App) { a.CredentialsGeneration = 2 },
		"bindings":    func(a *domain.App) { a.Bindings = a.Bindings[:1] },
	} {
		c := testApp()
		mut(&c)
		if Fingerprint(inputs(t, c)) == g1 {
			t.Errorf("%s change must change the fingerprint", name)
		}
	}
	// Secrets never influence the fingerprint directly.
	d := testApp()
	d.Bindings[0].Password = "other-password"
	if Fingerprint(inputs(t, d)) != g1 {
		t.Fatal("secret values must not enter the fingerprint")
	}
}

func TestRenderedConfig(t *testing.T) {
	app := testApp()
	cfg, id, _, err := RenderAppConfig(app, AppContext{TrustedProxies: []string{"10.200.0.2"}, ImagePasswd: []byte("root:x:0:0::/root:/bin/bash\nshop:x:1:1::/:/bin/sh\n"), ImageGroup: []byte("root:x:0:\n")})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range cfg {
		files[f.name] = string(f.data)
	}
	creds := files["credentials.env"]
	for _, want := range []string{"DB_CONNECTION=mysql", "DB_HOST=mysql84", "DB_PASSWORD=dbsecret12345", "BENTO_DB_1_DATABASE=/var/lib/bento/sqlite/shop_0123456789/shop.db", "REDIS_PREFIX=shop:"} {
		if !strings.Contains(creds, want) {
			t.Errorf("credentials missing %s", want)
		}
	}
	if strings.Contains(files["runtime.env"], "secret") {
		t.Fatal("runtime.env must not contain secrets")
	}
	if !strings.Contains(files["runtime.env"], "BENTO_WORKDIR=/home/shop/app") {
		t.Fatal("runtime workdir must be the fixed code directory")
	}
	if !strings.Contains(files["nginx.conf"], "set_real_ip_from 10.200.0.2") || !strings.Contains(files["nginx.conf"], "root /home/shop/app/public") || !strings.Contains(files["nginx.conf"], "disable_symlinks on from=/home/shop/app") {
		t.Fatal("nginx trust/symlink policy")
	}
	if !strings.Contains(files["nginx.conf"], `location ~ /\.(?!well-known`) {
		t.Fatal("dotfiles must be denied")
	}
	if strings.Contains(files["php-fpm.conf"], "\nuser =") {
		t.Fatal("pool must not switch identity")
	}
	for _, want := range []string{"pm = ondemand\n", "pm.max_children = 6\n", "pm.process_idle_timeout = 10s\n", "php_value[memory_limit] = 128M\n", "php_value[max_execution_time] = 60\n", "php_value[max_input_vars] = 1000\n"} {
		if !strings.Contains(files["php-fpm.conf"], want) {
			t.Errorf("php-fpm.conf missing %q", want)
		}
	}
	if !strings.Contains(files[PHPIniFile], "memory_limit = 256M\n") {
		t.Fatalf("CLI ini: %s", files[PHPIniFile])
	}
	if !strings.Contains(files["fastcgi.conf"], "fastcgi_read_timeout 120s;") {
		t.Fatal("fastcgi timeout must keep its 120s floor")
	}
	if !strings.Contains(files["minicrond.toml"], `name = "zz-bento-sqlite-vacuum-shop-0123456789"`) || !strings.Contains(files["minicrond.toml"], `schedule = "3 2 * * 1"`) {
		t.Fatal(files["minicrond.toml"])
	}
	passwd := string(id[0].data)
	if !strings.Contains(passwd, "app10000:x:10000:10000") || !strings.HasPrefix(passwd, "root:x:0:0") {
		t.Fatalf("identity files must preserve image entries and avoid name collisions: %s", passwd)
	}
}

func TestHTTPWorkdirIsRelativeToCodeDirectory(t *testing.T) {
	app := testApp()
	app.Runtime = domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{
		Toolchain: "node", Version: "24", Argv: []string{"node", "server.js"}, Workdir: "services/api", Port: 3000,
	}}
	cfg, _, _, err := RenderAppConfig(app, AppContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range cfg {
		if f.name == "runtime.env" && strings.Contains(string(f.data), "BENTO_WORKDIR=/home/shop/app/services/api") {
			return
		}
	}
	t.Fatal("HTTP workdir was not resolved from the fixed code directory")
}

func TestBuildContextDeterministic(t *testing.T) {
	a, h1, err := assets.BuildContext("php")
	if err != nil {
		t.Fatal(err)
	}
	b, h2, _ := assets.BuildContext("php")
	if h1 != h2 || !bytes.Equal(a, b) {
		t.Fatal("build context is not deterministic")
	}
	k1, _ := PlanImage(domain.ImageKey{Kind: domain.RuntimePHP, Toolchain: "php", Version: "8.4"})
	k2, _ := PlanImage(domain.ImageKey{Kind: domain.RuntimeHTTP, Toolchain: "node", Version: "24"})
	if k1.Tag() == k2.Tag() || !strings.HasPrefix(k1.Tag(), "bento-runtime/php:8.4-") {
		t.Fatal(k1.Tag(), k2.Tag())
	}
}

func TestAppConfigDriftDetectsTemplateOutputChanges(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("config files are written root-owned")
	}
	app := testApp()
	ctx := AppContext{Layout: platform.Layout{Root: t.TempDir()}, ImagePasswd: []byte("root:x:0:0::/root:/bin/bash\n"), ImageGroup: []byte("root:x:0:\n")}
	if drift, err := AppConfigDrift(app, ctx); err != nil || !drift {
		t.Fatalf("missing config must drift: %v %v", drift, err)
	}
	_, ch, err := WriteAppConfigChanges(app, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if drift, err := AppConfigDrift(app, ctx); err != nil || !drift {
		t.Fatalf("written but never applied config must drift: %v %v", drift, err)
	}
	if err := ch.MarkAllApplied(); err != nil {
		t.Fatal(err)
	}
	if drift, err := AppConfigDrift(app, ctx); err != nil || drift {
		t.Fatalf("written and applied config must not drift: %v %v", drift, err)
	}
	// A second write (e.g. a tool container) sees nothing pending.
	if _, ch2, err := WriteAppConfigChanges(app, ctx); err != nil || ch2.Frontend || ch2.Pool || ch2.Scheduler {
		t.Fatalf("applied config must report no scoped changes: %+v %v", ch2, err)
	}
	path := filepath.Join(ctx.Layout.AppConfigDir(app.ID), "nginx.conf")
	if err := os.WriteFile(path, []byte("# rendered by an older template\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	if drift, err := AppConfigDrift(app, ctx); err != nil || !drift {
		t.Fatalf("stale frontend config must drift: %v %v", drift, err)
	}
}

func TestPHPLimitsFollowOverrides(t *testing.T) {
	app := testApp()
	app.Runtime.PHP.Mode = domain.PHPModeHighConcurrency
	app.Runtime.PHP.MaxExecutionSeconds = 250
	app.Runtime.PHP.CLIMemoryLimitMB = 1024
	cfg, _, _, err := RenderAppConfig(app, AppContext{})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range cfg {
		files[f.name] = string(f.data)
	}
	if !strings.Contains(files["php-fpm.conf"], "pm.max_children = 18\n") || !strings.Contains(files["php-fpm.conf"], "php_value[memory_limit] = 48M\n") {
		t.Fatal(files["php-fpm.conf"])
	}
	if !strings.Contains(files["fastcgi.conf"], "fastcgi_read_timeout 260s;") || !strings.Contains(files[PHPIniFile], "memory_limit = 1024M") {
		t.Fatal("fastcgi timeout and CLI ini must follow overrides")
	}
	app.Runtime.PHP.Mode = "small"
	if _, _, _, err := RenderAppConfig(app, AppContext{}); err == nil {
		t.Fatal("a stored app with an unknown mode must fail to render")
	}
}
