package edge

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
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func TestPlanACMEBacksOffAcrossRenders(t *testing.T) {
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	saved := t0.Add(-time.Hour)
	hosts := []ACMEHost{{Name: "a.example.com", UpdatedAt: saved}}
	// A new host is tried at once.
	hold, st, failed := PlanACME(t0, hosts, nil, nil)
	if hold["a.example.com"] || len(failed) != 0 || !st["a.example.com"].AttemptFrom.Equal(t0) {
		t.Fatalf("first render: hold=%v st=%+v", hold, st)
	}
	// Renders inside the window (every reload) change nothing.
	if hold, next, _ := PlanACME(t0.Add(4*time.Minute), hosts, nil, st); hold["a.example.com"] || !SameACMEState(st, next) {
		t.Fatal("a render inside the attempt window must neither hold nor change state")
	}
	// The window closes without a certificate: held for an hour.
	hold, st, failed = PlanACME(t0.Add(ACMEAttemptWindow), hosts, nil, st)
	if !hold["a.example.com"] || len(failed) != 1 || failed[0].Failures != 1 ||
		!st["a.example.com"].RetryAt.Equal(t0.Add(ACMEAttemptWindow+time.Hour)) {
		t.Fatalf("closed window: hold=%v failed=%+v st=%+v", hold, failed, st)
	}
	if hold, _, failed := PlanACME(t0.Add(30*time.Minute), hosts, nil, st); !hold["a.example.com"] || len(failed) != 0 {
		t.Fatal("still held while backing off, without a new failure")
	}
	// The backoff ends: a new window; it fails again and the wait doubles.
	retry := st["a.example.com"].RetryAt
	hold, st, _ = PlanACME(retry, hosts, nil, st)
	if hold["a.example.com"] || !st["a.example.com"].AttemptFrom.Equal(retry) {
		t.Fatal("a new window opens when the backoff ends")
	}
	_, st, failed = PlanACME(retry.Add(ACMEAttemptWindow), hosts, nil, st)
	if failed[0].Failures != 2 || !failed[0].RetryAt.Equal(retry.Add(ACMEAttemptWindow+2*time.Hour)) {
		t.Fatalf("second failure: %+v", failed)
	}
	// Saving the host retries now.
	resaved := []ACMEHost{{Name: "a.example.com", UpdatedAt: retry.Add(time.Hour)}}
	hold, st2, _ := PlanACME(retry.Add(time.Hour), resaved, nil, st)
	if hold["a.example.com"] || st2["a.example.com"].Failures != 0 {
		t.Fatalf("re-saved host: hold=%v st=%+v", hold, st2)
	}
	// A valid certificate clears the state; an expired one does not count.
	now := retry.Add(time.Hour)
	if _, next, _ := PlanACME(now, hosts, map[string]time.Time{"a.example.com": now.Add(-time.Second)}, st); len(next) == 0 {
		t.Fatal("an expired certificate is not issuance")
	}
	hold, next, _ := PlanACME(now, hosts, map[string]time.Time{"a.example.com": now.Add(90 * 24 * time.Hour)}, st)
	if hold["a.example.com"] || len(next) != 0 {
		t.Fatalf("issued host: hold=%v next=%+v", hold, next)
	}
	// A host no longer served by ACME drops its state.
	if _, next, _ := PlanACME(now, nil, nil, st); len(next) != 0 {
		t.Fatal("state of a removed host must be dropped")
	}
}

func TestACMEBackoffDoublesToADay(t *testing.T) {
	want := []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for i, w := range want {
		if got := ACMEBackoff(i + 1); got != w {
			t.Errorf("ACMEBackoff(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func writeCert(t *testing.T, path string, names []string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIssuedCertificatesReadsTheStateDirectory(t *testing.T) {
	dir := t.TempDir()
	soon, later := time.Now().Add(24*time.Hour).Truncate(time.Second), time.Now().Add(60*24*time.Hour).Truncate(time.Second)
	writeCert(t, filepath.Join(dir, "bento_acme", "x1.crt"), []string{"a.example.com"}, soon)
	writeCert(t, filepath.Join(dir, "bento_acme", "x2.crt"), []string{"A.example.com", "b.example.com"}, later)
	_ = os.WriteFile(filepath.Join(dir, "bento_acme", "x3.crt"), []byte("not a certificate"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "bento_acme", "x2.key"), []byte("key"), 0o600)
	got := IssuedCertificates(dir)
	if !got["a.example.com"].Equal(later) || !got["b.example.com"].Equal(later) || len(got) != 2 {
		t.Fatalf("issued: %v", got)
	}
	if len(IssuedCertificates(filepath.Join(dir, "missing"))) != 0 {
		t.Fatal("a missing directory has no certificates")
	}
}

func TestRenderHeldACMEHostUsesBootCertificate(t *testing.T) {
	acme := domain.Route{TLS: domain.TLSACME}
	h1, h2 := upstreamHost("held.example.com", false, "http://10.0.0.1"), upstreamHost("ok.example.com", false, "http://10.0.0.1")
	h1.Route, h2.Route = acme, acme
	files, err := Render(Input{
		Settings: domain.EdgeSettings{HTTPPort: 80, HTTPSPort: 443, ACMEURL: "https://acme.test/dir"},
		Hosts:    []domain.Host{h1, h2},
		ACMEHold: map[string]bool{"held.example.com": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	held := string(files["sites/host-held.example.com.conf"])
	if strings.Contains(held, "acme_certificate") || !strings.Contains(held, "ssl_certificate "+CertsMount+"/boot.crt;") {
		t.Fatalf("held host must serve the boot certificate:\n%s", held)
	}
	if !strings.Contains(string(files["sites/host-ok.example.com.conf"]), "acme_certificate bento_acme;") {
		t.Fatal("other ACME hosts keep requesting")
	}
	if got := ACMEHosts(Input{Hosts: []domain.Host{h1, h2, upstreamHost("plain.example.com", false, "http://10.0.0.1")}}); len(got) != 2 {
		t.Fatalf("ACMEHosts: %+v", got)
	}
}
