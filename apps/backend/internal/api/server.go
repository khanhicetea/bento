package api

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/reconcile"
	"github.com/khanhicetea/bento/apps/backend/internal/scheduler"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

type Server struct {
	C              *operations.Controller
	R              *reconcile.Reconciler
	Store          *store.Store
	Layout         platform.Layout
	Log            *slog.Logger
	Version        string
	StartedAt      time.Time
	AllowedOrigins []string
	WebUI          fs.FS
	Relay          *scheduler.RelayManager
	// PublicAddrs lists the public listener addresses being served.
	PublicAddrs func() []string

	limiter loginLimiter
}

// Handler builds the route table. local marks the Unix control socket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireAuth(h)) }

	mux.HandleFunc("GET /api/v1/session", s.handleGetSession)
	mux.HandleFunc("POST /api/v1/session", s.handleLogin)
	mux.HandleFunc("DELETE /api/v1/session", s.handleLogout)

	api("GET /api/v1/system", s.handleSystem)
	api("GET /api/v1/catalog", s.handleCatalog)
	api("PUT /api/v1/auth/password", s.handleSetPassword)

	api("GET /api/v1/apps", s.handleListApps)
	api("POST /api/v1/apps", s.handleCreateApp)
	api("GET /api/v1/apps/{id}", s.handleGetApp)
	api("PATCH /api/v1/apps/{id}", s.handleUpdateApp)
	api("DELETE /api/v1/apps/{id}", s.handleRemoveApp)
	api("POST /api/v1/apps/{id}/start", s.lifecycle(s.C.StartApp))
	api("POST /api/v1/apps/{id}/stop", s.lifecycle(s.C.StopApp))
	api("POST /api/v1/apps/{id}/restart", s.lifecycle(s.C.RestartApp))
	api("POST /api/v1/apps/{id}/publish", s.lifecycle(s.C.PublishApp))
	api("POST /api/v1/apps/{id}/unpublish", s.lifecycle(s.C.UnpublishApp))
	api("POST /api/v1/apps/{id}/bindings", s.handleAddBinding)
	api("POST /api/v1/apps/{id}/bindings/{bid}/databases", s.handleAddDatabase)
	api("POST /api/v1/apps/{id}/permissions", s.handlePermissions)
	api("GET /api/v1/apps/{id}/git", s.handleGetGitSource)
	api("PUT /api/v1/apps/{id}/git", s.handlePutGitSource)
	api("DELETE /api/v1/apps/{id}/git", s.handleDeleteGitSource)
	api("POST /api/v1/apps/{id}/deploy", s.lifecycle(s.C.DeployApp))
	api("GET /api/v1/apps/{id}/webhook", s.handleGetWebhook)
	api("POST /api/v1/apps/{id}/webhook", s.handleEnableWebhook)
	api("DELETE /api/v1/apps/{id}/webhook", s.handleDisableWebhook)
	api("GET /api/v1/apps/{id}/readiness", s.handleReadiness)
	api("GET /api/v1/apps/{id}/metrics", s.handleAppMetrics)
	api("GET /api/v1/apps/{id}/logs", s.handleAppLogs)
	api("POST /api/v1/apps/{id}/exec", s.handleExec)
	api("POST /api/v1/apps/{id}/scheduler/command", s.handleSchedulerCommand)
	api("GET /api/v1/apps/{id}/terminal", s.handleTerminal)

	api("GET /api/v1/operations", s.handleListOps)
	api("GET /api/v1/operations/{id}", s.handleGetOp)
	api("GET /api/v1/operations/{id}/events", s.handleOpEvents)
	api("POST /api/v1/operations/{id}/cancel", s.handleCancelOp)

	api("GET /api/v1/services", s.handleListServices)
	api("POST /api/v1/services", s.handleCreateService)

	api("GET /api/v1/edge", s.handleGetEdge)
	api("PUT /api/v1/edge", s.handlePutEdge)
	api("GET /api/v1/tunnel", s.handleGetTunnel)
	api("PUT /api/v1/tunnel/token", s.handlePutTunnel)
	api("GET /api/v1/public", s.handleGetPublic)
	api("PUT /api/v1/public", s.handlePutPublic)
	api("GET /api/v1/proxies", s.handleListProxies)
	api("POST /api/v1/proxies", s.handleUpsertProxy)
	api("DELETE /api/v1/proxies/{name}", s.handleDeleteProxy)

	api("GET /api/v1/retired", s.handleListRetired)
	api("POST /api/v1/retired/{id}/prune", s.handlePrune)

	api("GET /api/v1/backups/artifacts", s.handleListArtifacts)
	api("GET /api/v1/backups/runs", s.handleListRuns)
	api("POST /api/v1/backups", s.handleRunBackup)
	api("POST /api/v1/backups/restore", s.handleRestore)
	api("GET /api/v1/backups/schedule", s.handleGetSchedule)
	api("PUT /api/v1/backups/schedule", s.handlePutSchedule)
	api("POST /api/v1/stack/export", s.handleExport)

	mux.Handle("/scheduler/apps/", s.schedulerGateway())
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, s.Log, notFound("no such endpoint"))
	}))
	mux.Handle("/", s.webUI())
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/scheduler/apps/") {
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", "default-src 'self'; frame-src 'self'; frame-ancestors 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; base-uri 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) webUI() http.Handler {
	if s.WebUI == nil {
		return http.NotFoundHandler()
	}
	files := http.FileServerFS(s.WebUI)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(s.WebUI, p); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback for client routes only; a missing asset is a real 404
		// rather than an HTML page served with the wrong content type.
		if path.Ext(p) != "" || strings.HasPrefix(p, "assets/") {
			http.NotFound(w, r)
			return
		}
		b, err := fs.ReadFile(s.WebUI, "index.html")
		if err != nil {
			http.Error(w, "web UI not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})
}

func (s *Server) accepted(w http.ResponseWriter, op store.Operation, app *dto.App) {
	url := "/api/v1/operations/" + op.ID
	w.Header().Set("Location", url)
	writeJSON(w, http.StatusAccepted, dto.Accepted{Operation: opToDTO(op, nil), StatusURL: url, App: app})
}

// ---- system ----

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := dto.SystemStatus{StackID: s.C.Stack.ID, StackName: s.C.Stack.Name, Root: s.Layout.Root, Version: s.Version,
		StartedAt: platform.FormatTime(s.StartedAt)}
	if v, err := s.C.Engine.Version(ctx); err != nil {
		st.DockerError = "docker unavailable"
		s.Log.Warn("docker version", "err", err)
	} else {
		st.DockerVersion, st.DockerAPI, st.Arch = v.ServerVersion, v.APIVersion, v.Arch
	}
	apps, err := store.ListApps(ctx, s.Store.DB())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	st.Apps = len(apps)
	for _, a := range apps {
		if a.DesiredRuntime == domain.DesiredRunning {
			st.RunningApps++
		}
	}
	queued, _ := store.ListOperations(ctx, s.Store.DB(), store.OpFilter{States: []store.OpState{store.OpQueued, store.OpRunning}, Limit: 500})
	st.QueuedOps = len(queued)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	tc := map[string][]string{}
	for name, versions := range domain.HTTPToolchains {
		tc[name] = domain.SortedKeys(versions)
	}
	writeJSON(w, http.StatusOK, dto.Catalog{
		PHPVersions: domain.SortedKeys(domain.PHPVersions), Toolchains: tc,
		MySQLVersions: domain.SortedKeys(domain.MySQLVersions), PostgresVersions: domain.SortedKeys(domain.PostgresVersions),
		PoolProfiles: domain.SortedKeys(domain.PoolProfiles),
	})
}

func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	var req dto.SetPasswordRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	if err := SetOperatorPassword(r.Context(), s.Store, req.Password); err != nil {
		writeError(w, s.Log, badRequest(err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- apps ----

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := store.ListApps(r.Context(), s.Store.DB())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.AppList{Apps: []dto.AppSummary{}}
	for _, a := range apps {
		d, err := s.appToDTO(r.Context(), a, false)
		if err != nil {
			writeError(w, s.Log, err)
			return
		}
		out.Apps = append(out.Apps, d.AppSummary)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) loadApp(w http.ResponseWriter, r *http.Request) (domain.App, bool) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, s.Log, err)
		return domain.App{}, false
	}
	app, err := store.GetApp(r.Context(), s.Store.DB(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = notFound("app not found")
		}
		writeError(w, s.Log, err)
		return app, false
	}
	return app, true
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	d, err := s.appToDTO(r.Context(), app, true)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateAppRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	in := operations.CreateAppInput{Slug: req.Slug, Runtime: runtimeFromDTO(req.Runtime), Ingress: domain.IngressMode(req.Ingress),
		Domains: req.Domains, Route: routeFromDTO(req.Route)}
	if req.Resources != nil {
		in.Resources = domain.Resources(*req.Resources)
	}
	for _, b := range req.Bindings {
		in.Bindings = append(in.Bindings, operations.BindingRequest{Engine: domain.Engine(b.Engine), Service: b.Service})
	}
	app, op, err := s.C.CreateApp(r.Context(), in, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	d, err := s.appToDTO(r.Context(), app, true)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, &d)
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.UpdateAppRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	in := operations.UpdateAppInput{ExpectedGeneration: int64(req.ExpectedGeneration), Domains: req.Domains}
	if req.Runtime != nil {
		rt := runtimeFromDTO(*req.Runtime)
		in.Runtime = &rt
	}
	if req.Resources != nil {
		res := domain.Resources(*req.Resources)
		in.Resources = &res
	}
	if req.Ingress != nil {
		m := domain.IngressMode(*req.Ingress)
		in.Ingress = &m
	}
	if req.Route != nil {
		rt := routeFromDTO(req.Route)
		in.Route = &rt
	}
	updated, op, err := s.C.UpdateApp(r.Context(), app.ID, in, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	d, err := s.appToDTO(r.Context(), updated, true)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, &d)
}

func (s *Server) lifecycle(fn func(ctx context.Context, id, idem string) (store.Operation, error)) http.HandlerFunc {
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
		op, err := fn(r.Context(), app.ID, idem)
		if err != nil {
			writeError(w, s.Log, err)
			return
		}
		// An explicit operator action resets the reconciliation budget.
		s.R.ResetBudget(app.ID)
		s.accepted(w, op, nil)
	}
}

