package dataservices

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

func TestRedisACLHashesSecretsAndScopesPrefixes(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	root := t.TempDir()
	m := &Manager{Layout: platform.Layout{Root: root}, Names: runtime.Names{StackID: "s", StackName: "t"}}
	svc := domain.DataService{Name: "redis", Engine: domain.EngineRedis}
	if err := m.EnsureSecrets(svc, false); err != nil {
		t.Fatal(err)
	}
	admin, _ := m.AdminPassword("redis")
	if err := m.WriteRedisConfig("redis", []RedisUser{{Username: "app-a1", Password: "plaintext-app-secret", Prefix: "shop:"}}); err != nil {
		t.Fatal(err)
	}
	acl, _ := os.ReadFile(filepath.Join(root, "services/redis/conf/users.acl"))
	s := string(acl)
	if strings.Contains(s, "plaintext-app-secret") || strings.Contains(s, admin) {
		t.Fatal("ACL file must contain only password hashes")
	}
	if !strings.Contains(s, "user app-a1 on sanitize-payload #") || !strings.Contains(s, "~shop:*") || !strings.Contains(s, "-@admin") || !strings.HasSuffix(strings.TrimSpace(s), "-@dangerous +info") {
		t.Fatal(s)
	}
	if err := m.WriteRedisConfig("redis", []RedisUser{{Username: "app-a1", Password: "x", Prefix: "*"}}); err == nil {
		t.Fatal("wildcard prefix accepted")
	}
}

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
	redis := domain.DataService{Name: "redis", Engine: domain.EngineRedis}
	if err := m.EnsureSecrets(redis, true); err != nil {
		t.Fatalf("redis secret is regenerable: %v", err)
	}
}
