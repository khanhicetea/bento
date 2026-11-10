package remoteaccess

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/logger"
)

// TestNetworkTailcatRoundTrip runs the real child against Tailscale's public
// Tailcat relays and connects with the tailcat client library. It needs
// internet access (HTTPS and UDP), so it runs only with
// BENTO_TAILCAT_NETWORK_TESTS=1 (or the Docker integration suite flag).
func TestNetworkTailcatRoundTrip(t *testing.T) {
	requireRoot(t)
	if os.Getenv("BENTO_TAILCAT_NETWORK_TESTS") != "1" && os.Getenv("BENTO_DOCKER_TESTS") != "1" {
		t.Skip("set BENTO_TAILCAT_NETWORK_TESTS=1 to reach the public Tailcat relays")
	}
	m := newTestManager(t)
	m.Args = []string{TestRealChildArg}
	if _, _, err := EnsureKey(m.Layout, "pg17", 0, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(Target{Service: "pg17", Port: 5432}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	st := m.WaitSettled(ctx, "pg17")
	if st.State != StateListening {
		t.Fatalf("state %s: %s", st.State, st.Message)
	}
	k, err := ReadKey(m.Layout, "pg17")
	if err != nil || k.RegionID <= 0 {
		t.Fatalf("region not recorded: %+v %v", k.RegionID, err)
	}
	cl := &tailcat.Client{Server: tailcat.Addr(st.Address), Logf: logger.Discard}
	defer cl.Close()
	c, err := cl.DialTCPPort(ctx, 5432)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo through the tunnel: %q %v", buf, err)
	}
	// Any other port is refused by the server's filter or OnTCP.
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	if other, err := cl.DialTCPPort(dctx, 22); err == nil {
		other.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, rerr := other.Read(make([]byte, 1)); rerr == nil {
			t.Fatal("port 22 answered through the tunnel")
		}
		other.Close()
	}
	// Restart: the address is the same.
	m.Stop("pg17")
	if err := m.Start(Target{Service: "pg17", Port: 5432}); err != nil {
		t.Fatal(err)
	}
	if again := m.WaitSettled(ctx, "pg17"); again.State != StateListening || again.Address != st.Address {
		t.Fatalf("restart: %s, same address %v", again.State, again.Address == st.Address)
	}
}
