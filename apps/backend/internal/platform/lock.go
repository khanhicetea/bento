package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrLocked is returned when an exclusive lock is already held elsewhere.
var ErrLocked = errors.New("lock is held by another process")

// FileLock is an exclusive advisory flock held for a process lifetime.
type FileLock struct {
	f    *os.File
	path string
}

// TryLock acquires an exclusive non-blocking lock at path and records the
// holder PID for diagnostics.
func TryLock(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := readHolder(f)
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			if holder != "" {
				return nil, fmt.Errorf("%w (%s, pid %s)", ErrLocked, path, holder)
			}
			return nil, fmt.Errorf("%w (%s)", ErrLocked, path)
		}
		return nil, err
	}
	// The holder PID is diagnostic only; the flock is what excludes others.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return &FileLock{f: f, path: path}, nil
}

// Lock acquires an exclusive lock, blocking until available.
func Lock(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileLock{f: f, path: path}, nil
}

func readHolder(f *os.File) string {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0) // best effort: a short or failed read yields ""
	return strings.TrimSpace(string(buf[:n]))
}

func (l *FileLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	// Closing the descriptor drops the flock even if the explicit unlock fails.
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
