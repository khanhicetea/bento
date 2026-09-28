package domain

import "testing"

func TestValidateSlug(t *testing.T) {
	for _, ok := range []string{"shop", "my-app", "a1b", "abc-123"} {
		if err := ValidateSlug(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ab", "Shop", "1app", "app-", "a--b", "root", "bento", "has_underscore", "a/b", "../x"} {
		if ValidateSlug(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestNormalizeDomain(t *testing.T) {
	d, err := NormalizeDomain(" Shop.Example.COM. ")
	if err != nil || d != "shop.example.com" {
		t.Fatal(d, err)
	}
	for _, bad := range []string{"localhost", "*.example.com", "a..b", "-a.com", "10.0.0.1", "exa mple.com", "a.com;evil"} {
		if _, err := NormalizeDomain(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestRuntimeUnionVariantConstraints(t *testing.T) {
	cases := []struct {
		name string
		rt   Runtime
		ok   bool
	}{
		{"php ok", Runtime{Kind: RuntimePHP, PHP: &PHPRuntime{Version: "8.4", DocumentRoot: "public"}}, true},
		{"php with http variant", Runtime{Kind: RuntimePHP, PHP: &PHPRuntime{Version: "8.4"}, HTTP: &HTTPRuntime{}}, false},
		{"php missing variant", Runtime{Kind: RuntimePHP}, false},
		{"php bad version", Runtime{Kind: RuntimePHP, PHP: &PHPRuntime{Version: "7.0"}}, false},
		{"php docroot escape", Runtime{Kind: RuntimePHP, PHP: &PHPRuntime{Version: "8.4", DocumentRoot: "../x"}}, false},
		{"php absolute docroot", Runtime{Kind: RuntimePHP, PHP: &PHPRuntime{Version: "8.4", DocumentRoot: "/etc"}}, false},
		{"php hidden docroot", Runtime{Kind: RuntimePHP, PHP: &PHPRuntime{Version: "8.4", DocumentRoot: ".git"}}, false},
		{"php bad routing", Runtime{Kind: RuntimePHP, PHP: &PHPRuntime{Version: "8.4", Routing: "any"}}, false},
		{"http ok", Runtime{Kind: RuntimeHTTP, HTTP: &HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}}}, true},
		{"http empty argv", Runtime{Kind: RuntimeHTTP, HTTP: &HTTPRuntime{Toolchain: "node", Version: "24"}}, false},
		{"http NUL argv", Runtime{Kind: RuntimeHTTP, HTTP: &HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"a\x00b"}}}, false},
		{"http privileged port", Runtime{Kind: RuntimeHTTP, HTTP: &HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"x"}, Port: 80}}, false},
		{"http workdir escape", Runtime{Kind: RuntimeHTTP, HTTP: &HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"x"}, Workdir: "a/../../b"}}, false},
		{"http unknown toolchain", Runtime{Kind: RuntimeHTTP, HTTP: &HTTPRuntime{Toolchain: "ruby", Version: "3", Argv: []string{"x"}}}, false},
		{"unknown kind", Runtime{Kind: "shell"}, false},
	}
	for _, c := range cases {
		var errs ValidationErrors
		rt := c.rt
		ValidateRuntime(&rt, &errs)
		if (len(errs) == 0) != c.ok {
			t.Errorf("%s: ok=%v errs=%v", c.name, c.ok, errs)
		}
	}
	// Defaults are applied.
	rt := Runtime{Kind: RuntimeHTTP, HTTP: &HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"x"}}}
	var errs ValidationErrors
	ValidateRuntime(&rt, &errs)
	if rt.HTTP.Port != 3000 {
		t.Fatalf("default port %d", rt.HTTP.Port)
	}
}

func TestValidateRouteAndUpstream(t *testing.T) {
	var errs ValidationErrors
	r := Route{TLS: TLSNone, RedirectHTTPS: true}
	ValidateRoute(&r, "route", &errs)
	if len(errs) == 0 {
		t.Fatal("redirect without TLS must fail")
	}
	for _, bad := range []string{"ftp://x", "http://", "http://u:p@h", "http://h/x;y", "http://h/${x}"} {
		if ValidateUpstream(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
	if err := ValidateUpstream("https://10.0.0.5:8443/app"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnv(t *testing.T) {
	var ok ValidationErrors
	ValidateEnv([]EnvVar{{"APP_ENV", "production"}, {"_X1", ""}}, &ok)
	if ok.Err() != nil {
		t.Fatalf("valid env rejected: %v", ok.Err())
	}
	for _, bad := range [][]EnvVar{
		{{"1BAD", "x"}},
		{{"BENTO_APP_ID", "x"}},
		{{"HOME", "x"}},
		{{"A", "1"}, {"A", "2"}},
		{{"A", "line\nbreak"}},
	} {
		var errs ValidationErrors
		ValidateEnv(bad, &errs)
		if errs.Err() == nil {
			t.Fatalf("expected rejection for %v", bad)
		}
	}
}

func TestDatabaseSuffixKeepsAppNamespacesDisjoint(t *testing.T) {
	for _, ok := range []string{"blog", "reports2", "a"} {
		if err := ValidateDatabaseSuffix(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	// "blog_x" would let app shop claim shop_blog_x, which belongs to app shop-blog.
	for _, bad := range []string{"", "main", "blog_x", "Blog", "1db", "blog-x"} {
		if ValidateDatabaseSuffix(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
