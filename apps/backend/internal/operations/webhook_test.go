package operations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func githubPush(secret, delivery, ref string) (http.Header, []byte) {
	body := []byte(`{"ref":"` + ref + `","after":"` + testCommit + `","pusher":{"name":"kit"}}`)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	h.Set("X-GitHub-Delivery", delivery)
	h.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(m.Sum(nil)))
	return h, body
}

func (h *harness) webhookApp(slug string) (domain.App, domain.Webhook) {
	h.t.Helper()
	app := h.createApp(slug)
	ctx := context.Background()
	if _, err := h.c.EnableWebhook(ctx, app.ID); !errors.Is(err, ErrPrecondition) {
		h.t.Fatalf("a webhook needs a git source first, got %v", err)
	}
	if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"}); err != nil {
		h.t.Fatal(err)
	}
	w, err := h.c.EnableWebhook(ctx, app.ID)
	if err != nil || len(w.HookID) != 32 || len(w.Secret) != 64 {
		h.t.Fatalf("enable: %+v %v", w, err)
	}
	return app, w
}

func TestWebhookEnableRotatesSecretKeepsURL(t *testing.T) {
	h := newHarness(t)
	app, first := h.webhookApp("shop")
	ctx := t.Context()
	second, err := h.c.EnableWebhook(ctx, app.ID)
	if err != nil || second.HookID != first.HookID || second.Secret == first.Secret {
		t.Fatal("rotation must keep the URL and replace the secret")
	}
	hd, body := githubPush(first.Secret, "d1", "refs/heads/main")
	if out, _ := h.c.HandleWebhook(ctx, first.HookID, hd, body); out.Status != http.StatusNotFound {
		t.Fatalf("the rotated-out secret must stop working, got %d", out.Status)
	}
	if err := h.c.RemoveGitSource(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.GetWebhook(ctx, h.store.DB(), app.ID); ok {
		t.Fatal("removing the git source must destroy the webhook")
	}
}

func TestWebhookRejectsUnknownAndUnauthenticated(t *testing.T) {
	h := newHarness(t)
	app, w := h.webhookApp("shop")
	ctx := t.Context()
	hd, body := githubPush("wrong-secret", "d1", "refs/heads/main")
	for _, hook := range []string{w.HookID, strings.Repeat("0", 32)} {
		out, err := h.c.HandleWebhook(ctx, hook, hd, body)
		if err != nil || out.Status != http.StatusNotFound {
			t.Fatalf("%s: unknown hooks and bad credentials must look the same: %d %v", hook, out.Status, err)
		}
	}
	cur, _, _ := store.GetWebhook(ctx, h.store.DB(), app.ID)
	if len(cur.Deliveries) != 0 {
		t.Fatal("unauthenticated requests must not be recorded")
	}
	if ops, _ := store.ListOperations(ctx, h.store.DB(), store.OpFilter{TargetID: app.ID}); slices.ContainsFunc(ops, func(o store.Operation) bool { return o.Kind == KindAppDeploy }) {
		t.Fatal("no deploy may be queued without a valid signature")
	}
}

func TestWebhookPushDeploysOnlyConfiguredBranch(t *testing.T) {
	h := newHarness(t)
	app, w := h.webhookApp("shop")
	ctx := t.Context()
	var key string
	var req docker.ExecRequest
	h.fake.ExecHook = deployHook("BENTO_COMMIT="+testCommit+"\n", "", 0, &key, &req)

	ping := http.Header{}
	ping.Set("X-GitHub-Event", "ping")
	ping.Set("Authorization", "Bearer "+w.Secret)
	if out, _ := h.c.HandleWebhook(ctx, w.HookID, ping, []byte(`{}`)); out.Status != http.StatusOK || out.Result != "ping" {
		t.Fatalf("ping: %+v", out)
	}
	hd, body := githubPush(w.Secret, "d-other", "refs/heads/feature")
	if out, _ := h.c.HandleWebhook(ctx, w.HookID, hd, body); out.Status != http.StatusAccepted || out.Result != "ignored-ref" || out.OperationID != "" {
		t.Fatalf("other branch: %+v", out)
	}
	hd, body = githubPush(w.Secret, "d-main", "refs/heads/main")
	out, err := h.c.HandleWebhook(ctx, w.HookID, hd, body)
	if err != nil || out.Status != http.StatusAccepted || out.Result != "deployed" || out.OperationID == "" {
		t.Fatalf("push: %+v %v", out, err)
	}
	op, _ := store.GetOperation(ctx, h.store.DB(), out.OperationID)
	got := h.wait(op)
	if got.State != store.OpSucceeded || got.Origin != DeployTriggerWebhook || !strings.Contains(string(got.Request), `"trigger":"webhook"`) {
		t.Fatalf("webhook deploy: %s %s %s", got.State, got.Origin, got.Request)
	}
	// A provider redelivery maps to the original operation.
	again, _ := h.c.HandleWebhook(ctx, w.HookID, hd, body)
	if again.Result != "duplicate" || again.OperationID != out.OperationID {
		t.Fatalf("redelivery: %+v", again)
	}
	cur, _, _ := store.GetWebhook(ctx, h.store.DB(), app.ID)
	results := []string{}
	for _, d := range cur.Deliveries {
		results = append(results, d.Result)
	}
	if strings.Join(results, ",") != "duplicate,deployed,ignored-ref,ping" {
		t.Fatalf("deliveries must be recorded newest first: %v", results)
	}
}

// M5: after a failed deploy, a provider redelivery (same delivery ID) deploys
// again instead of answering duplicate with the failed operation.
func TestWebhookRedeliveryAfterFailureDeploysAgain(t *testing.T) {
	h := newHarness(t)
	_, w := h.webhookApp("shop")
	ctx := t.Context()
	var key string
	var req docker.ExecRequest
	h.fake.ExecHook = deployHook("", "permission denied (publickey)", 128, &key, &req)
	hd, body := githubPush(w.Secret, "d1", "refs/heads/main")
	first, _ := h.c.HandleWebhook(ctx, w.HookID, hd, body)
	op, _ := store.GetOperation(ctx, h.store.DB(), first.OperationID)
	if got := h.wait(op); got.State != store.OpFailed {
		t.Fatalf("first deploy must fail, got %s", got.State)
	}
	h.fake.ExecHook = deployHook("BENTO_COMMIT="+testCommit+"\n", "", 0, &key, &req)
	again, _ := h.c.HandleWebhook(ctx, w.HookID, hd, body)
	if again.Result != "deployed" || again.OperationID == first.OperationID {
		t.Fatalf("redelivery after failure: %+v", again)
	}
	op, _ = store.GetOperation(ctx, h.store.DB(), again.OperationID)
	if got := h.wait(op); got.State != store.OpSucceeded {
		t.Fatalf("redeployed: %s %s", got.State, got.ErrorMessage)
	}
	if third, _ := h.c.HandleWebhook(ctx, w.HookID, hd, body); third.Result != "duplicate" || third.OperationID != again.OperationID {
		t.Fatalf("redelivery after success must dedupe: %+v", third)
	}
}

func TestWebhookCoalescesWhileADeployIsQueued(t *testing.T) {
	h := newHarness(t)
	app, w := h.webhookApp("shop")
	ctx := t.Context()
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	h.fake.ExecHook = func(_ string, r docker.ExecRequest) docker.ExecResult {
		if slices.Contains(r.Cmd, "bento-deploy") {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return docker.ExecResult{Stdout: []byte("BENTO_COMMIT=" + testCommit + "\n")}
		}
		return docker.ExecResult{Stdout: []byte("ready")}
	}
	running, err := h.c.DeployApp(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	hd, body := githubPush(w.Secret, "d1", "refs/heads/main")
	first, _ := h.c.HandleWebhook(ctx, w.HookID, hd, body)
	hd, body = githubPush(w.Secret, "d2", "refs/heads/main")
	second, _ := h.c.HandleWebhook(ctx, w.HookID, hd, body)
	close(release)
	if first.Result != "deployed" || second.Result != "coalesced" || second.OperationID != first.OperationID || first.OperationID == running.ID {
		t.Fatalf("a push during a running deploy queues one more; later pushes join it: %+v %+v", first, second)
	}
	h.wait(running)
	op, _ := store.GetOperation(ctx, h.store.DB(), first.OperationID)
	if got := h.wait(op); got.State != store.OpSucceeded {
		t.Fatalf("queued deploy: %s %s", got.State, got.ErrorMessage)
	}
}

func writeDeployScript(t *testing.T, h *harness, app domain.App, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(h.layout.AppHome(app.Slug), DeployScriptName)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(p, app.UID, app.GID); err != nil {
		t.Fatal(err)
	}
	return p
}

// scriptHook answers the git exec and the deploy.sh exec separately.
func scriptHook(exit int, stderr string, script *docker.ExecRequest, execs *[]string) func(string, docker.ExecRequest) docker.ExecResult {
	return func(_ string, r docker.ExecRequest) docker.ExecResult {
		*execs = append(*execs, strings.Join(r.Cmd, " "))
		switch {
		case slices.Contains(r.Cmd, "bento-deploy"):
			return docker.ExecResult{Stdout: []byte("BENTO_COMMIT=" + testCommit + "\n")}
		case slices.ContainsFunc(r.Cmd, func(s string) bool { return strings.HasSuffix(s, "/"+DeployScriptName) }):
			*script = r
			return docker.ExecResult{ExitCode: exit, Stdout: []byte("migrated\n"), Stderr: []byte(stderr)}
		}
		return docker.ExecResult{Stdout: []byte("ready")}
	}
}

func TestDeployRunsHomeScriptAsAppWithNormalizedEnv(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	var script docker.ExecRequest
	var execs []string
	h.fake.ExecHook = scriptHook(0, "", &script, &execs)

	got := h.mustSucceed(h.c.DeployApp(ctx, app.ID, ""))
	if script.Cmd != nil || !strings.Contains(string(got.Result), `"script":false`) {
		t.Fatal("without ~/deploy.sh no script may run")
	}

	writeDeployScript(t, h, app, 0o755)
	got = h.mustSucceed(h.c.DeployApp(ctx, app.ID, ""))
	if want := app.ContainerHome() + "/" + DeployScriptName; !slices.Contains(script.Cmd, want) || script.Cmd[0] != "/usr/local/bin/bento-exec" {
		t.Fatalf("deploy.sh must run through bento-exec: %v", script.Cmd)
	}
	if want := fmt.Sprintf("%d:%d", app.UID, app.GID); script.User != want {
		t.Fatalf("deploy.sh must run as the app identity, got %q", script.User)
	}
	for _, want := range []string{"BENTO_DEPLOY_TRIGGER=manual", "BENTO_COMMIT=" + testCommit, "BENTO_PREVIOUS_COMMIT=" + testCommit,
		"BENTO_BRANCH=main", "BENTO_EXEC_WORKDIR=" + app.ContainerCode(), "BENTO_OPERATION_ID=" + got.ID} {
		if !slices.Contains(script.Env, want) {
			t.Errorf("deploy.sh env missing %s: %v", want, script.Env)
		}
	}
	if script.Stdin != nil {
		t.Fatal("deploy.sh must not receive the deploy key")
	}
	gitAt := slices.IndexFunc(execs, func(c string) bool { return strings.Contains(c, "bento-deploy") })
	scriptAt := slices.IndexFunc(execs, func(c string) bool { return strings.HasSuffix(c, "/"+DeployScriptName) })
	if gitAt < 0 || scriptAt < gitAt {
		t.Fatal("deploy.sh runs after the fetch, in its own exec")
	}
	events, _ := store.ListEvents(ctx, h.store.DB(), got.ID, 0)
	if !slices.ContainsFunc(events, func(e store.OpEvent) bool { return e.Message == "deploy.sh: migrated" }) {
		t.Fatal("script output must be streamed into operation events")
	}
}

func TestDeployScriptFailureSkipsRecordAndReload(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	writeDeployScript(t, h, app, 0o755)
	var script docker.ExecRequest
	var execs []string
	h.fake.ExecHook = scriptHook(3, "migration failed", &script, &execs)
	op, err := h.c.DeployApp(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.State != store.OpFailed || got.ErrorCode != "deploy-script-failed" || !strings.Contains(got.ErrorMessage, "migration failed") {
		t.Fatalf("got %s %s: %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	if slices.ContainsFunc(execs, func(c string) bool { return strings.Contains(c, "s6-svc") }) {
		t.Fatal("a failed script must not reload the app")
	}
	if cur, _, _ := store.GetGitSource(ctx, h.store.DB(), app.ID); cur.DeployedCommit != "" {
		t.Fatal("a failed script must not record the deploy")
	}
}

func TestDeployScriptMustBeAPlainExecutableFile(t *testing.T) {
	cases := map[string]func(t *testing.T, h *harness, app domain.App){
		"not executable": func(t *testing.T, h *harness, app domain.App) { writeDeployScript(t, h, app, 0o644) },
		"symlink": func(t *testing.T, h *harness, app domain.App) {
			if err := os.Symlink("/etc/passwd", filepath.Join(h.layout.AppHome(app.Slug), DeployScriptName)); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(t *testing.T, h *harness, app domain.App) {
			if err := os.Mkdir(filepath.Join(h.layout.AppHome(app.Slug), DeployScriptName), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"foreign owner": func(t *testing.T, h *harness, app domain.App) {
			p := writeDeployScript(t, h, app, 0o755)
			if err := os.Chown(p, app.UID+1, app.GID); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			app := h.createApp("shop")
			ctx := t.Context()
			if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"}); err != nil {
				t.Fatal(err)
			}
			setup(t, h, app)
			var script docker.ExecRequest
			var execs []string
			h.fake.ExecHook = scriptHook(0, "", &script, &execs)
			op, err := h.c.DeployApp(ctx, app.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := h.wait(op); got.State != store.OpFailed || got.ErrorCode != "deploy-script-invalid" {
				t.Fatalf("got %s %s: %s", got.State, got.ErrorCode, got.ErrorMessage)
			}
			if len(execs) != 0 {
				t.Fatal("an invalid script is refused before anything runs")
			}
		})
	}
}

func TestDeployScriptFaultInjection(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	writeDeployScript(t, h, app, 0o755)
	// Fail only the first script exec (killed), then recover on retry.
	calls := 0
	h.fake.ExecHook = func(_ string, r docker.ExecRequest) docker.ExecResult {
		switch {
		case slices.Contains(r.Cmd, "bento-deploy"):
			return docker.ExecResult{Stdout: []byte("BENTO_COMMIT=" + testCommit + "\n")}
		case slices.ContainsFunc(r.Cmd, func(s string) bool { return strings.HasSuffix(s, "/"+DeployScriptName) }):
			if calls++; calls == 1 {
				return docker.ExecResult{ExitCode: 137}
			}
		}
		return docker.ExecResult{Stdout: []byte("ready")}
	}
	op, err := h.c.DeployApp(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.wait(op); got.State != store.OpFailed {
		t.Fatalf("expected failure, got %s", got.State)
	}
	h.mustSucceed(h.c.DeployApp(ctx, app.ID, ""))
	tools, _ := h.fake.List(ctx, map[string]string{"io.bento.app-id": app.ID, "io.bento.role": "tool"})
	if len(tools) != 0 {
		t.Fatal("no tooling container may leak after a script failure")
	}
}

func TestAppsGatewayIsTheHostAddressInTheSubnet(t *testing.T) {
	if got := (NetworkSettings{AppsSubnet: "127.0.0.0/8"}).AppsGateway(); got != "127.0.0.1" {
		t.Fatalf("got %q", got)
	}
	if got := (NetworkSettings{AppsSubnet: "192.0.2.0/24"}).AppsGateway(); got != "" {
		t.Fatalf("a subnet without a host address must yield no gateway, got %q", got)
	}
	h := newHarness(t)
	if gw, err := h.c.AppsGateway(t.Context()); err != nil || gw != "" {
		t.Fatalf("reading the gateway must never plan networks: %q %v", gw, err)
	}
}
