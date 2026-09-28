package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Database browser gateway.
//
// The management UI asks for a single-use ticket for one binding (session +
// CSRF). The browser opens the ticket on the utils listener, which is a
// different origin from the management UI and may be exposed publicly; the
// ticket is exchanged for an HttpOnly grant cookie scoped to that binding's
// path. Every proxied request re-checks the grant, the operator session
// behind it, and the binding, then injects that binding's connection into the
// request to the shared Adminer container. The browser never sees database
// credentials, and one grant cannot reach another binding.
const (
	dbadminTicketTTL  = time.Minute
	dbadminGrantIdle  = 30 * time.Minute
	dbadminCookie     = "bento_dbadmin"
	dbadminPathPrefix = "/_dbadmin/"
	dbadminMaxBody    = 80 << 20
	dbadminDeadline   = 15 * time.Minute
)

var (
	dbadminTicketPath  = regexp.MustCompile(`^/_dbadmin/t/([A-Za-z0-9_-]{43})$`)
	dbadminBindingPath = regexp.MustCompile(`^/_dbadmin/b/([a-z0-9][a-z0-9_-]{1,63})(/.*)?$`)
)

type dbadminPass struct {
	appID       string
	bindingID   string
	sessionHash string
	expires     time.Time
}

// dbadminGate holds tickets and grants in memory, keyed by token hash. A
// backend restart invalidates them; the operator opens the link again.
type dbadminGate struct {
	mu       sync.Mutex
	tickets  map[string]dbadminPass
	grants   map[string]dbadminPass
	endpoint string
	checked  time.Time
}

func (g *dbadminGate) issue(p dbadminPass) string {
	token := platform.RandomToken(32)
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tickets == nil {
		g.tickets, g.grants = map[string]dbadminPass{}, map[string]dbadminPass{}
	}
	for k, t := range g.tickets {
		if now.After(t.expires) {
			delete(g.tickets, k)
		}
	}
	for k, t := range g.grants {
		if now.After(t.expires) {
			delete(g.grants, k)
		}
	}
	g.tickets[tokenHash(token)] = p
	return token
}

// redeem consumes a ticket and returns a new grant token.
func (g *dbadminGate) redeem(ticket string) (dbadminPass, string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := tokenHash(ticket)
	p, ok := g.tickets[h]
	delete(g.tickets, h)
	if !ok || time.Now().After(p.expires) {
		return dbadminPass{}, "", false
	}
	grant := platform.RandomToken(32)
	p.expires = time.Now().Add(dbadminGrantIdle)
	g.grants[tokenHash(grant)] = p
	return p, grant, true
}

// grant returns a live grant and slides its idle expiry.
func (g *dbadminGate) grant(token string) (dbadminPass, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := tokenHash(token)
	p, ok := g.grants[h]
	if !ok {
		return p, false
	}
	if time.Now().After(p.expires) {
		delete(g.grants, h)
		return p, false
	}
	p.expires = time.Now().Add(dbadminGrantIdle)
	g.grants[h] = p
	return p, true
}

func (g *dbadminGate) revoke(token string) {
	g.mu.Lock()
	delete(g.grants, tokenHash(token))
	g.mu.Unlock()
}

// resolve returns the Adminer address, re-inspecting at most every 10s.
func (s *Server) dbadminEndpoint(ctx context.Context) (string, error) {
	g := &s.dbadmin
	g.mu.Lock()
	if g.endpoint != "" && time.Since(g.checked) < 10*time.Second {
		defer g.mu.Unlock()
		return g.endpoint, nil
	}
	g.mu.Unlock()
	ep, err := s.C.DBAdminEndpoint(ctx)
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	g.endpoint, g.checked = ep, time.Now()
	g.mu.Unlock()
	return ep, nil
}

func (s *Server) forgetDBAdminEndpoint() {
	s.dbadmin.mu.Lock()
	s.dbadmin.endpoint = ""
	s.dbadmin.mu.Unlock()
}

// ---- management API ----

func (s *Server) handleGetDBAdmin(w http.ResponseWriter, r *http.Request) {
	ds, err := s.C.DBAdminSettings(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.DBAdminStatus{Enabled: ds.Enabled, State: s.containerState(r.Context(), s.C.Names.DBAdminContainer())})
}

