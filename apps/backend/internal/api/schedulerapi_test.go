package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
)

// fakeMinicron answers the minicrond routes the scheduler API uses and
// records each request it receives.
func fakeMinicron(t *testing.T, s *Server) *[]string {
	var mu sync.Mutex
	var seen []string
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" key="+r.Header.Get("Idempotency-Key"))
		mu.Unlock()
	}
	mux.HandleFunc("GET /api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Write([]byte(`{"items":[
			{"name":"cron","kind":"job","source":"db","command":"php artisan schedule:run","schedule":"* * * * *",
			 "env":{"API_KEY":"s3cret-job-value"},"secret_env":{"TOKEN":"env:TOKEN"},"next_fire_at":"2026-10-26T10:00:00Z"},
			{"name":"queue","kind":"worker","source":"db","enabled":false,"argv":["php","artisan","queue:work"],
			 "worker_state":{"held":true,"active":false,"failures":5,"status":"exhausted","exhaustion":{"at":"x"}}},
			{"name":"zz-bento-vacuum","kind":"job","source":"config","command":"vacuum"}]}`))
	})
	mux.HandleFunc("GET /api/v1/jobs/{name}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		switch r.PathValue("name") {
		case "cron":
			w.Header().Set("ETag", "7")
			w.Write([]byte(`{"definition":{"definition_id":3,"name":"cron","kind":"job","revision":7,"source":"db",
				"command":"php artisan schedule:run","schedule":"* * * * *","env":{"API_KEY":"s3cret-job-value","OLD":"x"},
				"alerts":["ops"]},"hash":"h","active_runs":0}`))
		case "zz-bento-vacuum":
			w.Write([]byte(`{"definition":{"name":"zz-bento-vacuum","kind":"job","source":"config","command":"vacuum"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"code":"not_found","message":"definition not found"}}`))
		}
	})
	saveJob := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" if-match="+r.Header.Get("If-Match")+
			" if-none-match="+r.Header.Get("If-None-Match")+" "+string(raw))
		mu.Unlock()
		if body["name"] == "dup" {
			w.WriteHeader(http.StatusPreconditionFailed)
			w.Write([]byte(`{"error":{"code":"revision_conflict","message":"definition exists"}}`))
			return
		}
		body["source"] = "db"
		json.NewEncoder(w).Encode(body)
	}
	mux.HandleFunc("POST /api/v1/jobs", saveJob)
	mux.HandleFunc("PUT /api/v1/jobs/{name}", saveJob)
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Write([]byte(`{"items":[{"run_id":"r1","job":"cron","kind":"job","status":"succeeded","trigger":"schedule",
			"attempt":1,"exit_code":0,"queued_at":"2026-10-26T09:00:00Z","log_bytes":12}],"next_before":"r1"}`))
	})
	mux.HandleFunc("GET /api/v1/runs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.PathValue("id") != "r1" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"code":"not_found","message":"run not found"}}`))
			return
		}
		// "hello\n" and "oops" base64-encoded.
		w.Write([]byte(`{"items":[{"sequence":1,"stream":1,"payload":"aGVsbG8K"},{"sequence":2,"stream":2,"payload":"b29wcw=="}]}`))
	})
	mux.HandleFunc("POST /api/v1/jobs/{name}/trigger", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"run_id":"r2","job":"` + r.PathValue("name") + `","kind":"job","status":"queued","trigger":"manual","queued_at":"2026-10-26T09:01:00Z"}`))
	})
	mux.HandleFunc("POST /api/v1/jobs/{name}/{action}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if strings.HasPrefix(r.PathValue("name"), "zz-bento-") {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":{"code":"read_only","message":"config-owned definition is view only"}}`))
			return
		}
		w.Write([]byte(`{"enabled":true}`))
	})
	mux.HandleFunc("POST /api/v1/workers/{name}/{action}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"restarting":true}`))
	})
	mux.HandleFunc("POST /api/v1/runs/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"stopping":true}`))
	})
	// minicrond serves under the app's base path.
	srv := httptest.NewServer(http.StripPrefix("/apps/shop/scheduler", mux))
	t.Cleanup(srv.Close)
	s.schedulerDial = func(ctx context.Context, _ domain.App) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}
	return &seen
}

