package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// TestIntegrationAdminSQLErrorsDoNotEchoStatements checks, on real psql and
// mysql clients, that a failing administrative statement's error text never
// quotes the statement itself (psql runs with VERBOSITY=terse, which drops the
// LINE and CONTEXT lines). Grant statements embed a binding's password, so a
// client that echoed them would leak it into operation errors.
func TestIntegrationAdminSQLErrorsDoNotEchoStatements(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	const marker = "e2eMarkerNotForErrors7Q"
	for _, tc := range []struct {
		engine  domain.Engine
		version string
		sql     []string
	}{
		{domain.EnginePostgres, "17", []string{
			"SELECT '" + marker + "' FROM no_such_table;",
			"DO $$ BEGIN PERFORM '" + marker + "'; RAISE EXCEPTION 'boom'; END $$;",
			"DO $$ BEGIN EXECUTE 'CREATE ROLE x PASSWORD ''" + marker + "'' BROKEN'; END $$;",
		}},
		{domain.EngineMySQL, "8.4", []string{
			"SELECT '" + marker + "' FROM no_such_db.t;",
			"CREATE USER 'x'@'%' IDENTIFIED BY '" + marker + "' BROKEN;",
		}},
	} {
		svc, op, err := e.c.CreateService(ctx, tc.engine, tc.version, "")
		e.wait(op, err)
		ins, err := e.sdk.Inspect(ctx, e.c.Names.ServiceContainer(svc.Name))
		if err != nil || ins == nil {
			t.Fatalf("inspect %s: %v", svc.Name, err)
		}
		for _, sql := range tc.sql {
			_, err := e.c.Data().SQL(ctx, svc, ins.ID, "", sql)
			if err == nil {
				t.Fatalf("%s: %q unexpectedly succeeded", tc.engine, sql)
			}
			if strings.Contains(err.Error(), marker) {
				t.Fatalf("%s error echoes the statement: %v", tc.engine, err)
			}
			t.Logf("%s: %v", tc.engine, err)
		}
	}
}
