package operations

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

const KindAppDeploy = "app.deploy"

// deployTimeout bounds one clone or fetch inside the tooling container.
const deployTimeout = 15 * time.Minute

// GitSourceInput configures an app's repository. RotateKey replaces the deploy
// key; the new public key must then be registered with the git host again.
type GitSourceInput struct {
	RepoURL   string
	Branch    string
	RotateKey bool
}

// NewDeployKey generates an ed25519 deploy key in OpenSSH format.
func NewDeployKey(comment string) (private, public, fingerprint string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", "", err
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment
	return string(pem.EncodeToMemory(block)), authorized, ssh.FingerprintSHA256(sshPub), nil
}

// SetGitSource persists an app's repository source. A deploy key is generated
// on first configuration or on rotation and is otherwise kept, so changing the
// branch never invalidates a key already registered with the git host. This is
// pure intent; nothing is fetched until a deploy is submitted.
func (c *Controller) SetGitSource(ctx context.Context, id string, in GitSourceInput) (domain.GitSource, error) {
	var errs domain.ValidationErrors
	in.RepoURL, in.Branch = strings.TrimSpace(in.RepoURL), strings.TrimSpace(in.Branch)
	domain.ValidateGitSource(in.RepoURL, in.Branch, &errs)
	if err := errs.Err(); err != nil {
		return domain.GitSource{}, err
	}
	var out domain.GitSource
	err := c.Store.Tx(ctx, func(q store.Q) error {
		app, err := store.GetApp(ctx, q, id)
		if err != nil {
			return err
		}
		g, ok, err := store.GetGitSource(ctx, q, app.ID)
		if err != nil {
			return err
		}
		if !ok || in.RotateKey || g.PrivateKey == "" {
			comment := "bento-" + c.Stack.Name + "-" + app.Slug
			if g.PrivateKey, g.PublicKey, g.Fingerprint, err = NewDeployKey(comment); err != nil {
				return err
			}
			g.KeyCreatedAt = time.Now().UTC()
		}
		if ok && g.RepoURL != "" && g.RepoURL != in.RepoURL {
			// A different repository starts a new deploy history.
			g.DeployedCommit, g.DeployedAt = "", time.Time{}
		}
		g.RepoURL, g.Branch = in.RepoURL, in.Branch
		out = g
		return store.PutGitSource(ctx, q, app.ID, g)
	})
	return out, err
}

// RemoveGitSource forgets the repository and destroys the deploy key. The
// checked-out code in the app home is left untouched.
func (c *Controller) RemoveGitSource(ctx context.Context, id string) error {
	return c.Store.Tx(ctx, func(q store.Q) error {
		app, err := store.GetApp(ctx, q, id)
		if err != nil {
			return err
		}
		if _, ok, err := store.GetGitSource(ctx, q, app.ID); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: app %s has no git source", store.ErrNotFound, app.Slug)
		}
		return store.DeleteGitSource(ctx, q, app.ID)
	})
}

// DeployApp submits a deploy of the configured branch into the app's code
// directory.
func (c *Controller) DeployApp(ctx context.Context, id, idem string) (store.Operation, error) {
	app, err := store.GetApp(ctx, c.Store.DB(), id)
	if err != nil {
		return store.Operation{}, err
	}
	if !app.Provisioned {
		return store.Operation{}, fmt.Errorf("%w: app %s is not provisioned yet", ErrPrecondition, app.Slug)
	}
	if _, ok, err := store.GetGitSource(ctx, c.Store.DB(), app.ID); err != nil {
		return store.Operation{}, err
	} else if !ok {
		return store.Operation{}, fmt.Errorf("%w: configure a git source for %s first", ErrPrecondition, app.Slug)
	}
	op, _, err := c.Submit(ctx, Submission{Kind: KindAppDeploy, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem})
	return op, err
}

