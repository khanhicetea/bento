package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestHardenSchedulerResponse(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Set-Cookie":              {"x=1"},
			"X-Frame-Options":         {"ALLOWALL"},
			"Content-Security-Policy": {"frame-ancestors *"},
			"X-Content-Type-Options":  {"sniff"},
		},
		Request: &http.Request{URL: &url.URL{Path: "/_bento/scheduler/a/shop/api/v1/jobs"}},
	}
	if err := hardenSchedulerResponse(resp); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Set-Cookie":                   "",
		"X-Frame-Options":              "DENY",
		"Content-Security-Policy":      "frame-ancestors 'none'",
		"X-Content-Type-Options":       "nosniff",
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

func ticketPathFor(c *client, slug string) (int, string) {
	resp, out := c.write("POST", "/api/v1/apps/"+slug+"/scheduler/ticket", `{}`)
	var tk dto.SchedulerTicket
	json.Unmarshal([]byte(out), &tk)
	return resp.StatusCode, tk.Path
}

// The scheduler UI is app-controlled, so it must never be served on the
// management origin; on the utils listener it needs a ticket-issued grant.
func TestSchedulerGatewayTicketsAndGrants(t *testing.T) {
	c, h := newServer(t)
	c.login()
	ctx := context.Background()
	for _, slug := range []string{"shop", "blog"} {
		_, body := c.write("POST", "/api/v1/apps", `{"slug":"`+slug+`","runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"]}},"domains":["`+slug+`.example.com"]}`)
		var acc dto.Accepted
		json.Unmarshal([]byte(body), &acc)
		h.Wait(acc.Operation.ID)
	}
	// A stopped app gets no ticket.
	if code, _ := ticketPathFor(c, "shop"); code != http.StatusPreconditionFailed {
		t.Fatalf("stopped app ticket -> %d", code)
	}
	if _, err := h.Store.DB().ExecContext(ctx, `UPDATE apps SET desired_runtime='running'`); err != nil {
		t.Fatal(err)
	}
	if app, err := store.GetApp(ctx, h.Store.DB(), "shop"); err != nil || app.DesiredRuntime != domain.DesiredRunning {
		t.Fatalf("shop: %v %v", err, app.DesiredRuntime)
	}

	// The management origin no longer proxies scheduler content.
	if resp, body := c.do("GET", "/scheduler/apps/shop/", "", nil); strings.Contains(body, "minicrond") || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("management origin scheduler route -> %d", resp.StatusCode)
	}

	ticketPath := func(slug string) (int, string) { return ticketPathFor(c, slug) }
	if resp, _ := c.do("POST", "/api/v1/apps/shop/scheduler/ticket", `{}`, map[string]string{"Origin": origin}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ticket without csrf -> %d", resp.StatusCode)
	}
	if resp, _ := c.do("POST", "/api/v1/apps/shop/scheduler/ticket", `{}`, map[string]string{"Origin": "null", csrfHeader: c.csrf}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ticket from opaque origin -> %d", resp.StatusCode)
	}
	if code, _ := ticketPath("nosuch"); code != http.StatusNotFound {
		t.Fatalf("unknown app -> %d", code)
	}

	utils := httptest.NewServer(c.api.UtilsHandler())
	t.Cleanup(utils.Close)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	send := func(method, path string, hdr map[string]string, cookies ...*http.Cookie) *http.Response {
		req, _ := http.NewRequest(method, utils.URL+path, nil)
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

	// No grant, the management session cookie alone, and bad tickets.
	if resp := send("GET", "/_bento/scheduler/a/shop/", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no grant -> %d", resp.StatusCode)
	}
	if resp := send("GET", "/_bento/scheduler/a/shop/", nil, c.cookie); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session cookie as grant -> %d", resp.StatusCode)
	}
	if resp := send("GET", "/_bento/scheduler/t/"+strings.Repeat("A", 43), nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged ticket -> %d", resp.StatusCode)
	}

	_, path := ticketPath("shop")
	if resp := send("POST", path, nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST redeem -> %d", resp.StatusCode)
	}
	resp := send("GET", path, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/_bento/scheduler/a/shop/" {
		t.Fatalf("redeem -> %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	var grant *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == schedulerCookie {
			grant = ck
		}
	}
	if grant == nil || !grant.HttpOnly || grant.SameSite != http.SameSiteLaxMode || grant.Path != "/_bento/scheduler/a/shop/" {
		t.Fatalf("grant cookie %+v", grant)
	}
	if resp := send("GET", path, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ticket replay -> %d", resp.StatusCode)
	}
	if resp := send("GET", "/_bento/scheduler/a/blog/", nil, grant); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("grant used for another app -> %d", resp.StatusCode)
	}
	for _, p := range []string{"/_bento/scheduler/a/UPPER/", "/_bento/scheduler/a/shop/../blog/", "/_bento/scheduler/x"} {
		if resp := send("GET", p, nil, grant); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusBadGateway {
			t.Errorf("%s -> %d", p, resp.StatusCode)
		}
	}

	// A valid grant passes every check and reaches the relay (no minicrond
	// runs in tests, so the proxy answers 502).
	if resp := send("GET", "/_bento/scheduler/a/shop/", nil, grant); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("granted read -> %d", resp.StatusCode)
	}
	for name, hdr := range map[string]map[string]string{
		"no origin":      {},
		"cross-site":     {"Sec-Fetch-Site": "cross-site"},
		"foreign origin": {"Origin": "http://evil.test"},
		"opaque origin":  {"Origin": "null"},
	} {
		if resp := send("POST", "/_bento/scheduler/a/shop/api/v1/token/rotate", hdr, grant); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s write -> %d", name, resp.StatusCode)
		}
	}
	if resp := send("POST", "/_bento/scheduler/a/shop/api/v1/token/rotate", map[string]string{"Origin": utils.URL}, grant); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("same-origin write -> %d", resp.StatusCode)
	}

	// Logout ends every grant issued to the session.
	c.do("DELETE", "/api/v1/session", "", nil)
	if resp := send("GET", "/_bento/scheduler/a/shop/", nil, grant); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("grant survived logout -> %d", resp.StatusCode)
	}
}
