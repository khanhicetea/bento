package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// protectionCases are request paths and whether the default protection must
// answer 404. A false case is a legitimate path that must reach the app: the
// patterns are PCRE with lookarounds, so only real Nginx can vouch for them.
var protectionCases = []struct {
	blocked bool
	path    string
}{
	{false, `/`},
	{false, `/index.php`},
	{false, `/about`},
	{false, `/products/phones`},
	{false, `/css/app.css`},
	{false, `/js/app.3f2a9c1d.js`},
	{false, `/.well-known/acme-challenge/abc`},
	{false, `/.well-known/security.txt`},
	{false, `/sitemap.xml`},
	{false, `/sitemap.xml.gz`},
	{false, `/downloads/report.pdf`},
	{false, `/images/logo.png`},
	{false, `/wp`},
	{false, `/wordpress-tips`},
	{false, `/blog/wp-contents`},
	{false, `/plugins/wpa-login`},
	{false, `/assets/css/app.css`},
	{false, `/vendor/bootstrap/css/bootstrap.css`},
	{false, `/php-guide`},
	{false, `/info`},
	{false, `/tests/phpinfo`},
	{false, `/archive/2024`},
	{false, `/mapping`},
	{false, `/shell`},
	{false, `/api/package/list`},
	{false, `/composer`},
	{false, `/backup-policy`},
	{false, `/sql-tutorial`},
	{true, `/.env`},
	{true, `/.git/config`},
	{true, `/.git/HEAD`},
	{true, `/app/.env`},
	{true, `/.htaccess`},
	{true, `/.DS_Store`},
	{true, `/.well-known/../.env`},
	{true, `/.well-known/x/.hidden`},
	{true, `/wp-login.php`},
	{true, `/WP-LOGIN.PHP`},
	{true, `/wp-admin/`},
	{true, `/wp-admin/admin-ajax.php`},
	{true, `/wp-content/uploads/a.jpg`},
	{true, `/wp-includes/js/jquery.js`},
	{true, `/blog/wp-login.php`},
	{true, `/wordpress/wp-admin/install.php`},
	{true, `/wp-json/wp/v2/users`},
	{true, `/xmlrpc.php`},
	{true, `/wp-config.php`},
	{true, `/wp-cron.php`},
	{true, `/wlwmanifest.xml`},
	{true, `/wp-login`},
	{true, `/phpmyadmin/`},
	{true, `/phpMyAdmin-5.2.1/index.php`},
	{true, `/pma/`},
	{true, `/myadmin/index.php`},
	{true, `/adminer.php`},
	{true, `/adminer-4.8.1.php`},
	{true, `/phpinfo.php`},
	{true, `/info.php.bak`},
	{true, `/shell.php`},
	{true, `/c99.php`},
	{true, `/vendor/phpunit/phpunit/src/Util/PHP/eval-stdin.php`},
	{true, `/uploads/shell.php`},
	{true, `/wp-content/uploads/2020/x.php`},
	{true, `/storage/app/x.php`},
	{true, `/images/avatar.php`},
	{true, `/files/x.php/y`},
	{true, `/index.phps`},
	{true, `/page.phtml`},
	{true, `/lib/config.inc`},
	{true, `/x.php5`},
	{true, `/x.phar`},
	{true, `/config.php.bak`},
	{true, `/config.php~`},
	{true, `/config.php.old`},
	{true, `/config.php.orig`},
	{true, `/config.php.save`},
	{true, `/config.php.swp`},
	{true, `/dump.sql`},
	{true, `/db.sqlite3`},
	{true, `/debug.log`},
	{true, `/config.php.dist`},
	{true, `/backup.zip`},
	{true, `/site.tar.gz`},
	{true, `/site.tgz`},
	{true, `/a.7z`},
	{true, `/a.rar`},
	{true, `/backup.tar`},
	{true, `/app.js.map`},
	{true, `/style.css.map`},
	{true, `/composer.json`},
	{true, `/composer.lock`},
	{true, `/package.json`},
	{true, `/package-lock.json`},
	{true, `/yarn.lock`},
	{true, `/pnpm-lock.yaml`},
	{true, `/Gemfile.lock`},
	{true, `/requirements.txt`},
	{true, `/sub/dir/composer.json`},
	{true, `/vendor/composer/installed.json`},
	{true, `/go.mod`},
	{true, `/Cargo.lock`},
}

