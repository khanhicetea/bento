package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
)

// fakeAPI answers the few routes the tests use.
func fakeAPI(t *testing.T, seen *[]string) *Backend {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Header.Get("X-Test-Scope"))
		json.NewEncoder(w).Encode(dto.AppList{Apps: []dto.AppSummary{{ID: "shop", Slug: "shop"}}})
	})
	mux.HandleFunc("GET /api/v1/apps/shop/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tail") != "5" {
			t.Errorf("tail %q", r.URL.Query().Get("tail"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i := range 3 {
			fmt.Fprintf(w, "id: t%d\nevent: log\ndata: {\"ts\":\"t%d\",\"line\":\"line %d\"}\n\n", i, i, i)
		}
		fmt.Fprint(w, "event: end\ndata: {\"reason\":\"done\"}\n\n")
	})
	mux.HandleFunc("POST /api/v1/apps/shop/restart", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Idempotency-Key"), "mcp-") {
			t.Errorf("missing idempotency key")
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(dto.Accepted{Operation: dto.Operation{ID: "op_1", State: dto.OperationStateQueued}})
	})
	mux.HandleFunc("GET /api/v1/operations/op_1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(dto.Operation{ID: "op_1", State: dto.OperationStateRunning})
	})
	mux.HandleFunc("GET /api/v1/system", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(dto.ErrorResponse{Error: dto.ErrorBody{Code: dto.ErrorCodeForbidden, Message: "nope"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Backend{HTTP: &http.Client{Transport: rewrite{srv.URL}}, Header: http.Header{"X-Test-Scope": {"read"}}}
}

// rewrite points the backend's fixed http://bento base at a test server.
type rewrite struct{ base string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	u := r.base + req.URL.RequestURI()
	nreq, err := http.NewRequestWithContext(req.Context(), req.Method, u, req.Body)
	if err != nil {
		return nil, err
	}
	nreq.Header = req.Header
	return http.DefaultTransport.RoundTrip(nreq)
}

func stdio(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.ServeStdio(t.Context(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q", l)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func byID(msgs []map[string]any, id float64) map[string]any {
	for _, m := range msgs {
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func text(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	res := m["result"].(map[string]any)
	return res["content"].([]any)[0].(map[string]any)["text"].(string), res["isError"].(bool)
}

func TestStdioProtocol(t *testing.T) {
	var seen []string
	s := NewServer(fakeAPI(t, &seen), dto.TokenScopeRead, "test")
	msgs := stdio(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"nope"}`,
		`not json`,
		`[{"jsonrpc":"2.0","id":4,"method":"ping"}]`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"restart_app","arguments":{"app":"shop"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_app_logs","arguments":{"app":"shop","tail":5}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_status"}}`,
		`{"jsonrpc":"2.0","id":8,"method":"ping"}`,
	)
	// One reply per request plus the two parse failures; none for the notification.
	if len(msgs) != 9 {
		t.Fatalf("%d replies: %v", len(msgs), msgs)
	}
	if v := byID(msgs, 1)["result"].(map[string]any)["protocolVersion"]; v != protocolVersions[0] {
		t.Fatalf("unknown version must get the latest, got %v", v)
	}
	for _, tl := range byID(msgs, 2)["result"].(map[string]any)["tools"].([]any) {
		td := tl.(map[string]any)
		if td["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Errorf("read scope lists mutating tool %v", td["name"])
		}
		if td["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("%v inputSchema must be an object", td["name"])
		}
	}
	if byID(msgs, 3)["error"].(map[string]any)["code"] != float64(codeMethodNotFound) {
		t.Fatalf("unknown method %v", byID(msgs, 3))
	}
	if byID(msgs, 5)["error"] == nil {
		t.Fatalf("read scope must not call restart_app")
	}
	logs, isErr := text(t, byID(msgs, 6))
	if isErr || !strings.Contains(logs, "t2 line 2") || !strings.Contains(logs, `"truncated": false`) {
		t.Fatalf("logs %v %s", isErr, logs)
	}
	status, isErr := text(t, byID(msgs, 7))
	if !isErr || !strings.Contains(status, "forbidden: nope") {
		t.Fatalf("API error must be in-band: %v %s", isErr, status)
	}
	if byID(msgs, 8)["result"] == nil {
		t.Fatal("ping")
	}
	for _, sc := range seen {
		if sc != "read" {
			t.Fatalf("backend header not sent: %v", seen)
		}
	}
}

func TestOperateToolsAndWait(t *testing.T) {
	var seen []string
	s := NewServer(fakeAPI(t, &seen), dto.TokenScopeOperate, "test")
	msgs := stdio(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"restart_app","arguments":{"app":"shop"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"wait_operation","arguments":{"id":"op_1","timeout_seconds":1}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"wait_operation","arguments":{"id":"../x"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_app","arguments":{"spec":{}}}}`,
	)
	if out, isErr := text(t, byID(msgs, 1)); isErr || !strings.Contains(out, "wait_operation with id op_1") {
		t.Fatalf("restart %v %s", isErr, out)
	}
	if out, isErr := text(t, byID(msgs, 2)); isErr || !strings.Contains(out, `"done": false`) {
		t.Fatalf("wait %v %s", isErr, out)
	}
	if _, isErr := text(t, byID(msgs, 3)); !isErr {
		t.Fatal("bad op id accepted")
	}
	if byID(msgs, 4)["error"] == nil {
		t.Fatal("operate scope must not list create_app")
	}
}

func TestStdioCancel(t *testing.T) {
	var seen []string
	s := NewServer(fakeAPI(t, &seen), dto.TokenScopeRead, "test")
	pr, pw := io.Pipe()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- s.ServeStdio(context.Background(), pr, &out) }()
	io.WriteString(pw, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"wait_operation","arguments":{"id":"op_1","timeout_seconds":60}}}`+"\n")
	time.Sleep(200 * time.Millisecond)
	io.WriteString(pw, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9}}`+"\n")
	pw.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop wait_operation")
	}
	if out.Len() != 0 {
		t.Fatalf("cancelled request must get no reply: %s", out.String())
	}
}

func TestToolSchemasAreValid(t *testing.T) {
	names := map[string]bool{}
	for _, tl := range allTools() {
		if names[tl.Name] {
			t.Errorf("duplicate tool %s", tl.Name)
		}
		names[tl.Name] = true
		if tl.Description == "" || tl.Title == "" || scopeRank(tl.Scope) == 0 {
			t.Errorf("%s: missing description, title or scope", tl.Name)
		}
		// Read scope lists only read-only tools; a higher scope may also have
		// read-only tools (admin file reads).
		if tl.Scope == dto.TokenScopeRead && !tl.ReadOnly {
			t.Errorf("%s: read scope tools must be read-only", tl.Name)
		}
		if _, err := json.Marshal(tl.descriptor()); err != nil {
			t.Errorf("%s: %v", tl.Name, err)
		}
	}
}

func TestSchedulerTools(t *testing.T) {
	var seen []string
	b := fakeAPI(t, &seen)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(dto.AppList{Apps: []dto.AppSummary{{ID: "app_1", Slug: "shop"}}})
	})
	var mu sync.Mutex
	var paths []string
	mux.HandleFunc("/api/v1/apps/app_1/scheduler/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			big := strings.Repeat("x", 40<<10)
			json.NewEncoder(w).Encode(dto.SchedulerRunLog{RunID: "r1", Next: 3, Lines: []dto.SchedulerLogLine{
				{Seq: 1, Line: big}, {Seq: 2, Line: big}, {Seq: 3, Line: "tail"}}})
		case strings.HasSuffix(r.URL.Path, "/run"):
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(dto.SchedulerRun{ID: "r9", Job: "cron", Status: "queued"})
		default:
			json.NewEncoder(w).Encode(dto.SchedulerResult{Name: "queue", Action: "restart"})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	b.HTTP = &http.Client{Transport: rewrite{srv.URL}}

	s := NewServer(b, dto.TokenScopeOperate, "test")
	msgs := stdio(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_scheduler_run_log","arguments":{"app":"shop","run_id":"r1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_scheduler_job","arguments":{"app":"shop","name":"cron"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"control_scheduler_worker","arguments":{"app":"shop","name":"queue","action":"restart"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"run_scheduler_job","arguments":{"app":"shop","name":"../daemon/reload"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"set_scheduler_job_enabled","arguments":{"app":"shop","name":"cron","enabled":false}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list_scheduler_runs","arguments":{"app":"shop","job":"cron","limit":5}}}`,
	)
	out, isErr := text(t, byID(msgs, 1))
	if isErr || strings.Contains(out, "tail") || !strings.Contains(out, `"next": 1`) || !strings.Contains(out, `"more": true`) {
		t.Fatalf("log page must be cut to the byte budget: %v %.200s", isErr, out)
	}
	if out, isErr := text(t, byID(msgs, 2)); isErr || !strings.Contains(out, "run_id r9") {
		t.Fatalf("run job %v %s", isErr, out)
	}
	if _, isErr := text(t, byID(msgs, 3)); isErr {
		t.Fatal("restart worker failed")
	}
	if _, isErr := text(t, byID(msgs, 4)); !isErr {
		t.Fatal("bad job name accepted")
	}
	if byID(msgs, 5)["error"] == nil {
		t.Fatal("operate scope must not list set_scheduler_job_enabled")
	}
	// Tool calls run concurrently, so compare the set.
	want := []string{
		"GET /api/v1/apps/app_1/scheduler/runs/r1/log?after=0&limit=200",
		"GET /api/v1/apps/app_1/scheduler/runs?job=cron&limit=5",
		"POST /api/v1/apps/app_1/scheduler/jobs/cron/run",
		"POST /api/v1/apps/app_1/scheduler/workers/queue/restart",
	}
	slices.Sort(paths)
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("backend saw %v", paths)
	}
}

func TestSchedulerJobWriteTools(t *testing.T) {
	var seen []string
	b := fakeAPI(t, &seen)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(dto.AppList{Apps: []dto.AppSummary{{ID: "app_1", Slug: "shop"}}})
	})
	var mu sync.Mutex
	got := map[string]string{}
	save := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.Method+" "+r.URL.Path] = string(raw)
		mu.Unlock()
		json.NewEncoder(w).Encode(dto.SchedulerJob{Name: "report", Kind: dto.SchedulerJobKindJob, EnvKeys: []string{}})
	}
	mux.HandleFunc("POST /api/v1/apps/app_1/scheduler/jobs", save)
	mux.HandleFunc("PATCH /api/v1/apps/app_1/scheduler/jobs/{name}", save)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	b.HTTP = &http.Client{Transport: rewrite{srv.URL}}

	msgs := stdio(t, NewServer(b, dto.TokenScopeAdmin, "test"),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_scheduler_job","arguments":{"app":"shop","name":"report","kind":"job","command":"php artisan report","schedule":"0 3 * * *","env":{"MODE":"m"}}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"update_scheduler_job","arguments":{"app":"shop","name":"report","working_dir":"","timeout":0,"remove_env":["MODE"]}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"update_scheduler_job","arguments":{"app":"shop","name":"../x"}}}`,
	)
	for _, id := range []float64{1, 2} {
		if out, isErr := text(t, byID(msgs, id)); isErr {
			t.Fatalf("call %v: %s", id, out)
		}
	}
	if _, isErr := text(t, byID(msgs, 3)); !isErr {
		t.Fatal("bad name accepted")
	}
	if b := got["POST /api/v1/apps/app_1/scheduler/jobs"]; b != `{"name":"report","kind":"job","command":"php artisan report","schedule":"0 3 * * *","env":{"MODE":"m"}}` {
		t.Fatalf("create body %s", b)
	}
	// Explicit empty and zero values are sent, so they can clear a field.
	if b := got["PATCH /api/v1/apps/app_1/scheduler/jobs/report"]; b != `{"workingDir":"","timeout":0,"removeEnv":["MODE"]}` {
		t.Fatalf("update body %s", b)
	}
	// Operate does not list the write tools.
	msgs = stdio(t, NewServer(b, dto.TokenScopeOperate, "test"),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_scheduler_job","arguments":{"app":"shop","name":"x","kind":"job","command":"true"}}}`)
	if byID(msgs, 1)["error"] == nil {
		t.Fatal("operate scope must not list create_scheduler_job")
	}
}

func TestFileTools(t *testing.T) {
	var seen []string
	b := fakeAPI(t, &seen)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(dto.AppList{Apps: []dto.AppSummary{{ID: "app_1", Slug: "shop"}}})
	})
	var mu sync.Mutex
	got := map[string]string{}
	mux.HandleFunc("/api/v1/apps/app_1/files/", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.Method] = r.URL.RequestURI() + " " + string(raw)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"path": "x"})
	})
	mux.HandleFunc("GET /api/v1/apps/app_1/files", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got["LIST"] = r.URL.RequestURI()
		mu.Unlock()
		json.NewEncoder(w).Encode(dto.AppFileList{Entries: []dto.AppFileEntry{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	b.HTTP = &http.Client{Transport: rewrite{srv.URL}}

	msgs := stdio(t, NewServer(b, dto.TokenScopeAdmin, "test"),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_app_files","arguments":{"app":"shop","path":"config"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_app_file","arguments":{"app":"shop","path":"config/app.php","offset":10}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"edit_app_file","arguments":{"app":"shop","path":"a.php","old_text":"x","new_text":"y"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"write_app_file","arguments":{"app":"shop","path":"b.php","content":"<?php"}}}`,
	)
	for _, id := range []float64{1, 2, 3, 4} {
		if out, isErr := text(t, byID(msgs, id)); isErr {
			t.Fatalf("call %v: %s", id, out)
		}
	}
	want := map[string]string{
		"LIST":  "/api/v1/apps/app_1/files?path=config",
		"GET":   "/api/v1/apps/app_1/files/content?offset=10&path=config%2Fapp.php ",
		"PATCH": `/api/v1/apps/app_1/files/content {"path":"a.php","oldText":"x","newText":"y"}`,
		"PUT":   `/api/v1/apps/app_1/files/content {"path":"b.php","content":"\u003c?php"}`,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
	msgs = stdio(t, NewServer(b, dto.TokenScopeOperate, "test"),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_app_file","arguments":{"app":"shop","path":"a.php"}}}`)
	if byID(msgs, 1)["error"] == nil {
		t.Fatal("operate scope must not list read_app_file")
	}
}
