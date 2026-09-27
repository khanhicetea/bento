package operations

import (
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func TestExecRequestUsesCodeDirectory(t *testing.T) {
	app := domain.App{Slug: "shop", UID: 10000, GID: 10000}

	req, err := ExecRequestFor(app, []string{"pwd"}, "packages/api")
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Env) != 1 || req.Env[0] != "BENTO_EXEC_WORKDIR=/home/shop/app/packages/api" {
		t.Fatalf("unexpected exec environment: %v", req.Env)
	}

	req, err = ExecRequestFor(app, []string{"pwd"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if req.Env[0] != "BENTO_EXEC_WORKDIR=/home/shop/app" {
		t.Fatalf("unexpected default exec environment: %v", req.Env)
	}
}
