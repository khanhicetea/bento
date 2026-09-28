package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// Terminal bounds. A shell outlives its WebSocket: when the browser drops the
// connection (network blip, reload, switching tabs) the shell and its tool
// container are kept detached for terminalGrace so the client can reattach.
const (
	terminalIdle    = 30 * time.Minute
	terminalGrace   = 15 * time.Minute
	terminalMaxAge  = 4 * time.Hour
	terminalBacklog = 64 << 10
)

// WebSocket close codes the client uses to decide whether to reconnect.
const (
	closeTakenOver websocket.StatusCode = 4001
	closeSlow      websocket.StatusCode = 4002
)

type termControl struct {
	Type    string `json:"type"`
	Cols    uint   `json:"cols,omitempty"`
	Rows    uint   `json:"rows,omitempty"`
	Code    int    `json:"code"`
	ID      string `json:"id,omitempty"`
	Resumed bool   `json:"resumed,omitempty"`
}

// terminalEnv persists interactive history in the app home, which is the
// same bind mount for tool and running containers, so both modes share it.
// Lines are appended at every prompt so a dropped shell loses nothing.
func terminalEnv(app domain.App) []string {
	return []string{
		"TERM=xterm-256color",
		"HISTFILE=" + app.ContainerHome() + "/.bash_history",
		"HISTSIZE=10000",
		"HISTFILESIZE=10000",
		"HISTCONTROL=ignoreboth",
		"PROMPT_COMMAND=history -a",
	}
}

type terminalRegistry struct {
	mu       sync.Mutex
	sessions map[string]*termSession
}

func (g *terminalRegistry) add(t *termSession) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessions == nil {
		g.sessions = map[string]*termSession{}
	}
	g.sessions[t.id] = t
}

func (g *terminalRegistry) get(id string) *termSession {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessions[id]
}

func (g *terminalRegistry) remove(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.sessions, id)
}

// termSub is one attached WebSocket. reason is set before ch is closed.
type termSub struct {
	ch     chan []byte
	reason websocket.StatusCode
}

// termSession is a live exec with at most one attached client. Output is
// kept in a bounded backlog that is replayed on reattach.
type termSession struct {
	id, appID, owner, mode string
	exec                   *docker.ExecSession
	cancel                 context.CancelFunc
	resize                 func(rows, cols uint)

	mu      sync.Mutex
	backlog []byte
	sub     *termSub
	gen     uint64
	grace   *time.Timer
	done    chan struct{}
	code    int
}

// kill ends the shell; the pump observes the closed stream and finishes.
func (t *termSession) kill() {
	t.cancel()
	_ = t.exec.Conn.Close()
}

func (t *termSession) pump() {
	buf := make([]byte, 32<<10)
	for {
		n, err := t.exec.Read.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			t.mu.Lock()
			t.backlog = append(t.backlog, chunk...)
			if over := len(t.backlog) - terminalBacklog; over > 0 {
				t.backlog = append([]byte(nil), t.backlog[over:]...)
			}
			if t.sub != nil {
				select {
				case t.sub.ch <- chunk:
				default:
					t.dropLocked(closeSlow)
				}
			}
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *termSession) finish(code int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.code = code
	close(t.done)
	if t.grace != nil {
		t.grace.Stop()
		t.grace = nil
	}
	t.dropLocked(websocket.StatusNormalClosure)
}

func (t *termSession) dropLocked(reason websocket.StatusCode) {
	if t.sub != nil {
		t.sub.reason = reason
		close(t.sub.ch)
		t.sub = nil
	}
}

func (t *termSession) exited() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

// attach makes a new client the only subscriber, displacing any other tab.
func (t *termSession) attach() (*termSub, uint64, []byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exited() {
		return nil, 0, nil, false
	}
	t.dropLocked(closeTakenOver)
	if t.grace != nil {
		t.grace.Stop()
		t.grace = nil
	}
	t.gen++
	t.sub = &termSub{ch: make(chan []byte, 256)}
	return t.sub, t.gen, append([]byte(nil), t.backlog...), true
}

// detach starts the grace period unless a newer client already attached.
func (t *termSession) detach(gen uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if gen != t.gen || t.exited() {
		return
	}
	t.dropLocked(websocket.StatusNormalClosure)
	if t.grace == nil {
		t.grace = time.AfterFunc(terminalGrace, t.kill)
	}
}

// openTerminal starts a shell in a new tool container or the running
// instance. Its lifetime is bounded by terminalMaxAge, not by the request.
func (s *Server) openTerminal(app domain.App, owner, mode string, rows, cols int) (*termSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), terminalMaxAge)
	var target string
	var err error
	var tool *operations.ToolSession
	if mode == "running" {
		target, err = s.C.RunningInstance(ctx, app)
	} else {
		tool, err = s.C.OpenTool(ctx, app, terminalMaxAge)
		if tool != nil {
			target = tool.ContainerID
		}
	}
	if err != nil {
		cancel()
		tool.Close()
		return nil, err
	}
	er, _ := operations.ExecRequestFor(app, []string{"bash", "-l"}, "")
	er.Env = append(er.Env, terminalEnv(app)...)
	sess, err := s.C.Engine.ExecAttach(ctx, target, er, uint(rows), uint(cols))
	if err != nil {
		cancel()
		tool.Close()
		return nil, err
	}
	t := &termSession{
		id: platform.RandomHex(16), appID: app.ID, owner: owner, mode: mode,
		exec: sess, cancel: cancel, done: make(chan struct{}),
		resize: func(rows, cols uint) { _ = s.C.Engine.ExecResize(ctx, sess.ID, rows, cols) },
	}
	s.terminals.add(t)
	go func() {
		<-ctx.Done()
		t.kill()
	}()
	go func() {
		t.pump()
		code := -1
		if c, done, e := s.C.Engine.ExecExitCode(context.Background(), sess.ID); e == nil && done {
			code = c
		}
		s.terminals.remove(t.id)
		t.kill()
		tool.Close()
		t.finish(code)
	}()
	return t, nil
}

