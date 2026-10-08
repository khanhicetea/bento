package stack

import (
	"path/filepath"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestDisableResticSchedules(t *testing.T) {
	ctx := t.Context()
	s, err := store.Create(filepath.Join(t.TempDir(), "bento.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	set := domain.DefaultResticSettings()
	set.Repository = "b2:bento/apps/shop"
	set.Schedule = domain.ResticSchedule{Enabled: true, Cron: "30 3 * * *"}
	if err := store.PutSetting(ctx, s.DB(), "restic:a123", set); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"repositoryId": "r1", "schedule": map[string]any{"enabled": true}}
	if err := store.PutSetting(ctx, s.DB(), "restic-state:a123", state); err != nil {
		t.Fatal(err)
	}
	if err := disableResticSchedules(ctx, s.DB()); err != nil {
		t.Fatal(err)
	}
	var got domain.ResticSettings
	if _, err := store.GetSetting(ctx, s.DB(), "restic:a123", &got); err != nil {
		t.Fatal(err)
	}
	if got.Schedule.Enabled || got.Schedule.Cron != "30 3 * * *" || got.Repository != set.Repository {
		t.Fatalf("settings after import %+v", got)
	}
	var st map[string]any
	if _, err := store.GetSetting(ctx, s.DB(), "restic-state:a123", &st); err != nil {
		t.Fatal(err)
	}
	if st["schedule"].(map[string]any)["enabled"] != true {
		t.Fatal("only restic:<app> settings may be rewritten")
	}
}