// deployScript runs as the app identity inside a tooling container. The deploy
// key arrives on stdin and lives only in the container's private /tmp tmpfs.
// Host keys are pinned on first use in the app's own known_hosts. Tracked files
// are reset to the branch; untracked files (.env, vendor, uploads) are kept.
const deployScript = `set -euo pipefail
key="$(mktemp /tmp/bento-deploy-key.XXXXXX)"
trap 'rm -f "$key"' EXIT
cat >"$key"
mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
export GIT_SSH_COMMAND="ssh -i $key -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$HOME/.ssh/known_hosts"
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1
cd "$BENTO_CODE_DIR"
if [ -d .git ]; then
  origin="$(git config --get remote.origin.url || true)"
  if [ "$origin" != "$BENTO_GIT_URL" ]; then
    # The same host/path over another transport (HTTPS to SSH) is repointed.
    repo_id() { printf '%s' "$1" | sed -E 's#^(ssh|https)://##; s#^[^@/]+@##; s#:([0-9]+/)?#/#; s#\.git$##; s#/+$##'; }
    if [ -z "$origin" ] || [ "$(repo_id "$origin")" != "$(repo_id "$BENTO_GIT_URL")" ]; then
      echo "code directory is a checkout of '$origin', not the configured repository" >&2
      exit 65
    fi
    git remote set-url origin "$BENTO_GIT_URL"
  fi
  git fetch --quiet --prune origin "+refs/heads/$BENTO_GIT_BRANCH:refs/remotes/origin/$BENTO_GIT_BRANCH"
  git checkout --quiet --force -B "$BENTO_GIT_BRANCH" "refs/remotes/origin/$BENTO_GIT_BRANCH"
  git reset --quiet --hard "refs/remotes/origin/$BENTO_GIT_BRANCH"
elif [ -z "$(ls -A)" ]; then
  git clone --quiet --branch "$BENTO_GIT_BRANCH" --single-branch -- "$BENTO_GIT_URL" .
else
  echo "code directory is not empty and is not a git checkout" >&2
  exit 66
fi
git submodule update --quiet --init --recursive
echo "BENTO_COMMIT=$(git rev-parse HEAD)"
echo "BENTO_SUBJECT=$(git log -1 --format=%s | head -c 200)"
`

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

func (c *Controller) handleDeploy(ctx context.Context, r *Run) (any, error) {
	app, err := c.loadApp(ctx, r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	g, ok, err := store.GetGitSource(ctx, c.Store.DB(), app.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, Fail("git-source-missing", "Configure a git source for the app, then deploy again.", "app %s has no git source", app.Slug)
	}
	if err := r.Phase(ctx, "open-tool"); err != nil {
		return nil, err
	}
	tool, err := c.OpenTool(ctx, app, deployTimeout+time.Minute)
	if err != nil {
		return nil, err
	}
	defer tool.Close()

	if err := r.Phase(ctx, "fetch"); err != nil {
		return nil, err
	}
	r.Info(ctx, "deploying %s (%s) into %s", g.RepoURL, g.Branch, app.ContainerCode())
	req, err := ExecRequestFor(app, []string{"bash", "-c", deployScript, "bento-deploy"}, "")
	if err != nil {
		return nil, err
	}
	req.Env = append(req.Env, "BENTO_CODE_DIR="+app.ContainerCode(), "BENTO_GIT_URL="+g.RepoURL, "BENTO_GIT_BRANCH="+g.Branch)
	key := ""
	if g.UsesSSH() {
		key = g.PrivateKey
	}
	req.Stdin = strings.NewReader(key)
	execCtx, cancel := context.WithTimeout(ctx, deployTimeout)
	res, err := c.Engine.Exec(execCtx, tool.ContainerID, req)
	cancel()
	if err != nil {
		return nil, err
	}
	commit, subject := "", ""
	for line := range strings.Lines(string(res.Stdout)) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "BENTO_COMMIT="):
			commit = strings.TrimPrefix(line, "BENTO_COMMIT=")
		case strings.HasPrefix(line, "BENTO_SUBJECT="):
			subject = strings.TrimPrefix(line, "BENTO_SUBJECT=")
		}
	}
	gitLog := tailLines(res.Stderr, 40)
	for _, line := range gitLog {
		r.Info(ctx, "git: %s", line)
	}
	if res.ExitCode != 0 || !commitPattern.MatchString(commit) {
		return nil, deployFailure(app, g, res.ExitCode, gitLog)
	}
	r.Info(ctx, "checked out %s %s", commit[:12], subject)

	if err := r.Phase(ctx, "record"); err != nil {
		return nil, err
	}
	deployedAt := time.Now().UTC()
	if err := c.Store.Tx(ctx, func(q store.Q) error {
		cur, ok, err := store.GetGitSource(ctx, q, app.ID)
		if err != nil || !ok || cur.RepoURL != g.RepoURL {
			return err // removed or repointed meanwhile; nothing to record
		}
		cur.DeployedCommit, cur.DeployedAt = commit, deployedAt
		return store.PutGitSource(ctx, q, app.ID, cur)
	}); err != nil {
		return nil, err
	}

	result := map[string]any{"commit": commit, "subject": subject, "branch": g.Branch, "reloaded": ""}
	if app.DesiredRuntime != domain.DesiredRunning {
		r.Info(ctx, "app is stopped; start it to serve the new code")
		return result, nil
	}
	service, err := c.reloadAppProcess(ctx, r, app)
	if err != nil {
		return nil, err
	}
	result["reloaded"] = service
	return result, nil
}

