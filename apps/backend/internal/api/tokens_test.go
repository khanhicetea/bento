package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func (c *client) createToken(name string, sc dto.TokenScope) string {
	c.t.Helper()
	resp, body := c.write("POST", "/api/v1/tokens", `{"name":"`+name+`","scope":"`+string(sc)+`","currentPassword":"`+password+`"}`)
	if resp.StatusCode != http.StatusCreated {
		c.t.Fatalf("create token %s -> %d %s", name, resp.StatusCode, body)
	}
	var out dto.CreatedAPIToken
	if err := json.Unmarshal([]byte(body), &out); err != nil || !strings.HasPrefix(out.Token, TokenPrefix) {
		c.t.Fatalf("created token %s %v", body, err)
	}
	return out.Token
}

// bearer sends a request with only a bearer token: no cookie, Origin or CSRF.
func (c *client) bearer(tok, method, path, body string) (*http.Response, string) {
	c.t.Helper()
	cookie := c.cookie
	c.cookie = nil
	defer func() { c.cookie = cookie }()
	return c.do(method, path, body, map[string]string{"Authorization": "Bearer " + tok})
}

func createShop(t *testing.T, c *client, waitFn func(string) store.Operation) {
	t.Helper()
	_, body := c.write("POST", "/api/v1/apps", `{"slug":"shop","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"]}},"hosts":[{"name":"shop.example.com"}]}`)
	var acc dto.Accepted
	json.Unmarshal([]byte(body), &acc)
	waitFn(acc.Operation.ID)
}

func TestRouteScopesNameRegisteredRoutes(t *testing.T) {
	c, _ := newServer(t)
	_, patterns := c.api.routes()
	for p := range routeScopes {
		if !slices.Contains(patterns, p) {
			t.Errorf("routeScopes lists %q, which is not a route", p)
		}
	}
	// Destructive and credential routes stay out of every token scope.
	for _, p := range []string{
		"DELETE /api/v1/apps/{id}", "POST /api/v1/apps/{id}/exec", "GET /api/v1/apps/{id}/terminal",
		"POST /api/v1/apps/{id}/scheduler/command",
		"POST /api/v1/apps/{id}/restic/restore-db", "POST /api/v1/apps/{id}/restic/init",
		"POST /api/v1/stack/export", "PUT /api/v1/auth/password", "PUT /api/v1/tunnel/token",
		"POST /api/v1/retired/{id}/prune", "DELETE /api/v1/hosts/{name}", "POST /api/v1/apps/{id}/webhook",
		"POST /api/v1/apps/{id}/bindings/{bid}/dbadmin", "PUT /api/v1/apps/{id}/restic",
		"POST /api/v1/apps/{id}/bindings/{bid}/login", "PUT /api/v1/services/{name}/remote",
		"GET /api/v1/tokens", "POST /api/v1/tokens", "DELETE /api/v1/tokens/{id}",
	} {
		if !slices.Contains(patterns, p) {
			t.Errorf("expected route %q", p)
		}
		if routeScope(p) != scopeFull {
			t.Errorf("%s must need a full session, has %s", p, routeScope(p))
		}
	}
}

