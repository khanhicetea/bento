package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/edge"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// stubStatusCmd fetches the edge's loopback stub_status. The nginx image has
// no curl or wget, so bash's /dev/tcp does the HTTP/1.0 request.
var stubStatusCmd = []string{"bash", "-c",
	`exec 3<>/dev/tcp/127.0.0.1/` + strconv.Itoa(edge.StubStatusPort) + ` && printf 'GET /stub_status HTTP/1.0\r\nHost: localhost\r\n\r\n' >&3 && cat <&3`}

type edgeSample struct {
	at                time.Time
	accepts, requests int64
}

// edgeSampler keeps the previous sample so rates can be derived.
type edgeSampler struct {
	mu   sync.Mutex
	last edgeSample
}

func (e *edgeSampler) rates(cur edgeSample) (reqPS, accPS float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	prev := e.last
	e.last = cur
	dt := cur.at.Sub(prev.at).Seconds()
	// Counters reset when the edge restarts; report zero rather than negative.
	if prev.at.IsZero() || dt < 0.5 || cur.requests < prev.requests || cur.accepts < prev.accepts {
		return 0, 0
	}
	return float64(cur.requests-prev.requests) / dt, float64(cur.accepts-prev.accepts) / dt
}

func (s *Server) handleEdgeMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	res, err := s.C.Engine.Exec(ctx, s.C.Names.EdgeContainer(), docker.ExecRequest{Cmd: stubStatusCmd, OutputLimit: 4096})
	if err != nil || res.ExitCode != 0 {
		if err != nil {
			s.Log.Debug("edge metrics exec failed", "err", err)
		}
		writeError(w, s.Log, &apiError{status: http.StatusServiceUnavailable, code: dto.ErrorCodeUnavailable, msg: "edge metrics are unavailable; is the edge running?"})
		return
	}
	m, err := parseStubStatus(res.Stdout)
	if err != nil {
		s.Log.Warn("edge stub_status unparseable", "err", err)
		writeError(w, s.Log, &apiError{status: http.StatusServiceUnavailable, code: dto.ErrorCodeUnavailable, msg: "edge metrics are unavailable"})
		return
	}
	now := time.Now()
	m.SampledAt = platform.FormatTime(now)
	m.RequestsPerSecond, m.AcceptsPerSecond = s.edgeStats.rates(edgeSample{at: now, accepts: m.Accepts, requests: m.Requests})
	writeJSON(w, http.StatusOK, m)
}

// parseStubStatus parses nginx stub_status output, with or without HTTP headers:
//
//	Active connections: 2
//	server accepts handled requests
//	 10 10 25
//	Reading: 0 Writing: 1 Waiting: 1
func parseStubStatus(raw []byte) (dto.EdgeMetrics, error) {
	var m dto.EdgeMetrics
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		raw = raw[i+4:]
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 4 {
		return m, fmt.Errorf("expected 4 lines, got %d", len(lines))
	}
	num := func(s string) (int64, error) { return strconv.ParseInt(strings.TrimSpace(s), 10, 64) }
	var err error
	active, ok := strings.CutPrefix(strings.TrimSpace(lines[0]), "Active connections:")
	if !ok {
		return m, fmt.Errorf("missing active connections")
	}
	if m.Active, err = num(active); err != nil {
		return m, err
	}
	f := strings.Fields(lines[2])
	if len(f) != 3 {
		return m, fmt.Errorf("malformed counters line")
	}
	for i, dst := range []*int64{&m.Accepts, &m.Handled, &m.Requests} {
		if *dst, err = num(f[i]); err != nil {
			return m, err
		}
	}
	f = strings.Fields(lines[3])
	if len(f) != 6 || f[0] != "Reading:" || f[2] != "Writing:" || f[4] != "Waiting:" {
		return m, fmt.Errorf("malformed state line")
	}
	for i, dst := range []*int64{&m.Reading, &m.Writing, &m.Waiting} {
		if *dst, err = num(f[i*2+1]); err != nil {
			return m, err
		}
	}
	m.Dropped = max(m.Accepts-m.Handled, 0)
	return m, nil
}
