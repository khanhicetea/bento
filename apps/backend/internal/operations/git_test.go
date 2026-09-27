package operations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func TestNewDeployKeyIsUsableOpenSSHEd25519(t *testing.T) {
	priv, pub, fp, err := NewDeployKey("bento-test-shop")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		t.Fatalf("private key must parse as OpenSSH: %v", err)
	}
	parsed, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil || comment != "bento-test-shop" || parsed.Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("public key %q: %v", pub, err)
	}
	if string(parsed.Marshal()) != string(signer.PublicKey().Marshal()) || ssh.FingerprintSHA256(parsed) != fp {
		t.Fatal("public key and fingerprint must match the private key")
	}
}

func TestSetGitSourceKeepsKeyUntilRotated(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := context.Background()
	if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "https://user:tok@github.com/o/r.git", Branch: "main"}); err == nil {
		t.Fatal("embedded HTTPS credentials must be refused")
	}
	first, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"})
	if err != nil || first.PrivateKey == "" || !strings.HasPrefix(first.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("first set: %+v %v", first, err)
	}
	second, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "release"})
	if err != nil || second.PrivateKey != first.PrivateKey || second.Branch != "release" {
		t.Fatal("changing the branch must keep the registered deploy key")
	}
	rotated, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "release", RotateKey: true})
	if err != nil || rotated.PrivateKey == first.PrivateKey || rotated.Fingerprint == first.Fingerprint {
		t.Fatal("rotation must replace the deploy key")
	}
	if err := h.c.RemoveGitSource(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.GetGitSource(ctx, h.store.DB(), app.ID); ok {
		t.Fatal("remove must forget the source and key")
	}
	if err := h.c.RemoveGitSource(ctx, app.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
}

func TestDeployRequiresGitSource(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	if _, err := h.c.DeployApp(context.Background(), app.ID, ""); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("deploy without a source must be refused, got %v", err)
	}
}

// deployHook answers the deploy exec and captures what reached the container.
func deployHook(stdout, stderr string, exit int, gotKey *string, gotReq *docker.ExecRequest) func(string, docker.ExecRequest) docker.ExecResult {
	return func(_ string, req docker.ExecRequest) docker.ExecResult {
		if !slices.Contains(req.Cmd, "bento-deploy") {
			return docker.ExecResult{Stdout: []byte("ready")}
		}
		if req.Stdin != nil {
			b, _ := io.ReadAll(req.Stdin)
			*gotKey = string(b)
		}
		*gotReq = req
		return docker.ExecResult{ExitCode: exit, Stdout: []byte(stdout), Stderr: []byte(stderr)}
	}
}

func TestDeployPassesKeyOnStdinRecordsCommitAndRestarts(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := context.Background()
	g, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	h.mustSucceed(h.c.StartApp(ctx, app.ID, ""))
	var key string
	var req docker.ExecRequest
	var execs []string
	inner := deployHook("BENTO_COMMIT="+testCommit+"\nBENTO_SUBJECT=init\n", "", 0, &key, &req)
	h.fake.ExecHook = func(id string, r docker.ExecRequest) docker.ExecResult {
		execs = append(execs, strings.Join(r.Cmd, " "))
		return inner(id, r)
	}
	created, stops := h.fake.CallCount("Create"), h.fake.CallCount("Stop")
	got := h.mustSucceed(h.c.DeployApp(ctx, app.ID, ""))

	if key != g.PrivateKey {
		t.Fatal("the deploy key must be passed on exec stdin")
	}
	for _, s := range append(slices.Clone(req.Cmd), req.Env...) {
		if strings.Contains(s, "PRIVATE KEY") {
			t.Fatal("the deploy key must never reach argv or env")
		}
	}
	if want := fmt.Sprintf("%d:%d", app.UID, app.GID); req.User != want {
		t.Fatalf("deploy must run as the app identity, got %q", req.User)
	}
	cur, _, _ := store.GetGitSource(ctx, h.store.DB(), app.ID)
	if cur.DeployedCommit != testCommit || cur.DeployedAt.IsZero() {
		t.Fatalf("deployed commit not recorded: %+v", cur)
	}
	if !strings.Contains(string(got.Result), `"reloaded":"app"`) {
		t.Fatalf("a running app must have its process reloaded: %s", got.Result)
	}
	if h.fake.CallCount("Create") != created+1 || h.fake.CallCount("Stop") != stops {
		t.Fatal("deploy must not stop or recreate the instance; only the tooling container is created")
	}
	if !slices.ContainsFunc(execs, func(c string) bool { return strings.Contains(c, "s6-svc -r /run/service/app") }) {
		t.Fatal("the http app service must be restarted through s6")
	}
	tools, _ := h.fake.List(ctx, map[string]string{"io.bento.app-id": app.ID, "io.bento.role": "tool"})
	if len(tools) != 0 {
		t.Fatal("the tooling container must be removed after deploy")
	}
}

