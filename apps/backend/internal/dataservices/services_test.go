package dataservices

import (
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
	if err := m.EnsureSecrets(svc); err != nil {
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
