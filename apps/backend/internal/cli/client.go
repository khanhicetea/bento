package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

// Client talks to the resident backend over its Unix control socket. The
// socket is root-only and the backend verifies peer credentials; no secret
// is ever passed in argv.
type Client struct {
	HTTP   *http.Client
	Layout platform.Layout
}

func NewClient(layout platform.Layout) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", layout.ControlSocket())
	}}
	return &Client{HTTP: &http.Client{Transport: tr}, Layout: layout}
}

// APIError is a decoded error response.
type APIError struct {
	Status int
	Body   dto.ErrorBody
}

func (e *APIError) Error() string {
	var msg strings.Builder
	fmt.Fprintf(&msg, "%s: %s", e.Body.Code, e.Body.Message)
	for _, f := range e.Body.Fields {
		fmt.Fprintf(&msg, "\n  %s: %s", f.Field, f.Message)
	}
	return msg.String()
}

func (c *Client) Do(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://bento"+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("the Bento backend is not running for %s (start it with `bento serve --stack %s`)", c.Layout.Root, c.Layout.Root)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e dto.ErrorResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e); err != nil {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Body: e.Error}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response from %s %s: %w", method, path, err)
	}
	return nil
}

// Mutate submits a mutation with a fresh idempotency key so a transport
// retry cannot duplicate work, then optionally waits for the operation.
func (c *Client) Mutate(ctx context.Context, method, path string, body any, wait bool, w io.Writer) (dto.Accepted, error) {
	var acc dto.Accepted
	key := "cli-" + platform.RandomHex(12)
	var err error
	for range 3 {
		err = c.Do(ctx, method, path, body, &acc, map[string]string{"Idempotency-Key": key})
		if err == nil {
			break
		}
		if _, ok := errors.AsType[*APIError](err); ok {
			return acc, err
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return acc, err
	}
	fmt.Fprintf(w, "accepted %s (%s)\n", acc.Operation.ID, acc.Operation.Kind)
	if !wait {
		return acc, nil
	}
	op, err := c.WaitOperation(ctx, acc.Operation.ID, w)
	acc.Operation = op
	if err != nil {
		return acc, err
	}
	if op.State != dto.OperationStateSucceeded {
		msg := fmt.Sprintf("operation %s %s: %s", op.ID, op.State, op.ErrorMessage)
		if op.Guidance != "" {
			msg += "\nguidance: " + op.Guidance
		}
		return acc, errors.New(msg)
	}
	return acc, nil
}

// WaitOperation polls until the operation is terminal, printing new events.
func (c *Client) WaitOperation(ctx context.Context, id string, w io.Writer) (dto.Operation, error) {
	seen := 0
	for {
		var op dto.Operation
		if err := c.Do(ctx, "GET", "/api/v1/operations/"+id, nil, &op, nil); err != nil {
			return op, err
		}
		for _, e := range op.Events {
			if e.Seq > seen {
				seen = e.Seq
				fmt.Fprintf(w, "  [%s] %s\n", e.Level, e.Message)
			}
		}
		switch op.State {
		case dto.OperationStateSucceeded, dto.OperationStateFailed, dto.OperationStateCancelled, dto.OperationStateInterrupted:
			return op, nil
		}
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
