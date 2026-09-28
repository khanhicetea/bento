package api

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
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
	api    *Server
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
	return &client{t: t, srv: srv, api: s}, h
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

func TestGitSourceEndpoints(t *testing.T) {
	c, h := newServer(t)
	c.login()
	_, body := c.write("POST", "/api/v1/apps", `{"slug":"shop","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"]}},"domains":["shop.example.com"]}`)
	var acc dto.Accepted
	json.Unmarshal([]byte(body), &acc)
	h.Wait(acc.Operation.ID)

	resp, out := c.do("GET", "/api/v1/apps/shop/git", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(out, `"configured":false`) {
		t.Fatalf("unconfigured get -> %d %s", resp.StatusCode, out)
	}
	resp, _ = c.write("POST", "/api/v1/apps/shop/deploy", `{}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("deploy without source -> %d", resp.StatusCode)
	}
	// CSRF is required for key generation.
	resp, _ = c.do("PUT", "/api/v1/apps/shop/git", `{"repoUrl":"git@github.com:o/r.git","branch":"main"}`, map[string]string{"Origin": origin})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("put without csrf -> %d", resp.StatusCode)
	}
	for _, bad := range []string{
		`{"repoUrl":"https://u:tok@github.com/o/r.git","branch":"main"}`,
		`{"repoUrl":"--upload-pack=touch /tmp/x","branch":"main"}`,
		`{"repoUrl":"ext::sh -c touch% /tmp/x","branch":"main"}`,
		`{"repoUrl":"git@github.com:o/r.git","branch":"--orphan"}`,
		`{"repoUrl":"git@github.com:o/r.git","branch":"a..b"}`,
		`{"repoUrl":"git@github.com:o/r.git","branch":"main","extra":1}`,
	} {
		resp, out := c.write("PUT", "/api/v1/apps/shop/git", bad)
		if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s -> %d %s", bad, resp.StatusCode, out)
		}
	}
	resp, out = c.write("PUT", "/api/v1/apps/shop/git", `{"repoUrl":"git@github.com:o/r.git","branch":"main"}`)
	var g dto.GitSource
	json.Unmarshal([]byte(out), &g)
	if resp.StatusCode != 200 || !g.Configured || !g.UsesSSH || !strings.HasPrefix(g.PublicKey, "ssh-ed25519 ") || g.Fingerprint == "" {
		t.Fatalf("put -> %d %s", resp.StatusCode, out)
	}
	stored, _, _ := store.GetGitSource(context.Background(), h.Store.DB(), acc.Operation.TargetID)
	keyBody := strings.Split(stored.PrivateKey, "\n")[1]
	for _, p := range []string{"/api/v1/apps/shop/git", "/api/v1/apps/shop", "/api/v1/operations"} {
		_, out := c.do("GET", p, "", nil)
		if strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, keyBody) {
			t.Fatalf("%s leaked the deploy private key", p)
		}
	}
	resp, out = c.write("POST", "/api/v1/apps/shop/deploy", `{}`)
	if resp.StatusCode != http.StatusAccepted || resp.Header.Get("Location") == "" {
		t.Fatalf("deploy -> %d %s", resp.StatusCode, out)
	}
	resp, _ = c.write("DELETE", "/api/v1/apps/shop/git", "")
	if resp.StatusCode != 200 {
		t.Fatalf("delete -> %d", resp.StatusCode)
	}
	resp, _ = c.write("DELETE", "/api/v1/apps/shop/git", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete -> %d", resp.StatusCode)
	}
}

func TestWebhookEndpoints(t *testing.T) {
	c, h := newServer(t)
	c.login()
	_, body := c.write("POST", "/api/v1/apps", `{"slug":"shop","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"]}},"domains":["shop.example.com"]}`)
	var acc dto.Accepted
	json.Unmarshal([]byte(body), &acc)
	h.Wait(acc.Operation.ID)

	anon := &client{t: t, srv: c.srv}
	if resp, _ := anon.do("GET", "/api/v1/apps/shop/webhook", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous get -> %d", resp.StatusCode)
	}
	if resp, _ := c.do("POST", "/api/v1/apps/shop/webhook", `{}`, map[string]string{"Origin": origin}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("enable without csrf -> %d", resp.StatusCode)
	}
	if resp, _ := c.write("POST", "/api/v1/apps/shop/webhook", `{}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("enable without git source -> %d", resp.StatusCode)
	}
	c.write("PUT", "/api/v1/apps/shop/git", `{"repoUrl":"git@github.com:o/r.git","branch":"main"}`)
	if resp, out := c.write("POST", "/api/v1/apps/shop/webhook", `{"extra":1}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field -> %d %s", resp.StatusCode, out)
	}
	resp, out := c.write("POST", "/api/v1/apps/shop/webhook", `{}`)
	var hook dto.WebhookSecret
	json.Unmarshal([]byte(out), &hook)
	if resp.StatusCode != 200 || !hook.Enabled || len(hook.Secret) != 64 || !strings.HasPrefix(hook.Path, "/_bento/webhook/deploy/") ||
		resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("enable -> %d %s", resp.StatusCode, out)
	}
	for _, p := range []string{"/api/v1/apps/shop/webhook", "/api/v1/apps/shop", "/api/v1/apps/shop/git", "/api/v1/operations"} {
		if _, out := c.do("GET", p, "", nil); strings.Contains(out, hook.Secret) {
			t.Fatalf("%s leaked the webhook secret", p)
		}
	}

	for _, bad := range []string{`{"baseUrl":"ftp://x"}`, `{"baseUrl":"https://h.example.com/path"}`, `{"baseUrl":"https://u:p@h.example.com"}`} {
		if resp, out := c.write("PUT", "/api/v1/utils", bad); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s -> %d %s", bad, resp.StatusCode, out)
		}
	}
	if resp, out := c.write("PUT", "/api/v1/utils", `{"baseUrl":"https://hooks.example.com/"}`); resp.StatusCode != 200 ||
		!strings.Contains(out, `"baseUrl":"https://hooks.example.com"`) {
		t.Fatalf("set utils base -> %d %s", resp.StatusCode, out)
	}
	_, out = c.do("GET", "/api/v1/apps/shop/webhook", "", nil)
	if !strings.Contains(out, `"url":"https://hooks.example.com`+hook.Path+`"`) {
		t.Fatalf("webhook URL must use the utils base URL: %s", out)
	}

	hooks := httptest.NewServer(c.api.UtilsHandler())
	t.Cleanup(hooks.Close)
	post := func(path, body string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest("POST", hooks.URL+path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	bearer := map[string]string{"Authorization": "Bearer " + hook.Secret}
	// The utils listener serves nothing but self-authenticating routes.
	for _, p := range []string{"/api/v1/apps", "/api/v1/session", "/", "/scheduler/apps/shop/"} {
		if code, _ := post(p, `{}`, bearer); code != http.StatusNotFound {
			t.Errorf("utils listener served %s -> %d", p, code)
		}
	}
	if code, _ := post("/_bento/webhook/deploy/NOT-A-HOOK", `{}`, bearer); code != http.StatusNotFound {
		t.Fatalf("malformed hook id -> %d", code)
	}
	if code, _ := post(hook.Path+"?secret="+hook.Secret, `{}`, nil); code != http.StatusNotFound {
		t.Fatalf("a secret in the query string must not authenticate -> %d", code)
	}
	if code, _ := post(hook.Path, strings.Repeat("x", MaxWebhookBodyBytes+1), bearer); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body -> %d", code)
	}
	code, out := post(hook.Path, ``, bearer)
	if code != http.StatusAccepted || !strings.Contains(out, `"result":"deployed"`) || !strings.Contains(out, `"operationId":"op_`) {
		t.Fatalf("generic trigger -> %d %s", code, out)
	}
	_, out = c.do("GET", "/api/v1/apps/shop/webhook", "", nil)
	var got dto.Webhook
	json.Unmarshal([]byte(out), &got)
	if len(got.Deliveries) != 1 || got.Deliveries[0].Provider != "generic" || got.Deliveries[0].Result != "deployed" {
		t.Fatalf("delivery history: %s", out)
	}
	if resp, _ := c.write("DELETE", "/api/v1/apps/shop/webhook", ""); resp.StatusCode != 200 {
		t.Fatalf("disable -> %d", resp.StatusCode)
	}
	if code, _ := post(hook.Path, ``, bearer); code != http.StatusNotFound {
		t.Fatalf("a disabled hook must stop working -> %d", code)
	}
}