func (s *Server) handleRemoveApp(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.ConfirmRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.RemoveApp(r.Context(), app.ID, req.Confirm, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleAddBinding(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.BindingRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.AddBinding(r.Context(), app.ID, operations.BindingRequest{Engine: domain.Engine(req.Engine), Service: req.Service}, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleAddDatabase(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	bid, err := pathID(r, "bid")
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	var req dto.AddDatabaseRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.AddDatabase(r.Context(), app.ID, bid, req.Name, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handlePermissions(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.PermissionsRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.RepairPermissions(r.Context(), app.ID, req.Mode, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleGetGitSource(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	g, found, err := store.GetGitSource(r.Context(), s.Store.DB(), app.ID)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, gitSourceToDTO(g, found))
}

func (s *Server) handlePutGitSource(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.GitSourceRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	g, err := s.C.SetGitSource(r.Context(), app.ID, operations.GitSourceInput{RepoURL: req.RepoURL, Branch: req.Branch, RotateKey: req.RotateKey})
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, gitSourceToDTO(g, true))
}

func (s *Server) handleDeleteGitSource(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	if err := s.C.RemoveGitSource(r.Context(), app.ID); err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, gitSourceToDTO(domain.GitSource{}, false))
}

func (s *Server) handleGetWebhook(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	hook, found, err := store.GetWebhook(r.Context(), s.Store.DB(), app.ID)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, webhookToDTO(hook, found, s.webhookBase(r, app), s.publicTargets()))
}

// handleEnableWebhook creates the webhook or rotates its secret. It is the
// only response that ever carries the secret.
func (s *Server) handleEnableWebhook(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req struct{}
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	hook, err := s.C.EnableWebhook(r.Context(), app.ID)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, dto.WebhookSecret{Webhook: webhookToDTO(hook, true, s.webhookBase(r, app), s.publicTargets()), Secret: hook.Secret})
}

func (s *Server) handleDisableWebhook(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	if err := s.C.DisableWebhook(r.Context(), app.ID); err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, webhookToDTO(domain.Webhook{}, false, "", s.publicTargets()))
}

func (s *Server) handleGetPublic(w http.ResponseWriter, r *http.Request) {
	ps, err := s.C.PublicSettings(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.PublicSettings{BaseURL: ps.BaseURL, Targets: nonNil(s.publicTargets())})
}

func (s *Server) handlePutPublic(w http.ResponseWriter, r *http.Request) {
	var req dto.PublicSettingsRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	ps, err := s.C.SetPublicSettings(r.Context(), domain.PublicSettings{BaseURL: req.BaseURL})
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.PublicSettings{BaseURL: ps.BaseURL, Targets: nonNil(s.publicTargets())})
}

// webhookBase is the configured public base URL; failing that, the origin of
// the app's primary domain when the edge forwards /_webhook/* to Bento; else "".
func (s *Server) webhookBase(r *http.Request, app domain.App) string {
	if ps, err := s.C.PublicSettings(r.Context()); err == nil && ps.BaseURL != "" {
		return ps.BaseURL
	}
	es, err := s.C.EdgeSettings(r.Context())
	if err != nil || !es.Enabled || s.C.PublicAppsPort == 0 || app.Ingress != domain.IngressManaged || app.Publication != domain.Published {
		return ""
	}
	for _, d := range app.Domains {
		if !d.Primary {
			continue
		}
		if app.Route.TLS == domain.TLSNone {
			return "http://" + d.Name + portSuffix(es.HTTPPort, 80)
		}
		return "https://" + d.Name + portSuffix(es.HTTPSPort, 443)
	}
	return ""
}

// publicTargets are the public listener origins a proxy can forward to.
func (s *Server) publicTargets() []string {
	if s.PublicAddrs == nil {
		return nil
	}
	var out []string
	for _, a := range s.PublicAddrs() {
		out = append(out, "http://"+a)
	}
	return out
}

func portSuffix(port, def int) string {
	if port == def || port == 0 {
		return ""
	}
	return ":" + strconv.Itoa(port)
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.C.CheckReady(r.Context(), app))
}