// handleTerminal is an authenticated, bidirectional terminal over
// WebSocket. Binary frames carry terminal bytes; text frames carry JSON
// control messages (resize and close from client; session and exit from
// server). Passing ?session= reattaches a detached shell of the same app,
// mode, and principal, replaying its recent output.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	p, _ := principalFrom(r.Context())
	owner := "local"
	if p.Kind == "session" {
		// WebSocket upgrades are GETs: enforce exact Origin and the CSRF token.
		if !s.originAllowed(r.Header.Get("Origin")) || r.URL.Query().Get("csrf") != p.Session.CSRFToken {
			writeError(w, s.Log, &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden, msg: "origin or CSRF check failed"})
			return
		}
		owner = "session:" + p.Session.TokenHash
	}
	mode := "tool"
	if r.URL.Query().Get("mode") == "running" {
		mode = "running"
	}
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if cols <= 0 || cols > 1000 {
		cols = 120
	}
	if rows <= 0 || rows > 1000 {
		rows = 32
	}

	var sub *termSub
	var gen uint64
	var backlog []byte
	resumed := false
	t := s.terminals.get(r.URL.Query().Get("session"))
	if t != nil && t.appID == app.ID && t.owner == owner && t.mode == mode {
		sub, gen, backlog, resumed = t.attach()
	}
	if !resumed {
		var err error
		if t, err = s.openTerminal(app, owner, mode, rows, cols); err != nil {
			writeError(w, s.Log, err)
			return
		}
		if sub, gen, _, ok = t.attach(); !ok {
			writeError(w, s.Log, &apiError{status: http.StatusConflict, code: dto.ErrorCodeConflict, msg: "shell exited immediately"})
			return
		}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // origin verified above
	if err != nil {
		t.detach(gen)
		return
	}
	defer conn.CloseNow()
	defer t.detach(gen)
	conn.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()

	hello, _ := json.Marshal(termControl{Type: "session", ID: t.id, Resumed: resumed})
	if conn.Write(ctx, websocket.MessageText, hello) != nil {
		return
	}
	if resumed {
		t.resize(uint(rows), uint(cols))
		if len(backlog) > 0 && conn.Write(ctx, websocket.MessageBinary, backlog) != nil {
			return
		}
	}

	activity := make(chan struct{}, 1)
	touch := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, open := <-sub.ch:
				if !open {
					if t.exited() {
						msg, _ := json.Marshal(termControl{Type: "exit", Code: t.code})
						_ = conn.Write(ctx, websocket.MessageText, msg)
						_ = conn.Close(websocket.StatusNormalClosure, "exited")
					} else if sub.reason == closeTakenOver {
						_ = conn.Close(closeTakenOver, "attached in another tab")
					} else if sub.reason == closeSlow {
						_ = conn.Close(closeSlow, "client too slow")
					}
					return
				}
				touch()
				if conn.Write(ctx, websocket.MessageBinary, chunk) != nil {
					return
				}
			case <-activity:
			case <-time.After(terminalIdle):
				_ = conn.Close(websocket.StatusPolicyViolation, "idle timeout")
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
			if _, err := t.exec.Conn.Write(data); err != nil {
				return
			}
			continue
		}
		var c termControl
		if json.Unmarshal(data, &c) != nil {
			continue
		}
		switch {
		case c.Type == "resize" && c.Cols > 0 && c.Rows > 0 && c.Cols <= 1000 && c.Rows <= 1000:
			t.resize(c.Rows, c.Cols)
		case c.Type == "close":
			t.kill()
		}
	}
}