// reloadAppProcess makes a running app serve newly deployed code without
// recreating or restarting its container, so the scheduler and local Nginx keep
// running. PHP gets a graceful FPM reload (SIGUSR2: in-flight requests finish,
// workers and opcache are renewed); an HTTP process has only its s6 service
// restarted. A desired-running app without a running instance is started.
func (c *Controller) reloadAppProcess(ctx context.Context, r *Run, app domain.App) (string, error) {
	obs, err := c.observe(ctx, app)
	if err != nil {
		return "", err
	}
	if !obs.Exists || !obs.Running || !obs.Owned {
		r.Info(ctx, "no running instance; starting the app")
		_, gen, err := c.ensureInstance(ctx, r, app, false)
		if err != nil {
			return "", err
		}
		return "instance", c.waitReady(ctx, r, app, gen)
	}
	service, argv := "app", []string{"/package/admin/s6/command/s6-svc", "-r", "/run/service/app"}
	if app.Runtime.Kind == domain.RuntimePHP {
		service, argv = "php-fpm", []string{"/package/admin/s6/command/s6-svc", "-2", "/run/service/php-fpm"}
	}
	if err := r.Phase(ctx, "reload "+service); err != nil {
		return "", err
	}
	res, err := c.appExec(ctx, app, obs.ContainerID, argv...)
	if err != nil || res.ExitCode != 0 {
		return "", Fail("reload-failed", "The new code is checked out; restart the app to load it.",
			"%s reload failed: %v %s", service, err, strings.TrimSpace(string(res.Stderr)))
	}
	r.Info(ctx, "%s reloaded", service)
	return service, c.waitReady(ctx, r, app, obs.Generation)
}

func deployFailure(app domain.App, g domain.GitSource, exit int, gitLog []string) error {
	detail := "no output"
	if len(gitLog) > 0 {
		detail = gitLog[len(gitLog)-1]
	}
	for _, line := range gitLog {
		if strings.Contains(line, "Permission denied") || strings.HasPrefix(line, "fatal:") || strings.HasPrefix(line, "ERROR:") {
			detail = line
			break
		}
	}
	joined := strings.Join(gitLog, "\n")
	switch {
	case exit == 65:
		return Fail("git-origin-mismatch", "Point the app at the repository already checked out, or move the existing code away.", "%s", detail)
	case exit == 66:
		return Fail("code-dir-not-empty", "Deploy only clones into an empty code directory. Move the existing files out of app/ or initialize them as a checkout of the repository.", "%s", detail)
	case strings.Contains(joined, "Permission denied (publickey)"), strings.Contains(joined, "Repository not found"),
		strings.Contains(joined, "Could not read from remote repository"):
		return Fail("git-access-denied", fmt.Sprintf("Add this read-only deploy key to the repository (%s) and deploy again: %s", g.Fingerprint, g.PublicKey),
			"app %s could not read %s: %s", app.Slug, g.RepoURL, detail)
	case strings.Contains(joined, "Host key verification failed"), strings.Contains(joined, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		return Fail("git-host-key", "The git host key differs from the one pinned in ~/.ssh/known_hosts of the app. Verify the host and edit that file if the change is legitimate.",
			"host key check failed for %s", g.RepoURL)
	case strings.Contains(joined, "Remote branch") && strings.Contains(joined, "not found"),
		strings.Contains(joined, "couldn't find remote ref"):
		return Fail("git-branch-missing", "Check the configured branch name.", "branch %s was not found in %s", g.Branch, g.RepoURL)
	}
	return Fail("deploy-failed", "Inspect the git output in the operation events.", "git exited with status %d: %s", exit, detail)
}

func tailLines(b []byte, n int) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}
