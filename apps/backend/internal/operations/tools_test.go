package operations

import (
	"errors"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
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

func TestOpenToolUsesExistingBuild(t *testing.T) {
	h := newHarness(t)
	app := h.runningApp("shop")
	builds := len(h.fake.Builds)
	tool, err := h.c.OpenTool(t.Context(), app, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tool.Close()
	if len(h.fake.Builds) != builds {
		t.Fatalf("a tool session built an image: %d -> %d builds", builds, len(h.fake.Builds))
	}
}

func TestOpenToolRefusesWithoutABuild(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	app := h.runningApp("shop")
	h.planned(app)
	assigned := func() (store.AppRuntimeBuild, error) {
		return store.GetAppRuntimeBuild(ctx, h.c.Store.DB(), app.ID)
	}
	check := func(label string) {
		t.Helper()
		builds, calls := len(h.fake.Builds), h.fake.CallCount("BuildImage")
		before, beforeErr := assigned()
		tool, err := h.c.OpenTool(ctx, app, time.Minute)
		if tool != nil {
			tool.Close()
			t.Fatalf("%s: a tool opened without a usable build", label)
		}
		if oe, ok := errors.AsType[*OpError](err); !ok || oe.Code != "image-not-ready" {
			t.Fatalf("%s: want image-not-ready, got %v", label, err)
		}
		if len(h.fake.Builds) != builds || h.fake.CallCount("BuildImage") != calls {
			t.Fatalf("%s: OpenTool built an image", label)
		}
		after, afterErr := assigned()
		if before != after || (beforeErr == nil) != (afterErr == nil) {
			t.Fatalf("%s: build assignment changed: %+v (%v) -> %+v (%v)", label, before, beforeErr, after, afterErr)
		}
	}
	// An upgrade or sweep removed the image of the assigned build.
	h.fake.Images = map[string]string{}
	check("image gone")
}
