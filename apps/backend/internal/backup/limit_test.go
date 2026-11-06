package backup

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLimitWriterReportsTruncation(t *testing.T) {
	var b bytes.Buffer
	lw := &limitWriter{w: &b, n: 4}
	if n, err := lw.Write([]byte("abc")); n != 3 || err != nil || lw.truncated {
		t.Fatalf("n=%d err=%v truncated=%v", n, err, lw.truncated)
	}
	if n, err := lw.Write([]byte("def")); n != 3 || err != nil || !lw.truncated || b.String() != "abcd" {
		t.Fatalf("n=%d err=%v truncated=%v kept %q", n, err, lw.truncated, b.String())
	}
}

// A staged dump stops before it eats into the space Bento keeps for its
// state, checking before the first write and then every 64 MB.
func TestReserveWriterStopsBeforeTheReserve(t *testing.T) {
	free := uint64(DumpDiskReserve + 100<<20)
	checks := 0
	defer func(f func(string) (uint64, error)) { freeSpace = f }(freeSpace)
	freeSpace = func(string) (uint64, error) { checks++; return free, nil }
	var out bytes.Buffer
	w := &reserveWriter{w: &out, dir: t.TempDir()}
	chunk := make([]byte, 1<<20)
	for range 64 {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if checks != 1 {
		t.Fatalf("checked %d times in the first 64 MB", checks)
	}
	free = DumpDiskReserve - 1
	if _, err := w.Write(chunk); err == nil || !strings.Contains(err.Error(), "stopped the dump") {
		t.Fatalf("a write below the reserve: %v", err)
	}
	if out.Len() != 64<<20 {
		t.Fatalf("wrote %d bytes", out.Len())
	}
	// Free space that cannot be read does not block a backup.
	freeSpace = func(string) (uint64, error) { return 0, errors.New("statfs") }
	if _, err := (&reserveWriter{w: io.Discard, dir: "x"}).Write(chunk); err != nil {
		t.Fatal(err)
	}
}
