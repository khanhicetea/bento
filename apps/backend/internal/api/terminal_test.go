package api

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
)

func newTestTermSession(t *testing.T) (*termSession, net.Conn) {
	t.Helper()
	shell, remote := net.Pipe()
	_, cancel := context.WithCancel(context.Background())
	ts := &termSession{
		id: "t1", exec: &docker.ExecSession{ID: "e1", Conn: shell, Read: bufio.NewReader(shell)},
		cancel: cancel, resize: func(uint, uint) {}, done: make(chan struct{}),
	}
	go func() {
		ts.pump()
		ts.kill()
		ts.finish(7)
	}()
	t.Cleanup(func() { _ = remote.Close() })
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
