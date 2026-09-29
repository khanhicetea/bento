package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteErrorClientCancelIsNotLoggedAsError(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	rec := httptest.NewRecorder()
	writeError(rec, log, fmt.Errorf("inspect container: %w", context.Canceled))
	if rec.Code != statusClientClosedRequest {
		t.Fatalf("status = %d, want %d", rec.Code, statusClientClosedRequest)
	}
	if logs.Len() != 0 {
		t.Fatalf("client cancellation must not be logged above debug: %s", logs.String())
	}

	rec = httptest.NewRecorder()
	writeError(rec, log, errors.New("boom"))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "request failed") {
		t.Fatalf("unexpected errors must still be logged: status=%d log=%q", rec.Code, logs.String())
	}
}
