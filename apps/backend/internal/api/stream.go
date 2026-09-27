package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/moby/moby/api/pkg/stdcopy"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Stream bounds.
const (
	logMaxDuration   = 30 * time.Minute
	logMaxBytes      = 20 << 20
	logMaxTail       = 5000
	terminalIdle     = 30 * time.Minute
	terminalMaxAge   = 4 * time.Hour
	execOutputLimit  = 1 << 20
	execTimeout      = 10 * time.Minute
	schedulerTimeout = 2 * time.Minute
)

// redactor replaces an app's known secret values in app-controlled output.
func redactor(app domain.App) *strings.Replacer {
	var pairs []string
	for _, b := range app.Bindings {
		if b.Password != "" {
			pairs = append(pairs, b.Password, "[redacted]")
		}
	}
	if app.Redis.Password != "" {
		pairs = append(pairs, app.Redis.Password, "[redacted]")
	}
	return strings.NewReplacer(pairs...)
}

func sse(w http.ResponseWriter) (http.Flusher, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return f, true
}

func sseEvent(w io.Writer, event, id string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if id != "" {
		fmt.Fprintf(w, "id: %s\n", id)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}

// handleAppLogs streams the persistent instance's Docker logs as SSE.
// Reconnects pass ?since=<RFC3339> (or Last-Event-ID) to resume.
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	tail := 200
	if t := q.Get("tail"); t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 0 || n > logMaxTail {
			writeError(w, s.Log, badRequest(fmt.Sprintf("tail must be 0-%d", logMaxTail)))
			return
		}
		tail = n
	}
	since := q.Get("since")
	if id := r.Header.Get("Last-Event-ID"); id != "" {
		since = id
	}
	if since != "" {
		if _, err := time.Parse(time.RFC3339Nano, since); err != nil {
			writeError(w, s.Log, badRequest("since must be an RFC 3339 timestamp"))
			return
		}
	}
	follow := q.Get("follow") == "1"
	ctx, cancel := context.WithTimeout(r.Context(), logMaxDuration)
	defer cancel()
	rc, tty, err := s.C.Engine.Logs(ctx, s.C.Names.AppContainer(app.ID), strconv.Itoa(tail), follow, since)
	if err != nil {
		writeError(w, s.Log, notFound("no instance logs available"))
		return
	}
	defer rc.Close()
	flusher, ok := sse(w)
	if !ok {
		return
	}
	pr, pw := io.Pipe()
	go func() {
		if tty {
			_, err := io.Copy(pw, rc)
			pw.CloseWithError(err)
			return
		}
		_, err := stdcopy.StdCopy(pw, pw, rc)
		pw.CloseWithError(err)
	}()
	red := redactor(app)
	sc := bufio.NewScanner(io.LimitReader(pr, logMaxBytes))
	sc.Buffer(make([]byte, 64<<10), 256<<10)
	for sc.Scan() {
		line := red.Replace(sc.Text())
		ts, msg, _ := strings.Cut(line, " ")
		if err := sseEvent(w, "log", ts, map[string]string{"ts": ts, "line": msg}); err != nil {
			return
		}
		flusher.Flush()
	}
	_ = sseEvent(w, "end", "", map[string]string{"reason": "stream ended; reconnect with since=<last ts>"})
	flusher.Flush()
}

// handleOpEvents streams an operation's journal until it is terminal.
func (s *Server) handleOpEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !strings.HasPrefix(id, "op_") || !idPattern.MatchString(id) {
		writeError(w, s.Log, badRequest("invalid operation id"))
		return
	}
	if _, err := store.GetOperation(r.Context(), s.Store.DB(), id); err != nil {
		writeError(w, s.Log, err)
		return
	}
	after := 0
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		after, _ = strconv.Atoi(v)
	}
	flusher, ok := sse(w)
	if !ok {
		return
	}
	ch, unsub := s.C.Subscribe()
	defer unsub()
	ctx, cancel := context.WithTimeout(r.Context(), time.Hour)
	defer cancel()
	for {
		events, err := store.ListEvents(ctx, s.Store.DB(), id, after)
		if err != nil {
			return
		}
		for _, e := range events {
			after = e.Seq
			if sseEvent(w, "event", strconv.Itoa(e.Seq), dto.OperationEvent{Seq: e.Seq, At: e.At, Level: e.Level, Message: e.Message}) != nil {
				return
			}
		}
		op, err := store.GetOperation(ctx, s.Store.DB(), id)
		if err != nil {
			return
		}
		_ = sseEvent(w, "state", "", opToDTO(op, nil))
		flusher.Flush()
		if op.State.Terminal() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ch:
		case <-time.After(2 * time.Second):
		}
	}
}

