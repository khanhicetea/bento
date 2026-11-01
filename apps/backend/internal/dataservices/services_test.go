package dataservices

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

func TestIdentifiersAreStrict(t *testing.T) {
	for _, bad := range []string{"a'b", "a`b", "A", "1a", "a;drop", strings.Repeat("a", 64)} {
		if _, err := ident(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestServerArgsRenderAllowlistedSettings(t *testing.T) {
	pg := domain.DataService{Name: "postgres18", Engine: domain.EnginePostgres, Settings: domain.ServiceSettings{
		"shared_buffers": "2G", "work_mem": "512K", "max_connections": "200",
		"listen_addresses": "*", // not allowlisted: dropped, never passed through
	}}
	got := strings.Join(ServerArgs(pg), " ")
	if got != "postgres -c max_connections=200 -c shared_buffers=2GB -c work_mem=512kB" {
		t.Fatalf("postgres args %q", got)
	}
	my := domain.DataService{Name: "mysql84", Engine: domain.EngineMySQL, Settings: domain.ServiceSettings{
		"innodb_buffer_pool_size": "1G", "max_connections": "1 --skip-grant-tables",
	}}
	if got := strings.Join(ServerArgs(my), " "); got != "mysqld --innodb-buffer-pool-size=1G" {
		t.Fatalf("mysql args %q", got)
	}
	if ServerArgs(domain.DataService{Engine: domain.EngineMySQL}) != nil || SettingsFingerprint(domain.DataService{}) != "" {
		t.Fatal("untuned services keep the image command and carry no fingerprint")
	}
	if SettingsFingerprint(pg) == SettingsFingerprint(my) {
		t.Fatal("fingerprints must differ")
	}
}

func TestEnsureSecretsRefusesToRegenerateEstablishedSecret(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	m := &Manager{Layout: platform.Layout{Root: t.TempDir()}, Names: runtime.Names{StackID: "s", StackName: "t"}}
	svc := domain.DataService{Name: "mysql84", Engine: domain.EngineMySQL}
	if err := m.EnsureSecrets(svc, true); !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("established service without a secret: %v", err)
	}
	if err := m.EnsureSecrets(svc, false); err != nil {
		t.Fatal(err)
	}
	first, _ := m.AdminPassword("mysql84")
	if err := m.EnsureSecrets(svc, true); err != nil {
		t.Fatal(err)
	}
	if again, _ := m.AdminPassword("mysql84"); again != first || first == "" {
		t.Fatal("an existing secret must be kept")
	}
}
