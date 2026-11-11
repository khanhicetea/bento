package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/moby/moby/client"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// bin is a running `bento serve` built from this tree, on a disposable root,
// driven only through its public surfaces: REST (browser session, Origin,
// CSRF), the terminal WebSocket and the CLI over the control socket.
type bin struct {
	t       *testing.T
	exe     string
	root    string
	base    string
	origin  string
	cookie  *http.Cookie
	csrf    string
	stackID string
}

const e2ePassword = "an end to end test password"

func startBinary(t *testing.T) *bin {
	t.Helper()
	if os.Getenv("BENTO_DOCKER_TESTS") != "1" || os.Geteuid() != 0 {
		t.Skip("set BENTO_DOCKER_TESTS=1 and run as root for real Docker integration tests")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "bento")
	build := exec.Command("go", "build", "-o", exe, "../../cmd/bento")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	root := filepath.Join(dir, "stack")
	b := &bin{t: t, exe: exe, root: root}
	init := exec.Command(exe, "--stack", root, "init", "--name", "e2e"+platform.RandomHex(3),
		"--mysql", "8.4", "--postgres", "17", "--uid-first", "47000", "--uid-last", "47999", "--password-stdin")
	init.Stdin = strings.NewReader(e2ePassword + "\n")
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	st, err := store.Open(platform.Layout{Root: root}.Database())
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.GetStackIdentity(context.Background(), st.DB())
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	b.stackID = id.ID

	port := freePort(t)
	b.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	b.origin = b.base
	logf, err := os.Create(filepath.Join(dir, "serve.log"))
	if err != nil {
		t.Fatal(err)
	}
	srv := exec.Command(exe, "--stack", root, "serve", "--listen", fmt.Sprintf("127.0.0.1:%d", port), "--utils-listen", "off")
	srv.Stdout, srv.Stderr = logf, logf
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { srv.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(60 * time.Second):
			srv.Process.Kill()
			<-done
		}
		logf.Close()
		if t.Failed() {
			if log, err := os.ReadFile(logf.Name()); err == nil {
				t.Logf("serve log (tail):\n%s", tail(string(log), 4000))
			}
		}
		cleanupStack(b.stackID)
	})
	deadline := time.Now().Add(60 * time.Second)
	for {
		if resp, err := http.Get(b.base + "/api/v1/session"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("bento serve did not answer")
		}
		time.Sleep(300 * time.Millisecond)
	}
	b.login()
	return b
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func (b *bin) req(method, path string, body any, hdr map[string]string) (int, []byte) {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	r, _ := http.NewRequest(method, b.base+path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if b.cookie != nil {
		r.AddCookie(b.cookie)
	}
	if method != http.MethodGet {
		r.Header.Set("Origin", b.origin)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		if b.csrf != "" {
			r.Header.Set("X-CSRF-Token", b.csrf)
		}
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// must decodes a 2xx answer into out.
func (b *bin) must(method, path string, body, out any, hdr map[string]string) {
	b.t.Helper()
	code, raw := b.req(method, path, body, hdr)
	if code/100 != 2 {
		b.t.Fatalf("%s %s -> %d %s", method, path, code, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			b.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
}

func (b *bin) login() {
	b.t.Helper()
	b.cookie, b.csrf = nil, ""
	r, _ := http.NewRequest("POST", b.base+"/api/v1/session", strings.NewReader(`{"password":"`+e2ePassword+`"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", b.origin)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		b.t.Fatalf("login %d %s", resp.StatusCode, raw)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "bento_session" {
			b.cookie = c
		}
	}
	var s dto.Session
	json.Unmarshal(raw, &s)
	b.csrf = s.CSRFToken
	if b.cookie == nil || b.csrf == "" {
		b.t.Fatal("login returned no session")
	}
}

// waitOp polls an operation until it is terminal and returns it.
func (b *bin) waitOp(id string) dto.Operation {
	b.t.Helper()
	deadline := time.Now().Add(25 * time.Minute)
	for time.Now().Before(deadline) {
		var op dto.Operation
		b.must("GET", "/api/v1/operations/"+id, nil, &op, nil)
		switch op.State {
		case dto.OperationStateSucceeded, dto.OperationStateFailed, dto.OperationStateCancelled, dto.OperationStateInterrupted:
			return op
		}
		time.Sleep(time.Second)
	}
	b.t.Fatalf("operation %s did not finish", id)
	return dto.Operation{}
}

func (b *bin) succeed(acc dto.Accepted) dto.Operation {
	b.t.Helper()
	op := b.waitOp(acc.Operation.ID)
	if op.State != dto.OperationStateSucceeded {
		b.t.Fatalf("%s %s: %s: %s", op.Kind, op.State, op.ErrorCode, op.ErrorMessage)
	}
	return op
}

// idle waits until no operation is queued or running.
func (b *bin) idle() {
	b.t.Helper()
	deadline := time.Now().Add(25 * time.Minute)
	for time.Now().Before(deadline) {
		var list dto.OperationList
		b.must("GET", "/api/v1/operations", nil, &list, nil)
		busy := false
		for _, op := range list.Operations {
			if op.State == dto.OperationStateQueued || op.State == dto.OperationStateRunning {
				busy = true
			}
		}
		if !busy {
			return
		}
		time.Sleep(2 * time.Second)
	}
	b.t.Fatal("stack never became idle")
}

func (b *bin) cli(args ...string) string {
	b.t.Helper()
	out, err := exec.Command(b.exe, append([]string{"--stack", b.root}, args...)...).CombinedOutput()
	if err != nil {
		b.t.Fatalf("bento %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (b *bin) stackImages() int {
	b.t.Helper()
	cli, err := client.New(client.WithHostFromEnv(), client.WithAPIVersionNegotiation())
	if err != nil {
		b.t.Fatal(err)
	}
	defer cli.Close()
	f := make(client.Filters).Add("label", runtime.LabelStackID+"="+b.stackID)
	is, err := cli.ImageList(context.Background(), client.ImageListOptions{Filters: f})
	if err != nil {
		b.t.Fatal(err)
	}
	return len(is.Items)
}

// serviceExec runs a command in a data service container as its admin, with
// the client credentials the backend materialized (never argv).
func serviceExec(t *testing.T, name string, cmd []string, stdin string) string {
	t.Helper()
	sdk, err := docker.NewSDK()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ins, err := sdk.Inspect(ctx, name)
	if err != nil || ins == nil {
		t.Fatalf("inspect %s: %v", name, err)
	}
	res, err := sdk.Exec(ctx, ins.ID, docker.ExecRequest{Cmd: cmd, Stdin: strings.NewReader(stdin)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("%v exited %d: %s", cmd, res.ExitCode, res.Stderr)
	}
	return string(res.Stdout)
}

// TestIntegrationEndToEndBinary walks one app through its whole life on a real
// backend binary and checks, on real Docker, the behaviour the 2026-11-09
// plans changed: secret redaction in metrics (004), tool sessions that never
// build (012), terminals ended by session revocation (010), idempotent
// tunnel-token replays (013), exact prune confirmation and real database drops
// (006), and the CLI against the same backend.
func TestIntegrationEndToEndBinary(t *testing.T) {
	b := startBinary(t)
	b.idle() // initial MySQL and PostgreSQL services

	var svcList dto.ServiceList
	b.must("GET", "/api/v1/services", nil, &svcList, nil)
	services := svcList.Services
	var mysqlSvc string
	for _, s := range services {
		if s.Engine == dto.EngineMySQL {
			mysqlSvc = s.Name
		}
	}
	if mysqlSvc == "" {
		t.Fatalf("no MySQL service after init: %+v", services)
	}

	// ---- create, configure, start ----
	const secret = "e2eS3cretTokenValue9f8e7d6c"
	var acc dto.Accepted
	b.must("POST", "/api/v1/apps", dto.CreateAppRequest{
		Slug: "e2eapp",
		Runtime: dto.RuntimeSpec{Kind: dto.RuntimeKindHTTP, HTTP: &dto.HTTPRuntime{
			Toolchain: "node", Version: "24", Argv: []string{"node", "s.js", secret}, Port: 3000}},
		Hosts: []dto.AppHostRequest{},
		Bindings: []dto.BindingRequest{
			{Engine: dto.EngineSQLite},
			{Engine: dto.EngineMySQL, Service: mysqlSvc},
		},
	}, &acc, nil)
	b.succeed(acc)
	app := acc.App
	if app == nil {
		t.Fatal("create returned no app")
	}
	layout := platform.Layout{Root: b.root}
	writeFile(t, filepath.Join(layout.AppCode("e2eapp"), "s.js"),
		"require('http').createServer((q,s)=>s.end('ok')).listen(3000,'0.0.0.0')", app.UID)
	env := []dto.EnvVar{{Key: "API_TOKEN", Value: secret}}
	b.must("PATCH", "/api/v1/apps/"+app.ID, dto.UpdateAppRequest{Env: &env}, &acc, nil)
	if acc.Operation.ID != "" {
		b.succeed(acc)
	}
	b.must("POST", "/api/v1/apps/"+app.ID+"/start", nil, &acc, nil)
	b.succeed(acc)

	var full dto.App
	b.must("GET", "/api/v1/apps/"+app.ID, nil, &full, nil)
	var mysqlBinding dto.Binding
	for _, bd := range full.Bindings {
		if bd.Engine == dto.EngineMySQL {
			mysqlBinding = bd
		}
	}
	if len(mysqlBinding.Databases) == 0 {
		t.Fatalf("no MySQL database bound: %+v", full.Bindings)
	}

	// ---- plan 004: metrics never show an app secret ----
	t.Run("metrics redact secrets", func(t *testing.T) {
		var m dto.AppMetrics
		deadline := time.Now().Add(60 * time.Second)
		for {
			b.must("GET", "/api/v1/apps/"+app.ID+"/metrics", nil, &m, nil)
			sawNode := false
			for _, p := range m.Processes {
				if strings.Contains(p.Command, "s.js") {
					sawNode = true
				}
			}
			if m.Running && sawNode {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("node process never listed: %+v", m)
			}
			time.Sleep(time.Second)
		}
		redacted := false
		for _, p := range m.Processes {
			if strings.Contains(p.Command, secret) {
				t.Fatalf("process %s leaks the secret: %q", p.PID, p.Command)
			}
			if strings.Contains(p.Command, "s.js") && strings.Contains(p.Command, "[redacted]") {
				redacted = true
			}
		}
		if !redacted {
			t.Fatalf("node command was not redacted: %+v", m.Processes)
		}
	})

	// ---- plan 012: exec in a tool session uses the existing build ----
	t.Run("tool exec never builds", func(t *testing.T) {
		before := b.stackImages()
		var res dto.ExecResult
		b.must("POST", "/api/v1/apps/"+app.ID+"/exec", dto.ExecRequest{Argv: []string{"id", "-u"}}, &res, nil)
		if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != fmt.Sprint(app.UID) {
			t.Fatalf("tool exec: %+v", res)
		}
		if after := b.stackImages(); after != before {
			t.Fatalf("a tool exec changed the stack's images: %d -> %d", before, after)
		}
	})

	// ---- CLI over the control socket sees the same backend ----
	t.Run("cli", func(t *testing.T) {
		if out := b.cli("app", "show", "e2eapp"); !strings.Contains(out, "e2eapp") {
			t.Fatalf("app show: %s", out)
		}
		if out := b.cli("--json", "ops", "--target", app.ID); !strings.Contains(out, "app.start") {
			t.Fatalf("ops: %s", out)
		}
		if out := b.cli("app", "exec", "e2eapp", "--", "sh", "-c", "echo $API_TOKEN"); strings.Contains(out, secret) {
			t.Fatalf("CLI exec output leaks the secret: %s", out)
		}
	})

	// ---- plan 013: a replayed tunnel-token request leaves the file alone ----
	t.Run("tunnel token replay", func(t *testing.T) {
		tokenA := "eyJhIjoiAAAA" + strings.Repeat("a", 40)
		tokenB := "eyJhIjoiBBBB" + strings.Repeat("b", 40)
		key := map[string]string{"Idempotency-Key": "e2e-tunnel-" + platform.RandomHex(4)}
		var first, second dto.Accepted
		b.must("PUT", "/api/v1/tunnel/token", dto.SetTunnelTokenRequest{Token: tokenA}, &first, key)
		b.must("PUT", "/api/v1/tunnel/token", dto.SetTunnelTokenRequest{Token: tokenB}, &second, key)
		if first.Operation.ID != second.Operation.ID {
			t.Fatalf("replay created a second operation: %s vs %s", first.Operation.ID, second.Operation.ID)
		}
		got, err := os.ReadFile(filepath.Join(layout.TunnelDir(), "token"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != tokenA {
			t.Fatal("the replayed request replaced the token file")
		}
		b.waitOp(first.Operation.ID) // a fake token may fail to connect; only the file matters
		var off dto.Accepted
		b.must("PUT", "/api/v1/tunnel/token", dto.SetTunnelTokenRequest{Token: ""}, &off, nil)
		b.waitOp(off.Operation.ID)
	})

	// ---- plan 010: revoking the session ends its terminal ----
	t.Run("terminal ends on logout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		hdr := http.Header{}
		hdr.Set("Origin", b.origin)
		hdr.Add("Cookie", b.cookie.Name+"="+b.cookie.Value)
		ws := "ws" + strings.TrimPrefix(b.base, "http") + "/api/v1/apps/" + app.ID + "/terminal"
		conn, resp, err := websocket.Dial(ctx, ws, &websocket.DialOptions{HTTPHeader: hdr,
			Subprotocols: []string{"bento.v1", "bento.csrf." + b.csrf}}) // as the browser offers it
		if err != nil {
			code := 0
			if resp != nil {
				code = resp.StatusCode
			}
			t.Fatalf("terminal dial: %v (%d)", err, code)
		}
		defer conn.CloseNow()
		if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo term-$((40+2))\n")); err != nil {
			t.Fatal(err)
		}
		var seen strings.Builder
		for !strings.Contains(seen.String(), "term-42") {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("terminal read before logout: %v (seen %q)", err, seen.String())
			}
			seen.Write(data)
		}
		code, raw := b.req("DELETE", "/api/v1/session", nil, nil)
		if code/100 != 2 {
			t.Fatalf("logout %d %s", code, raw)
		}
		start := time.Now()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				if ctx.Err() != nil {
					t.Fatal("terminal stayed open after its session was revoked")
				}
				t.Logf("terminal closed %s after logout: %v", time.Since(start).Round(time.Second), err)
				break
			}
		}
		if time.Since(start) > 75*time.Second {
			t.Fatalf("terminal closed only after %s", time.Since(start))
		}
		b.login()
	})

	// ---- plan 006: remove, then prune with an exact confirmation ----
	t.Run("remove and prune", func(t *testing.T) {
		mysqlQuery := []string{"mysql", "--defaults-extra-file=/run/bento-secrets/client.cnf", "-N", "-B"}
		svcContainer := ""
		{
			cli, err := client.New(client.WithHostFromEnv(), client.WithAPIVersionNegotiation())
			if err != nil {
				t.Fatal(err)
			}
			f := make(client.Filters).Add("label", runtime.LabelStackID+"="+b.stackID)
			cs, err := cli.ContainerList(context.Background(), client.ContainerListOptions{All: true, Filters: f})
			cli.Close()
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range cs.Items {
				for _, n := range c.Names {
					if strings.Contains(n, mysqlSvc) {
						svcContainer = c.ID
					}
				}
			}
		}
		if svcContainer == "" {
			t.Fatal("MySQL service container not found")
		}
		dbs := serviceExec(t, svcContainer, mysqlQuery, "SHOW DATABASES;")
		if !strings.Contains(dbs, mysqlBinding.Databases[0]) {
			t.Fatalf("bound database %s missing before removal: %s", mysqlBinding.Databases[0], dbs)
		}

		if code, _ := b.req("DELETE", "/api/v1/apps/"+app.ID, dto.ConfirmRequest{Confirm: "delete"}, nil); code/100 == 2 {
			t.Fatal("removal accepted an inexact confirmation")
		}
		b.must("DELETE", "/api/v1/apps/"+app.ID, dto.ConfirmRequest{Confirm: "delete e2eapp"}, &acc, nil)
		b.succeed(acc)
		if _, err := os.Stat(layout.AppHome("e2eapp")); err != nil {
			t.Fatalf("removal must retain the home: %v", err)
		}

		for _, bad := range []string{"", "Delete", "delete e2eapp", "delete ", "yes"} {
			if code, raw := b.req("POST", "/api/v1/retired/"+app.ID+"/prune", dto.ConfirmRequest{Confirm: bad}, nil); code/100 == 2 {
				t.Fatalf("prune accepted confirmation %q: %s", bad, raw)
			}
		}
		if _, err := os.Stat(layout.AppHome("e2eapp")); err != nil {
			t.Fatalf("a refused prune touched the home: %v", err)
		}
		var list dto.RetiredList
		b.must("GET", "/api/v1/retired", nil, &list, nil)
		listed := false
		for _, r := range list.Retired {
			listed = listed || r.AppID == app.ID
		}
		if !listed {
			t.Fatalf("app not listed as retired: %+v", list)
		}
		const confirm = "delete"
		b.must("POST", "/api/v1/retired/"+app.ID+"/prune", dto.ConfirmRequest{Confirm: confirm}, &acc, nil)
		b.succeed(acc)
		if _, err := os.Stat(layout.AppHome("e2eapp")); !os.IsNotExist(err) {
			t.Fatalf("prune left the home: %v", err)
		}
		if dbs := serviceExec(t, svcContainer, mysqlQuery, "SHOW DATABASES;"); strings.Contains(dbs, mysqlBinding.Databases[0]) {
			t.Fatalf("prune left database %s: %s", mysqlBinding.Databases[0], dbs)
		}
		users := serviceExec(t, svcContainer, mysqlQuery, "SELECT user FROM mysql.user;")
		if strings.Contains(users, mysqlBinding.Username) {
			t.Fatalf("prune left MySQL user %s", mysqlBinding.Username)
		}
		if code, _ := b.req("POST", "/api/v1/retired/"+app.ID+"/prune", dto.ConfirmRequest{Confirm: confirm}, nil); code/100 == 2 {
			t.Fatal("a second prune was accepted")
		}
	})
}
