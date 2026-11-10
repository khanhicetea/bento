package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/remoteaccess"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestMain(m *testing.M) {
	remoteaccess.MaybeRunTestChild()
	os.Exit(m.Run())
}

func (c *client) remoteOf(name string) dto.RemoteAccess {
	c.t.Helper()
	_, out := c.do("GET", "/api/v1/services", "", nil)
	var list dto.ServiceList
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		c.t.Fatalf("list %s: %v", out, err)
	}
	for _, s := range list.Services {
		if s.Name == name {
			return s.RemoteAccess
		}
	}
	c.t.Fatalf("no service %s in %s", name, out)
	return dto.RemoteAccess{}
}

func TestRemoteAccessEndpoints(t *testing.T) {
	c, h := newServer(t)
	c.login()
	if resp, out := c.write("POST", "/api/v1/services", `{"engine":"postgres","version":"17"}`); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create -> %d %s", resp.StatusCode, out)
	}
	h.WaitIdle()
	path := "/api/v1/services/postgres17/remote"
	if resp, _ := c.do("PUT", path, `{"action":"on"}`, map[string]string{"Origin": origin}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("remote without csrf -> %d", resp.StatusCode)
	}
	for body, want := range map[string]int{
		`{"action":"open"}`:              http.StatusUnprocessableEntity,
		`{"action":"on","extra":1}`:      http.StatusBadRequest,
		`{"action":"rotate"}`:            http.StatusConflict, // nothing to rotate while off
		`{"action":["on"]}`:              http.StatusBadRequest,
		`{"action":"on"}{"action":"on"}`: http.StatusBadRequest,
	} {
		if resp, out := c.write("PUT", path, body); resp.StatusCode != want {
			t.Errorf("%s -> %d %s, want %d", body, resp.StatusCode, out, want)
		}
	}
	if resp, _ := c.write("PUT", "/api/v1/services/postgres99/remote", `{"action":"on"}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown service -> %d", resp.StatusCode)
	}
	if ra := c.remoteOf("postgres17"); ra.Enabled || ra.State != dto.RemoteAccessStateOff || ra.LocalPort != 15432 ||
		ra.EnginePort != 5432 || ra.MaxConnections != 32 {
		t.Fatalf("off: %+v", ra)
	}
	resp, out := c.write("PUT", path, `{"action":"on"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("on -> %d %s", resp.StatusCode, out)
	}
	var acc dto.Accepted
	json.Unmarshal([]byte(out), &acc)
	if op := h.Wait(acc.Operation.ID); op.State != store.OpSucceeded {
		t.Fatalf("on: %s %s", op.ErrorCode, op.ErrorMessage)
	}
	ra := c.remoteOf("postgres17")
	if !ra.Enabled || ra.State != dto.RemoteAccessStateListening || !strings.HasPrefix(ra.Address, "tc") ||
		ra.Command != "tailcat forward "+ra.Address+" 15432:5432" {
		t.Fatalf("on: %+v", ra)
	}
	// The address is a credential: tokens never see it, and no operation
	// record carries it.
	admin := c.createToken("admin", dto.TokenScopeAdmin)
	if resp, body := c.bearer(admin, "GET", "/api/v1/services", ""); resp.StatusCode != 200 ||
		strings.Contains(body, ra.Address) || !strings.Contains(body, `"listening"`) {
		t.Fatalf("token view -> %d %s", resp.StatusCode, body)
	}
	if resp, _ := c.bearer(admin, "PUT", path, `{"action":"off"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin token toggled remote access -> %d", resp.StatusCode)
	}
	if _, body := c.do("GET", "/api/v1/operations", "", nil); strings.Contains(body, ra.Address) {
		t.Fatal("the operations list carries the address")
	}
	resp, out = c.write("PUT", path, `{"action":"rotate"}`)
	json.Unmarshal([]byte(out), &acc)
	if resp.StatusCode != http.StatusAccepted || h.Wait(acc.Operation.ID).State != store.OpSucceeded {
		t.Fatalf("rotate -> %d %s", resp.StatusCode, out)
	}
	if rotated := c.remoteOf("postgres17"); rotated.Address == ra.Address || rotated.State != dto.RemoteAccessStateListening {
		t.Fatalf("rotate kept the address or failed: %+v", rotated)
	}
	resp, out = c.write("PUT", path, `{"action":"off"}`)
	json.Unmarshal([]byte(out), &acc)
	if resp.StatusCode != http.StatusAccepted || h.Wait(acc.Operation.ID).State != store.OpSucceeded {
		t.Fatalf("off -> %d %s", resp.StatusCode, out)
	}
	if off := c.remoteOf("postgres17"); off.Enabled || off.Address != "" || off.State != dto.RemoteAccessStateOff {
		t.Fatalf("off: %+v", off)
	}
}

func TestBindingLoginEndpoint(t *testing.T) {
	c, h := newServer(t)
	c.login()
	createShop(t, c, h.Wait)
	ctx, db := t.Context(), h.Store.DB()
	app, _ := store.GetApp(ctx, db, "shop")
	const secret = "Sup3rSecretBindingPw"
	for _, svc := range []domain.DataService{{Name: "mysql84", Engine: domain.EngineMySQL, Version: "8.4"},
		{Name: "pg17", Engine: domain.EnginePostgres, Version: "17"}, {Name: "pg16", Engine: domain.EnginePostgres, Version: "16"}} {
		if err := store.InsertService(ctx, db, svc); err != nil && !errors.Is(err, store.ErrConflict) {
			t.Fatal(err)
		}
	}
	for _, b := range []domain.Binding{
		{ID: "bmysql", AppID: app.ID, Engine: domain.EngineMySQL, Service: "mysql84", Username: "ushop", Password: secret},
		{ID: "bpg", AppID: app.ID, Engine: domain.EnginePostgres, Service: "pg17", Username: "ushop", Password: secret},
		{ID: "bnew", AppID: app.ID, Engine: domain.EnginePostgres, Service: "pg16", Username: "", Password: ""},
	} {
		if err := store.InsertBinding(ctx, db, b); err != nil {
			t.Fatal(err)
		}
		if b.Username != "" {
			store.AddBindingDatabase(ctx, db, b.ID, "shop_main")
		}
	}
	if err := store.InsertBinding(ctx, db, domain.Binding{ID: "bsqlite", AppID: app.ID, Engine: domain.EngineSQLite,
		SQLiteFileID: "f1"}); err != nil {
		t.Fatal(err)
	}
	login := "/api/v1/apps/shop/bindings/bpg/login"
	if resp, _ := c.do("POST", login, `{}`, map[string]string{"Origin": origin}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("login without csrf -> %d", resp.StatusCode)
	}
	if resp, body := c.do("GET", login, "", nil); resp.StatusCode != http.StatusNotFound || strings.Contains(body, secret) {
		t.Fatalf("GET login -> %d", resp.StatusCode)
	}
	admin := c.createToken("admin", dto.TokenScopeAdmin)
	if resp, body := c.bearer(admin, "POST", login, `{}`); resp.StatusCode != http.StatusForbidden || strings.Contains(body, secret) {
		t.Fatalf("token login -> %d", resp.StatusCode)
	}
	for p, want := range map[string]int{
		"/api/v1/apps/shop/bindings/bsqlite/login": http.StatusBadRequest,
		"/api/v1/apps/shop/bindings/nosuch/login":  http.StatusNotFound,
		"/api/v1/apps/nosuch/bindings/bpg/login":   http.StatusNotFound,
		"/api/v1/apps/shop/bindings/bnew/login":    http.StatusConflict,
	} {
		if resp, body := c.write("POST", p, `{}`); resp.StatusCode != want || strings.Contains(body, secret) {
			t.Errorf("%s -> %d %s, want %d", p, resp.StatusCode, body, want)
		}
	}
	resp, body := c.write("POST", login, `{}`)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("login -> %d %q %s", resp.StatusCode, resp.Header.Get("Cache-Control"), body)
	}
	var got dto.BindingLogin
	json.Unmarshal([]byte(body), &got)
	if got.Password != secret || got.Username != "ushop" || got.Host != "127.0.0.1" || got.Port != 15432 ||
		len(got.URLs) != 1 || got.URLs[0].URL != "postgresql://ushop:"+secret+"@127.0.0.1:15432/shop_main" {
		t.Fatalf("login %+v", got)
	}
	resp, body = c.write("POST", "/api/v1/apps/shop/bindings/bmysql/login", `{}`)
	json.Unmarshal([]byte(body), &got)
	if resp.StatusCode != 200 || got.Port != 13306 || got.URLs[0].URL != "mysql://ushop:"+secret+"@127.0.0.1:13306/shop_main" {
		t.Fatalf("mysql login %d %+v", resp.StatusCode, got)
	}
	// Root on the control socket may reveal; a scoped socket (bento mcp) may not.
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
	local := func(scope string) (int, string) {
		req, _ := http.NewRequest("POST", "http://bento"+login, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		if scope != "" {
			req.Header.Set(ScopeHeader, scope)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, out := local("admin"); code != http.StatusForbidden || strings.Contains(out, secret) {
		t.Fatalf("scoped socket login -> %d", code)
	}
	if code, out := local(""); code != http.StatusOK || !strings.Contains(out, secret) {
		t.Fatalf("root socket login -> %d %s", code, out)
	}

	// Each reveal is journaled; no other response carries the password.
	_, ops := c.do("GET", "/api/v1/operations?target="+app.ID, "", nil)
	if strings.Count(ops, `"binding.login"`) != 3 {
		t.Fatalf("reveals not journaled: %s", ops)
	}
	for _, p := range []string{"/api/v1/apps", "/api/v1/apps/shop", "/api/v1/operations", "/api/v1/services"} {
		if _, out := c.do("GET", p, "", nil); strings.Contains(out, secret) {
			t.Fatalf("%s leaked the password", p)
		}
	}
}
