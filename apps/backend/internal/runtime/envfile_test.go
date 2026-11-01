package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The image's env loader must export every value verbatim. `IFS='=' read`
// dropped a single trailing '=' (base64 padding) before.
func TestEnvLoaderKeepsValuesVerbatim(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	loader, err := filepath.Abs("../../templates/images/common/rootfs/usr/local/lib/bento/env.bash")
	if err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(t.TempDir(), "app.env")
	content := strings.Join([]string{
		"# comment",
		"",
		"PAD1=c2VjcmV0=",
		"PAD2=c2VjcmV0MQ==",
		"MID=a=b=c",
		"EMPTY=",
		"BARE",
		"SPACES=  two words  ",
		"CRLF=value\r",
		"LAST=no-newline=",
	}, "\n")
	if err := os.WriteFile(env, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only the function is needed: the loader's own top-level calls tolerate
	// missing /etc/bento files.
	script := `source "$1" >/dev/null 2>&1; bento_load_env_file "$2" || exit $?
for k in PAD1 PAD2 MID EMPTY BARE SPACES CRLF LAST; do printf '%s=[%s]\n' "$k" "${!k-unset}"; done`
	out, err := exec.Command(bash, "-c", script, "bash", loader, env).CombinedOutput()
	if err != nil {
		t.Fatalf("loader failed: %v\n%s", err, out)
	}
	want := strings.Join([]string{
		"PAD1=[c2VjcmV0=]",
		"PAD2=[c2VjcmV0MQ==]",
		"MID=[a=b=c]",
		"EMPTY=[]",
		"BARE=[]",
		"SPACES=[  two words  ]",
		"CRLF=[value]",
		"LAST=[no-newline=]",
	}, "\n") + "\n"
	if string(out) != want {
		t.Fatalf("loaded env:\n%s\nwant:\n%s", out, want)
	}

	bad := filepath.Join(t.TempDir(), "bad.env")
	os.WriteFile(bad, []byte("1BAD=x\n"), 0o600)
	cmd := exec.Command(bash, "-c", `source "$1" >/dev/null 2>&1; bento_load_env_file "$2"`, "bash", loader, bad)
	if err := cmd.Run(); err == nil {
		t.Fatal("an invalid key must be refused")
	}
}
