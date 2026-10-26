package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
)

// runFileScripts answers the fake engine's exec by running the real file
// scripts with local bash, with code standing in for the app's ~/app.
func runFileScripts(t *testing.T, f *docker.Fake, code string) {
	f.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
		argv := req.Cmd[1:] // drop bento-exec
		if len(argv) < 6 || argv[0] != "bash" || argv[3] != "bento-files" {
			return docker.ExecResult{}
		}
		argv[4] = code
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = req.Stdin
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		code := 0
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Errorf("run script: %v", err)
			}
			code = ee.ExitCode()
		}
		return docker.ExecResult{ExitCode: code, Stdout: out.Bytes(), Stderr: errb.Bytes()}
	}
}

func TestAppFiles(t *testing.T) {
	c, h := newServer(t)
	c.login()
	createShop(t, c, h.Wait)
	operate := c.createToken("ops", dto.TokenScopeOperate)
	admin := c.createToken("admin", dto.TokenScopeAdmin)

	tmp := t.TempDir()
	code := filepath.Join(tmp, "app")
	for name, body := range map[string]string{
		"app/index.php":           "<?php\necho 'hi';\n",
		"app/src/Config.php":      "<?php\nreturn ['debug' => false];\n",
		"app/.env":                "APP_KEY=hidden\n",
		"app/.git/config":         "[core]\n",
		"app/logo.png":            "\x89PNG\x00\x00",
		"app/big.txt":             strings.Repeat("é", 40),
		"credentials.env":         "DB_PASSWORD=outside\n",
		"app/storage/.gitignore":  "*\n",
		"app/storage/logs/a.log":  "line\n",
		"app/storage/logs/b.log":  "line\n",
		"app/vendor/autoload.php": "<?php\n",
	} {
		p := filepath.Join(tmp, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o640)
	}
	os.Symlink("../credentials.env", filepath.Join(code, "creds"))
	os.Symlink(".env", filepath.Join(code, "dotenv"))
	os.Symlink("src/Config.php", filepath.Join(code, "cfg.php"))
	runFileScripts(t, h.Fake, code)

	get := func(tok, path string) (int, string) {
		resp, body := c.bearer(tok, "GET", path, "")
		return resp.StatusCode, body
	}
	q := func(p string) string { return url.QueryEscape(p) }

	if st, _ := get(operate, "/api/v1/apps/shop/files"); st != http.StatusForbidden {
		t.Fatalf("operate list -> %d", st)
	}
	st, body := get(admin, "/api/v1/apps/shop/files")
	var list dto.AppFileList
	json.Unmarshal([]byte(body), &list)
	var names []string
	for _, e := range list.Entries {
		names = append(names, e.Name+":"+string(e.Type))
	}
	if st != 200 || strings.Join(names, ",") != "src:dir,storage:dir,vendor:dir,big.txt:file,cfg.php:symlink,creds:symlink,dotenv:symlink,index.php:file,logo.png:file" {
		t.Fatalf("list -> %d %v %s", st, names, body)
	}
	if st, body := get(admin, "/api/v1/apps/shop/files?path=storage"); st != 200 || strings.Contains(body, "gitignore") {
		t.Fatalf("hidden entries must not be listed: %d %s", st, body)
	}

	st, body = get(admin, "/api/v1/apps/shop/files/content?path="+q("src/Config.php"))
	var fc dto.AppFileContent
	json.Unmarshal([]byte(body), &fc)
	if st != 200 || fc.Content != "<?php\nreturn ['debug' => false];\n" || !fc.EOF || len(fc.Sha256) != 64 {
		t.Fatalf("read -> %d %s", st, body)
	}
	if st, body := get(admin, "/api/v1/apps/shop/files/content?path=cfg.php"); st != 200 || !strings.Contains(body, "debug") {
		t.Fatalf("symlink inside the app dir -> %d %s", st, body)
	}
	// A window that ends inside a two-byte rune stops before it.
	st, body = get(admin, "/api/v1/apps/shop/files/content?path=big.txt&limit=5")
	json.Unmarshal([]byte(body), &fc)
	if st != 200 || fc.Content != "éé" || fc.Length != 4 || fc.EOF {
		t.Fatalf("window -> %d %s", st, body)
	}

	// Outside ~/app, hidden, binary and missing paths are refused.
	for p, want := range map[string]int{
		"../credentials.env": 400, "/etc/passwd": 400, ".env": 400, ".git/config": 400, "storage/.gitignore": 400,
		"creds": 400, "dotenv": 400, "logo.png": 400, "src": 400, "missing.php": 404,
	} {
		if st, body := get(admin, "/api/v1/apps/shop/files/content?path="+q(p)); st != want {
			t.Errorf("read %s -> %d %s", p, st, body)
		}
	}
	if st, body := get(admin, "/api/v1/apps/shop/files?path=creds"); st != 400 {
		t.Errorf("list through symlink to outside -> %d %s", st, body)
	}

	put := func(body string) (int, string) {
		resp, out := c.bearer(admin, "PUT", "/api/v1/apps/shop/files/content", body)
		return resp.StatusCode, out
	}
	patch := func(body string) (int, string) {
		resp, out := c.bearer(admin, "PATCH", "/api/v1/apps/shop/files/content", body)
		return resp.StatusCode, out
	}
	if resp, _ := c.bearer(operate, "PUT", "/api/v1/apps/shop/files/content", `{"path":"x.php","content":"x"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operate write -> %d", resp.StatusCode)
	}
	// Create, then a create over an existing file is a conflict.
	if st, body := put(`{"path":"src/New.php","content":"<?php\n"}`); st != http.StatusCreated || !strings.Contains(body, `"created":true`) {
		t.Fatalf("create -> %d %s", st, body)
	}
	if st, _ := put(`{"path":"src/New.php","content":"x"}`); st != http.StatusConflict {
		t.Fatalf("create over existing -> %d", st)
	}
	// Replace needs the current sha256.
	_, body = get(admin, "/api/v1/apps/shop/files/content?path=index.php")
	json.Unmarshal([]byte(body), &fc)
	if st, _ := put(`{"path":"index.php","content":"x","expectedSha256":"` + strings.Repeat("0", 64) + `"}`); st != http.StatusConflict {
		t.Fatalf("stale replace -> %d", st)
	}
	if st, body := put(`{"path":"index.php","content":"<?php\necho 'bye';\n","expectedSha256":"` + fc.Sha256 + `"}`); st != 200 {
		t.Fatalf("replace -> %d %s", st, body)
	}
	if b, _ := os.ReadFile(filepath.Join(code, "index.php")); string(b) != "<?php\necho 'bye';\n" {
		t.Fatalf("file now %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(code, "index.php")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("replace must keep the mode, got %v", fi.Mode().Perm())
	}
	// Writes through symlinks, into hidden paths or outside are refused.
	for _, b := range []string{
		`{"path":"cfg.php","content":"x","expectedSha256":"` + strings.Repeat("0", 64) + `"}`,
		`{"path":".env","content":"x"}`,
		`{"path":"../credentials.env","content":"x"}`,
		`{"path":"creds/x","content":"x"}`,
		`{"path":"nodir/x.php","content":"x"}`,
		`{"path":"a.php","content":"DB_PASSWORD=[redacted]"}`,
		`{"path":"a.php","content":"x","expectedSha256":"nothex"}`,
	} {
		if st, out := put(b); st < 400 || st >= 500 {
			t.Errorf("write %s -> %d %s", b, st, out)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(tmp, "credentials.env")); string(b) != "DB_PASSWORD=outside\n" {
		t.Fatal("outside file changed")
	}

	// Replace edits the real bytes: one exact match, or all with all.
	if st, body := patch(`{"path":"src/Config.php","oldText":"false","newText":"true"}`); st != 200 || !strings.Contains(body, `"replacements":1`) {
		t.Fatalf("replace text -> %d %s", st, body)
	}
	if b, _ := os.ReadFile(filepath.Join(code, "src/Config.php")); !strings.Contains(string(b), "'debug' => true") {
		t.Fatalf("config now %q", b)
	}
	if st, _ := patch(`{"path":"storage/logs/a.log","oldText":"nope","newText":"x"}`); st != 400 {
		t.Fatalf("missing text -> %d", st)
	}
	os.WriteFile(filepath.Join(code, "dup.txt"), []byte("a a a"), 0o644)
	if st, _ := patch(`{"path":"dup.txt","oldText":"a","newText":"b"}`); st != 400 {
		t.Fatalf("ambiguous text -> %d", st)
	}
	if st, body := patch(`{"path":"dup.txt","oldText":"a","newText":"b","all":true}`); st != 200 || !strings.Contains(body, `"replacements":3`) {
		t.Fatalf("replace all -> %d %s", st, body)
	}
	if st, _ := patch(`{"path":"dup.txt","oldText":"b","newText":"c","expectedSha256":"` + strings.Repeat("0", 64) + `"}`); st != http.StatusConflict {
		t.Fatalf("stale replace text -> %d", st)
	}
	entries, _ := os.ReadDir(code)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".bento-edit") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
