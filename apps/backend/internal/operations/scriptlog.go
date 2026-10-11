package operations

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"
)

const (
	// scriptLogLive is how many script lines are journalled as they arrive;
	// later lines are only kept in the tail and journalled when the script ends.
	// Live plus tail stays well under the store's per-operation journal cap
	// (200 events), so the start of a deploy is never trimmed away.
	scriptLogLive = 120
	// scriptLogTail is how many trailing lines are kept for the end-of-run
	// flush and the failure summary.
	scriptLogTail = 40
)

// ansiEscape matches CSI sequences (colours, cursor moves) and OSC sequences
// that tools such as Composer and artisan emit even when not on a terminal.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// scriptLog journals a script's stdout and stderr as one stream, in the order
// Docker delivered them, so an error appears after the output that preceded
// it. Lines are stripped of escape sequences and redacted. It is safe for the
// exec copy goroutine to keep writing after Exec has returned on a timeout.
type scriptLog struct {
	ctx    context.Context
	r      *Run
	prefix string
	redact *strings.Replacer

	mu      sync.Mutex
	emitted int
	omitted int
	tail    []scriptLine
}

type scriptLine struct {
	warn bool
	text string
	live bool // already journalled
}

func newScriptLog(ctx context.Context, r *Run, prefix string, redact *strings.Replacer) *scriptLog {
	return &scriptLog{ctx: ctx, r: r, prefix: prefix, redact: redact}
}

// Stream returns a writer for one of the script's streams; stderr lines are
// journalled as warnings.
func (l *scriptLog) Stream(warn bool) *scriptStream { return &scriptStream{log: l, warn: warn} }

func (l *scriptLog) line(warn bool, raw string) {
	text := strings.TrimSpace(ansiEscape.ReplaceAllString(strings.TrimRight(raw, "\r"), ""))
	// Progress bars redraw with carriage returns; keep only the final state.
	if i := strings.LastIndexByte(text, '\r'); i >= 0 {
		text = strings.TrimSpace(text[i+1:])
	}
	if text == "" {
		return
	}
	text = l.redact.Replace(text)
	l.mu.Lock()
	defer l.mu.Unlock()
	ln := scriptLine{warn: warn, text: text}
	if l.emitted < scriptLogLive {
		l.emitted++
		ln.live = true
		l.journal(ln)
	} else {
		l.omitted++
	}
	l.tail = append(l.tail, ln)
	if len(l.tail) > scriptLogTail {
		l.tail = l.tail[1:]
	}
}

func (l *scriptLog) journal(ln scriptLine) {
	if ln.warn {
		l.r.Warn(l.ctx, "%s%s", l.prefix, ln.text)
	} else {
		l.r.Info(l.ctx, "%s%s", l.prefix, ln.text)
	}
}

// Finish journals the tail lines that were held back because the live budget
// ran out, preceded by a count of the lines in between.
func (l *scriptLog) Finish() {
	l.mu.Lock()
	defer l.mu.Unlock()
	held := 0
	for _, ln := range l.tail {
		if !ln.live {
			held++
		}
	}
	if held == 0 {
		return
	}
	if skipped := l.omitted - held; skipped > 0 {
		l.r.Info(l.ctx, "%s… %d lines omitted …", l.prefix, skipped)
	}
	for i, ln := range l.tail {
		if !ln.live {
			l.journal(ln)
			l.tail[i].live = true
		}
	}
	l.omitted = 0
}

// Summary is the last line the script printed on either stream, which is
// where shells, Composer, and artisan put the error that ended a run.
func (l *scriptLog) Summary() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.tail) == 0 {
		return ""
	}
	return l.tail[len(l.tail)-1].text
}

// scriptStream splits one stream into lines. Each stream keeps its own
// partial line so interleaved writes never merge two streams' text.
type scriptStream struct {
	log  *scriptLog
	warn bool

	mu  sync.Mutex
	buf []byte
}

func (s *scriptStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		s.log.line(s.warn, string(s.buf[:i]))
		s.buf = s.buf[i+1:]
	}
	// Bound a line that never ends (a progress bar without newlines).
	if len(s.buf) > 64<<10 {
		s.log.line(s.warn, string(s.buf))
		s.buf = s.buf[:0]
	}
	return len(p), nil
}

// Flush emits a final line that had no trailing newline.
func (s *scriptStream) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) > 0 {
		s.log.line(s.warn, string(s.buf))
		s.buf = s.buf[:0]
	}
}
