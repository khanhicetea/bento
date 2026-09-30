package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestHardenSchedulerResponse(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Set-Cookie":              {"x=1"},
			"X-Frame-Options":         {"DENY"},
			"Referrer-Policy":         {"no-referrer"},
			"X-Content-Type-Options":  {"nosniff"},
			"Content-Security-Policy": {"default-src 'self'"},
		},
	}
	if err := hardenSchedulerResponse(resp); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Set-Cookie":                   "",
		"X-Frame-Options":              "",
		"Referrer-Policy":              "",
		"X-Content-Type-Options":       "",
		"Content-Security-Policy":      "default-src 'self'",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cache-Control":                "no-store",
	}
	for k, v := range want {
		got := resp.Header.Values(k)
		if (v == "" && len(got) != 0) || (v != "" && (len(got) != 1 || got[0] != v)) {
			t.Errorf("%s = %q, want %q", k, got, v)
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

// The scheduler UI is served on the management origin under
// /apps/<slug>/scheduler/, behind an operator session and an exact Origin on
// writes; the utils listener never serves it.
func TestSchedulerGateway(t *testing.T) {
	c, h := newServer(t)
	c.login()
	ctx := t.Context()
	for _, slug := range []string{"shop", "blog"} {
		_, body := c.write("POST", "/api/v1/apps", `{"slug":"`+slug+`","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"]}},"domains":["`+slug+`.example.com"]}`)
		var acc dto.Accepted
		json.Unmarshal([]byte(body), &acc)
		h.Wait(acc.Operation.ID)
	}
	get := func(path string, hdr map[string]string) *http.Response {
		resp, _ := c.do("GET", path, "", hdr)
		return resp
	}

	// A stopped app has no scheduler to reach.
	if resp := get("/apps/shop/scheduler/", nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stopped app -> %d", resp.StatusCode)
	}
	if _, err := h.Store.DB().ExecContext(ctx, `UPDATE apps SET desired_runtime='running'`); err != nil {
		t.Fatal(err)
	}
	if app, err := store.GetApp(ctx, h.Store.DB(), "shop"); err != nil || app.DesiredRuntime != domain.DesiredRunning {
		t.Fatalf("shop: %v %v", err, app.DesiredRuntime)
	}

	// A valid session passes every check and reaches the relay (no minicrond
	// runs in tests, so the proxy answers 502).
	resp := get("/apps/shop/scheduler/", nil)
	if resp.StatusCode != http.StatusBadGateway || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("session read -> %d", resp.StatusCode)
	}
	if resp := get("/apps/shop/scheduler/api/v1/jobs", nil); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("nested read -> %d", resp.StatusCode)
	}
	// The bare path is the web UI's client route, not the proxy.
	if resp := get("/apps/shop/scheduler", nil); resp.StatusCode == http.StatusBadGateway {
		t.Fatalf("bare scheduler path reached the proxy")
	}

	// Refused before the relay: unknown app, malformed slugs, app ids.
	for path, want := range map[string]int{
		"/apps/nosuch/scheduler/": http.StatusNotFound,
		"/apps/UPPER/scheduler/":  http.StatusNotFound,
	} {
		if resp := get(path, nil); resp.StatusCode != want {
			t.Errorf("%s -> %d, want %d", path, resp.StatusCode, want)
		}
	}
	app, err := store.GetApp(ctx, h.Store.DB(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if resp := get("/apps/"+app.ID+"/scheduler/", nil); resp.StatusCode == http.StatusBadGateway {
		t.Fatalf("app id reached the proxy")
	}

	// Writes need an exact Origin and same-origin fetch metadata, not a CSRF
	// token (the app-served UI never sees it).
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
	if resp, _ := c.do("POST", "/apps/shop/scheduler/api/v1/token/rotate", `{}`, map[string]string{"Origin": origin}); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("same-origin write -> %d", resp.StatusCode)
	}

	// No session, no scheduler; logout ends access.
	anon := &client{t: t, api: c.api, srv: c.srv}
	if resp, _ := anon.do("GET", "/apps/shop/scheduler/", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous -> %d", resp.StatusCode)
	}
	c.do("DELETE", "/api/v1/session", "", map[string]string{"Origin": origin, csrfHeader: c.csrf})
	if resp := get("/apps/shop/scheduler/", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session survived logout -> %d", resp.StatusCode)
	}
}
