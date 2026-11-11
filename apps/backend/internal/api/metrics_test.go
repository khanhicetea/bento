package api

import (
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func TestAppProcessesRedactsAppSecrets(t *testing.T) {
	const dbPassword = "Zq8LmT3vXe1RbN6cYd9HfW2sKu4JpA7G"
	const envSecret = "tok_live_51HxampleSecretValue"
	if !domain.SensitiveEnvKey("API_TOKEN") {
		t.Fatal("API_TOKEN must be a sensitive env key for this test")
	}
	var app domain.App
	app.Bindings = []domain.Binding{{Password: dbPassword}}
	app.Runtime.Env = []domain.EnvVar{{Key: "API_TOKEN", Value: envSecret}}

	got := appProcesses(app, []docker.Process{
		{PID: "1", Command: "worker --dsn mysql://app:" + dbPassword + "@db/app"},
		{PID: "2", Command: "sync --token=" + envSecret},
		{PID: "3", Command: "php-fpm: pool www"},
	})
	if len(got) != 3 {
		t.Fatalf("got %d processes, want 3", len(got))
	}
	for _, p := range got {
		if strings.Contains(p.Command, dbPassword) || strings.Contains(p.Command, envSecret) {
			t.Fatalf("secret leaked in command %q", p.Command)
		}
	}
	for _, p := range got[:2] {
		if !strings.Contains(p.Command, "[redacted]") {
			t.Fatalf("command %q not redacted", p.Command)
		}
	}
	if got[2].Command != "php-fpm: pool www" {
		t.Fatalf("ordinary command changed: %q", got[2].Command)
	}
	if empty := appProcesses(app, nil); empty == nil || len(empty) != 0 {
		t.Fatalf("empty list must be a non-nil empty slice, got %#v", empty)
	}
}