// ---- operations ----

func (s *Server) handleListOps(w http.ResponseWriter, r *http.Request) {
	f := store.OpFilter{Limit: 100}
	if t := r.URL.Query().Get("target"); t != "" {
		if !idPattern.MatchString(t) {
			writeError(w, s.Log, badRequest("invalid target"))
			return
		}
		f.TargetID = t
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 500 {
			writeError(w, s.Log, badRequest("limit must be 1-500"))
			return
		}
		f.Limit = n
	}
	ops, err := store.ListOperations(r.Context(), s.Store.DB(), f)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.OperationList{Operations: []dto.Operation{}}
	for _, o := range ops {
		out.Operations = append(out.Operations, opToDTO(o, nil))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetOp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !strings.HasPrefix(id, "op_") || !idPattern.MatchString(id) {
		writeError(w, s.Log, badRequest("invalid operation id"))
		return
	}
	op, err := store.GetOperation(r.Context(), s.Store.DB(), id)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	events, err := store.ListEvents(r.Context(), s.Store.DB(), id, 0)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, opToDTO(op, events))
}

func (s *Server) handleCancelOp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !strings.HasPrefix(id, "op_") || !idPattern.MatchString(id) {
		writeError(w, s.Log, badRequest("invalid operation id"))
		return
	}
	op, err := store.RequestCancel(r.Context(), s.Store.DB(), id)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.C.Wake()
	writeJSON(w, http.StatusOK, opToDTO(op, nil))
}

