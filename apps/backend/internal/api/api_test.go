package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/reconcile"
	"github.com/khanhicetea/bento/apps/backend/internal/scheduler"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
	"github.com/khanhicetea/bento/apps/backend/internal/testutil"
	"github.com/khanhicetea/bento/apps/backend/internal/webui"
)

const origin = "http://127.0.0.1:7780"
const password = "correct horse battery staple"

type client struct {
	t      *testing.T
	srv    *httptest.Server
	cookie *http.Cookie
	csrf   string
}

func newServer(t *testing.T) (*client, *testutil.Harness) {
	h := testutil.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{C: h.C, R: reconcile.New(h.C, log), Store: h.Store, Layout: h.Layout, Log: log, Version: "test",
		StartedAt: time.Now(), AllowedOrigins: []string{origin}, WebUI: webui.FS(), Relay: scheduler.NewRelayManager(h.Layout, "/proc/self/exe", log)}
	if err := SetOperatorPassword(context.Background(), h.Store, password); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &client{t: t, srv: srv}, h
}

func (c *client) do(method, path, body string, hdr map[string]string) (*http.Response, string) {
	c.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func (c *client) login() {
	c.t.Helper()
	resp, body := c.do("POST", "/api/v1/session", `{"password":"`+password+`"}`, map[string]string{"Origin": origin})
	if resp.StatusCode != 200 {
		c.t.Fatalf("login %d %s", resp.StatusCode, body)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == SessionCookie {
			c.cookie = ck
			if !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode {
				c.t.Fatal("session cookie must be HttpOnly and SameSite=Strict")
			}
		}
	}
	var s dto.Session
	json.Unmarshal([]byte(body), &s)
	c.csrf = s.CSRFToken
}

func (c *client) write(method, path, body string) (*http.Response, string) {
	return c.do(method, path, body, map[string]string{"Origin": origin, csrfHeader: c.csrf, "Sec-Fetch-Site": "same-origin"})
}

func TestUnauthenticatedSurfacesAreClosed(t *testing.T) {
	c, _ := newServer(t)
	for _, p := range []string{"/api/v1/apps", "/api/v1/system", "/api/v1/operations", "/scheduler/apps/shop/", "/api/v1/apps/shop/terminal"} {
		resp, _ := c.do("GET", p, "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s -> %d", p, resp.StatusCode)
		}
	}
	resp, _ := c.do("POST", "/api/v1/apps", `{}`, map[string]string{"Origin": origin})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("write without session -> %d", resp.StatusCode)
	}
}

func TestLoginRequiresExactOriginAndPassword(t *testing.T) {
	c, _ := newServer(t)
	for _, o := range []string{"", "http://evil.test", "http://127.0.0.1:7781", origin + "/"} {
		resp, _ := c.do("POST", "/api/v1/session", `{"password":"`+password+`"}`, map[string]string{"Origin": o})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("origin %q -> %d", o, resp.StatusCode)
		}
	}
	resp, _ := c.do("POST", "/api/v1/session", `{"password":"wrong password here"}`, map[string]string{"Origin": origin})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password -> %d", resp.StatusCode)
	}
}

func TestWritesRequireCSRFOriginAndFetchMetadata(t *testing.T) {
	c, _ := newServer(t)
	c.login()
	body := `{"engine":"mysql","version":"8.4"}`
	cases := map[string]map[string]string{
		"no csrf":        {"Origin": origin},
		"wrong csrf":     {"Origin": origin, csrfHeader: "nope"},
		"foreign origin": {"Origin": "http://evil.test", csrfHeader: c.csrf},
		"no origin":      {csrfHeader: c.csrf},
		"cross-site":     {"Origin": origin, csrfHeader: c.csrf, "Sec-Fetch-Site": "cross-site"},
	}
	for name, hdr := range cases {
		resp, _ := c.do("POST", "/api/v1/services", body, hdr)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s -> %d", name, resp.StatusCode)
		}
	}
	resp, out := c.write("POST", "/api/v1/services", body)
	if resp.StatusCode != http.StatusAccepted || resp.Header.Get("Location") == "" {
		t.Fatalf("valid write -> %d %s", resp.StatusCode, out)
	}
	// Logout revokes the session.
	c.do("DELETE", "/api/v1/session", "", nil)
	resp, _ = c.do("GET", "/api/v1/apps", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("session survived logout")
	}
}

