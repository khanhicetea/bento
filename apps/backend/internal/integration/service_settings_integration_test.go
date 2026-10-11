package integration

import (
	"context"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Tuned settings reach the running servers, survive on the same volume, and
// a restart keeps them.
func TestIntegrationServiceSettings(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cases := []struct {
		engine           domain.Engine
		version, name    string
		settings         map[string]string
		query, wantValue string
	}{
		{domain.EngineMySQL, "8.4", "mysql84",
			map[string]string{"innodb_buffer_pool_size": "256M", "max_connections": "77"},
			"SELECT CONCAT(@@innodb_buffer_pool_size, ',', @@max_connections);", "268435456,77"},
		{domain.EnginePostgres, "18", "postgres18",
			map[string]string{"shared_buffers": "64M", "work_mem": "8M", "max_connections": "55"},
			"SELECT current_setting('shared_buffers') || ',' || current_setting('work_mem') || ',' || current_setting('max_connections');",
			"64MB,8MB,55"},
	}
	for _, tc := range cases {
		_, op, err := e.c.CreateService(ctx, tc.engine, tc.version, "")
		e.wait(op, err)
		svc, _ := store.GetService(ctx, e.s.DB(), tc.name)
		// A marker row proves the data survives the container replacement.
		mark := "CREATE TABLE bento_mark(id int); INSERT INTO bento_mark VALUES (1);"
		if tc.engine == domain.EngineMySQL {
			mark = "CREATE DATABASE bento_mark; CREATE TABLE bento_mark.t(id int); INSERT INTO bento_mark.t VALUES (1);"
		}
		e.sql(svc.DataService, mark)

		op, err = e.c.ConfigureService(ctx, tc.name, tc.settings, "")
		e.wait(op, err)
		if got := e.sql(svc.DataService, tc.query); got != tc.wantValue {
			t.Fatalf("%s settings: got %q, want %q", tc.name, got, tc.wantValue)
		}
		op, err = e.c.RestartService(ctx, tc.name, "")
		e.wait(op, err)
		if got := e.sql(svc.DataService, tc.query); got != tc.wantValue {
			t.Fatalf("%s after restart: got %q, want %q", tc.name, got, tc.wantValue)
		}
		count := "SELECT count(*) FROM bento_mark;"
		if tc.engine == domain.EngineMySQL {
			count = "SELECT count(*) FROM bento_mark.t;"
		}
		if got := e.sql(svc.DataService, count); got != "1" {
			t.Fatalf("%s lost data: %q", tc.name, got)
		}
	}
}

func (e *env) sql(s domain.DataService, q string) string {
	e.t.Helper()
	ins, err := e.sdk.Inspect(context.Background(), e.c.Names.ServiceContainer(s.Name))
	if err != nil || ins == nil {
		e.t.Fatalf("inspect %s: %v", s.Name, err)
	}
	out, err := e.c.Data().SQL(context.Background(), s, ins.ID, "", q)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}
