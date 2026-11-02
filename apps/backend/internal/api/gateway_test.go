package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/schedulerui"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestHardenSchedulerResponse(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":            {"application/json; charset=utf-8"},
			"Set-Cookie":              {"x=1"},
			"X-Frame-Options":         {"ALLOWALL"},
			"Referrer-Policy":         {"unsafe-url"},
			"X-Content-Type-Options":  {"nosniff"},
			"Content-Security-Policy": {"default-src *"},
			"Location":                {"http://evil.test/"},
			"Refresh":                 {"0; url=http://evil.test/"},
			"Link":                    {"<http://evil.test/x.js>; rel=preload"},
			"Cache-Control":           {"public, max-age=600"},
		},
	}
	if err := hardenSchedulerResponse(resp); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Content-Type":                 "application/json; charset=utf-8",
		"Set-Cookie":                   "",
		"X-Frame-Options":              "",
		"Referrer-Policy":              "",
		"X-Content-Type-Options":       "",
		"Location":                     "",
		"Refresh":                      "",
		"Link":                         "",
		"Content-Security-Policy":      schedulerAPICSP,
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cache-Control":                "no-store",
	}
	for k, v := range want {
		got := resp.Header.Values(k)
		if (v == "" && len(got) != 0) || (v != "" && (len(got) != 1 || got[0] != v)) {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	// Only API data passes: no page, script, or redirect from the app's socket.
	for name, tc := range map[string]struct {
		status int
		ct     string
		length int64
		ok     bool
	}{
		"json":            {200, "application/json", -1, true},
		"plain log":       {200, "text/plain; charset=utf-8", -1, true},
		"event stream":    {200, "text/event-stream", -1, true},
		"toml export":     {200, "application/toml", -1, true},
		"empty 204":       {204, "", 0, true},
		"304":             {304, "", -1, true},
		"html":            {200, "text/html; charset=utf-8", -1, false},
		"html uppercase":  {200, "TEXT/HTML", -1, false},
		"xhtml":           {200, "application/xhtml+xml", -1, false},
		"svg":             {200, "image/svg+xml", -1, false},
		"script":          {200, "text/javascript", -1, false},
		"css":             {200, "text/css", -1, false},
		"untyped body":    {200, "", -1, false},
		"malformed type":  {200, "application/json; ===", -1, false},
		"redirect":        {302, "application/json", -1, false},
		"permanent redir": {308, "", 0, false},
	} {
		h := http.Header{}
		if tc.ct != "" {
			h.Set("Content-Type", tc.ct)
		}
		err := hardenSchedulerResponse(&http.Response{StatusCode: tc.status, Header: h, ContentLength: tc.length})
		if tc.ok && err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
		if !tc.ok && !errors.Is(err, errSchedulerResponse) {
			t.Errorf("%s admitted (err %v)", name, err)
		}
	}
}

func TestOriginAllowedRefusesOpaque(t *testing.T) {
	s := &Server{AllowedOrigins: []string{"null", "", "http://127.0.0.1:7070"}}
	for _, o := range []string{"null", ""} {
		if s.originAllowed(o) {
			t.Errorf("origin %q allowed", o)
		}
	}
	if !s.originAllowed("http://127.0.0.1:7070") {
		t.Error("configured origin refused")
	}
}

// hostileMinicron stands in for a server a compromised app put behind its
// minicrond socket. It answers every path with its own page and script, and
// a few API paths with data, HTML, or a redirect. It returns the dial count.
func hostileMinicron(t *testing.T, s *Server) *atomic.Int64 {
	t.Helper()
	const evil = `<script>fetch('/api/v1/session')</script>`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "bento_session=planted; Path=/")
		w.Write([]byte(`{"items":[]}`))
	})
	mux.HandleFunc("POST /api/v1/token/rotate", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-CSRF-Token") != "" {
			t.Errorf("credentials crossed the relay: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"t","fingerprint":"f"}`))
	})
	mux.HandleFunc("GET /api/v1/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(evil))
	})
	mux.HandleFunc("GET /api/v1/sniff", func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil // no type: Go would sniff text/html
		w.Write([]byte(evil))
	})
	mux.HandleFunc("GET /api/v1/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.test/", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(evil))
	})
	srv := httptest.NewServer(http.StripPrefix("/apps/shop/scheduler", mux))
	t.Cleanup(srv.Close)
	var dials atomic.Int64
	s.schedulerDial = func(ctx context.Context, _ domain.App) (net.Conn, error) {
		dials.Add(1)
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}
	return &dials
}