// handleExec runs a bounded, non-interactive command as the app identity in
// a scoped tooling container (default) or the running instance.
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.ExecRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	if len(req.Argv) == 0 || len(req.Argv) > 256 {
		writeError(w, s.Log, domain.ValidationErrors{{Field: "argv", Message: "1-256 arguments required"}})
		return
	}
	er, err := operations.ExecRequestFor(app, req.Argv, req.Workdir)
	if err != nil {
		writeError(w, s.Log, domain.ValidationErrors{{Field: "workdir", Message: err.Error()}})
		return
	}
	er.OutputLimit = execOutputLimit
	ctx, cancel := context.WithTimeout(r.Context(), execTimeout)
	defer cancel()
	var target string
	if req.Running {
		target, err = s.C.RunningInstance(ctx, app)
	} else {
		var tool *operations.ToolSession
		tool, err = s.C.OpenTool(ctx, app, execTimeout)
		if tool != nil {
			defer tool.Close()
			target = tool.ContainerID
		}
	}
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	res, err := s.C.Engine.Exec(ctx, target, er)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.ExecResult{ExitCode: res.ExitCode, Stdout: string(res.Stdout), Stderr: string(res.Stderr), Truncated: res.Truncated})
}

// handleSchedulerCommand runs `minicrond <argv>` inside the selected running
// app as its numeric identity. Argv is passed as argv, never via a shell,
// and no second daemon is ever started.
func (s *Server) handleSchedulerCommand(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.SchedulerCommandRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	if len(req.Argv) == 0 || len(req.Argv) > 128 {
		writeError(w, s.Log, domain.ValidationErrors{{Field: "argv", Message: "1-128 arguments required"}})
		return
	}
	if req.Argv[0] == "daemon" {
		writeError(w, s.Log, domain.ValidationErrors{{Field: "argv", Message: "starting a second daemon is not allowed"}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), schedulerTimeout)
	defer cancel()
	id, err := s.C.RunningInstance(ctx, app)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	er, _ := operations.ExecRequestFor(app, append([]string{"minicrond"}, req.Argv...), "")
	er.OutputLimit = execOutputLimit
	res, err := s.C.Engine.Exec(ctx, id, er)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.ExecResult{ExitCode: res.ExitCode, Stdout: string(res.Stdout), Stderr: string(res.Stderr), Truncated: res.Truncated})
}

type termControl struct {
	Type string `json:"type"`
	Cols uint   `json:"cols"`
	Rows uint   `json:"rows"`
	Code int    `json:"code"`
}

// handleTerminal is an authenticated, bidirectional terminal over
// WebSocket. Binary frames carry terminal bytes; text frames carry JSON
// control messages (resize from client, exit status from server).
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	p, _ := principalFrom(r.Context())
	if p.Kind == "session" {
		// WebSocket upgrades are GETs: enforce exact Origin and the CSRF token.
		if !s.originAllowed(r.Header.Get("Origin")) || r.URL.Query().Get("csrf") != p.Session.CSRFToken {
			writeError(w, s.Log, &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden, msg: "origin or CSRF check failed"})
			return
		}
	}
	running := r.URL.Query().Get("mode") == "running"
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if cols <= 0 || cols > 1000 {
		cols = 120
	}
	if rows <= 0 || rows > 1000 {
		rows = 32
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), terminalMaxAge)
	defer cancel()
	var target string
	var err error
	var tool *operations.ToolSession
	if running {
		target, err = s.C.RunningInstance(ctx, app)
	} else {
		tool, err = s.C.OpenTool(ctx, app, terminalMaxAge)
		if tool != nil {
			defer tool.Close()
			target = tool.ContainerID
		}
	}
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	er, _ := operations.ExecRequestFor(app, []string{"bash", "-l"}, "")
	er.Env = append(er.Env, "TERM=xterm-256color")
	sess, err := s.C.Engine.ExecAttach(ctx, target, er, uint(rows), uint(cols))
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	defer sess.Conn.Close()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // origin verified above
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 10)
	activity := make(chan struct{}, 1)
	touch := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := sess.Read.Read(buf)
			if n > 0 {
				touch()
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				code := -1
				if c, done, e := s.C.Engine.ExecExitCode(context.Background(), sess.ID); e == nil && done {
					code = c
				}
				msg, _ := json.Marshal(termControl{Type: "exit", Code: code})
				_ = conn.Write(ctx, websocket.MessageText, msg)
				_ = conn.Close(websocket.StatusNormalClosure, "exited")
				cancel()
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-activity:
			case <-time.After(terminalIdle):
				_ = conn.Close(websocket.StatusPolicyViolation, "idle timeout")
				cancel()
				return
			}
		}
	}()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		touch()
		if typ == websocket.MessageBinary {
			if _, err := sess.Conn.Write(data); err != nil {
				return
			}
			continue
		}
		var c termControl
		if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Cols > 0 && c.Rows > 0 && c.Cols <= 1000 && c.Rows <= 1000 {
			_ = s.C.Engine.ExecResize(ctx, sess.ID, c.Rows, c.Cols)
		}
	}
}

var _ = docker.ExecRequest{}