func (s *Server) handlePutDBAdmin(w http.ResponseWriter, r *http.Request) {
	var req dto.DBAdminSettingsRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SetDBAdmin(r.Context(), req.Enabled, idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

// handleDBAdminTicket issues a single-use ticket for one SQL binding. Only a
// browser session can hold a grant, so the local CLI transport is refused.
func (s *Server) handleDBAdminTicket(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Kind != "session" {
		writeError(w, s.Log, &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden, msg: "the database browser needs a browser session"})
		return
	}
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	bid, err := pathID(r, "bid")
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	b, err := dbadminBinding(app, bid)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	ds, err := s.C.DBAdminSettings(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	if !ds.Enabled {
		writeError(w, s.Log, &apiError{status: http.StatusPreconditionFailed, code: dto.ErrorCodePrecondition,
			msg: "the database browser is disabled; enable it under Ingress → Utils"})
		return
	}
	us, err := s.C.UtilsSettings(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	expires := time.Now().Add(dbadminTicketTTL)
	ticket := s.dbadmin.issue(dbadminPass{appID: app.ID, bindingID: b.ID, sessionHash: p.Session.TokenHash, expires: expires})
	writeJSON(w, http.StatusOK, dto.DBAdminTicket{Path: dbadminPathPrefix + "t/" + ticket, BaseURL: us.BaseURL,
		LoopbackPort: s.utilsLoopbackPort(), ExpiresAt: platform.FormatTime(expires)})
}

// dbadminBinding returns an app's MySQL/PostgreSQL binding with databases.
func dbadminBinding(app domain.App, id string) (domain.Binding, error) {
	for _, b := range app.Bindings {
		if b.ID != id {
			continue
		}
		if b.Engine != domain.EngineMySQL && b.Engine != domain.EnginePostgres {
			return b, badRequest("the database browser supports MySQL and PostgreSQL bindings only")
		}
		if b.Service == "" || b.Username == "" || b.Password == "" || len(b.Databases) == 0 {
			return b, &apiError{status: http.StatusPreconditionFailed, code: dto.ErrorCodePrecondition, msg: "the binding has no provisioned database yet"}
		}
		return b, nil
	}
	return domain.Binding{}, notFound("binding not found")
}

func (s *Server) utilsLoopbackPort() int {
	if s.UtilsAddrs == nil {
		return 0
	}
	for _, a := range s.UtilsAddrs() {
		host, port, err := net.SplitHostPort(a)
		if ip := net.ParseIP(host); err == nil && ip != nil && ip.IsLoopback() {
			n, _ := strconv.Atoi(port)
			return n
		}
	}
	return 0
}

// ---- utils listener gateway ----

type dbadminTargetKey struct{}

type dbadminTarget struct {
	endpoint string
	token    string
	binding  domain.Binding
	https    bool
	prefix   string
}

func requestHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func dbadminText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}

// sameOriginWrite refuses cross-site state changes. The utils listener has no
// fixed origin (it may sit behind any host), so the request's own host is
// the only acceptable Origin.
func sameOriginWrite(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	scheme := "http"
	if requestHTTPS(r) {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host
}

func (s *Server) dbadminGateway() http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			t, _ := pr.In.Context().Value(dbadminTargetKey{}).(dbadminTarget)
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = t.endpoint
			pr.Out.Host = "dbadmin"
			h := pr.Out.Header
			for k := range h {
				if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-bento-") || strings.HasPrefix(lk, "x-forwarded-") {
					h.Del(k)
				}
			}
			for _, k := range []string{"Authorization", "Forwarded", "Cookie", "Proxy-Authorization"} {
				h.Del(k)
			}
			// Only Adminer's own cookies reach Adminer.
			var kept []string
			for _, c := range pr.In.Cookies() {
				if strings.HasPrefix(c.Name, "adminer_") {
					kept = append(kept, c.Name+"="+c.Value)
				}
			}
			if len(kept) > 0 {
				h.Set("Cookie", strings.Join(kept, "; "))
			}
			driver := "server"
			if t.binding.Engine == domain.EnginePostgres {
				driver = "pgsql"
			}
			h.Set("X-Bento-Gateway-Token", t.token)
			h.Set("X-Bento-Driver", driver)
			h.Set("X-Bento-Server", t.binding.Service)
			h.Set("X-Bento-Username", t.binding.Username)
			h.Set("X-Bento-Password", base64.StdEncoding.EncodeToString([]byte(t.binding.Password)))
			h.Set("X-Bento-Databases", strings.Join(t.binding.Databases, ","))
			if t.https {
				h.Set("X-Bento-Https", "1")
			}
		},
		Transport: &http.Transport{
			DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			MaxIdleConns:           8,
			IdleConnTimeout:        30 * time.Second,
			ResponseHeaderTimeout:  dbadminDeadline,
			MaxResponseHeaderBytes: 64 << 10,
		},
		ModifyResponse: func(resp *http.Response) error {
			t, _ := resp.Request.Context().Value(dbadminTargetKey{}).(dbadminTarget)
			h := resp.Header
			cookies := h.Values("Set-Cookie")
			h.Del("Set-Cookie")
			for _, raw := range cookies {
				c, err := http.ParseSetCookie(raw)
				if err != nil || !strings.HasPrefix(c.Name, "adminer_") {
					continue
				}
				c.Path, c.Domain, c.HttpOnly, c.SameSite, c.Secure = t.prefix, "", true, http.SameSiteLaxMode, t.https
				h.Add("Set-Cookie", c.String())
			}
			// PHP's built-in server echoes Host; Referrer-Policy is set by the
			// gateway before proxying.
			for _, k := range []string{"X-Powered-By", "Host", "Referrer-Policy"} {
				h.Del(k)
			}
			h.Set("X-Frame-Options", "DENY")
			h.Add("Content-Security-Policy", "frame-ancestors 'none'")
			h.Set("X-Content-Type-Options", "nosniff")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.forgetDBAdminEndpoint()
			s.Log.Warn("database browser gateway", "err", err)
			dbadminText(w, http.StatusBadGateway, "database browser unavailable")
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		if m := dbadminTicketPath.FindStringSubmatch(r.URL.Path); m != nil {
			s.redeemDBAdminTicket(w, r, m[1])
			return
		}
		m := dbadminBindingPath.FindStringSubmatch(r.URL.Path)
		if m == nil || strings.Contains(r.URL.Path, "/../") || strings.HasSuffix(r.URL.Path, "/..") {
			dbadminText(w, http.StatusNotFound, "not found")
			return
		}
		prefix := dbadminPathPrefix + "b/" + m[1] + "/"
		if m[2] == "" {
			http.Redirect(w, r, prefix, http.StatusFound)
			return
		}
		c, err := r.Cookie(dbadminCookie)
		if err != nil || len(c.Value) > 128 {
			dbadminText(w, http.StatusUnauthorized, "open the database browser from the Bento UI")
			return
		}
		pass, ok := s.dbadmin.grant(c.Value)
		if !ok || pass.bindingID != m[1] {
			dbadminText(w, http.StatusUnauthorized, "this database browser link expired; open it again from the Bento UI")
			return
		}
		if _, err := store.GetLiveSession(r.Context(), s.Store.DB(), pass.sessionHash, time.Now()); err != nil {
			s.dbadmin.revoke(c.Value)
			dbadminText(w, http.StatusUnauthorized, "your Bento session ended; sign in and open the database browser again")
			return
		}
		if unsafeMethod(r.Method) && !sameOriginWrite(r) {
			dbadminText(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		ds, err := s.C.DBAdminSettings(r.Context())
		if err != nil || !ds.Enabled {
			dbadminText(w, http.StatusServiceUnavailable, "the database browser is disabled")
			return
		}
		app, err := store.GetApp(r.Context(), s.Store.DB(), pass.appID)
		if err != nil {
			dbadminText(w, http.StatusNotFound, "app not found")
			return
		}
		b, err := dbadminBinding(app, pass.bindingID)
		if err != nil {
			dbadminText(w, http.StatusNotFound, err.Error())
			return
		}
		ep, err := s.dbadminEndpoint(r.Context())
		if err != nil {
			dbadminText(w, http.StatusServiceUnavailable, "database browser unavailable: "+err.Error())
			return
		}
		token, err := s.C.DBAdminToken()
		if err != nil || token == "" {
			dbadminText(w, http.StatusServiceUnavailable, "database browser unavailable: missing gateway token")
			return
		}
		// Imports, exports, and long queries outlive the utils listener's
		// default timeouts.
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Now().Add(dbadminDeadline))
		_ = rc.SetWriteDeadline(time.Now().Add(dbadminDeadline))
		r.Body = http.MaxBytesReader(w, r.Body, dbadminMaxBody)
		ctx := context.WithValue(r.Context(), dbadminTargetKey{}, dbadminTarget{endpoint: ep, token: token, binding: b, https: requestHTTPS(r), prefix: prefix})
		proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) redeemDBAdminTicket(w http.ResponseWriter, r *http.Request, ticket string) {
	if r.Method != http.MethodGet {
		dbadminText(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	pass, grant, ok := s.dbadmin.redeem(ticket)
	if !ok {
		dbadminText(w, http.StatusUnauthorized, "this database browser link expired or was already used; open it again from the Bento UI")
		return
	}
	if _, err := store.GetLiveSession(r.Context(), s.Store.DB(), pass.sessionHash, time.Now()); err != nil {
		s.dbadmin.revoke(grant)
		if !errors.Is(err, store.ErrNotFound) {
			s.Log.Warn("database browser session check", "err", err)
		}
		dbadminText(w, http.StatusUnauthorized, "your Bento session ended; sign in and open the database browser again")
		return
	}
	s.Log.Info("database browser opened", "app", pass.appID, "binding", pass.bindingID)
	prefix := dbadminPathPrefix + "b/" + pass.bindingID + "/"
	http.SetCookie(w, &http.Cookie{Name: dbadminCookie, Value: grant, Path: prefix, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: requestHTTPS(r)})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, prefix, http.StatusSeeOther)
}
