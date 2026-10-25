package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// Backend reaches the Bento REST API. The HTTP client's transport decides
// how: the control socket for `bento mcp`, or in-process for POST /mcp.
type Backend struct {
	HTTP *http.Client
	// Header is added to every request (the stdio transport sends its scope).
	Header http.Header
}

// maxResponse bounds one API response read by a tool.
const maxResponse = 8 << 20

// APIError is a decoded API error; tools return it to the model as-is.
type APIError struct {
	Status int
	Body   dto.ErrorBody
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", e.Body.Code, e.Body.Message)
	for _, f := range e.Body.Fields {
		fmt.Fprintf(&b, "\n  %s: %s", f.Field, f.Message)
	}
	return b.String()
}

func (b *Backend) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://bento"+path, rd)
	if err != nil {
		return nil, err
	}
	for k, vs := range b.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		// One key per tool call: a transport retry cannot duplicate work.
		req.Header.Set("Idempotency-Key", "mcp-"+platform.RandomHex(12))
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bento backend unreachable: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var e dto.ErrorResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e); err != nil || e.Error.Code == "" {
			return nil, fmt.Errorf("HTTP %d from %s %s", resp.StatusCode, method, path)
		}
		return nil, &APIError{Status: resp.StatusCode, Body: e.Error}
	}
	return resp, nil
}

// call sends one request and decodes the JSON response into out.
func (b *Backend) call(ctx context.Context, method, path string, body, out any) error {
	resp, err := b.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

// sseEvent is one server-sent event.
type sseEvent struct {
	Event string
	Data  string
}

// stream reads a finite SSE response (logs without follow) to its end.
func (b *Backend) stream(ctx context.Context, path string, each func(sseEvent) bool) error {
	resp, err := b.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(io.LimitReader(resp.Body, maxResponse))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var ev sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.Event != "" || ev.Data != "" {
				if !each(ev) {
					return nil
				}
			}
			ev = sseEvent{}
		case strings.HasPrefix(line, "event:"):
			ev.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			ev.Data += strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
		}
	}
	return sc.Err()
}