func TestTokenScopesAreEnforced(t *testing.T) {
	c, h := newServer(t)
	c.login()
	createShop(t, c, h.Wait)
	read := c.createToken("reader", dto.TokenScopeRead)
	operate := c.createToken("ops", dto.TokenScopeOperate)
	admin := c.createToken("admin", dto.TokenScopeAdmin)

	if resp, body := c.bearer(read, "GET", "/api/v1/apps", ""); resp.StatusCode != 200 || !strings.Contains(body, `"shop"`) {
		t.Fatalf("read list -> %d %s", resp.StatusCode, body)
	}
	if resp, _ := c.bearer(read, "POST", "/api/v1/apps/shop/restart", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read restart -> %d", resp.StatusCode)
	}
	// Token writes need no Origin or CSRF.
	resp, body := c.bearer(operate, "POST", "/api/v1/apps/shop/start", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("operate start -> %d %s", resp.StatusCode, body)
	}
	var acc dto.Accepted
	json.Unmarshal([]byte(body), &acc)
	if acc.Operation.Origin != "token:ops" {
		t.Fatalf("operation origin %q", acc.Operation.Origin)
	}
	h.Wait(acc.Operation.ID)
	if resp, _ := c.bearer(operate, "POST", "/api/v1/apps/shop/publish", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operate publish -> %d", resp.StatusCode)
	}
	for _, rq := range [][3]string{
		{"DELETE", "/api/v1/apps/shop", `{"confirm":"delete shop"}`},
		{"POST", "/api/v1/apps/shop/exec", `{"argv":["id"]}`},
		{"GET", "/api/v1/tokens", ""},
		{"POST", "/api/v1/tokens", `{"name":"more","scope":"admin"}`},
		{"POST", "/api/v1/stack/export", `{}`},
	} {
		if resp, _ := c.bearer(admin, rq[0], rq[1], rq[2]); resp.StatusCode != http.StatusForbidden {
			t.Errorf("admin %s %s -> %d", rq[0], rq[1], resp.StatusCode)
		}
	}
	// Restricted callers never see or change env values.
	if resp, body := c.write("PATCH", "/api/v1/apps/shop", `{"env":[{"key":"API_KEY","value":"s3cret-value"}]}`); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("session env update -> %d %s", resp.StatusCode, body)
	}
	h.WaitIdle()
	if _, body := c.bearer(read, "GET", "/api/v1/apps/shop", ""); strings.Contains(body, "s3cret-value") || !strings.Contains(body, "API_KEY") {
		t.Fatalf("env not redacted for token: %s", body)
	}
	if _, body := c.do("GET", "/api/v1/apps/shop", "", nil); !strings.Contains(body, "s3cret-value") {
		t.Fatalf("session must still see env: %s", body)
	}
	if resp, _ := c.bearer(admin, "PATCH", "/api/v1/apps/shop", `{"env":[]}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("token env update -> %d", resp.StatusCode)
	}
	if resp, _ := c.bearer(operate, "POST", "/api/v1/apps/shop/restic/restore-db", `{"snapshot":"latest","database":"x","confirm":"replace x"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("token database restore -> %d", resp.StatusCode)
	}
}