// The scheduler UI is served on the management origin under
// /apps/<slug>/scheduler/, behind an operator session and an exact Origin on
// writes. The page and its assets come from the vendored bundle; only /api/
// reaches the app's socket, and only data comes back. The utils listener never
// serves it.
func TestSchedulerGateway(t *testing.T) {
	c, h := newServer(t)
	c.login()
	ctx := t.Context()
	dials := hostileMinicron(t, c.api)
	for _, slug := range []string{"shop", "blog"} {
		_, body := c.write("POST", "/api/v1/apps", `{"slug":"`+slug+`","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"]}},"hosts":[{"name":"`+slug+`.example.com"}]}`)
		var acc dto.Accepted
		json.Unmarshal([]byte(body), &acc)
		h.Wait(acc.Operation.ID)
	}
	get := func(path string, hdr map[string]string) (*http.Response, string) {
		return c.do("GET", path, "", hdr)
	}

	// A stopped app has no scheduler to reach.
	if resp, _ := get("/apps/shop/scheduler/", nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stopped app -> %d", resp.StatusCode)
	}
	if _, err := h.Store.DB().ExecContext(ctx, `UPDATE apps SET desired_runtime='running'`); err != nil {
		t.Fatal(err)
	}
	if app, err := store.GetApp(ctx, h.Store.DB(), "shop"); err != nil || app.DesiredRuntime != domain.DesiredRunning {
		t.Fatalf("shop: %v %v", err, app.DesiredRuntime)
	}

	// The shell is the vendored page for every client route, framable only by
	// this origin, whatever the app's socket would have served.
	shell, err := schedulerui.Index("/apps/shop/scheduler/")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/apps/shop/scheduler/", "/apps/shop/scheduler/runs/0b4f", "/apps/shop/scheduler/jobs/backup.daily"} {
		resp, body := get(path, nil)
		if resp.StatusCode != http.StatusOK || body != string(shell) {
			t.Fatalf("%s -> %d %q", path, resp.StatusCode, body)
		}
		if got := resp.Header.Values("Content-Security-Policy"); !slices.Equal(got, []string{schedulerShellCSP}) {
			t.Errorf("%s CSP %q", path, got)
		}
		if resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" || resp.Header.Get("Cache-Control") != "no-store" ||
			!strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s headers %v", path, resp.Header)
		}
	}
	links := regexp.MustCompile(`(?:src|href)="assets/([^"]+)"`).FindAllStringSubmatch(string(shell), -1)
	if len(links) < 2 {
		t.Fatalf("shell links %v", links)
	}
	for _, m := range links {
		asset, _ := schedulerui.Lookup(m[1])
		resp, body := get("/apps/shop/scheduler/assets/"+m[1], nil)
		if resp.StatusCode != http.StatusOK || body != string(asset.Body) || resp.Header.Get("Content-Type") != asset.ContentType ||
			resp.Header.Get("ETag") != asset.ETag {
			t.Fatalf("asset %s -> %d %v", m[1], resp.StatusCode, resp.Header)
		}
		if resp, _ := get("/apps/shop/scheduler/assets/"+m[1], map[string]string{"If-None-Match": asset.ETag}); resp.StatusCode != http.StatusNotModified {
			t.Errorf("asset %s revalidation -> %d", m[1], resp.StatusCode)
		}
	}
	for _, path := range []string{"/apps/shop/scheduler/assets/evil.js", "/apps/shop/scheduler/assets/index.html", "/apps/shop/scheduler/api"} {
		if resp, _ := get(path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s -> %d", path, resp.StatusCode)
		}
	}
	if resp, _ := c.do("POST", "/apps/shop/scheduler/", `{}`, map[string]string{"Origin": origin}); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST shell -> %d", resp.StatusCode)
	}
	if n := dials.Load(); n != 0 {
		t.Fatalf("the page or its assets dialed the app's socket %d times", n)
	}

	// API data crosses the relay, sandboxed and without app cookies.
	resp, body := get("/apps/shop/scheduler/api/v1/jobs", nil)
	if resp.StatusCode != http.StatusOK || body != `{"items":[]}` {
		t.Fatalf("api read -> %d %s", resp.StatusCode, body)
	}
	if !slices.Contains(resp.Header.Values("Content-Security-Policy"), schedulerAPICSP) ||
		resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("Set-Cookie") != "" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("api headers %v", resp.Header)
	}
	if dials.Load() != 1 {
		t.Fatalf("api read dials %d", dials.Load())
	}
	// Pages, untyped bodies, and redirects from the socket never reach the browser.
	for _, path := range []string{"/apps/shop/scheduler/api/v1/page", "/apps/shop/scheduler/api/v1/sniff", "/apps/shop/scheduler/api/v1/redirect"} {
		resp, body := get(path, nil)
		if resp.StatusCode != http.StatusBadGateway || strings.Contains(body, "<script>") ||
			strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s -> %d %q %v", path, resp.StatusCode, body, resp.Header)
		}
	}

	// The bare path is the web UI's client route, not the gateway.
	before := dials.Load()
	if resp, _ := get("/apps/shop/scheduler", nil); resp.Header.Get("Content-Security-Policy") == schedulerShellCSP {
		t.Fatalf("bare scheduler path reached the gateway")
	}

	// Refused before the relay: unknown app, malformed slugs, app ids.
	for path, want := range map[string]int{
		"/apps/nosuch/scheduler/api/v1/jobs": http.StatusNotFound,
		"/apps/UPPER/scheduler/api/v1/jobs":  http.StatusNotFound,
	} {
		if resp, _ := get(path, nil); resp.StatusCode != want {
			t.Errorf("%s -> %d, want %d", path, resp.StatusCode, want)
		}
	}
	app, err := store.GetApp(ctx, h.Store.DB(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := get("/apps/"+app.ID+"/scheduler/api/v1/jobs", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("app id -> %d", resp.StatusCode)
	}

	// Writes need an exact Origin and same-origin fetch metadata, not a CSRF
	// token (minicrond's UI never sees it).
	for name, hdr := range map[string]map[string]string{
		"no origin":      {},
		"cross-site":     {"Origin": origin, "Sec-Fetch-Site": "cross-site"},
		"foreign origin": {"Origin": "http://evil.test"},
		"opaque origin":  {"Origin": "null"},
	} {
		if resp, _ := c.do("POST", "/apps/shop/scheduler/api/v1/token/rotate", `{}`, hdr); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s write -> %d", name, resp.StatusCode)
		}
	}
	if dials.Load() != before {
		t.Fatalf("a refused request dialed the app's socket")
	}
	if resp, body := c.do("POST", "/apps/shop/scheduler/api/v1/token/rotate", `{}`,
		map[string]string{"Origin": origin, csrfHeader: c.csrf}); resp.StatusCode != http.StatusOK {
		t.Fatalf("same-origin write -> %d %s", resp.StatusCode, body)
	}

	// No session, no scheduler; logout ends access.
	anon := &client{t: t, api: c.api, srv: c.srv}
	if resp, _ := anon.do("GET", "/apps/shop/scheduler/", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous -> %d", resp.StatusCode)
	}
	c.do("DELETE", "/api/v1/session", "", map[string]string{"Origin": origin, csrfHeader: c.csrf})
	if resp, _ := get("/apps/shop/scheduler/", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session survived logout -> %d", resp.StatusCode)
	}
}
