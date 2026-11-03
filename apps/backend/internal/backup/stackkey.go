package backup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// maxKeyBytes bounds a restic key read from disk.
const maxKeyBytes = 1024

// EnsureStackKey returns the stack backup password, creating it (root,
// 0400) when the stack has none; created reports whether this call made it.
// The file is published with link(2), which never replaces an existing key,
// so concurrent callers always agree on one password.
func EnsureStackKey(l platform.Layout) (key string, created bool, err error) {
	key, err = ReadStackKey(l)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return key, false, err
	}
	if err := platform.EnsureDir(l.ResticKeyDir(), 0o700, platform.RootOwner); err != nil {
		return "", false, err
	}
	key = platform.RandomToken(32)
	tmp := l.StackBackupKey() + ".new-" + platform.RandomHex(6)
	if err := platform.AtomicWrite(tmp, []byte(key), 0o400, platform.RootOwner); err != nil {
		return "", false, err
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, l.StackBackupKey()); err != nil {
		if errors.Is(err, fs.ErrExist) {
			key, err = ReadStackKey(l)
			return key, false, err
		}
		return "", false, err
	}
	return key, true, nil
}

// ReadStackKey reads the stack backup password. It must be a non-empty
// regular file of one line; fs.ErrNotExist means the stack has none yet.
func ReadStackKey(l platform.Layout) (string, error) {
	f, err := os.OpenFile(l.StackBackupKey(), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil {
		return "", err
	} else if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", l.StackBackupKey())
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxKeyBytes+1))
	if err != nil {
		return "", err
	}
	key := strings.TrimRight(string(raw), "\r\n")
	if key == "" || len(key) > maxKeyBytes || strings.ContainsAny(key, "\x00\r\n") {
		return "", fmt.Errorf("%s must hold one non-empty line of at most %d bytes", l.StackBackupKey(), maxKeyBytes)
	}
	return key, nil
}
