package edge

import (
	"crypto/x509"
	"encoding/pem"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The nginx ACME module keeps an unissued certificate's retry state in
// memory and resets it to "request now" on every configuration reload. Bento
// reloads the edge on most app operations, so a host whose HTTP-01
// validation cannot succeed (DNS elsewhere, port 80 unreachable) would be
// retried on each reload and exhaust the CA's failed-validation limit
// (Let's Encrypt: 5 per hostname and account per hour). PlanACME keeps its
// own per-host state, persisted by the backend, that reloads do not reset.
const (
	// ACMEAttemptWindow is how long a host is rendered with acme_certificate
	// before a missing certificate counts as a failed attempt. Issuance
	// normally takes well under a minute; the module itself retries a failed
	// order after 60 s, so a window holds at most a few validations.
	ACMEAttemptWindow = 5 * time.Minute
	// ACMEFirstBackoff doubles per consecutive failure up to ACMEMaxBackoff.
	ACMEFirstBackoff = time.Hour
	ACMEMaxBackoff   = 24 * time.Hour
)

// ACMEHostState is the persisted issuance state of one ACME host that has no
// valid certificate yet.
type ACMEHostState struct {
	// AttemptFrom is when the current attempt window opened; zero while
	// backing off.
	AttemptFrom time.Time `json:"attemptFrom,omitzero"`
	Failures    int       `json:"failures,omitempty"`
	RetryAt     time.Time `json:"retryAt,omitzero"`
	// HostUpdatedAt is the host's UpdatedAt when the state was recorded; a
	// later save of the host (an operator retry) starts over.
	HostUpdatedAt time.Time `json:"hostUpdatedAt"`
}

// ACMEHost is one host the edge would serve with an ACME certificate.
type ACMEHost struct {
	Name      string
	UpdatedAt time.Time
}

// ACMEFailure reports a window that closed without a certificate.
type ACMEFailure struct {
	Host     string
	Failures int
	RetryAt  time.Time
}

// PlanACME decides which ACME hosts are held (served with the boot
// certificate) and returns the next persisted state. A host with a valid
// certificate is never held and has no state. It is pure: callers that only
// check for drift discard the next state, the edge apply persists it.
func PlanACME(
	now time.Time,
	hosts []ACMEHost,
	issued map[string]time.Time,
	prev map[string]ACMEHostState,
) (hold map[string]bool, next map[string]ACMEHostState, failed []ACMEFailure) {
	hold, next = map[string]bool{}, map[string]ACMEHostState{}
	for _, h := range hosts {
		if exp, ok := issued[h.Name]; ok && exp.After(now) {
			continue
		}
		st, ok := prev[h.Name]
		if !ok || h.UpdatedAt.After(st.HostUpdatedAt) {
			next[h.Name] = ACMEHostState{AttemptFrom: now, HostUpdatedAt: h.UpdatedAt}
			continue
		}
		switch {
		case !st.AttemptFrom.IsZero() && now.Sub(st.AttemptFrom) < ACMEAttemptWindow:
			// Inside the attempt window.
		case !st.AttemptFrom.IsZero():
			st.Failures++
			st.AttemptFrom = time.Time{}
			st.RetryAt = now.Add(ACMEBackoff(st.Failures))
			failed = append(failed, ACMEFailure{Host: h.Name, Failures: st.Failures, RetryAt: st.RetryAt})
			hold[h.Name] = true
		case now.Before(st.RetryAt):
			hold[h.Name] = true
		default:
			st.AttemptFrom = now // the backoff is over: open a new window
		}
		next[h.Name] = st
	}
	return hold, next, failed
}

// ACMEBackoff is the wait after the n-th consecutive failed window.
func ACMEBackoff(n int) time.Duration {
	d := ACMEFirstBackoff
	for i := 1; i < n && d < ACMEMaxBackoff; i++ {
		d *= 2
	}
	return min(d, ACMEMaxBackoff)
}

// SameACMEState reports whether two persisted states are equal.
func SameACMEState(a, b map[string]ACMEHostState) bool {
	return maps.EqualFunc(a, b, func(x, y ACMEHostState) bool {
		return x.AttemptFrom.Equal(y.AttemptFrom) && x.Failures == y.Failures && x.RetryAt.Equal(y.RetryAt) &&
			x.HostUpdatedAt.Equal(y.HostUpdatedAt)
	})
}

// IssuedCertificates scans the ACME module's state directory for issued
// certificates and returns, per DNS name, the latest expiry. Unreadable or
// foreign files are skipped: the scan only ever finds evidence of success.
func IssuedCertificates(dir string) map[string]time.Time {
	out := map[string]time.Time{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".crt") {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		block, _ := pem.Decode(b)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil
		}
		for _, name := range cert.DNSNames {
			name = strings.ToLower(name)
			if cert.NotAfter.After(out[name]) {
				out[name] = cert.NotAfter
			}
		}
		return nil
	})
	return out
}
