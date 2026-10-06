package domain

import (
	"slices"
	"testing"
)

func TestValidateResticSettings(t *testing.T) {
	ok := DefaultResticSettings()
	ok.Repository = "b2:bento/apps/shop"
	if errs := ValidateResticSettings(ok); len(errs) != 0 {
		t.Fatalf("defaults with a repository must validate: %v", errs)
	}
	cases := map[string]func(*ResticSettings){
		"no repository":    func(s *ResticSettings) { s.Repository = "" },
		"no paths":         func(s *ResticSettings) { s.Paths = nil },
		"absolute path":    func(s *ResticSettings) { s.Paths = []string{"/etc"} },
		"escaping path":    func(s *ResticSettings) { s.Paths = []string{"app/../../x"} },
		"sidecar path":     func(s *ResticSettings) { s.Paths = []string{HomeSidecarName} },
		"sqlite root":      func(s *ResticSettings) { s.SQLitePaths = []string{"."} },
		"absolute exclude": func(s *ResticSettings) { s.Excludes = []string{"/backup/home/x"} },
		"negated absolute": func(s *ResticSettings) { s.Excludes = []string{"!/x"} },
		"escaping exclude": func(s *ResticSettings) { s.Excludes = []string{"a/../../b"} },
		"newline exclude":  func(s *ResticSettings) { s.Excludes = []string{"a\nb"} },
		"keeps nothing":    func(s *ResticSettings) { s.Retention = ResticRetention{} },
		"negative keep":    func(s *ResticSettings) { s.Retention.Daily = -1 },
		"enabled, no cron": func(s *ResticSettings) { s.Schedule = ResticSchedule{Enabled: true} },
	}
	for name, mutate := range cases {
		s := ok
		mutate(&s)
		if errs := ValidateResticSettings(s); len(errs) == 0 {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestResticExcludeLines(t *testing.T) {
	s := ResticSettings{
		DefaultExcludes: true,
		Excludes:        []string{"node_modules", "app/storage/framework/cache/", "!app/storage/framework/cache/keep", "./tmp/x"},
	}
	got := ResticExcludeLines(s, "/backup/home", []string{".local/share/minicron/state.db"})
	want := []string{
		"/backup/home/" + HomeSidecarName,
		"/backup/home/.local/share/minicron/state.db",
		"/backup/home/.local/share/minicron/state.db-wal",
		"node_modules",
		"/backup/home/app/storage/framework/cache",
		"!/backup/home/app/storage/framework/cache/keep",
		"/backup/home/tmp/x",
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("missing %q in %q", w, got)
		}
	}
	for _, d := range ResticDefaultExcludes {
		if !slices.Contains(got, "/backup/home/"+d) {
			t.Errorf("default exclude %q missing", d)
		}
	}
	s.DefaultExcludes = false
	if slices.Contains(ResticExcludeLines(s, "/backup/home", nil), "/backup/home/.cache") {
		t.Error("default excludes applied while disabled")
	}
}

func TestResticExcludeLinesAlwaysSkipMinicronLogsAndSocket(t *testing.T) {
	for _, defaults := range []bool{true, false} {
		got := ResticExcludeLines(ResticSettings{DefaultExcludes: defaults}, "/backup/home", nil)
		for _, f := range []string{"minicron-logs.db", "minicron-logs.db-wal", "minicron-logs.db-shm", "minicron.sock"} {
			if !slices.Contains(got, "/backup/home/"+MinicronDataDir+"/"+f) {
				t.Errorf("defaults=%v: %s not excluded in %q", defaults, f, got)
			}
		}
	}
}

func TestSensitiveEnvKey(t *testing.T) {
	for _, k := range []string{"APP_KEY", "AWS_SECRET_ACCESS_KEY", "DB_PASSWORD", "stripe_secret", "GITHUB_TOKEN", "APIKEY"} {
		if !SensitiveEnvKey(k) {
			t.Errorf("%s should be sensitive", k)
		}
	}
	for _, k := range []string{"APP_ENV", "APP_URL", "LOG_LEVEL", "KEYBOARD_LAYOUT"} {
		if SensitiveEnvKey(k) {
			t.Errorf("%s should not be sensitive", k)
		}
	}
}
