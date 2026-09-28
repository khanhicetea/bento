package api

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	arts, err := backup.ListArtifacts(s.Layout.BackupsDir())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.BackupArtifactList{Artifacts: []dto.BackupArtifact{}}
	for _, a := range arts {
		out.Artifacts = append(out.Artifacts, dto.BackupArtifact{Path: a.Path, AppSlug: a.AppSlug, Engine: dto.Engine(a.Engine), Database: a.Database,
			SizeBytes: int(a.SizeBytes), CreatedAt: platform.FormatTime(a.CreatedAt)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := store.ListBackupRuns(r.Context(), s.Store.DB(), 50)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.BackupRunList{Runs: []dto.BackupRun{}}
	for _, b := range runs {
		out.Runs = append(out.Runs, dto.BackupRun{ID: b.ID, Trigger: b.Trigger, State: b.State, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt,
			Artifacts: nonNil(b.Artifacts), UploadState: b.UploadState, Error: b.Error})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) {
	var req dto.BackupRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitBackup(r.Context(), operations.BackupRequest{Scope: req.Scope, AppID: req.AppID, BindingID: req.BindingID,
		Compression: req.Compression, Upload: req.Upload, Trigger: "manual"}, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var req dto.RestoreRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitRestore(r.Context(), operations.RestoreRequest{Artifact: req.Artifact, AppID: req.AppID, Database: req.Database}, req.Confirm, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func scheduleDTO(sc domain.BackupSchedule, st operations.ScheduleState, now time.Time) dto.BackupSchedule {
	return dto.BackupSchedule{Enabled: sc.Enabled, Cron: sc.Cron, Compression: sc.Compression, Retain: sc.Retain, RcloneRemote: sc.RcloneRemote,
		NextRun: platform.FormatTime(operations.NextBackup(sc, now)), LastRun: st.LastRun, LastState: st.LastState, TimeZone: zoneLabel(now)}
}

// zoneLabel names now's UTC offset ("UTC", "UTC+07:00", "UTC-03:30").
func zoneLabel(now time.Time) string {
	_, offset := now.Zone()
	if offset == 0 {
		return "UTC"
	}
	return "UTC" + now.Format("-07:00")
}

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	sc, st, err := s.C.BackupSchedule(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, scheduleDTO(sc, st, time.Now()))
}

func (s *Server) handlePutSchedule(w http.ResponseWriter, r *http.Request) {
	var req dto.BackupSchedule
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	sc := domain.BackupSchedule{Enabled: req.Enabled, Cron: req.Cron, Compression: req.Compression, Retain: req.Retain, RcloneRemote: req.RcloneRemote}
	if err := s.C.SetBackupSchedule(r.Context(), sc); err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.handleGetSchedule(w, r)
}

// handleRcloneStatus reports remote names and types only; rclone.conf values
// are credentials and never leave the host.
func (s *Server) handleRcloneStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := backup.ReadRcloneConfig(s.Layout.RcloneDir())
	out := dto.RcloneStatus{Present: cfg.Present, Encrypted: cfg.Encrypted, Remotes: []dto.RcloneRemote{}}
	if err != nil {
		out.Error = err.Error()
	}
	for _, rm := range cfg.Remotes {
		out.Remotes = append(out.Remotes, dto.RcloneRemote{Name: rm.Name, Type: rm.Type})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRcloneTest(w http.ResponseWriter, r *http.Request) {
	var req dto.RcloneTestRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitRcloneTest(r.Context(), req.Remote, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	var req dto.ExportRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitExport(r.Context(), req.Destination, req.Confirm, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleDownloadArtifact(w http.ResponseWriter, r *http.Request) {
	path, err := backup.ResolveArtifact(s.Layout.BackupsDir(), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, s.Log, notFound("no such backup"))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, s.Log, notFound("no such backup"))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	var req dto.BackupDeleteRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitBackupDelete(r.Context(), req.Artifact, req.Confirm, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}
