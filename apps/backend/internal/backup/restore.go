package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// RestoreSQLite replaces the binding's database file. The app must be
// stopped so no writer holds the file. The directory belongs to the app, so
// every step works relative to a descriptor of it opened without following a
// link: the database is written, owned and renamed into place by name inside
// that directory and never through a path the app could redirect.
func (d Deps) RestoreSQLite(app domain.App, b domain.Binding, artifactPath string) error {
	dir := d.Layout.SQLiteFileDir(b.SQLiteFileID)
	if err := platform.NoSymlinkBetween(d.Layout.SQLiteDir(), dir); err != nil {
		return err
	}
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer unix.Close(dfd)
	src, err := os.Open(artifactPath)
	if err != nil {
		return err
	}
	defer src.Close()
	partial := ".restore-" + platform.RandomHex(6)
	fd, err := unix.Openat(dfd, partial, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", partial, err)
	}
	f := os.NewFile(uintptr(fd), partial)
	discard := func() { _ = unix.Unlinkat(dfd, partial, 0) }
	head := make([]byte, 16)
	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		_ = f.Close()
		discard()
		return fmt.Errorf("read artifact: %w", err)
	}
	if n < 16 || string(head[:15]) != "SQLite format 3" {
		_ = f.Close()
		discard()
		return errors.New("artifact is not a SQLite database")
	}
	// Any write, copy, sync, chown or close failure aborts before the rename
	// so a truncated database is never published.
	_, err = f.Write(head)
	if err == nil {
		_, err = io.Copy(f, src)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Chown(app.UID, app.GID)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		discard()
		return err
	}
	target := app.Slug + ".db"
	// Stale journal files of the replaced database must not be replayed
	// against the restored file; they may or may not exist.
	for _, sfx := range []string{"-wal", "-shm"} {
		if err := unix.Unlinkat(dfd, target+sfx, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			discard()
			return fmt.Errorf("remove stale journal: %w", err)
		}
	}
	if err := unix.Renameat(dfd, partial, dfd, target); err != nil {
		discard()
		return err
	}
	return unix.Fsync(dfd)
}

// dumpPeekBytes is how much of a MySQL dump is inspected for its charset.
const dumpPeekBytes = 64 << 10

var (
	reCreateDBCharset = regexp.MustCompile(`(?i)CREATE DATABASE[^;]*?CHARACTER SET\s*=?\s*([a-z0-9_]+)(?:[^;]*?COLLATE\s*=?\s*([a-z0-9_]+))?`)
	reSetNames        = regexp.MustCompile(`(?i)SET NAMES\s+'?([a-z0-9_]+)'?(?:\s+COLLATE\s+'?([a-z0-9_]+)'?)?`)
)

// mysqlCharsetClause returns the CHARACTER SET/COLLATE clause for a restored
// database: the dump's CREATE DATABASE line wins, then its first SET NAMES,
// then the Bento default. Only identifier characters are ever accepted.
func mysqlCharsetClause(head []byte) string {
	for _, re := range []*regexp.Regexp{reCreateDBCharset, reSetNames} {
		m := re.FindSubmatch(head)
		if m == nil {
			continue
		}
		cs, coll := strings.ToLower(string(m[1])), strings.ToLower(string(m[2]))
		if cs == "binary" {
			break
		}
		if coll == "" && cs == domain.MySQLDefaultCharset {
			coll = domain.MySQLDefaultCollation
		}
		if coll == "" {
			return "CHARACTER SET " + cs
		}
		return "CHARACTER SET " + cs + " COLLATE " + coll
	}
	return "CHARACTER SET " + domain.MySQLDefaultCharset + " COLLATE " + domain.MySQLDefaultCollation
}
