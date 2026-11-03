package api

import (
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
)

func TestHostCertificateIsReportedForLiveACMEHostsOnly(t *testing.T) {
	acme := domain.Host{Name: "a.example.com", Target: domain.HostTargetUpstream, Upstreams: []string{"http://10.0.0.1"},
		Route: domain.Route{TLS: domain.TLSACME}, Enabled: true}
	retry := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	got := hostToDTO(acme, "", true, &operations.ACMEHostStatus{State: operations.ACMEWaiting, RetryAt: retry, Failures: 2})
	if got.Certificate == nil || got.Certificate.State != dto.HostCertificateStateWaiting ||
		got.Certificate.RetryAt == "" || got.Certificate.Failures != 2 {
		t.Fatalf("waiting host: %+v", got.Certificate)
	}
	if got := hostToDTO(acme, "", true, nil); got.Certificate == nil ||
		got.Certificate.State != dto.HostCertificateStateRequesting {
		t.Fatalf("a live ACME host with no state yet is requesting: %+v", got.Certificate)
	}
	if got := hostToDTO(acme, "", false, nil); got.Certificate != nil {
		t.Fatal("a host the edge does not serve has no certificate state")
	}
	plain := acme
	plain.Route.TLS = domain.TLSSelfSigned
	if got := hostToDTO(plain, "", true, nil); got.Certificate != nil {
		t.Fatal("only ACME hosts report a certificate")
	}
}
