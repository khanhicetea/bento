package schedulerui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// The vendored UI must be the one the runtime images run. Bumping
// MINICROND_VERSION without `make vendor-minicrond-ui` fails here.
func TestVendoredVersionMatchesPin(t *testing.T) {
	if got, want := Version(), domain.RuntimeArtifacts["MINICROND_VERSION"]; got != want {
		t.Fatalf("vendored minicrond UI %q, runtime images pin %q: run make vendor-minicrond-ui", got, want)
	}
}

// Every link of the shell resolves to a vendored asset under the base path.
func TestIndexLinksResolveToVendoredAssets(t *testing.T) {
	page, err := Index("/apps/shop/scheduler/")
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	if !strings.Contains(s, `<base href="/apps/shop/scheduler/" />`) {
		t.Fatalf("base tag not rewritten:\n%s", s)
	}
	links := regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(s, -1)
	n := 0
	for _, m := range links {
		ref := m[1]
		if ref == "/apps/shop/scheduler/" {
			continue
		}
		name, ok := strings.CutPrefix(ref, "assets/")
		if !ok {
			t.Errorf("link %q is not under assets/", ref)
			continue
		}
		if _, ok := Lookup(name); !ok {
			t.Errorf("link %q has no vendored asset", ref)
		}
		n++
	}
	if n < 2 {
		t.Fatalf("expected a script and a stylesheet, found %d asset links", n)
	}
}

func TestLookupServesOnlyScriptsAndStyles(t *testing.T) {
	for _, name := range []string{"index.html", "../VERSION", "VERSION", "missing.js"} {
		if _, ok := Lookup(name); ok {
			t.Errorf("Lookup(%q) served", name)
		}
	}
}