// TestIntegrationProtectionNginx renders the default protection into a real
// Nginx (the edge image) and requests every case, then checks that turning a
// group off lets its paths through while the other groups keep blocking.
func TestIntegrationProtectionNginx(t *testing.T) {
	sdk := requireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := docker.EnsureImage(ctx, sdk, domain.EdgeImage, nil); err != nil {
		t.Fatal(err)
	}
	stack := fmt.Sprintf("protection-test-%d", time.Now().UnixNano())
	id, err := sdk.Create(ctx, docker.ContainerSpec{
		Name: stack,
		Config: &container.Config{
			Image:      domain.EdgeImage,
			Entrypoint: []string{"sleep", "600"},
			Labels:     map[string]string{"io.bento.stack-id": stack},
		},
		HostConfig: &container.HostConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sdk.Remove(context.WithoutCancel(ctx), id); err != nil {
			t.Errorf("remove %s: %v", stack, err)
		}
	})
	if err := sdk.Start(ctx, id); err != nil {
		t.Fatal(err)
	}

	sh := func(script string) string {
		t.Helper()
		res, err := sdk.Exec(ctx, id, docker.ExecRequest{Cmd: []string{"bash", "-s"}, Stdin: strings.NewReader(script)})
		if err != nil {
			t.Fatal(err)
		}
		if res.ExitCode != 0 {
			t.Fatalf("script failed (%d): %s%s", res.ExitCode, res.Stdout, res.Stderr)
		}
		return string(res.Stdout)
	}
	// status requests every path in one exec and prints "<status> <path>".
	status := func(paths []string) map[string]string {
		t.Helper()
		var script strings.Builder
		script.WriteString(`req() { exec 3<>/dev/tcp/127.0.0.1/8080; printf "GET %s HTTP/1.0\r\nHost: x\r\n\r\n" "$1" >&3; echo "$(head -1 <&3 | cut -d' ' -f2) $1"; exec 3<&-; }` + "\n")
		for _, p := range paths {
			script.WriteString("req '" + p + "'\n")
		}
		got := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(sh(script.String())), "\n") {
			code, path, _ := strings.Cut(line, " ")
			got[path] = code
		}
		return got
	}
	apply := func(p domain.Protection) {
		t.Helper()
		conf := "pid /tmp/nginx.pid;\nerror_log stderr warn;\nevents { worker_connections 64; }\nhttp {\n  access_log off;\n  server {\n    listen 8080;\n" +
			ProtectionConfig(p, "    ") +
			"\n    location ~* \\.(?:css|js|mjs|map|jpg|png|svg|gz|zip|json|txt)$ { return 200 \"static\"; }\n    location / { return 200 \"app\"; }\n  }\n}\n"
		sh("cat > /tmp/nginx.conf <<'CONF'\n" + conf + "CONF\nnginx -t -c /tmp/nginx.conf\n")
		sh("if [ -f /tmp/nginx.pid ]; then nginx -c /tmp/nginx.conf -s stop; sleep 0.5; fi; nginx -c /tmp/nginx.conf\nsleep 0.5\n")
	}

	paths := make([]string, len(protectionCases))
	for i, c := range protectionCases {
		paths[i] = c.path
	}

	apply(domain.DefaultProtection())
	got := status(paths)
	for _, c := range protectionCases {
		want := "200"
		if c.blocked {
			want = "404"
		}
		if got[c.path] != want {
			t.Errorf("all on: %s answered %q, want %s", c.path, got[c.path], want)
		}
	}

	// Each group alone: only its own paths are refused. Every path above is
	// blocked by exactly the group that names it, so the group a path belongs
	// to is taken from which single-group run blocks it.
	blockedBy := map[string]string{}
	groups := map[string]domain.Protection{
		"dotfiles":  {BlockDotFiles: true},
		"phpprobes": {BlockPHPProbes: true},
		"sensitive": {BlockSensitiveFiles: true},
	}
	for name, p := range groups {
		apply(p)
		for path, code := range status(paths) {
			if code == "404" {
				blockedBy[path] += name + " "
			}
		}
	}
	for _, c := range protectionCases {
		if c.blocked && blockedBy[c.path] == "" {
			t.Errorf("%s is blocked with everything on but by no single group", c.path)
		}
		if !c.blocked && blockedBy[c.path] != "" {
			t.Errorf("%s is legitimate but groups %q block it", c.path, blockedBy[c.path])
		}
	}

	// Everything off: nothing is refused.
	apply(domain.Protection{})
	for path, code := range status(paths) {
		if code != "200" {
			t.Errorf("all off: %s answered %s", path, code)
		}
	}
}
