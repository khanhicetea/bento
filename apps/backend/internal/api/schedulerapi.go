package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

// App scheduler API.
//
// A narrow, typed view of an app's minicrond for API tokens and MCP: list
// jobs and runs, read run logs, run a job, control workers. Each handler
// builds one fixed request to minicrond's own HTTP API and sends it over the
// same UID-matched relay as the Scheduler tab. Admin callers may also create
// and edit jobs; a job is an arbitrary command run as the app, so that is
// exec-level access and stays out of read and operate. Importing and deleting
// jobs stay session-only (scheduler/command and the Scheduler tab).
//
// Output is allowlisted field by field: env values never leave, only their
// keys, and the app's secrets are redacted from commands and log lines.

const (
	schedulerAPIMaxBody  = 4 << 20
	schedulerLogDefault  = 200
	schedulerLogMax      = 1000
	schedulerRunsDefault = 20
	schedulerRunsMax     = 200
)

var (
	schedulerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,99}$`)
	schedulerRunPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
)

func (s *Server) dialScheduler(ctx context.Context, app domain.App) (net.Conn, error) {
	if s.schedulerDial != nil {
		return s.schedulerDial(ctx, app)
	}
	return s.Relay.Dial(ctx, app)
}

// minicronError is minicrond's error envelope.
type minicronError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// schedulerCall sends one bodyless request to the app's minicrond and decodes
// its JSON answer into out.
func (s *Server) schedulerCall(ctx context.Context, app domain.App, method, path, idemKey string, out any) error {
	var hdr http.Header
	if idemKey != "" {
		hdr = http.Header{"Idempotency-Key": {idemKey}}
	}
	_, err := s.schedulerDo(ctx, app, method, path, hdr, nil, out)
	return err
}

// schedulerDo sends one request to the app's minicrond, with an optional JSON
// body and extra headers, and decodes its JSON answer into out. minicrond
// errors become API errors with the app's secrets redacted.
func (s *Server) schedulerDo(ctx context.Context, app domain.App, method, path string, hdr http.Header, body, out any) (http.Header, error) {
	if !app.Minicrond {
		return nil, &apiError{status: http.StatusConflict, code: dto.ErrorCodePrecondition,
			msg: "minicrond is off for this app; turn it on to use its scheduler"}
	}
	if app.DesiredRuntime != domain.DesiredRunning {
		return nil, &apiError{status: http.StatusConflict, code: dto.ErrorCodePrecondition,
			msg: "the app is stopped; start it to use its scheduler"}
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, schedulerTimeout)
	defer cancel()
	tr := &http.Transport{
		DialContext:            func(ctx context.Context, _, _ string) (net.Conn, error) { return s.dialScheduler(ctx, app) },
		DisableKeepAlives:      true,
		ResponseHeaderTimeout:  30 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer tr.CloseIdleConnections()
	// minicrond serves its UI and API under the app's base path.
	req, err := http.NewRequestWithContext(ctx, method, "http://minicrond"+runtime.SchedulerBasePath(app.Slug)+path, rd)
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		s.Log.Warn("scheduler api", "app", app.Slug, "err", err)
		return nil, &apiError{status: http.StatusServiceUnavailable, code: dto.ErrorCodeUnavailable,
			msg: "scheduler unavailable: the app must be running"}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, schedulerAPIMaxBody+1))
	if err != nil {
		return nil, &apiError{status: http.StatusBadGateway, code: dto.ErrorCodeUnavailable, msg: "scheduler response failed"}
	}
	if len(respBody) > schedulerAPIMaxBody {
		return nil, &apiError{status: http.StatusBadGateway, code: dto.ErrorCodeTooLarge, msg: "scheduler response too large"}
	}
	if resp.StatusCode >= 400 {
		var e minicronError
		_ = json.Unmarshal(respBody, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		ae := &apiError{msg: "scheduler: " + redactor(app).Replace(msg)}
		switch resp.StatusCode {
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			ae.status, ae.code = http.StatusBadRequest, dto.ErrorCodeValidation
		case http.StatusForbidden:
			ae.status, ae.code = http.StatusConflict, dto.ErrorCodePrecondition
		case http.StatusNotFound:
			ae.status, ae.code = http.StatusNotFound, dto.ErrorCodeNotFound
		case http.StatusConflict, http.StatusPreconditionFailed:
			ae.status, ae.code = http.StatusConflict, dto.ErrorCodeConflict
		case http.StatusTooManyRequests, http.StatusServiceUnavailable:
			ae.status, ae.code = http.StatusServiceUnavailable, dto.ErrorCodeRateLimited
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				ae.msg += "; retry after " + ra + "s"
			}
		default:
			ae.status, ae.code = http.StatusBadGateway, dto.ErrorCodeUnavailable
		}
		return nil, ae
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return nil, &apiError{status: http.StatusBadGateway, code: dto.ErrorCodeUnavailable,
				msg: "scheduler sent an unreadable response"}
		}
	}
	return resp.Header, nil
}

// schedulerName reads and validates a job or worker name path value.
func schedulerName(r *http.Request) (string, error) {
	n := r.PathValue("name")
	if !schedulerNamePattern.MatchString(n) {
		return "", badRequest("name must be a job or worker name such as queue-default")
	}
	return n, nil
}

func schedulerRunID(r *http.Request) (string, error) {
	id := r.PathValue("run")
	if !schedulerRunPattern.MatchString(id) {
		return "", badRequest("run must be a run id from the scheduler runs list")
	}
	return id, nil
}

// boundedInt parses an optional query integer within [1, maximum].
func boundedInt(r *http.Request, key string, def, maximum int) (int, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maximum {
		return 0, badRequest(key + " must be 1-" + strconv.Itoa(maximum))
	}
	return n, nil
}

func rfc3339(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// minicron wire shapes: only the fields Bento passes on are decoded.
type minicronJob struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Source     string            `json:"source"`
	Enabled    *bool             `json:"enabled"`
	Command    string            `json:"command"`
	Argv       []string          `json:"argv"`
	Schedule   string            `json:"schedule"`
	Timezone   string            `json:"timezone"`
	WorkingDir string            `json:"working_dir"`
	Env        map[string]string `json:"env"`
	SecretEnv  map[string]string `json:"secret_env"`
	NextFireAt *time.Time        `json:"next_fire_at"`
	Worker     *struct {
		Held       bool            `json:"held"`
		Active     bool            `json:"active"`
		Failures   int             `json:"failures"`
		Status     string          `json:"status"`
		Exhaustion json.RawMessage `json:"exhaustion"`
	} `json:"worker_state"`
}

type minicronRun struct {
	ID           string     `json:"run_id"`
	Job          string     `json:"job"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	EndReason    string     `json:"end_reason"`
	Trigger      string     `json:"trigger"`
	Attempt      int        `json:"attempt"`
	ExitCode     *int       `json:"exit_code"`
	Signal       string     `json:"signal"`
	QueuedAt     time.Time  `json:"queued_at"`
	StartedAt    *time.Time `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at"`
	LogBytes     int64      `json:"log_bytes"`
	LogTruncated bool       `json:"log_truncated"`
}