func TestDBAdminTicketGateway(t *testing.T) {
	c, h := newServer(t)
	c.login()
	ctx := context.Background()
	_, body := c.write("POST", "/api/v1/apps", `{"slug":"shop","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"]}},"domains":["shop.example.com"]}`)
	var acc dto.Accepted
	json.Unmarshal([]byte(body), &acc)
	h.Wait(acc.Operation.ID)
	app, _ := store.GetApp(ctx, h.Store.DB(), "shop")
	db := h.Store.DB()
	for _, svc := range []domain.DataService{{Name: "mysql84", Engine: domain.EngineMySQL, Version: "8.4"}, {Name: "pg17", Engine: domain.EnginePostgres, Version: "17"}} {
		if err := store.InsertService(ctx, db, svc); err != nil && !errors.Is(err, store.ErrConflict) {
			t.Fatal(err)
		}
	}
	for _, b := range []domain.Binding{
		{ID: "bmysql", AppID: app.ID, Engine: domain.EngineMySQL, Service: "mysql84", Username: "ushop", Password: "Sup3rSecretPw"},
		{ID: "bpg", AppID: app.ID, Engine: domain.EnginePostgres, Service: "pg17", Username: "ushop", Password: "OtherSecretPw"},
	} {
		if err := store.InsertBinding(ctx, db, b); err != nil {
			t.Fatal(err)
		}
		if err := store.AddBindingDatabase(ctx, db, b.ID, "shop"); err != nil {
			t.Fatal(err)
		}
	}

	ticketPath := func(binding string) (int, string) {
		resp, out := c.write("POST", "/api/v1/apps/shop/bindings/"+binding+"/dbadmin", `{}`)
		var tk dto.DBAdminTicket
		json.Unmarshal([]byte(out), &tk)
		return resp.StatusCode, tk.Path
	}
	if code, _ := ticketPath("bmysql"); code != http.StatusPreconditionFailed {
		t.Fatalf("ticket while disabled -> %d", code)
	}
	if err := store.PutSetting(ctx, db, "dbadmin", domain.DBAdminSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if resp, _ := c.do("POST", "/api/v1/apps/shop/bindings/bmysql/dbadmin", `{}`, map[string]string{"Origin": origin}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ticket without csrf -> %d", resp.StatusCode)
	}
	if code, _ := ticketPath("nosuch"); code != http.StatusNotFound {
		t.Fatalf("unknown binding -> %d", code)
	}

	// Stand-in Adminer: records what the gateway forwards.
	var seen http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		http.SetCookie(w, &http.Cookie{Name: "adminer_sid", Value: "s1", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "bento_session", Value: "evil", Path: "/"})
		w.Write([]byte("adminer"))
	}))
	t.Cleanup(upstream.Close)
	if err := os.MkdirAll(h.Layout.DBAdminDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(h.Layout.DBAdminDir()+"/gateway-token", []byte(strings.Repeat("t", 43)), 0o600)
	c.api.dbadmin.endpoint, c.api.dbadmin.checked = strings.TrimPrefix(upstream.URL, "http://"), time.Now().Add(time.Hour)

	utils := httptest.NewServer(c.api.UtilsHandler())
	t.Cleanup(utils.Close)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, hdr map[string]string, cookies ...*http.Cookie) *http.Response {
		req, _ := http.NewRequest("GET", utils.URL+path, nil)
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	_, path := ticketPath("bmysql")
	resp := get(path, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/_bento/dbadmin/b/bmysql/" {
		t.Fatalf("redeem -> %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	var grant *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == dbadminCookie {
			grant = ck
		}
	}
	if grant == nil || !grant.HttpOnly || grant.Path != "/_bento/dbadmin/b/bmysql/" {
		t.Fatalf("grant cookie %+v", grant)
	}
	if resp := get(path, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ticket reuse -> %d", resp.StatusCode)
	}
	if resp := get("/_bento/dbadmin/b/bpg/", nil, grant); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("grant used for another binding -> %d", resp.StatusCode)
	}
	if resp := get("/_bento/dbadmin/b/bmysql/", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no grant -> %d", resp.StatusCode)
	}

	resp = get("/_bento/dbadmin/b/bmysql/?db=shop", map[string]string{"X-Bento-Password": "spoof", "X-Bento-Server": "evil"},
		grant, &http.Cookie{Name: "adminer_sid", Value: "s1"}, c.cookie)
	if resp.StatusCode != 200 {
		t.Fatalf("proxied -> %d", resp.StatusCode)
	}
	if seen.Get("X-Bento-Server") != "mysql84" || seen.Get("X-Bento-Driver") != "server" || seen.Get("X-Bento-Username") != "ushop" ||
		seen.Get("X-Bento-Password") != "U3VwM3JTZWNyZXRQdw==" || seen.Get("X-Bento-Databases") != "shop" || seen.Get("X-Bento-Gateway-Token") == "" {
		t.Fatalf("injected headers %v", seen)
	}
	if ck := seen.Get("Cookie"); ck != "adminer_sid=s1" {
		t.Fatalf("forwarded cookies %q", ck)
	}
	sc := resp.Header.Values("Set-Cookie")
	if len(sc) != 1 || !strings.Contains(sc[0], "adminer_sid=s1") || !strings.Contains(sc[0], "Path=/_bento/dbadmin/b/bmysql/") {
		t.Fatalf("response cookies %v", sc)
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("response headers %v", resp.Header)
	}

	post := func(hdr map[string]string) int {
		req, _ := http.NewRequest("POST", utils.URL+"/_bento/dbadmin/b/bmysql/?sql=", strings.NewReader("query=1"))
		req.AddCookie(grant)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for name, hdr := range map[string]map[string]string{
		"no origin":      {},
		"cross-site":     {"Sec-Fetch-Site": "cross-site"},
		"foreign origin": {"Origin": "http://evil.test"},
	} {
		if code := post(hdr); code != http.StatusForbidden {
			t.Errorf("%s write -> %d", name, code)
		}
	}
	if code := post(map[string]string{"Origin": utils.URL}); code != 200 {
		t.Fatalf("same-origin write -> %d", code)
	}

	// Logout ends every grant issued to the session.
	c.do("DELETE", "/api/v1/session", "", nil)
	if resp := get("/_bento/dbadmin/b/bmysql/", nil, grant); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("grant survived logout -> %d", resp.StatusCode)
	}
}
