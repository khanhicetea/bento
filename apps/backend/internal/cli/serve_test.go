package cli

import "testing"

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