func (m minicronRun) dto() dto.SchedulerRun {
	return dto.SchedulerRun{ID: m.ID, Job: m.Job, Kind: m.Kind, Status: m.Status, EndReason: m.EndReason,
		Trigger: m.Trigger, Attempt: m.Attempt, ExitCode: m.ExitCode, Signal: m.Signal,
		QueuedAt: rfc3339(&m.QueuedAt), StartedAt: rfc3339(m.StartedAt), EndedAt: rfc3339(m.EndedAt),
		LogBytes: m.LogBytes, LogTruncated: m.LogTruncated}
}

func schedulerJobDTO(app domain.App, m minicronJob) dto.SchedulerJob {
	red := redactor(app)
	j := dto.SchedulerJob{
		Name: m.Name, Kind: dto.SchedulerJobKind(m.Kind), ReadOnly: m.Source == "config",
		Enabled: m.Enabled == nil || *m.Enabled, Command: red.Replace(m.Command), Schedule: m.Schedule,
		Timezone: m.Timezone, WorkingDir: m.WorkingDir, NextFireAt: rfc3339(m.NextFireAt), EnvKeys: []string{},
	}
	for _, a := range m.Argv {
		j.Argv = append(j.Argv, red.Replace(a))
	}
	for k := range m.Env {
		j.EnvKeys = append(j.EnvKeys, k)
	}
	for k := range m.SecretEnv {
		if !slices.Contains(j.EnvKeys, k) {
			j.EnvKeys = append(j.EnvKeys, k)
		}
	}
	slices.Sort(j.EnvKeys)
	if w := m.Worker; w != nil {
		j.Worker = &dto.SchedulerWorkerState{Status: w.Status, Active: w.Active, Held: w.Held, Failures: w.Failures,
			Exhausted: len(w.Exhaustion) > 0 && string(w.Exhaustion) != "null"}
	}
	return j
}

