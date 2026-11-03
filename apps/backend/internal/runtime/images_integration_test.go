package runtime

import (
	"context"
	"os"
	"strings"
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
		{Kind: domain.RuntimePHP, Toolchain: "php", Version: "7.4"}, // deb.sury.org packages on Debian
		{Kind: domain.RuntimePHP, Toolchain: "php", Version: "8.0"},
		{Kind: domain.RuntimePHP, Toolchain: "php", Version: "8.1"},
		{Kind: domain.RuntimeHTTP, Toolchain: "node", Version: "24"},
		{Kind: domain.RuntimeHTTP, Toolchain: "bun", Version: "1.4"},
		{Kind: domain.RuntimeHTTP, Toolchain: "bun", Version: "1.2"},
		{Kind: domain.RuntimeHTTP, Toolchain: "python", Version: "3.13"}, // compiled from source
	}
	for _, key := range keys {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
		start := time.Now()
		spec, err := PlanImage(key)
		if err != nil {
			t.Fatal(err)
		}
		release := m.Hold(key)
		b, err := m.Build(ctx, spec, time.Now(), false, func(s string) { t.Log(key.String(), s) })
		release()
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		t.Logf("%s -> %s (%s) in %s: %v", key, b.Tag, b.ID, time.Since(start).Round(time.Second), b.Manifest)
		want := []string{"debian", "mise", "valkey", "debian-packages"}
		if key.Kind == domain.RuntimePHP {
			want = append(want, "php", "phpredis", "composer", "nginx", "node", "npm", "pnpm")
		} else {
			want = append(want, key.Toolchain)
		}
		for _, k := range want {
			if b.Manifest[k] == "" {
				t.Errorf("%s: manifest lacks %s: %v", key, k, b.Manifest)
			}
		}
		if key.Kind == domain.RuntimeHTTP && !strings.HasPrefix(b.Manifest[key.Toolchain], key.Version) {
			t.Errorf("%s resolved %s %s", key, key.Toolchain, b.Manifest[key.Toolchain])
		}
	}
}
