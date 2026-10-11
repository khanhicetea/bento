package operations

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// streamingHook replays the script's output through the request's writers in
// a fixed interleaving, the way the SDK engine streams a real exec.
func streamingHook(exit int, chunks []string, script *docker.ExecRequest) func(string, docker.ExecRequest) docker.ExecResult {
	return func(_ string, r docker.ExecRequest) docker.ExecResult {
		switch {
		case slices.Contains(r.Cmd, "bento-deploy"):
			return docker.ExecResult{Stdout: []byte("BENTO_COMMIT=" + testCommit + "\n")}
		case slices.ContainsFunc(r.Cmd, func(s string) bool { return strings.HasSuffix(s, "/"+DeployScriptName) }):
			*script = r
			for _, c := range chunks {
				if after, ok := strings.CutPrefix(c, "err:"); ok {
					r.Stderr.Write([]byte(after))
				} else {
					r.Stdout.Write([]byte(c))
				}
			}
			return docker.ExecResult{ExitCode: exit}
		}
		return docker.ExecResult{Stdout: []byte("ready")}
	}
}

func TestDeployScriptLogKeepsOrderAndSummarizesTheRealError(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	writeDeployScript(t, h, app, 0o755)
	var script docker.ExecRequest
	// Composer reports progress on stderr before artisan fails, like a
	// Laravel deploy whose route cache breaks.
	h.fake.ExecHook = streamingHook(1, []string{
		"err:Installing dependencies\n",
		"err:Use the `composer fund` command to find out more!\n",
		"\x1b[37;44m INFO \x1b[39;49m Caching framework bootstrap.\n",
		"config \x1b[90m....\x1b[39m \x1b[31;1mFAIL\x1b[39;22m\n",
		"err:In web.php line 8:\n",
		"err:  Constant E2E_MARK already defined",
	}, &script)
	op, err := h.c.DeployApp(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if got.ErrorCode != "deploy-script-failed" || !strings.HasSuffix(got.ErrorMessage, ": Constant E2E_MARK already defined") {
		t.Fatalf("summary must be the script's last line: %s %s", got.ErrorCode, got.ErrorMessage)
	}
	events, _ := store.ListEvents(ctx, h.store.DB(), got.ID, 0)
	var lines []string
	for _, e := range events {
		if after, ok := strings.CutPrefix(e.Message, "deploy.sh: "); ok {
			lines = append(lines, e.Level+" "+after)
		}
	}
	want := []string{
		"warn Installing dependencies",
		"warn Use the `composer fund` command to find out more!",
		"info INFO  Caching framework bootstrap.",
		"info config .... FAIL",
		"warn In web.php line 8:",
		"warn Constant E2E_MARK already defined",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("journal must keep arrival order without escape codes:\n got %q\nwant %q", lines, want)
	}
	if script.OutputLimit != 0 {
		t.Fatal("streamed output is bounded by the log, not by capture")
	}
}

func TestScriptLogBoundsVolumeAndKeepsTheTail(t *testing.T) {
	h := newHarness(t)
	app := h.createApp("shop")
	ctx := t.Context()
	if _, err := h.c.SetGitSource(ctx, app.ID, GitSourceInput{RepoURL: "git@github.com:o/r.git", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	writeDeployScript(t, h, app, 0o755)
	var chunks []string
	for i := range 1000 {
		chunks = append(chunks, fmt.Sprintf("line %d\n", i))
	}
	chunks = append(chunks, "err:progress 10%\rprogress 100%\n", "err:fatal: done\n")
	var script docker.ExecRequest
	h.fake.ExecHook = streamingHook(2, chunks, &script)
	op, err := h.c.DeployApp(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got := h.wait(op)
	if !strings.HasSuffix(got.ErrorMessage, ": fatal: done") {
		t.Fatalf("summary: %s", got.ErrorMessage)
	}
	events, _ := store.ListEvents(ctx, h.store.DB(), got.ID, 0)
	var lines []string
	for _, e := range events {
		if after, ok := strings.CutPrefix(e.Message, "deploy.sh: "); ok {
			lines = append(lines, after)
		}
	}
	// 120 live lines, the omission marker, then the last 40 lines.
	if len(lines) != scriptLogLive+1+scriptLogTail {
		t.Fatalf("journalled %d lines", len(lines))
	}
	if lines[scriptLogLive-1] != "line 119" || lines[scriptLogLive] != "… 842 lines omitted …" {
		t.Fatalf("live budget boundary: %q %q", lines[scriptLogLive-1], lines[scriptLogLive])
	}
	if tail := lines[len(lines)-3:]; !slices.Equal(tail, []string{"line 999", "progress 100%", "fatal: done"}) {
		t.Fatalf("tail: %q", tail)
	}
}
