package api

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func newTestTermSession(t *testing.T) (*termSession, net.Conn) {
	t.Helper()
	shell, remote := net.Pipe()
	ts := &termSession{
		id: "t1", exec: &docker.ExecSession{ID: "e1", Conn: shell, Read: bufio.NewReader(shell)},
		cancel: func() {}, resize: func(uint, uint) {}, done: make(chan struct{}),
	}
	go func() {
		ts.pump()
		ts.kill()
		ts.finish(7)
	}()
	// Closing the remote end ends the pump; wait so no goroutine outlives the test.
	t.Cleanup(func() {
		_ = remote.Close()
		<-ts.done
	})
	return ts, remote
}

func recv(t *testing.T, ch chan []byte) string {
	t.Helper()
	select {
	case b := <-ch:
		return string(b)
	case <-time.After(2 * time.Second):
		t.Fatal("no output")
		return ""
	}
}

func TestTermSessionReattachReplaysBacklog(t *testing.T) {
	ts, remote := newTestTermSession(t)
	sub, gen, backlog, ok := ts.attach()
	if !ok || len(backlog) != 0 {
		t.Fatalf("attach: ok=%v backlog=%q", ok, backlog)
	}
	_, _ = remote.Write([]byte("hello"))
	if got := recv(t, sub.ch); got != "hello" {
		t.Fatalf("got %q", got)
	}
	ts.detach(gen)
	if _, open := <-sub.ch; open {
		t.Fatal("detach must close the subscriber")
	}
	_, _ = remote.Write([]byte(" world"))
	time.Sleep(50 * time.Millisecond)
	sub2, gen2, backlog, ok := ts.attach()
	if !ok || string(backlog) != "hello world" {
		t.Fatalf("reattach: ok=%v backlog=%q", ok, backlog)
	}
	// A second tab takes over; the displaced client learns why.
	sub3, _, _, _ := ts.attach()
	if _, open := <-sub2.ch; open || sub2.reason != closeTakenOver {
		t.Fatalf("displaced subscriber: open=%v reason=%v", open, sub2.reason)
	}
	ts.detach(gen2) // stale generation is ignored
	if ts.grace != nil {
		t.Fatal("stale detach must not start the grace timer")
	}
	_ = remote.Close()
	if _, open := <-sub3.ch; open {
		t.Fatal("exit must close the subscriber")
	}
	if !ts.exited() || ts.code != 7 {
		t.Fatalf("exited=%v code=%d", ts.exited(), ts.code)
	}
	if _, _, _, ok := ts.attach(); ok {
		t.Fatal("attach after exit must fail")
	}
}

func TestTermSessionGraceKillsDetachedShell(t *testing.T) {
	ts, _ := newTestTermSession(t)
	_, gen, _, _ := ts.attach()
	ts.detach(gen)
	ts.mu.Lock()
	ts.grace.Reset(10 * time.Millisecond)
	ts.mu.Unlock()
	select {
	case <-ts.done:
	case <-time.After(2 * time.Second):
		t.Fatal("grace expiry must end the shell")
	}
}

func TestTerminalOwnerLive(t *testing.T) {
	c, h := newServer(t)
	ctx := t.Context()
	now := time.Now()
	if err := store.InsertSession(ctx, h.Store.DB(), store.Session{
		TokenHash: "livehash", CSRFToken: "csrf", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSession(ctx, h.Store.DB(), store.Session{
		TokenHash: "expiredhash", CSRFToken: "csrf", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	for owner, want := range map[string]bool{
		"local":               true,
		"session:livehash":    true,
		"session:expiredhash": false,
		"session:unknownhash": false,
	} {
		if got := c.api.terminalOwnerLive(ctx, owner); got != want {
			t.Errorf("owner %q live = %v, want %v", owner, got, want)
		}
	}
	if err := store.RevokeSession(ctx, h.Store.DB(), "livehash"); err != nil {
		t.Fatal(err)
	}
	if c.api.terminalOwnerLive(ctx, "session:livehash") {
		t.Error("a revoked session must not keep its terminal")
	}
}
