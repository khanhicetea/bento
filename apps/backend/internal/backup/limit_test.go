package backup

import (
	"bytes"
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
