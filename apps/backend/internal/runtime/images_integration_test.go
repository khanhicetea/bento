package runtime

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// requireDocker skips unless real Docker integration tests are explicitly enabled.
func requireDocker(t *testing.T) *docker.SDK {
	t.Helper()
	if os.Getenv("BENTO_DOCKER_TESTS") != "1" {
		t.Skip("set BENTO_DOCKER_TESTS=1 to run real Docker integration tests")
	}
	sdk, err := docker.NewSDK()
	if err != nil {
		t.Fatal(err)
	}
	return sdk
}

func TestIntegrationBuildRuntimeImages(t *testing.T) {
	sdk := requireDocker(t)
	m := &ImageManager{Engine: sdk}
	keys := []domain.ImageKey{
		{Kind: domain.RuntimePHP, Toolchain: "php", Version: "8.4"},
		{Kind: domain.RuntimeHTTP, Toolchain: "node", Version: "24"},
	}
	for _, key := range keys {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
		start := time.Now()
		id, spec, err := m.Ensure(ctx, key, func(s string) { t.Log(key.String(), s) })
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		t.Logf("%s -> %s (%s) in %s", key, spec.Tag(), id, time.Since(start).Round(time.Second))
		// Deterministic: planning twice yields the same tag.
		again, _ := PlanImage(key)
		if again.Tag() != spec.Tag() {
			t.Fatalf("non-deterministic tag %s vs %s", again.Tag(), spec.Tag())
		}
	}
}
