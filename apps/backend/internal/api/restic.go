package api

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func resticToDTO(v operations.ResticView, now time.Time) dto.Restic {
	s := v.Settings
	out := dto.Restic{
		Configured:  v.Configured,
		Initialized: v.State.RepositoryID != "",
		Settings: dto.ResticSettings{
			Repository: s.Repository, Paths: nonNil(s.Paths), Excludes: nonNil(s.Excludes),
			DefaultExcludes: s.DefaultExcludes, SQLitePaths: nonNil(s.SQLitePaths),
			Retention: dto.ResticRetention{Hourly: s.Retention.Hourly, Daily: s.Retention.Daily,
				Weekly: s.Retention.Weekly, Monthly: s.Retention.Monthly},
			Schedule: dto.ResticSchedule{Enabled: s.Schedule.Enabled, Cron: s.Schedule.Cron},
		},
		Snapshots:       []dto.ResticSnapshot{},
		Keys:            []dto.ResticKey{},
		RefreshedAt:     timeOrEmpty(v.State.RefreshedAt),
		LastPruneAt:     timeOrEmpty(v.State.LastPruneAt),
		DefaultExcludes: domain.ResticDefaultExcludes,
	}
	for _, sn := range v.State.Snapshots {
		out.Snapshots = append(out.Snapshots, dto.ResticSnapshot{ID: sn.ID, ShortID: sn.ShortID,
			Time: platform.FormatTime(sn.Time), Tags: nonNil(sn.Tags)})
	}
	for _, k := range v.State.Keys {
		out.Keys = append(out.Keys, dto.ResticKey{ID: k.ID, Current: k.Current, UserName: k.UserName,
			Created: platform.FormatTime(k.Created)})
	}
	out.LastBackup = runResultDTO(v.State.LastBackup)
	out.LastCheck = runResultDTO(v.State.LastCheck)
	out.NextRun = nextResticRun(v, now)
	return out
}

func nextResticRun(v operations.ResticView, now time.Time) string {
	s := v.Settings.Schedule
	if v.State.RepositoryID == "" || !s.Enabled {
		return ""
	}
	return timeOrEmpty(operations.NextRun(store.Schedule{Enabled: true, Cron: s.Cron}, now))
}

// handleAppBackups is the stack-wide view of app backups: one summary per
// app with restic settings and their recent runs merged, newest first.
func (s *Server) handleAppBackups(w http.ResponseWriter, r *http.Request) {
	list, err := s.C.ResticOverview(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	now := time.Now()
	out := dto.AppBackupOverview{Apps: []dto.AppBackupSummary{}, Runs: []dto.AppBackupRun{}, TimeZone: zoneLabel(now)}
	for _, a := range list {
		v := a.View
		out.Apps = append(out.Apps, dto.AppBackupSummary{
			AppID: a.App.ID, Slug: a.App.Slug, Repository: v.Settings.Repository, Initialized: v.State.RepositoryID != "",
			ScheduleEnabled: v.Settings.Schedule.Enabled, Cron: v.Settings.Schedule.Cron, NextRun: nextResticRun(v, now),
			Paths: nonNil(v.Settings.Paths), SnapshotCount: len(v.State.Snapshots),
			LastBackup: runResultDTO(v.State.LastBackup), LastCheck: runResultDTO(v.State.LastCheck),
		})
		for i := range v.State.History {
			out.Runs = append(out.Runs, dto.AppBackupRun{AppID: a.App.ID, Slug: a.App.Slug, Run: *runResultDTO(&v.State.History[i])})
		}
	}
	slices.SortFunc(out.Runs, func(a, b dto.AppBackupRun) int { return strings.Compare(b.Run.At, a.Run.At) })
	if len(out.Runs) > 100 {
		out.Runs = out.Runs[:100]
	}
	writeJSON(w, http.StatusOK, out)
}

func runResultDTO(r *domain.ResticRunResult) *dto.ResticRunResult {
	if r == nil {
		return nil
	}
	return &dto.ResticRunResult{OpID: r.OpID, Trigger: r.Trigger, At: platform.FormatTime(r.At), OK: r.OK, SnapshotID: r.SnapshotID,
		BytesAdded: r.BytesAdded, FilesNew: r.FilesNew, FilesTotal: r.FilesTotal, Seconds: r.Seconds, Error: r.Error}
}

func timeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return platform.FormatTime(t)
}

func (s *Server) handleGetRestic(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	v, err := s.C.ResticSettings(r.Context(), app.ID)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, resticToDTO(v, time.Now()))
}

func (s *Server) handlePutRestic(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.ResticSettings
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	v, err := s.C.SaveResticSettings(r.Context(), app.ID, domain.ResticSettings{
		Repository: req.Repository, Paths: req.Paths, Excludes: req.Excludes, DefaultExcludes: req.DefaultExcludes,
		SQLitePaths: req.SQLitePaths,
		Retention: domain.ResticRetention{Hourly: req.Retention.Hourly, Daily: req.Retention.Daily,
			Weekly: req.Retention.Weekly, Monthly: req.Retention.Monthly},
		Schedule: domain.ResticSchedule{Enabled: req.Schedule.Enabled, Cron: req.Schedule.Cron},
	})
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, resticToDTO(v, time.Now()))
}

// acceptedKey answers with the operation and, once, the key it created.
func (s *Server) acceptedKey(w http.ResponseWriter, op store.Operation, key string) {
	url := "/api/v1/operations/" + op.ID
	w.Header().Set("Location", url)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, dto.ResticKeyAccepted{
		Accepted: dto.Accepted{Operation: s.opDTO(op, nil), StatusURL: url}, Key: key})
}

func (s *Server) handleResticInit(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req struct{}
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, key, err := s.C.SubmitResticInit(r.Context(), app.ID, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.acceptedKey(w, op, key)
}

func (s *Server) handleResticConnect(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.ResticConnectRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitResticConnect(r.Context(), app.ID, req.Key, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleResticKeyAdd(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.ResticKeyAddRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, key, err := s.C.SubmitResticKeyAdd(r.Context(), app.ID, req.Label, req.Confirm, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.acceptedKey(w, op, key)
}

func (s *Server) handleResticKeyRemove(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitResticKeyRemove(r.Context(), app.ID, r.PathValue("keyId"), idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleResticBackup(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitResticBackup(r.Context(), app.ID, "manual", idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) resticSimple(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, ok := s.loadApp(w, r)
		if !ok {
			return
		}
		idem, err := idempotencyKey(r)
		if err != nil {
			writeError(w, s.Log, err)
			return
		}
		op, err := s.C.SubmitResticSimple(r.Context(), kind, app.ID, idem)
		if err != nil {
			writeError(w, s.Log, err)
			return
		}
		s.accepted(w, op, nil)
	}
}

func (s *Server) handleResticRestore(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.ResticRestoreRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitResticRestore(r.Context(), app.ID, operations.ResticRestoreRequest{
		Snapshot: req.Snapshot, Files: req.Files, Databases: req.Databases}, req.Confirm, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}