// ---- services ----

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	rows, err := store.ListServices(r.Context(), s.Store.DB())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.ServiceList{Services: []dto.Service{}}
	for _, row := range rows {
		svc := dto.Service{Name: row.Name, Engine: dto.Engine(row.Engine), Version: row.Version, Image: row.Image, Volume: row.Volume, Initialized: row.Initialized}
		ins, err := s.C.Engine.Inspect(r.Context(), s.C.Names.ServiceContainer(row.Name))
		switch {
		case err != nil:
			svc.State, svc.Message = dto.ObservedStateBlocked, "docker unavailable"
		case ins == nil:
			svc.State = dto.ObservedStateAbsent
		case ins.State.Running && ins.State.Health != nil && ins.State.Health.Status == "healthy":
			svc.State = dto.ObservedStateHealthy
		case ins.State.Running && ins.State.Health != nil && ins.State.Health.Status == "unhealthy":
			svc.State = dto.ObservedStateUnhealthy
		case ins.State.Running:
			svc.State = dto.ObservedStateStarting
		default:
			svc.State = dto.ObservedStateStopped
		}
		out.Services = append(out.Services, svc)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateServiceRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	if req.Engine != dto.EngineMySQL && req.Engine != dto.EnginePostgres {
		writeError(w, s.Log, domain.ValidationErrors{{Field: "engine", Message: "must be mysql or postgres"}})
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	_, op, err := s.C.CreateService(r.Context(), domain.Engine(req.Engine), req.Version, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

// ---- edge, tunnel, proxies ----

func (s *Server) handleGetEdge(w http.ResponseWriter, r *http.Request) {
	es, err := s.C.EdgeSettings(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	st := dto.EdgeStatus{Settings: dto.EdgeSettings(es), State: s.containerState(r.Context(), s.C.Names.EdgeContainer()), Routes: []string{}}
	if entries, err := os.ReadDir(s.Layout.EdgeConfDir() + "/live/sites"); err == nil {
		for _, e := range entries {
			st.Routes = append(st.Routes, strings.TrimSuffix(e.Name(), ".conf"))
		}
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) containerState(ctx context.Context, name string) dto.ObservedState {
	ins, err := s.C.Engine.Inspect(ctx, name)
	switch {
	case err != nil:
		return dto.ObservedStateBlocked
	case ins == nil:
		return dto.ObservedStateAbsent
	case ins.State.Running:
		return dto.ObservedStateHealthy
	default:
		return dto.ObservedStateStopped
	}
}

func (s *Server) handlePutEdge(w http.ResponseWriter, r *http.Request) {
	var req dto.EdgeSettings
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.ConfigureEdge(r.Context(), domain.EdgeSettings(req), idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleGetTunnel(w http.ResponseWriter, r *http.Request) {
	ts, err := s.C.TunnelSettings(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.TunnelStatus{Enabled: ts.Enabled, TokenGeneration: int(ts.TokenGeneration),
		State: s.containerState(r.Context(), s.C.Names.TunnelContainer()),
		Note:  "Cloudflare hostname and origin rules are operator-owned; target http://app-<appId>:<port> or the edge. The token is never returned."})
}

func (s *Server) handlePutTunnel(w http.ResponseWriter, r *http.Request) {
	var req dto.SetTunnelTokenRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SetTunnelToken(r.Context(), req.Token, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleListProxies(w http.ResponseWriter, r *http.Request) {
	ps, err := store.ListProxies(r.Context(), s.Store.DB())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.ProxyList{Proxies: []dto.Proxy{}}
	for _, p := range ps {
		out.Proxies = append(out.Proxies, proxyToDTO(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpsertProxy(w http.ResponseWriter, r *http.Request) {
	var req dto.ProxyRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	_, op, err := s.C.UpsertProxy(r.Context(), operations.ProxyInput{Name: req.Name, Upstreams: req.Upstreams, Domains: req.Domains,
		Route: routeFromDTO(req.Route), Enabled: req.Enabled}, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

func (s *Server) handleDeleteProxy(w http.ResponseWriter, r *http.Request) {
	name, err := pathID(r, "name")
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	var req dto.ConfirmRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.DeleteProxy(r.Context(), name, req.Confirm, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

// ---- retained data ----

func (s *Server) handleListRetired(w http.ResponseWriter, r *http.Request) {
	rs, err := store.ListRetired(r.Context(), s.Store.DB())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.RetiredList{Retired: []dto.RetiredApp{}}
	for _, ra := range rs {
		d := dto.RetiredApp{AppID: ra.AppID, Slug: ra.Slug, UID: ra.UID, RetiredAt: ra.RetiredAt, PrunedAt: ra.PrunedAt,
			Home: ra.Artifacts.Home, SQLiteFileIDs: nonNil(ra.Artifacts.SQLiteFileIDs), Relational: []dto.RetainedRelational{}}
		for _, rel := range ra.Artifacts.Relational {
			d.Relational = append(d.Relational, dto.RetainedRelational{Engine: dto.Engine(rel.Engine), Service: rel.Service, Username: rel.Username, Databases: nonNil(rel.Databases)})
		}
		out.Retired = append(out.Retired, d)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePrune(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	var req dto.ConfirmRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.PruneRetired(r.Context(), id, req.Confirm, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

// metricsMaxProcesses bounds the response; the UI renders them as a tree.
const metricsMaxProcesses = 200

func (s *Server) handleAppMetrics(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	name := s.C.Names.AppContainer(app.ID)
	out := dto.AppMetrics{Processes: []dto.AppProcess{}}
	st, err := s.C.Engine.Stats(ctx, name)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	if st == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	procs, err := s.C.Engine.Top(ctx, name)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out.Running = true
	out.SampledAt = platform.FormatTime(st.SampledAt)
	out.CPUPercent = st.CPUPercent
	out.OnlineCPUs = int(st.OnlineCPUs)
	out.MemoryBytes = int64(st.MemoryUsage)
	out.MemoryLimit = int64(st.MemoryLimit)
	out.NetworkRx, out.NetworkTx = int64(st.NetworkRx), int64(st.NetworkTx)
	out.BlockRead, out.BlockWrite = int64(st.BlockRead), int64(st.BlockWrite)
	out.PIDs = int(st.PIDs)
	out.ProcessTotal = len(procs)
	sort.SliceStable(procs, func(i, j int) bool {
		if procs[i].CPUPercent != procs[j].CPUPercent {
			return procs[i].CPUPercent > procs[j].CPUPercent
		}
		return procs[i].RSSKiB > procs[j].RSSKiB
	})
	if len(procs) > metricsMaxProcesses {
		procs = procs[:metricsMaxProcesses]
	}
	for _, p := range procs {
		out.Processes = append(out.Processes, dto.AppProcess{
			PID: p.PID, PPID: p.PPID, User: p.User, CPUPercent: p.CPUPercent, MemPercent: p.MemPercent,
			RSSBytes: int64(p.RSSKiB) * 1024, Elapsed: p.Elapsed, Command: p.Command,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
