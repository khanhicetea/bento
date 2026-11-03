package runtime

import (
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func TestProtectionConfigGroups(t *testing.T) {
	if got := ProtectionConfig(domain.Protection{}, "  "); got != "" {
		t.Fatalf("nothing enabled must render nothing:\n%s", got)
	}
	marks := map[string]string{
		"dotfiles":  "/\\.(?!well-known",
		"phpprobes": "wlwmanifest",
		"sensitive": "composer\\.json",
	}
	for name, p := range map[string]domain.Protection{
		"dotfiles":  {BlockDotFiles: true},
		"phpprobes": {BlockPHPProbes: true},
		"sensitive": {BlockSensitiveFiles: true},
	} {
		got := ProtectionConfig(p, "")
		for other, mark := range marks {
			if has := strings.Contains(got, mark); has != (other == name) {
				t.Errorf("%s only: contains %s patterns = %v", name, other, has)
			}
		}
	}
	all := ProtectionConfig(domain.DefaultProtection(), "")
	for _, want := range []string{"wlwmanifest", "eval-stdin", "phpmyadmin", `\.map$`, "vendor/composer/", "sw[a-p]", `(?<!\.xml)\.gz`} {
		if !strings.Contains(all, want) {
			t.Errorf("default protection lacks %s", want)
		}
	}
}

// Every block is a 404, never a 403 or deny, and every pattern is a single
// unquoted Nginx token: whitespace, braces or a semicolon would split or end it.
func TestProtectionBlocksAreSafeNginx(t *testing.T) {
	blocks := protectionBlocks(domain.DefaultProtection())
	if len(blocks) < 10 {
		t.Fatalf("expected every group's blocks, got %d", len(blocks))
	}
	for _, b := range blocks {
		if strings.ContainsAny(b.pattern, " \t\n{};\"'#") {
			t.Errorf("pattern is not one safe nginx token: %q", b.pattern)
		}
		if b.comment == "" {
			t.Errorf("block %q has no comment", b.pattern)
		}
	}
	out := ProtectionConfig(domain.DefaultProtection(), "")
	if strings.Contains(out, "deny") || strings.Contains(out, "return 403") {
		t.Fatalf("protection must answer 404 only:\n%s", out)
	}
	if n, want := strings.Count(out, "return 404;"), len(blocks); n != want {
		t.Fatalf("%d return 404 for %d blocks", n, want)
	}
	if strings.Count(out, "{") != strings.Count(out, "}") {
		t.Fatal("unbalanced braces")
	}
}

func TestProtectionConfigIndentsEveryLine(t *testing.T) {
	out := ProtectionConfig(domain.DefaultProtection(), "    ")
	for i, line := range strings.Split(out, "\n") {
		if line != "" && !strings.HasPrefix(line, "    ") {
			t.Fatalf("line %d not indented: %q", i, line)
		}
	}
	if strings.HasSuffix(out, "\n") {
		t.Fatal("the result must end without a newline")
	}
}

func TestProtectionPatch(t *testing.T) {
	off := false
	p := domain.DefaultProtection().Apply(&domain.ProtectionPatch{BlockPHPProbes: &off})
	if !p.BlockDotFiles || p.BlockPHPProbes || !p.BlockSensitiveFiles {
		t.Fatalf("%+v", p)
	}
	if q := p.Apply(nil); q != p {
		t.Fatal("a nil patch changes nothing")
	}
	if q := p.Apply(&domain.ProtectionPatch{}); q != p {
		t.Fatal("an empty patch changes nothing")
	}
}

func TestAppNginxProtection(t *testing.T) {
	render := func(mut func(*domain.App)) string {
		t.Helper()
		app := testApp()
		mut(&app)
		cfg, _, _, err := RenderAppConfig(app, AppContext{TrustedProxies: []string{"10.200.0.2"}})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range cfg {
			if f.name == "nginx.conf" {
				return string(f.data)
			}
		}
		t.Fatal("no nginx.conf")
		return ""
	}
	for _, routing := range []string{domain.RoutingFrontController, domain.RoutingLegacy} {
		on := render(func(a *domain.App) { a.Runtime.PHP.Routing = routing })
		// The blocks must precede the static-extension location: for a
		// regex match Nginx takes the first in file order, and that location
		// would otherwise serve .map files.
		blocks := strings.Index(on, "# Bento protection")
		static := strings.Index(on, `location ~* \.(?:css|js|mjs|map`)
		if blocks < 0 || static < 0 || blocks > static {
			t.Errorf("%s: protection at %d, static location at %d", routing, blocks, static)
		}
		if strings.Contains(on, "deny all") {
			t.Errorf("%s: dot files must answer 404, not deny", routing)
		}
		off := render(func(a *domain.App) {
			a.Runtime.PHP.Routing = routing
			a.Protection = domain.Protection{}
		})
		if strings.Contains(off, "Bento protection") || strings.Contains(off, "well-known") || strings.Contains(off, "wlwmanifest") {
			t.Errorf("%s: all off must render no protection:\n%s", routing, off)
		}
		// The rest of the server block is unaffected by the toggle.
		for _, want := range []string{"location = /robots.txt", "disable_symlinks on", "location ~* \\.(?:css|js|mjs|map"} {
			if !strings.Contains(off, want) {
				t.Errorf("%s: all off lost %q", routing, want)
			}
		}
	}
}
