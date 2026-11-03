package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// get returns the status and body of GET url, with an optional Host header.
func get(t *testing.T, url, host string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// Both Nginx layers refuse hostile paths with 404 and let them through when
// the app's protection is off, and turning it off reloads the running app.
// The HTTP app is protected only by the edge, the PHP app only by its own
// Nginx, so each layer is observed on its own.
func TestIntegrationAppProtection(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	httpPort, httpsPort := freePort(t), freePort(t)
	if op := e.configureEdge(httpPort, httpsPort); op.State != store.OpSucceeded {
		t.Fatalf("enable edge: %s: %s", op.ErrorCode, op.ErrorMessage)
	}

	node := domain.Runtime{Kind: domain.RuntimeHTTP, HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000}}
	php := domain.Runtime{Kind: domain.RuntimePHP, PHP: &domain.PHPRuntime{Version: "8.4", DocumentRoot: "public", Routing: "front-controller", Mode: "standard", UploadLimitMB: 16}}
	web, op, err := e.c.CreateApp(ctx, operations.CreateAppInput{Slug: "web", Runtime: node,
		Hosts:    []operations.AppHostInput{{Name: "web.example.com"}},
		Bindings: []operations.BindingRequest{{Engine: domain.EngineSQLite}}}, "")
	e.wait(op, err)
	pg := e.create("phpapp", php)
	writeFile(t, filepath.Join(e.layout.AppCode("web"), "s.js"),
		"require('http').createServer((q,s)=>s.end('web')).listen(3000,'0.0.0.0')", web.UID)
	code := e.layout.AppCode("phpapp")
	writeFile(t, filepath.Join(code, "public/index.php"), "<?php echo 'front';", pg.UID)
	writeFile(t, filepath.Join(code, "public/.env"), "SECRET=1", pg.UID)
	writeFile(t, filepath.Join(code, "public/composer.json"), "{}", pg.UID)
	writeFile(t, filepath.Join(code, "public/app.js.map"), "{}", pg.UID)
	writeFile(t, filepath.Join(code, "public/robots.txt"), "User-agent: *", pg.UID)
	for _, a := range []domain.App{web, pg} {
		e.wait(e.c.StartApp(ctx, a.ID, ""))
	}
	e.wait(e.c.PublishApp(ctx, web.ID, ""))

	edgeURL := func(path string) string { return fmt.Sprintf("http://127.0.0.1:%d%s", httpPort, path) }
	obs, _ := e.c.Observe(ctx, pg)
	appURL := func(path string) string { return fmt.Sprintf("http://%s:%d%s", obs.IP, pg.HTTPPort(), path) }

	probes := []struct {
		path    string
		blocked bool
	}{
		{"/.env", true},
		{"/wp-login.php", true},
		{"/wp-admin/", true},
		{"/composer.json", true},
		{"/app.js.map", true},
		{"/about", false},
		{"/robots.txt", false},
	}
	check := func(layer string, url func(string) string, host string) {
		t.Helper()
		for _, p := range probes {
			status, body := get(t, url(p.path), host)
			switch {
			case p.blocked && status != http.StatusNotFound:
				t.Errorf("%s: %s -> %d %q, want 404", layer, p.path, status, body)
			case !p.blocked && status != http.StatusOK:
				t.Errorf("%s: legitimate %s -> %d %q, want 200", layer, p.path, status, body)
			}
		}
	}
	check("edge", edgeURL, "web.example.com")
	check("app nginx", appURL, "")
	if status, _ := get(t, edgeURL("/.well-known/acme-challenge/x"), "web.example.com"); status != http.StatusOK {
		t.Errorf("/.well-known must reach the app through the edge: %d", status)
	}

	// Everything off: hostile paths reach the app in both layers.
	off := false
	patch := &domain.ProtectionPatch{BlockDotFiles: &off, BlockPHPProbes: &off, BlockSensitiveFiles: &off}
	for _, a := range []domain.App{web, pg} {
		_, op, err := e.c.UpdateApp(ctx, a.ID, operations.UpdateAppInput{Protection: patch}, "")
		e.wait(op, err)
	}
	if status, body := get(t, edgeURL("/.env"), "web.example.com"); status != http.StatusOK || body != "web" {
		t.Errorf("edge, protection off: /.env -> %d %q", status, body)
	}
	if status, body := get(t, appURL("/.env"), ""); status != http.StatusOK || body != "SECRET=1" {
		t.Errorf("app nginx, protection off: /.env -> %d %q", status, body)
	}
	if status, body := get(t, appURL("/wp-admin/"), ""); status != http.StatusOK || body != "front" {
		t.Errorf("app nginx, protection off: /wp-admin/ -> %d %q", status, body)
	}
	// The toggle reloads Nginx; it must not replace the running instance.
	if after, _ := e.c.Observe(ctx, pg); after.ContainerID != obs.ContainerID {
		t.Errorf("a protection change replaced the instance: %s -> %s", obs.ContainerID, after.ContainerID)
	}
}
