package cli

import (
	"strings"
	"testing"
)

func TestParsePublicListen(t *testing.T) {
	got, err := ParsePublicListen(DefaultPublicListen)
	if err != nil || len(got) != 2 || appsPort(got) != 7781 || got[0].String() != "127.0.0.1:7781" {
		t.Fatalf("defaults: %v %v", got, err)
	}
	if got, err := ParsePublicListen([]string{"off"}); err != nil || got != nil || appsPort(got) != 0 {
		t.Fatalf("off: %v %v", got, err)
	}
	if got, err := ParsePublicListen([]string{"0.0.0.0:9000", "localhost:9001", "[::1]:9002"}); err != nil || len(got) != 3 ||
		got[1].String() != "127.0.0.1:9001" || appsPort(got) != 0 {
		t.Fatalf("explicit: %v %v", got, err)
	}
	for _, bad := range []string{"7781", "example.com:7781", "apps:0", "127.0.0.1:99999", "apps:x"} {
		if _, err := ParsePublicListen([]string{bad}); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if _, err := ParsePublicListen([]string{"apps:7781", "apps:7781"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicates must be refused: %v", err)
	}
}
