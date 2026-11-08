package runtime

import (
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/assets"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func readDockerfile(t *testing.T, kind string) string {
	t.Helper()
	b, err := fs.ReadFile(assets.FS, "images/"+kind+"/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var dockerfileARG = regexp.MustCompile(`(?m)^ARG ([A-Z0-9_]+)$`)

// The classic builder only warns about a build arg no stage declares, and an
// undeclared one leaves FROM ${...} blank, so each plan must pass exactly the
// ARGs its Dockerfile declares.
func TestPlanArgsMatchDockerfileARGs(t *testing.T) {
	keys := []domain.ImageKey{
		{Kind: domain.RuntimePHP, Toolchain: "php", Version: "8.4"},
		{Kind: domain.RuntimePHP, Toolchain: "php", Version: "7.4"},
		{Kind: domain.RuntimePHP, Toolchain: "php", Version: "8.1"},
		{Kind: domain.RuntimeHTTP, Toolchain: "node", Version: "24"},
		{Kind: domain.RuntimeHTTP, Toolchain: "bun", Version: "1.2"},
		{Kind: domain.RuntimeHTTP, Toolchain: "python", Version: "3.13"},
	}
	for _, key := range keys {
		spec, err := PlanImage(key)
		if err != nil {
			t.Fatal(err)
		}
		declared := map[string]bool{}
		for _, m := range dockerfileARG.FindAllStringSubmatch(readDockerfile(t, spec.ContextKind), -1) {
			declared[m[1]] = true
		}
		got, want := slices.Sorted(maps.Keys(spec.Args)), slices.Sorted(maps.Keys(declared))
		if !slices.Equal(got, want) {
			t.Errorf("%s (%s): passes %v, Dockerfile declares %v", key, spec.ContextKind, got, want)
		}
		for k, v := range spec.Args {
			if v == "" {
				t.Errorf("%s: build arg %s is empty", key, k)
			}
		}
	}
}

// The standalone images pass exactly the ARGs their Dockerfiles declare too.
func TestStandaloneArgsMatchDockerfileARGs(t *testing.T) {
	for name, plan := range map[string]func() (ImageSpec, error){"valkey": ValkeyImageSpec, "restic": ResticImageSpec} {
		spec, err := plan()
		if err != nil {
			t.Fatal(err)
		}
		df, err := fs.ReadFile(assets.FS, "images-standalone/"+name+"/Dockerfile")
		if err != nil {
			t.Fatal(err)
		}
		declared := map[string]bool{}
		for _, m := range dockerfileARG.FindAllStringSubmatch(string(df), -1) {
			declared[m[1]] = true
		}
		if got, want := slices.Sorted(maps.Keys(spec.Args)), slices.Sorted(maps.Keys(declared)); !slices.Equal(got, want) {
			t.Errorf("%s: passes %v, Dockerfile declares %v", name, got, want)
		}
	}
}

// stage returns one build stage of a Dockerfile, from its FROM line to the
// blank line that ends it.
func stage(t *testing.T, df, name string) string {
	t.Helper()
	end := strings.Index(df, " AS "+name+"\n")
	if end < 0 {
		t.Fatalf("no stage %q", name)
	}
	start := strings.LastIndex(df[:end], "\nFROM ") + 1
	n := strings.Index(df[start:], "\n\n")
	if n < 0 {
		t.Fatalf("stage %q does not end with a blank line", name)
	}
	return df[start : start+n]
}

// The classic builder shares a layer only when everything beneath it is
// identical. The official-image and deb.sury.org PHP Dockerfiles keep their
// final stages identical up to the PHP-specific steps, and every runtime copies
// Valkey from the same stage, so a host stores those layers once per Debian
// release instead of once per runtime version.
func TestRuntimeImagesShareLayers(t *testing.T) {
	php, sury, http := readDockerfile(t, "php"), readDockerfile(t, "php-sury"), readDockerfile(t, "http")
	for _, name := range []string{"toolchain", "artifacts", "valkey"} {
		if stage(t, php, name) != stage(t, sury, name) {
			t.Errorf("stage %s differs between images/php and images/php-sury", name)
		}
	}
	for _, name := range []string{"toolchain", "valkey"} {
		if stage(t, php, name) != stage(t, http, name) {
			t.Errorf("stage %s differs between images/php and images/http", name)
		}
	}
	shared := func(df string) string {
		start := strings.LastIndex(df, "\nFROM ${DEBIAN_BASE}\n")
		end := strings.Index(df, "valkey-server --version\n")
		if start < 0 || end < start {
			t.Fatal("final stage lacks the shared PHP prefix")
		}
		return df[start:end]
	}
	if shared(php) != shared(sury) {
		t.Error("the shared final-stage prefix differs between images/php and images/php-sury")
	}
}