func (s *Server) handleSchedulerJobs(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var in struct {
		Items []minicronJob `json:"items"`
	}
	if err := s.schedulerCall(r.Context(), app, http.MethodGet, "/api/v1/jobs", "", &in); err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.SchedulerJobList{Jobs: []dto.SchedulerJob{}}
	for _, m := range in.Items {
		out.Jobs = append(out.Jobs, schedulerJobDTO(app, m))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSchedulerRuns(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	limit, err := boundedInt(r, "limit", schedulerRunsDefault, schedulerRunsMax)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if job := r.URL.Query().Get("job"); job != "" {
		if !schedulerNamePattern.MatchString(job) {
			writeError(w, s.Log, badRequest("job must be a job or worker name"))
			return
		}
		q.Set("job", job)
	}
	if before := r.URL.Query().Get("before"); before != "" {
		if !schedulerRunPattern.MatchString(before) {
			writeError(w, s.Log, badRequest("before must be a run id"))
			return
		}
		q.Set("before", before)
	}
	switch f := r.URL.Query().Get("filter"); f {
	case "":
	case "failed", "scheduled", "manual", "active":
		q.Set("filter", f)
	default:
		writeError(w, s.Log, badRequest("filter must be failed, scheduled, manual or active"))
		return
	}
	var in struct {
		Items      []minicronRun `json:"items"`
		NextBefore string        `json:"next_before"`
	}
	if err := s.schedulerCall(r.Context(), app, http.MethodGet, "/api/v1/runs?"+q.Encode(), "", &in); err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.SchedulerRunList{Runs: []dto.SchedulerRun{}, NextBefore: in.NextBefore}
	for _, m := range in.Items {
		out.Runs = append(out.Runs, m.dto())
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSchedulerRunLog(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	id, err := schedulerRunID(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	limit, err := boundedInt(r, "limit", schedulerLogDefault, schedulerLogMax)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	var after uint64
	if v := r.URL.Query().Get("after"); v != "" {
		if after, err = strconv.ParseUint(v, 10, 64); err != nil {
			writeError(w, s.Log, badRequest("after must be a log sequence number"))
			return
		}
	}
	q := url.Values{"after": {strconv.FormatUint(after, 10)}, "limit": {strconv.Itoa(limit)}}
	var in struct {
		Items []struct {
			Sequence uint64 `json:"sequence"`
			Stream   int    `json:"stream"`
			Payload  []byte `json:"payload"`
		} `json:"items"`
	}
	if err := s.schedulerCall(r.Context(), app, http.MethodGet, "/api/v1/runs/"+id+"/log?"+q.Encode(), "",
		&in); err != nil {
		writeError(w, s.Log, err)
		return
	}
	red := redactor(app)
	out := dto.SchedulerRunLog{RunID: id, Lines: []dto.SchedulerLogLine{}, Next: after, More: len(in.Items) >= limit}
	for _, f := range in.Items {
		line := strings.TrimSuffix(strings.TrimSuffix(string(f.Payload), "\n"), "\r")
		out.Lines = append(out.Lines, dto.SchedulerLogLine{Seq: f.Sequence, Stderr: f.Stream == 2, Line: red.Replace(line)})
		out.Next = f.Sequence
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSchedulerRunJob(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	name, err := schedulerName(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	var run minicronRun
	if err := s.schedulerCall(r.Context(), app, http.MethodPost, "/api/v1/jobs/"+name+"/trigger", key,
		&run); err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run.dto())
}

func (s *Server) handleSchedulerJobEnabled(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	name, err := schedulerName(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	var req dto.SchedulerEnabledRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	action := "disable"
	if req.Enabled {
		action = "enable"
	}
	if err := s.schedulerCall(r.Context(), app, http.MethodPost, "/api/v1/jobs/"+name+"/"+action, "",
		nil); err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.SchedulerResult{Name: name, Action: action})
}

func (s *Server) handleSchedulerWorker(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	name, err := schedulerName(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	action := r.PathValue("action")
	switch action {
	case "start", "stop", "restart":
	default:
		writeError(w, s.Log, notFound("worker action must be start, stop or restart"))
		return
	}
	if err := s.schedulerCall(r.Context(), app, http.MethodPost, "/api/v1/workers/"+name+"/"+action, "",
		nil); err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, dto.SchedulerResult{Name: name, Action: action})
}

func (s *Server) handleSchedulerStopRun(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	id, err := schedulerRunID(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	if err := s.schedulerCall(r.Context(), app, http.MethodPost, "/api/v1/runs/"+id+"/stop", "", nil); err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusAccepted, dto.SchedulerResult{Name: id, Action: "stop"})
}

// Job definition writes (admin). minicrond validates schedules, commands and
// working directories; Bento checks shape and bounds first so nothing odd is
// forwarded.

var schedulerEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

const (
	schedulerReservedPrefix = "zz-bento-"
	schedulerMaxCommand     = 64 << 10
	schedulerMaxArgv        = 128
	schedulerMaxEnv         = 100
	schedulerMaxEnvValue    = 32 << 10
)

func jobFieldError(field, msg string) error {
	return &apiError{status: http.StatusBadRequest, code: dto.ErrorCodeValidation, msg: "validation failed",
		fields: []dto.FieldError{{Field: field, Message: msg}}}
}

// applyJobRequest applies req onto def, minicrond's editable definition as
// raw JSON fields, so fields Bento does not model pass through unchanged.
func applyJobRequest(def map[string]any, req dto.SchedulerJobRequest) error {
	if req.Command != nil && req.Argv != nil {
		return jobFieldError("command", "set command or argv, not both")
	}
	if req.Command != nil {
		if *req.Command == "" || len(*req.Command) > schedulerMaxCommand {
			return jobFieldError("command", "must be 1-65536 bytes")
		}
		def["command"] = *req.Command
		delete(def, "argv")
	}
	if req.Argv != nil {
		if len(req.Argv) == 0 || len(req.Argv) > schedulerMaxArgv {
			return jobFieldError("argv", "1-128 arguments required")
		}
		def["argv"] = req.Argv
		delete(def, "command")
	}
	setStr := func(key string, v *string) {
		if v == nil {
			return
		}
		if *v == "" {
			delete(def, key)
		} else {
			def[key] = *v
		}
	}
	setStr("schedule", req.Schedule)
	setStr("timezone", req.Timezone)
	setStr("working_dir", req.WorkingDir)
	if req.Enabled != nil {
		def["enabled"] = *req.Enabled
	}
	for field, v := range map[string]*int{"timeout": req.Timeout, "retries": req.Retries} {
		if v == nil {
			continue
		}
		if *v < 0 {
			return jobFieldError(field, "must not be negative")
		}
		def[field] = *v
	}
	if req.Env == nil && req.RemoveEnv == nil {
		return nil
	}
	env := map[string]any{}
	if cur, ok := def["env"].(map[string]any); ok {
		env = cur
	}
	for _, k := range req.RemoveEnv {
		delete(env, k)
	}
	for k, v := range req.Env {
		if !schedulerEnvKeyPattern.MatchString(k) {
			return jobFieldError("env", "keys must match [A-Za-z_][A-Za-z0-9_]*")
		}
		if len(v) > schedulerMaxEnvValue {
			return jobFieldError("env", k+" is longer than 32 KiB")
		}
		env[k] = v
	}
	if len(env) > schedulerMaxEnv {
		return jobFieldError("env", "at most 100 variables")
	}
	if len(env) == 0 {
		delete(def, "env")
	} else {
		def["env"] = env
	}
	return nil
}

func (s *Server) handleSchedulerCreateJob(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.SchedulerJobRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	if !schedulerNamePattern.MatchString(req.Name) {
		writeError(w, s.Log, jobFieldError("name", "lowercase letters, digits, '.', '_' and '-', at most 100"))
		return
	}
	if strings.HasPrefix(req.Name, schedulerReservedPrefix) {
		writeError(w, s.Log, jobFieldError("name", "names starting with zz-bento- are reserved for Bento"))
		return
	}
	if req.Kind != dto.SchedulerJobKindJob && req.Kind != dto.SchedulerJobKindWorker {
		writeError(w, s.Log, jobFieldError("kind", "must be job or worker"))
		return
	}
	if req.Command == nil && req.Argv == nil {
		writeError(w, s.Log, jobFieldError("command", "set command or argv"))
		return
	}
	def := map[string]any{"name": req.Name, "kind": string(req.Kind)}
	if err := applyJobRequest(def, req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	var saved minicronJob
	// If-None-Match: * makes minicrond refuse to replace an existing job.
	if _, err := s.schedulerDo(r.Context(), app, http.MethodPost, "/api/v1/jobs",
		http.Header{"If-None-Match": {"*"}}, def, &saved); err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusCreated, schedulerJobDTO(app, saved))
}

func (s *Server) handleSchedulerUpdateJob(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	name, err := schedulerName(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	var req dto.SchedulerJobRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	if req.Name != "" && req.Name != name {
		writeError(w, s.Log, jobFieldError("name", "a job cannot be renamed"))
		return
	}
	var cur struct {
		Definition map[string]any `json:"definition"`
	}
	hdr, err := s.schedulerDo(r.Context(), app, http.MethodGet, "/api/v1/jobs/"+name, nil, nil, &cur)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	def := cur.Definition
	if def == nil {
		writeError(w, s.Log, &apiError{status: http.StatusBadGateway, code: dto.ErrorCodeUnavailable,
			msg: "scheduler sent no definition"})
		return
	}
	if def["source"] == "config" {
		writeError(w, s.Log, &apiError{status: http.StatusConflict, code: dto.ErrorCodePrecondition,
			msg: "this task is owned by Bento's config and is read-only"})
		return
	}
	if req.Kind != "" && string(req.Kind) != def["kind"] {
		writeError(w, s.Log, jobFieldError("kind", "a job cannot change kind"))
		return
	}
	if err := applyJobRequest(def, req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	for _, k := range []string{"definition_id", "revision", "source", "next_fire_at"} {
		delete(def, k)
	}
	// If-Match pins the revision read above: a concurrent edit is a conflict,
	// never silently overwritten.
	put := http.Header{}
	if etag := hdr.Get("ETag"); etag != "" {
		put.Set("If-Match", etag)
	}
	var saved minicronJob
	if _, err := s.schedulerDo(r.Context(), app, http.MethodPut, "/api/v1/jobs/"+name, put, def, &saved); err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, schedulerJobDTO(app, saved))
}
