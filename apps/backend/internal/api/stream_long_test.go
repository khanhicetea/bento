package api

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

func TestReadBoundedLineTruncatesAndContinues(t *testing.T) {
	long := strings.Repeat("x", 300)
	in := "a b\r\n" + long + "\nnext\ntail"
	br := bufio.NewReaderSize(strings.NewReader(in), 16)
	var got []string
	for {
		l, err := readBoundedLine(br, 100)
		if err != nil && l == "" {
			break
		}
		got = append(got, l)
		if err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
	}
	want := []string{"a b", strings.Repeat("x", 100) + logTruncMarker, "next", "tail"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestReadBoundedLineExactLimitNotTruncated(t *testing.T) {
	br := bufio.NewReaderSize(strings.NewReader(strings.Repeat("y", 16)+"\n"), 16)
	l, err := readBoundedLine(br, 16)
	if err != nil || l != strings.Repeat("y", 16) {
		t.Fatalf("got %q %v", l, err)
	}
}

func TestExecResultRedacted(t *testing.T) {
	app := domain.App{Bindings: []domain.Binding{{Password: "s3cret"}}}
	r := redactedExecResult(app, 2, []byte("pw=s3cret"), []byte("err s3cret"), true)
	if r.Stdout != "pw=[redacted]" || r.Stderr != "err [redacted]" || r.ExitCode != 2 || !r.Truncated {
		t.Fatalf("%+v", r)
	}
}
