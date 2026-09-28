package api

import (
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// MaxWebhookBodyBytes bounds a delivery; the edge enforces the same limit.
const MaxWebhookBodyBytes = 8 << 20

var hookIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// UtilsHandler serves the utils listener, which operators expose to the
// internet (edge, host nginx, Cloudflare Tunnel). It has no session, CSRF,
// UI, or management routes: every route must authenticate itself.
// /_webhook/* is authenticated by a per-app secret and can at most queue a
// deploy of the configured branch; /_dbadmin/* by a single-use ticket issued
// to an operator session (see dbadmin.go).
func (s *Server) UtilsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+domain.WebhookPathPrefix+"deploy/{hook}", s.handleDeployWebhook)
	mux.Handle(dbadminPathPrefix, s.dbadminGateway())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeHookJSON(w, http.StatusNotFound, map[string]string{"result": "not-found"})
	})
	return mux
}

func (s *Server) handleDeployWebhook(w http.ResponseWriter, r *http.Request) {
	hook := r.PathValue("hook")
	if !hookIDPattern.MatchString(hook) {
		writeHookJSON(w, http.StatusNotFound, map[string]string{"result": "not-found"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxWebhookBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeHookJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"result": "too-large"})
			return
		}
		writeHookJSON(w, http.StatusBadRequest, map[string]string{"result": "bad-request"})
		return
	}
	out, err := s.C.HandleWebhook(r.Context(), hook, r.Header, body)
	if err != nil {
		s.Log.Error("webhook delivery", "hook", hook[:8], "err", err)
		writeHookJSON(w, http.StatusInternalServerError, map[string]string{"result": "error"})
		return
	}
	s.Log.Info("webhook delivery", "hook", hook[:8], "status", out.Status, "result", out.Result, "op", out.OperationID)
	writeHookJSON(w, out.Status, out)
}

func writeHookJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(w, status, v)
}
