package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Stream bounds.
const (
	logMaxDuration   = 30 * time.Minute
	logMaxBytes      = 20 << 20
	logMaxTail       = 5000
	execOutputLimit  = 1 << 20
	execTimeout      = 10 * time.Minute
	schedulerTimeout = 2 * time.Minute
)

// redactor replaces an app's known secret values in app-controlled output.
func redactor(app domain.App) *strings.Replacer { return app.Redactor() }

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
	// Closing the read side unblocks the copier when the handler returns
	// early (client gone, byte limit reached); otherwise it would block in
	// pw.Write forever.
	defer pr.Close()
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
	br := bufio.NewReaderSize(io.LimitReader(pr, logMaxBytes), 64<<10)
	for {
		raw, err := readBoundedLine(br, logLineMax)
		if err != nil && raw == "" {
			break
		}
		line := red.Replace(raw)
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
		_ = sseEvent(w, "state", "", s.opDTO(op, nil))
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
	writeJSON(w, http.StatusOK, redactedExecResult(app, res.ExitCode, res.Stdout, res.Stderr, res.Truncated))
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
	writeJSON(w, http.StatusOK, redactedExecResult(app, res.ExitCode, res.Stdout, res.Stderr, res.Truncated))
}

// logLineMax bounds one streamed log line; longer lines are truncated with
// logTruncMarker and the remainder is discarded so the stream continues.
const (
	logLineMax     = 256 << 10
	logTruncMarker = " …[truncated]"
)

// readBoundedLine reads one newline-terminated line (without the newline),
// keeping at most max bytes. A non-nil error with an empty result means the
// stream ended; a final unterminated line is returned with io.EOF.
func readBoundedLine(br *bufio.Reader, limit int) (string, error) {
	var buf []byte
	truncated := false
	for {
		chunk, err := br.ReadSlice('\n')
		chunk = bytes.TrimSuffix(chunk, []byte("\n"))
		if room := limit - len(buf); len(chunk) > room {
			buf = append(buf, chunk[:max(room, 0)]...)
			truncated = true
		} else {
			buf = append(buf, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		line := strings.TrimSuffix(string(buf), "\r")
		if truncated {
			line += logTruncMarker
		}
		return line, err
	}
}

// redactedExecResult applies the app's log redaction to exec output.
func redactedExecResult(app domain.App, code int, stdout, stderr []byte, truncated bool) dto.ExecResult {
	red := redactor(app)
	return dto.ExecResult{ExitCode: code, Stdout: red.Replace(string(stdout)), Stderr: red.Replace(string(stderr)), Truncated: truncated}
}