func TestDeployPublicHTTPSSendsNoKeyAndStoppedAppStaysStopped(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := context.Background()
	if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "https://github.com/o/r.git", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	key := "unset"
	var req docker.ExecRequest
	h.fake.ExecHook = deployHook("BENTO_COMMIT="+testCommit+"\n", "", 0, &key, &req)
	got := h.mustSucceed(h.c.DeployApp(ctx, app.ID, ""))
	if key != "" {
		t.Fatal("public HTTPS deploys must not send the deploy key")
	}
	if !strings.Contains(string(got.Result), `"reloaded":""`) {
		t.Fatalf("a stopped app must stay stopped: %s", got.Result)
	}
}

func TestDeployFailuresAreDiagnosable(t *testing.T) {
	cases := []struct {
		stderr string
		exit   int
		code   string
	}{
		{"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", 128, "git-access-denied"},
		{"Host key verification failed.", 128, "git-host-key"},
		{"warning: Could not find remote branch nope to clone.\nfatal: Remote branch nope not found in upstream origin", 128, "git-branch-missing"},
		{"code directory is not empty and is not a git checkout", 66, "code-dir-not-empty"},
		{"code directory is a checkout of 'x', not the configured repository", 65, "git-origin-mismatch"},
		{"", 0, "deploy-failed"}, // exit 0 without a commit is still a failure
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			h := newHarness(t)
			app := h.createApp("shop")
			ctx := context.Background()
			if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"}); err != nil {
				t.Fatal(err)
			}
			var key string
			var req docker.ExecRequest
			h.fake.ExecHook = deployHook("", tc.stderr, tc.exit, &key, &req)
			op, err := h.c.DeployApp(ctx, app.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			got := h.wait(op)
			if got.State != store.OpFailed || got.ErrorCode != tc.code {
				t.Fatalf("want %s, got %s %s: %s", tc.code, got.State, got.ErrorCode, got.ErrorMessage)
			}
			if strings.Contains(got.ErrorMessage+got.Guidance, "PRIVATE KEY") {
				t.Fatal("failures must never include the private key")
			}
			cur, _, _ := store.GetGitSource(ctx, h.store.DB(), app.ID)
			if cur.DeployedCommit != "" {
				t.Fatal("a failed deploy must not record a commit")
			}
		})
	}
}

func TestDeployFaultInjectionThenRecover(t *testing.T) {
	for _, method := range []string{"Create", "Start", "Exec"} {
		t.Run(method, func(t *testing.T) {
			h := newHarness(t)
			app := h.createApp("shop")
			ctx := context.Background()
			if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"}); err != nil {
				t.Fatal(err)
			}
			var key string
			var req docker.ExecRequest
			h.fake.ExecHook = deployHook("BENTO_COMMIT="+testCommit+"\n", "", 0, &key, &req)
			h.fake.FailOn = map[string]error{method: errors.New("injected " + method + " failure")}
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
				t.Fatal("no tooling container may leak after a failure")
			}
		})
	}
}

func TestDeployScriptNeverWritesKeyOutsideTmp(t *testing.T) {
	if !strings.Contains(deployScript, `mktemp /tmp/`) || !strings.Contains(deployScript, `trap 'rm -f "$key"' EXIT`) {
		t.Fatal("the key must live only in the tooling container's /tmp and be removed")
	}
}
