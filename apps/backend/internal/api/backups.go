package api

import (
	"net/http"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/backup"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
)

// handleResticRestoreDB replaces one database of an app with its dump in one
// of the app's snapshots.
func (s *Server) handleResticRestoreDB(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.ResticRestoreDBRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SubmitResticRestoreDB(r.Context(), app.ID,
		operations.ResticRestoreDBRequest{Snapshot: req.Snapshot, Database: req.Database, Binding: req.Binding}, req.Confirm, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

// zoneLabel names now's UTC offset ("UTC", "UTC+07:00", "UTC-03:30").
func zoneLabel(now time.Time) string {
	_, offset := now.Zone()
	if offset == 0 {
		return "UTC"
	}
	return "UTC" + now.Format("-07:00")
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