func TestSessionExpiry(t *testing.T) {
	c, h := newServer(t)
	c.login()
	if _, err := h.Store.DB().ExecContext(context.Background(), "UPDATE sessions SET expires_at = '2000-01-01T00:00:00.000Z'"); err != nil {
		t.Fatal(err)
	}
	resp, _ := c.do("GET", "/api/v1/apps", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("expired session accepted")
	}
}

func TestStrictDecoding(t *testing.T) {
	c, _ := newServer(t)
	c.login()
	valid := `{"slug":"shop","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"],"workdir":"","port":3000}},"domains":[],"bindings":[]}`
	cases := map[string]struct {
		body   string
		status int
	}{
		"unknown field":      {`{"slug":"shop","bogus":1}`, 400},
		"trailing data":      {valid + ` {}`, 400},
		"wrong type":         {`{"slug":123}`, 400},
		"out of range int":   {`{"slug":"x","resources":{"memoryMb":99999999999999999999}}`, 400},
		"invalid enum":       {`{"slug":"shop","runtime":{"kind":"docker"},"domains":[],"bindings":[]}`, 422},
		"invalid slug":       {strings.Replace(valid, `"shop"`, `"../etc"`, 1), 422},
		"variant mismatch":   {`{"slug":"shop","runtime":{"kind":"php-fpm","http":{"toolchain":"node"}},"domains":[],"bindings":[]}`, 422},
		"invalid engine ref": {strings.Replace(valid, `"bindings":[]`, `"bindings":[{"engine":"oracle"}]`, 1), 422},
		"not json":           {`slug=shop`, 400},
		"too large":          {`{"slug":"` + strings.Repeat("a", MaxBodyBytes) + `"}`, 413},
	}
	for name, tc := range cases {
		resp, body := c.write("POST", "/api/v1/apps", tc.body)
		if resp.StatusCode != tc.status {
			t.Errorf("%s -> %d (want %d) %s", name, resp.StatusCode, tc.status, body)
			continue
		}
		var e dto.ErrorResponse
		if err := json.Unmarshal([]byte(body), &e); err != nil || e.Error.Code == "" {
			t.Errorf("%s: missing stable error code: %s", name, body)
		}
	}
	resp, _ := c.do("POST", "/api/v1/apps", valid, map[string]string{"Origin": origin, csrfHeader: c.csrf, "Content-Type": "text/plain"})
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("non-JSON content type -> %d", resp.StatusCode)
	}
	resp, _ = c.write("POST", "/api/v1/apps", valid)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("valid create -> %d", resp.StatusCode)
	}
}

func TestIdempotencyKeyOverHTTP(t *testing.T) {
	c, _ := newServer(t)
	c.login()
	body := `{"engine":"postgres","version":"17"}`
	hdr := map[string]string{"Origin": origin, csrfHeader: c.csrf, "Idempotency-Key": "retry-key-123"}
	_, a := c.do("POST", "/api/v1/services", body, hdr)
	_, b := c.do("POST", "/api/v1/services", body, hdr)
	var x, y dto.Accepted
	json.Unmarshal([]byte(a), &x)
	json.Unmarshal([]byte(b), &y)
	if x.Operation.ID == "" || x.Operation.ID != y.Operation.ID {
		t.Fatalf("idempotent retry returned %q and %q", x.Operation.ID, y.Operation.ID)
	}
	hdr["Idempotency-Key"] = "bad key!"
	resp, _ := c.do("POST", "/api/v1/services", body, hdr)
	if resp.StatusCode != 400 {
		t.Fatal("malformed idempotency key accepted")
	}
}

