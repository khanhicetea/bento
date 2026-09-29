package cli

import (
	"strings"
	"testing"
)

func TestListenIsLoopbackOnly(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:7780", "localhost:7780", "[::1]:7780", "127.0.0.2:80"} {
		if _, _, err := ValidateListen(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:7780", ":7780", "192.168.1.5:7780", "[::]:7780", "example.com:80", "127.0.0.1:0", "127.0.0.1"} {
		if _, _, err := ValidateListen(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	o := DefaultOrigins("127.0.0.1", 7780)
	if len(o) != 2 || o[0] != "http://127.0.0.1:7780" || o[1] != "http://localhost:7780" {
		t.Fatal(o)
	}
	for _, bad := range []string{"*", "http://x/path", "javascript:alert(1)", "http://"} {
		if validateOrigin(bad) == nil {
			t.Errorf("origin %q accepted", bad)
		}
	}
}

func TestServeRejectsOutOfRangeOpConcurrency(t *testing.T) {
	for _, n := range []int{-1, MaxOpConcurrency + 1} {
		err := Serve(ServeOptions{Root: t.TempDir(), Listen: "127.0.0.1:7780", OpConcurrency: n})
		if err == nil || !strings.Contains(err.Error(), "--op-concurrency") {
			t.Errorf("op-concurrency %d: got %v", n, err)
		}
	}
}

func TestValidateOriginAcceptsBareOriginsWithStableMessage(t *testing.T) {
	for _, ok := range []string{"http://localhost:5173", "https://bento.example.com", "https://bento.example.com/"} {
		if err := validateOrigin(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"%zz", "ftp://host", "http://host?q=1", "http://host/path"} {
		want := `invalid origin "` + bad + `" (expected scheme://host[:port])`
		if err := validateOrigin(bad); err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %s", bad, err, want)
		}
	}
}
