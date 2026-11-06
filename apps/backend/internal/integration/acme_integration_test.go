package integration

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
)

// The first ACME host on a running edge is a reload, not a recreate. nginx
// 1.30 segfaults (exit 139) when a reload adds a load_module, so the edge
// loads the ACME module even with no ACME host; adding one must leave the
// same edge container running. The issuer is unreachable: issuance is not
// what this checks.
func TestIntegrationEdgeFirstACMEHostKeepsEdgeRunning(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	httpPort, httpsPort := freePort(t), freePort(t)
	s := domain.DefaultEdgeSettings()
	s.Enabled, s.Bind, s.HTTPPort, s.HTTPSPort = true, "127.0.0.1", httpPort, httpsPort
	s.ACMEURL, s.ACMEEmail = "https://acme.invalid/dir", "ops@bento.example.org"
	op, err := e.c.ConfigureEdge(ctx, s, "")
	e.wait(op, err)
	before, running := e.edgeID()
	if !running {
		t.Fatal("edge must run before the ACME host")
	}

	_, op, err = e.c.CreateHost(ctx, operations.HostInput{Name: "first-acme.example.org", Target: domain.HostTargetRedirect,
		RedirectTo: "elsewhere.example.org", Enabled: true, Route: domain.Route{TLS: domain.TLSACME}}, "")
	e.wait(op, err)
	time.Sleep(3 * time.Second)
	after, running := e.edgeID()
	if !running {
		t.Fatal("the edge died on the reload that added the first ACME host")
	}
	if after != before {
		t.Fatal("adding an ACME host must reload the edge, not recreate it")
	}
	if !edgeAnswers(httpPort) {
		t.Fatal("edge must keep serving")
	}
	// Until its first certificate is issued (never, with this issuer) the host
	// presents the pending placeholder instead of failing the handshake.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp",
		fmt.Sprintf("127.0.0.1:%d", httpsPort),
		&tls.Config{ServerName: "first-acme.example.org", InsecureSkipVerify: true}) //nolint:gosec // placeholder is self-signed
	if err != nil {
		t.Fatalf("TLS handshake for an ACME host awaiting its certificate: %v", err)
	}
	defer conn.Close()
	if cn := conn.ConnectionState().PeerCertificates[0].Subject.CommonName; cn != "bento-pending" {
		t.Fatalf("awaiting host presented %q, want the bento-pending placeholder", cn)
	}
}