func TestTokenLifecycle(t *testing.T) {
	c, h := newServer(t)
	c.login()
	tok := c.createToken("ci", dto.TokenScopeRead)
	for _, bad := range []string{
		`{"name":"ci","scope":"read"}`,
		`{"name":"Bad Name","scope":"read"}`,
		`{"name":"x","scope":"root"}`,
		`{"name":"y","scope":"read","expiresInDays":400}`,
	} {
		if resp, _ := c.write("POST", "/api/v1/tokens", bad); resp.StatusCode < 400 {
			t.Errorf("%s -> %d", bad, resp.StatusCode)
		}
	}
	// Session writes keep their CSRF requirement.
	if resp, _ := c.do("POST", "/api/v1/tokens", `{"name":"z","scope":"read"}`, map[string]string{"Origin": origin}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("token create without csrf -> %d", resp.StatusCode)
	}
	_, body := c.do("GET", "/api/v1/tokens", "", nil)
	if strings.Contains(body, tok) || strings.Contains(body, "Hash") || !strings.Contains(body, `"ci"`) {
		t.Fatalf("token list leaks or misses: %s", body)
	}
	for _, bad := range []string{"Bearer nope", "Bearer " + TokenPrefix + "nope", "Basic " + tok, tok} {
		resp, _ := c.do("GET", "/api/v1/apps", "", map[string]string{"Authorization": bad})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth %q -> %d", bad, resp.StatusCode)
		}
	}
	if resp, _ := c.bearer(tok, "GET", "/api/v1/apps", ""); resp.StatusCode != 200 {
		t.Fatalf("live token -> %d", resp.StatusCode)
	}
	if _, body := c.do("GET", "/api/v1/tokens", "", nil); !strings.Contains(body, "lastUsedAt") {
		t.Fatalf("last use not recorded: %s", body)
	}
	if resp, _ := c.write("DELETE", "/api/v1/tokens/ci", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke -> %d", resp.StatusCode)
	}
	if resp, _ := c.bearer(tok, "GET", "/api/v1/apps", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token -> %d", resp.StatusCode)
	}
	// Expired tokens are refused.
	secret := TokenPrefix + "expired-token-secret"
	if err := store.InsertAPIToken(t.Context(), h.Store.DB(), store.APIToken{ID: "tok_old", Name: "old",
		Scope: "read", TokenHash: tokenHash(secret), CreatedAt: time.Now().Add(-48 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if resp, _ := c.bearer(secret, "GET", "/api/v1/apps", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token -> %d", resp.StatusCode)
	}
}

func mcpCall(t *testing.T, c *client, tok, body string, hdr map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	h := map[string]string{"Authorization": "Bearer " + tok, "Accept": "application/json, text/event-stream"}
	for k, v := range hdr {
		h[k] = v
	}
	cookie := c.cookie
	c.cookie = nil
	resp, out := c.do("POST", "/mcp", body, h)
	c.cookie = cookie
	var m map[string]any
	_ = json.Unmarshal([]byte(out), &m)
	return resp, m
}

func toolText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	content := res["content"].([]any)
	return content[0].(map[string]any)["text"].(string), res["isError"].(bool)
}

func TestMCPOverHTTP(t *testing.T) {
	c, h := newServer(t)
	c.login()
	createShop(t, c, h.Wait)
	read := c.createToken("reader", dto.TokenScopeRead)
	operate := c.createToken("ops", dto.TokenScopeOperate)

	resp, m := mcpCall(t, c, read, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, nil)
	if resp.StatusCode != 200 || m["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize -> %d %v", resp.StatusCode, m)
	}
	if resp, _ := mcpCall(t, c, read, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("notification -> %d", resp.StatusCode)
	}
	_, m = mcpCall(t, c, read, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, nil)
	var names []string
	for _, tl := range m["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if !slices.Contains(names, "get_app_logs") || slices.Contains(names, "start_app") {
		t.Fatalf("read tools %v", names)
	}
	_, m = mcpCall(t, c, read, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_apps","arguments":{}}}`, nil)
	if text, isErr := toolText(t, m); isErr || !strings.Contains(text, `"shop"`) {
		t.Fatalf("list_apps %v %s", isErr, text)
	}
	_, m = mcpCall(t, c, read, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"start_app","arguments":{"app":"shop"}}}`, nil)
	if m["error"] == nil {
		t.Fatalf("read token called start_app: %v", m)
	}
	_, m = mcpCall(t, c, operate, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"start_app","arguments":{"app":"shop"}}}`, nil)
	text, isErr := toolText(t, m)
	if isErr || !strings.Contains(text, `"origin": "token:ops"`) {
		t.Fatalf("start_app %v %s", isErr, text)
	}
	var out struct{ Operation dto.Operation }
	json.Unmarshal([]byte(text), &out)
	_, m = mcpCall(t, c, operate, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"wait_operation","arguments":{"id":"`+out.Operation.ID+`","timeout_seconds":10}}}`, nil)
	if text, isErr := toolText(t, m); isErr || !strings.Contains(text, `"done": true`) {
		t.Fatalf("wait_operation %v %s", isErr, text)
	}
	// Tool errors are in-band; an unknown app is not a protocol error.
	_, m = mcpCall(t, c, read, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_app","arguments":{"app":"nope"}}}`, nil)
	if text, isErr := toolText(t, m); !isErr || !strings.Contains(text, "list_apps") {
		t.Fatalf("unknown app %v %s", isErr, text)
	}

	if resp, _ := mcpCall(t, c, read, `{}`, map[string]string{"Origin": "http://evil.test"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin -> %d", resp.StatusCode)
	}
	if resp, _ := mcpCall(t, c, read, `{}`, map[string]string{"MCP-Protocol-Version": "1999-01-01"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad protocol version -> %d", resp.StatusCode)
	}
	if resp, _ := c.do("GET", "/mcp", "", map[string]string{"Authorization": "Bearer " + read}); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp -> %d", resp.StatusCode)
	}
	cookie := c.cookie
	c.cookie = nil
	if resp, _ := c.do("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no credentials -> %d", resp.StatusCode)
	}
	c.cookie = cookie
	// A browser session is not an MCP credential.
	if resp, _ := c.write("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session -> %d", resp.StatusCode)
	}
}