func TestSecretsNeverInResponses(t *testing.T) {
	c, h := newServer(t)
	c.login()
	_, body := c.write("POST", "/api/v1/apps", `{"slug":"shop","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"]}},"domains":["shop.example.com"],"bindings":[{"engine":"sqlite"}]}`)
	var acc dto.Accepted
	json.Unmarshal([]byte(body), &acc)
	h.Wait(acc.Operation.ID)
	app, _ := store.GetApp(context.Background(), h.Store.DB(), "shop")
	for _, p := range []string{"/api/v1/apps", "/api/v1/apps/shop", "/api/v1/operations", "/api/v1/tunnel"} {
		_, out := c.do("GET", p, "", nil)
		if strings.Contains(out, app.Redis.Password) {
			t.Fatalf("%s leaked a secret", p)
		}
	}
}

func TestSchedulerGatewayAuthorization(t *testing.T) {
	c, _ := newServer(t)
	c.login()
	resp, _ := c.do("GET", "/scheduler/apps/nosuch/", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown app -> %d", resp.StatusCode)
	}
	resp, _ = c.do("POST", "/scheduler/apps/nosuch/api/v1/token/rotate", "", map[string]string{"Origin": "http://evil.test"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin scheduler write -> %d", resp.StatusCode)
	}
	for _, p := range []string{"/scheduler/apps/UPPER/", "/scheduler/apps/a/", "/scheduler/apps/shop/../x"} {
		resp, _ = c.do("GET", p, "", nil)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s accepted", p)
		}
	}
}

func TestSecurityHeadersAndNoCORS(t *testing.T) {
	c, _ := newServer(t)
	resp, _ := c.do("GET", "/api/v1/session", "", map[string]string{"Origin": "http://evil.test"})
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS must not be permissive")
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("frame protections missing")
	}
}

// TestWireFidelity checks DTO JSON against the committed generated types:
// embedded structs flatten (tygo "extends"), arrays are never null, and
// every JSON name used on the wire exists in the TypeScript.
func TestWireFidelity(t *testing.T) {
	ts, err := os.ReadFile("../../../web/src/api/generated/types.ts")
	if err != nil {
		t.Skip("generated types not present")
	}
	app := dto.App{AppSummary: dto.AppSummary{ID: "a1", Slug: "s", Observed: dto.Observed{State: dto.ObservedStateHealthy},
		BindingSummary: []dto.BindingSummary{{Engine: dto.EngineSQLite}}},
		Domains: []dto.Domain{}, Bindings: []dto.Binding{{ID: "b", Engine: dto.EngineSQLite, Databases: []string{}}}}
	raw, _ := json.Marshal(app)
	var m map[string]any
	json.Unmarshal(raw, &m)
	if _, nested := m["AppSummary"]; nested {
		t.Fatal("embedded summary must flatten on the wire")
	}
	if m["slug"] != "s" {
		t.Fatal("summary fields must be top-level")
	}
	if !strings.Contains(string(ts), "export interface App extends AppSummary") {
		t.Fatal("generated App must extend AppSummary")
	}
	keyRe := regexp.MustCompile(`"([a-zA-Z0-9]+)":`)
	for _, k := range keyRe.FindAllStringSubmatch(string(raw), -1) {
		if !strings.Contains(string(ts), " "+k[1]+"?:") && !strings.Contains(string(ts), " "+k[1]+":") {
			t.Errorf("wire field %q missing from generated types", k[1])
		}
	}
	if strings.Contains(string(raw), "null") {
		t.Fatalf("non-optional arrays must not serialize as null: %s", raw)
	}
	for _, union := range []string{"export type ObservedState = ", "export type OperationState = ", "export type RuntimeKind = "} {
		if !strings.Contains(string(ts), union) {
			t.Errorf("missing union %s", union)
		}
	}
}

func TestSPAFallbackDoesNotMaskMissingAssets(t *testing.T) {
	c, _ := newServer(t)
	resp, _ := c.do("GET", "/assets/missing-chunk.js", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset -> %d", resp.StatusCode)
	}
}