func TestSchedulerAPI(t *testing.T) {
	c, h := newServer(t)
	c.login()
	seen := fakeMinicron(t, c.api)
	createShop(t, c, h.Wait)
	read := c.createToken("reader", dto.TokenScopeRead)
	operate := c.createToken("ops", dto.TokenScopeOperate)
	admin := c.createToken("admin", dto.TokenScopeAdmin)

	// A stopped app has no scheduler to talk to.
	if resp, body := c.bearer(read, "GET", "/api/v1/apps/shop/scheduler/jobs", ""); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(body, "precondition_failed") {
		t.Fatalf("stopped app jobs -> %d %s", resp.StatusCode, body)
	}
	resp, body := c.bearer(operate, "POST", "/api/v1/apps/shop/start", "")
	var acc dto.Accepted
	json.Unmarshal([]byte(body), &acc)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start -> %d %s", resp.StatusCode, body)
	}
	h.Wait(acc.Operation.ID)

	resp, body = c.bearer(read, "GET", "/api/v1/apps/shop/scheduler/jobs", "")
	if resp.StatusCode != 200 {
		t.Fatalf("jobs -> %d %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "s3cret-job-value") || strings.Contains(body, "env:TOKEN") {
		t.Fatalf("job env values leaked: %s", body)
	}
	var jobs dto.SchedulerJobList
	if err := json.Unmarshal([]byte(body), &jobs); err != nil || len(jobs.Jobs) != 3 {
		t.Fatalf("jobs %s %v", body, err)
	}
	if j := jobs.Jobs[0]; j.Kind != dto.SchedulerJobKindJob || !j.Enabled || strings.Join(j.EnvKeys, ",") != "API_KEY,TOKEN" ||
		j.NextFireAt != "2026-10-26T10:00:00Z" || j.ReadOnly {
		t.Fatalf("job %+v", j)
	}
	if w := jobs.Jobs[1]; w.Enabled || w.Worker == nil || !w.Worker.Exhausted || w.Worker.Failures != 5 {
		t.Fatalf("worker %+v", w)
	}
	if !jobs.Jobs[2].ReadOnly {
		t.Fatal("config-owned task must be read-only")
	}

	resp, body = c.bearer(read, "GET", "/api/v1/apps/shop/scheduler/runs?job=cron&filter=failed&limit=5", "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"id":"r1"`) || !strings.Contains(body, `"nextBefore":"r1"`) {
		t.Fatalf("runs -> %d %s", resp.StatusCode, body)
	}
	resp, body = c.bearer(read, "GET", "/api/v1/apps/shop/scheduler/runs/r1/log", "")
	var log dto.SchedulerRunLog
	json.Unmarshal([]byte(body), &log)
	if resp.StatusCode != 200 || len(log.Lines) != 2 || log.Lines[0].Line != "hello" || !log.Lines[1].Stderr || log.Next != 2 {
		t.Fatalf("log -> %d %s", resp.StatusCode, body)
	}
	if resp, body := c.bearer(read, "GET", "/api/v1/apps/shop/scheduler/runs/nope/log", ""); resp.StatusCode != 404 {
		t.Fatalf("missing run log -> %d %s", resp.StatusCode, body)
	}

	// Bad input never reaches minicrond.
	before := len(*seen)
	for _, rq := range [][2]string{
		{"GET", "/api/v1/apps/shop/scheduler/runs?job=../daemon"},
		{"GET", "/api/v1/apps/shop/scheduler/runs?filter=all"},
		{"GET", "/api/v1/apps/shop/scheduler/runs?limit=0"},
		{"GET", "/api/v1/apps/shop/scheduler/runs/r1/log?after=-1"},
		{"POST", "/api/v1/apps/shop/scheduler/jobs/Bad%20Name/run"},
		{"POST", "/api/v1/apps/shop/scheduler/workers/queue/delete"},
	} {
		tok := read
		if rq[0] == "POST" {
			tok = operate
		}
		if resp, body := c.bearer(tok, rq[0], rq[1], ""); resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Errorf("%s %s -> %d %s", rq[0], rq[1], resp.StatusCode, body)
		}
	}
	if len(*seen) != before {
		t.Fatalf("bad input reached minicrond: %v", (*seen)[before:])
	}

	// Scopes.
	if resp, _ := c.bearer(read, "POST", "/api/v1/apps/shop/scheduler/jobs/cron/run", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read run job -> %d", resp.StatusCode)
	}
	if resp, _ := c.bearer(operate, "POST", "/api/v1/apps/shop/scheduler/jobs/cron/enabled", `{"enabled":false}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operate enable -> %d", resp.StatusCode)
	}
	if resp, _ := c.bearer(admin, "POST", "/api/v1/apps/shop/scheduler/command", `{"argv":["list"]}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("token raw scheduler command -> %d", resp.StatusCode)
	}

	resp, body = c.do("POST", "/api/v1/apps/shop/scheduler/jobs/cron/run", "",
		map[string]string{"Authorization": "Bearer " + operate, "Idempotency-Key": "retry-key-123"})
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(body, `"id":"r2"`) || !strings.Contains(body, `"status":"queued"`) {
		t.Fatalf("run job -> %d %s", resp.StatusCode, body)
	}
	if resp, body := c.bearer(operate, "POST", "/api/v1/apps/shop/scheduler/workers/queue/restart", ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("restart worker -> %d %s", resp.StatusCode, body)
	}
	if resp, body := c.bearer(operate, "POST", "/api/v1/apps/shop/scheduler/runs/r2/stop", ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("stop run -> %d %s", resp.StatusCode, body)
	}
	if resp, body := c.bearer(admin, "POST", "/api/v1/apps/shop/scheduler/jobs/cron/enabled", `{"enabled":false}`); resp.StatusCode != 200 ||
		!strings.Contains(body, `"action":"disable"`) {
		t.Fatalf("disable -> %d %s", resp.StatusCode, body)
	}
	if resp, body := c.bearer(admin, "POST", "/api/v1/apps/shop/scheduler/jobs/zz-bento-vacuum/enabled", `{"enabled":false}`); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(body, "view only") {
		t.Fatalf("disable config task -> %d %s", resp.StatusCode, body)
	}

	want := []string{
		"GET /api/v1/jobs key=",
		"GET /api/v1/runs?filter=failed&job=cron&limit=5 key=",
		"GET /api/v1/runs/r1/log?after=0&limit=200 key=",
		"GET /api/v1/runs/nope/log?after=0&limit=200 key=",
		"POST /api/v1/jobs/cron/trigger key=retry-key-123",
		"POST /api/v1/workers/queue/restart key=",
		"POST /api/v1/runs/r2/stop key=",
		"POST /api/v1/jobs/cron/disable key=",
		"POST /api/v1/jobs/zz-bento-vacuum/disable key=",
	}
	if got := strings.Join(*seen, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("minicrond saw:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestSchedulerJobWrites(t *testing.T) {
	c, h := newServer(t)
	c.login()
	seen := fakeMinicron(t, c.api)
	createShop(t, c, h.Wait)
	operate := c.createToken("ops", dto.TokenScopeOperate)
	admin := c.createToken("admin", dto.TokenScopeAdmin)
	resp, body := c.bearer(operate, "POST", "/api/v1/apps/shop/start", "")
	var acc dto.Accepted
	json.Unmarshal([]byte(body), &acc)
	h.Wait(acc.Operation.ID)
	base := "/api/v1/apps/shop/scheduler/jobs"

	// Operate cannot write definitions.
	if resp, _ := c.bearer(operate, "POST", base, `{"name":"x","kind":"job","command":"true"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operate create -> %d", resp.StatusCode)
	}
	if resp, _ := c.bearer(operate, "PATCH", base+"/cron", `{"schedule":"0 * * * *"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operate edit -> %d", resp.StatusCode)
	}
	// Bad requests never reach minicrond.
	before := len(*seen)
	for _, rq := range [][3]string{
		{"POST", base, `{"name":"zz-bento-x","kind":"job","command":"true"}`},
		{"POST", base, `{"name":"Bad","kind":"job","command":"true"}`},
		{"POST", base, `{"name":"x","kind":"cron","command":"true"}`},
		{"POST", base, `{"name":"x","kind":"job"}`},
		{"POST", base, `{"name":"x","kind":"job","command":"a","argv":["b"]}`},
		{"POST", base, `{"name":"x","kind":"job","command":"true","env":{"BAD-KEY":"v"}}`},
		{"POST", base, `{"name":"x","kind":"job","command":"true","runAs":"root"}`},
		{"PATCH", base + "/cron", `{"name":"other"}`},
		{"PATCH", base + "/cron", `{"timeout":-1}`},
	} {
		if resp, body := c.bearer(admin, rq[0], rq[1], rq[2]); resp.StatusCode != http.StatusBadRequest &&
			resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s %s -> %d %s", rq[1], rq[2], resp.StatusCode, body)
		}
	}
	// The two PATCH cases above read the current definition first; nothing was written.
	for _, l := range (*seen)[before:] {
		if !strings.HasPrefix(l, "GET /api/v1/jobs/cron") {
			t.Fatalf("bad input reached minicrond: %s", l)
		}
	}

	resp, body = c.bearer(admin, "POST", base, `{"name":"report","kind":"job","command":"php artisan report","schedule":"0 3 * * *","env":{"MODE":"secret-mode"}}`)
	if resp.StatusCode != http.StatusCreated || strings.Contains(body, "secret-mode") || !strings.Contains(body, `"envKeys":["MODE"]`) {
		t.Fatalf("create -> %d %s", resp.StatusCode, body)
	}
	if got := (*seen)[len(*seen)-1]; got != `POST /api/v1/jobs if-match= if-none-match=* {"command":"php artisan report","env":{"MODE":"secret-mode"},"kind":"job","name":"report","schedule":"0 3 * * *"}` {
		t.Fatalf("create sent %s", got)
	}
	if resp, body := c.bearer(admin, "POST", base, `{"name":"dup","kind":"job","command":"true"}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate create -> %d %s", resp.StatusCode, body)
	}

	// An edit keeps env values and fields Bento does not model, and pins the revision.
	resp, body = c.bearer(admin, "PATCH", base+"/cron", `{"argv":["php","artisan","schedule:run"],"env":{"NEW":"n"},"removeEnv":["OLD"],"enabled":false}`)
	if resp.StatusCode != 200 || strings.Contains(body, "s3cret-job-value") || !strings.Contains(body, `"envKeys":["API_KEY","NEW"]`) {
		t.Fatalf("edit -> %d %s", resp.StatusCode, body)
	}
	if got := (*seen)[len(*seen)-1]; got != `PUT /api/v1/jobs/cron if-match=7 if-none-match= {"alerts":["ops"],"argv":["php","artisan","schedule:run"],"enabled":false,"env":{"API_KEY":"s3cret-job-value","NEW":"n"},"kind":"job","name":"cron","schedule":"* * * * *"}` {
		t.Fatalf("edit sent %s", got)
	}
	if resp, body := c.bearer(admin, "PATCH", base+"/zz-bento-vacuum", `{"schedule":"0 * * * *"}`); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(body, "read-only") {
		t.Fatalf("edit config task -> %d %s", resp.StatusCode, body)
	}
	if resp, _ := c.bearer(admin, "PATCH", base+"/missing", `{"schedule":"0 * * * *"}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("edit missing -> %d", resp.StatusCode)
	}
	if resp, _ := c.bearer(admin, "PATCH", base+"/cron", `{"kind":"worker"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("kind change -> %d", resp.StatusCode)
	}
}

// With minicrond off there is no scheduler: the API and the UI gateway refuse
// before dialing the relay.
func TestSchedulerRefusedWhenMinicrondOff(t *testing.T) {
	c, h := newServer(t)
	c.login()
	ctx := t.Context()
	c.api.schedulerDial = func(context.Context, domain.App) (net.Conn, error) {
		t.Error("dialed the scheduler of an app whose minicrond is off")
		return nil, errors.New("unexpected dial")
	}
	_, body := c.write("POST", "/api/v1/apps", `{"slug":"shop","minicrond":false,"valkey":true,"runtime":{"kind":"http-process","http":{"toolchain":"node","version":"24","argv":["node","s.js"]}}}`)
	var acc dto.Accepted
	json.Unmarshal([]byte(body), &acc)
	h.Wait(acc.Operation.ID)
	if _, err := h.Store.DB().ExecContext(ctx, `UPDATE apps SET desired_runtime='running'`); err != nil {
		t.Fatal(err)
	}
	_, body = c.do("GET", "/api/v1/apps/shop", "", nil)
	var app dto.App
	if err := json.Unmarshal([]byte(body), &app); err != nil || app.Minicrond || !app.Valkey {
		t.Fatalf("app %s %v", body, err)
	}
	if resp, body := c.do("GET", "/api/v1/apps/shop/scheduler/jobs", "", nil); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(body, "minicrond is off") {
		t.Fatalf("jobs -> %d %s", resp.StatusCode, body)
	}
	if resp, _ := c.do("GET", "/apps/shop/scheduler/", "", nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("gateway -> %d", resp.StatusCode)
	}
}