func TestControlSocketScopeHeader(t *testing.T) {
	c, h := newServer(t)
	c.login()
	createShop(t, c, h.Wait)
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: c.api.LocalOnly(c.api.Handler()), ConnContext: ConnContext}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	do := func(method, path, sc string) int {
		req, _ := http.NewRequest(method, "http://bento"+path, nil)
		if sc != "" {
			req.Header.Set(ScopeHeader, sc)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := do("GET", "/api/v1/tokens", ""); got != 200 {
		t.Fatalf("unscoped socket -> %d", got)
	}
	if got := do("GET", "/api/v1/tokens", "admin"); got != http.StatusForbidden {
		t.Fatalf("scoped socket, full route -> %d", got)
	}
	if got := do("POST", "/api/v1/apps/shop/restart", "read"); got != http.StatusForbidden {
		t.Fatalf("read socket restart -> %d", got)
	}
	if got := do("GET", "/api/v1/apps", "root"); got != http.StatusBadRequest {
		t.Fatalf("bad scope -> %d", got)
	}
	if got := do("POST", "/api/v1/apps/shop/start", "operate"); got != http.StatusAccepted {
		t.Fatalf("operate socket start -> %d", got)
	}
	h.WaitIdle()
}

// A browser session must prove the operator password before it creates an API
// token or changes the password, so a script running on the management origin
// cannot mint a lasting credential or lock the operator out. Root on the
// control socket recovers without it.
func TestCredentialChangesNeedCurrentPassword(t *testing.T) {
	c, _ := newServer(t)
	c.login()
	refused := func(what string, resp *http.Response, body string) {
		t.Helper()
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, `"field":"currentPassword"`) {
			t.Fatalf("%s -> %d %s", what, resp.StatusCode, body)
		}
	}
	for name, extra := range map[string]string{"missing": ``, "wrong": `,"currentPassword":"not the password"`} {
		resp, body := c.write("POST", "/api/v1/tokens", `{"name":"ci","scope":"read"`+extra+`}`)
		refused(name+" token", resp, body)
		resp, body = c.write("PUT", "/api/v1/auth/password", `{"password":"another long password"`+extra+`}`)
		refused(name+" password", resp, body)
	}
	if _, body := c.do("GET", "/api/v1/tokens", "", nil); strings.Contains(body, `"ci"`) {
		t.Fatalf("refused token was stored: %s", body)
	}
	c.createToken("ci", dto.TokenScopeRead)
	resp, body := c.write("PUT", "/api/v1/auth/password", `{"password":"another long password","currentPassword":"`+password+`"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("password change -> %d %s", resp.StatusCode, body)
	}
	if resp, _ := c.do("GET", "/api/v1/tokens", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session survived the password change -> %d", resp.StatusCode)
	}

	// Root on the control socket needs no current password.
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: c.api.LocalOnly(c.api.Handler()), ConnContext: ConnContext}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	for path, body := range map[string]string{
		"/api/v1/tokens":        `{"name":"local","scope":"read"}`,
		"/api/v1/auth/password": `{"password":"` + password + `"}`,
	} {
		method := "POST"
		if strings.HasSuffix(path, "password") {
			method = "PUT"
		}
		req, _ := http.NewRequest(method, "http://bento"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("socket %s %s -> %d", method, path, resp.StatusCode)
		}
	}
	c.login()
}
