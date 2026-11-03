package operations

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/edge"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func issueCert(t *testing.T, dir, name string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "bento_acme", "order.crt")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A host whose validation cannot succeed gets one attempt window, then the
// boot certificate until its backoff ends, however often the edge reloads.
func TestACMEHostWithoutCertificateBacksOffAcrossReloads(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}
	h := newHarnessWith(t, func(d *Deps) { d.Now = clock.Now })
	ctx := t.Context()
	enableEdge(t, h)
	const name = "grafana.example.com"
	_, op, err := h.c.CreateHost(ctx, HostInput{Name: name, Target: domain.HostTargetUpstream,
		Upstreams: []string{"http://10.0.0.5:3000"}, Route: domain.Route{TLS: domain.TLSACME}, Enabled: true}, "")
	h.mustSucceed(op, err)
	site := func() string {
		b, _ := os.ReadFile(filepath.Join(h.layout.EdgeConfDir(), "live", "sites", edge.RouteName(name)+".conf"))
		return string(b)
	}
	status := func() ACMEHostStatus {
		st, err := h.c.ACMEStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return st[name]
	}
	if !strings.Contains(site(), "acme_certificate bento_acme;") || status().State != ACMERequesting {
		t.Fatalf("a new ACME host is requested at once: %+v", status())
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("drift inside the attempt window: %v %v", drift, err)
	}

	clock.Add(edge.ACMEAttemptWindow + time.Second)
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || !drift {
		t.Fatalf("a closed window without a certificate is drift: %v %v", drift, err)
	}
	got := submitEdgeApply(t, h)
	if got.State != store.OpSucceeded || !hasWarning(h, got.ID, "ACME: no certificate for "+name) {
		t.Fatalf("edge apply after the window: %s %s", got.State, got.ErrorMessage)
	}
	if strings.Contains(site(), "acme_certificate") || !strings.Contains(site(), "boot.crt") {
		t.Fatal("a failing host must be served with the boot certificate")
	}
	if st := status(); st.State != ACMEWaiting || st.Failures != 1 || !st.RetryAt.Equal(clock.Now().Add(time.Hour)) {
		t.Fatalf("waiting status: %+v", st)
	}
	// Reloads while waiting (other apps' operations) do not retry the host.
	for range 3 {
		clock.Add(10 * time.Minute)
		submitEdgeApply(t, h)
		if strings.Contains(site(), "acme_certificate") {
			t.Fatal("an edge apply during the backoff must not request the certificate again")
		}
	}

	clock.Add(time.Hour)
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || !drift {
		t.Fatalf("the end of the backoff is drift: %v %v", drift, err)
	}
	submitEdgeApply(t, h)
	if !strings.Contains(site(), "acme_certificate bento_acme;") || status().State != ACMERequesting {
		t.Fatal("a new window requests the certificate again")
	}

	// The certificate arrives: state cleared, reported issued.
	issueCert(t, h.layout.EdgeACMEDir(), name, clock.Now().Add(90*24*time.Hour))
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || !drift {
		t.Fatalf("issuance clears state, which is drift: %v %v", drift, err)
	}
	submitEdgeApply(t, h)
	if st := status(); st.State != ACMEIssued || st.Failures != 0 {
		t.Fatalf("issued status: %+v", st)
	}
	if st, _ := h.c.acmeState(ctx); len(st) != 0 {
		t.Fatalf("no state is kept for an issued host: %+v", st)
	}
	if drift, err := h.c.EdgeConfigDrift(ctx); err != nil || drift {
		t.Fatalf("drift after issuance: %v %v", drift, err)
	}
}

func TestSavingAWaitingACMEHostRetriesNow(t *testing.T) {
	clock := &testClock{now: time.Now().UTC()}
	h := newHarnessWith(t, func(d *Deps) { d.Now = clock.Now })
	ctx := t.Context()
	enableEdge(t, h)
	in := HostInput{Name: "x.example.com", Target: domain.HostTargetUpstream,
		Upstreams: []string{"http://10.0.0.5:3000"}, Route: domain.Route{TLS: domain.TLSACME}, Enabled: true}
	_, op, err := h.c.CreateHost(ctx, in, "")
	h.mustSucceed(op, err)
	clock.Add(edge.ACMEAttemptWindow + time.Second)
	submitEdgeApply(t, h)
	if st, _ := h.c.ACMEStatus(ctx); st[in.Name].State != ACMEWaiting {
		t.Fatalf("not waiting: %+v", st[in.Name])
	}
	time.Sleep(10 * time.Millisecond) // the host's UpdatedAt must move
	_, op, err = h.c.UpdateHost(ctx, in, "")
	h.mustSucceed(op, err)
	if st, _ := h.c.ACMEStatus(ctx); st[in.Name].State != ACMERequesting || st[in.Name].Failures != 0 {
		t.Fatalf("a saved host must be retried at once: %+v", st[in.Name])
	}
}
