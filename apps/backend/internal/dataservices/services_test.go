package dataservices

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
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

// A database-level grant on an unescaped name is a pattern: "_" matches any
// character, so a_b_main would also cover another app's a1b_main.
func TestMySQLGrantIsExactAndRevokesWildcard(t *testing.T) {
	if got := GrantPattern("a_b_main"); got != `a\_b\_main` {
		t.Fatalf("pattern %q", got)
	}
	want := "GRANT ALL PRIVILEGES ON `a\\_b\\_main`.* TO 'ua'@'%';\n" +
		"REVOKE IF EXISTS ALL PRIVILEGES ON `a_b_main`.* FROM 'ua'@'%';\n"
	if got := MySQLGrant("a_b_main", "ua"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// Without a wildcard character the two forms are the same grant: revoking
	// the "legacy" one would remove the new one.
	if got := MySQLGrant("shop", "ua"); strings.Contains(got, "REVOKE") {
		t.Fatalf("revokes its own grant: %s", got)
	}
}

// A failing grant must never carry the binding password into the returned error,
// even when the server echoes the whole statement back (psql CONTEXT lines).
func TestProvisionErrorNeverContainsThePassword(t *testing.T) {
	password := strings.Repeat("p", 32)
	for _, engine := range []domain.Engine{domain.EngineMySQL, domain.EnginePostgres} {
		t.Run(string(engine), func(t *testing.T) {
			fake := docker.NewFake()
			fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
				var sql []byte
				if req.Stdin != nil {
					sql, _ = io.ReadAll(req.Stdin)
				}
				return docker.ExecResult{
					ExitCode: 1,
					Stderr:   append([]byte("ERROR: boom\nCONTEXT: SQL statement \""), sql...),
				}
			}
			m := &Manager{Engine: fake}
			svc := domain.DataService{Name: "svc", Engine: engine}
			err := m.ProvisionBinding(context.Background(), svc, "c1", domain.Binding{
				Username: "ua", Databases: []string{"shop"}, Password: password,
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), password) {
				t.Fatalf("error leaks the password: %v", err)
			}
			if !strings.Contains(err.Error(), "[redacted]") {
				t.Fatalf("error not redacted: %v", err)
			}
		})
	}
}

func TestPsqlRunsTerse(t *testing.T) {
	fake := docker.NewFake()
	var cmd []string
	fake.ExecHook = func(_ string, req docker.ExecRequest) docker.ExecResult {
		cmd = req.Cmd
		return docker.ExecResult{}
	}
	m := &Manager{Engine: fake}
	svc := domain.DataService{Name: "pg", Engine: domain.EnginePostgres}
	if _, err := m.SQL(context.Background(), svc, "c1", "", "SELECT 1;"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] == "-v" && cmd[i+1] == "VERBOSITY=terse" {
			return
		}
	}
	t.Fatalf("psql command %v lacks -v VERBOSITY=terse", cmd)
}
