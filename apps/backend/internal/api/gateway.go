package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Scheduler gateway.
//
// minicrond's UI is app-controlled content, so it is never served on the
// management origin. The management UI asks for a single-use ticket (session
// + CSRF); the browser opens it on the utils listener, a different origin,
// where it is exchanged for an HttpOnly grant cookie scoped to that app's
// prefix. Every proxied request re-checks the grant, the operator session
// behind it, and the app, then forwards the full prefixed path over that
// app's UID-matched relay. minicrond serves under runtime.SchedulerBasePath,
// which is this gateway's per-app prefix, so no path rewriting is needed.
const (
	schedulerTicketTTL  = time.Minute
	schedulerCookie     = "bento_scheduler"
	schedulerPathPrefix = "/_bento/scheduler/"
	schedulerMaxBody    = 10 << 20
)

var (
	schedulerTicketPath = regexp.MustCompile(`^/_bento/scheduler/t/([A-Za-z0-9_-]{43})$`)
	schedulerAppPath    = regexp.MustCompile(`^/_bento/scheduler/a/([a-z][a-z0-9-]{1,30}[a-z0-9])(/.*)?$`)
)

type gatewayAppKey struct{}

// handleSchedulerTicket issues a single-use scheduler ticket for one app.
// Only a browser session can hold a grant, so the local CLI transport is
// refused.
func (s *Server) handleSchedulerTicket(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Kind != "session" {
		writeError(w, s.Log, &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden, msg: "the scheduler UI needs a browser session"})
		return
	}
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	if app.DesiredRuntime != domain.DesiredRunning {
		writeError(w, s.Log, &apiError{status: http.StatusPreconditionFailed, code: dto.ErrorCodePrecondition, msg: "the app is stopped; its scheduler is not running"})
		return
	}
	us, err := s.C.UtilsSettings(r.Context())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	expires := time.Now().Add(schedulerTicketTTL)
	ticket := s.scheduler.issue(dbadminPass{appID: app.ID, bindingID: app.Slug, sessionHash: p.Session.TokenHash, expires: expires})
	writeJSON(w, http.StatusOK, dto.SchedulerTicket{Path: schedulerPathPrefix + "t/" + ticket, BaseURL: us.BaseURL,
		LoopbackPort: s.utilsLoopbackPort(), ExpiresAt: platform.FormatTime(expires)})
}

// schedulerGateway serves /_bento/scheduler/* on the utils listener.
func (s *Server) schedulerGateway() http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "minicrond"
			pr.Out.Host = "minicrond"
			h := pr.Out.Header
			for k := range h {
				if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-bento-") || strings.HasPrefix(lk, "x-forwarded-") {
					h.Del(k)
				}
			}
			for _, k := range []string{"Cookie", "Authorization", "Proxy-Authorization", "Forwarded", "X-CSRF-Token"} {
				h.Del(k)
			}
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				app, _ := ctx.Value(gatewayAppKey{}).(domain.App)
				return s.Relay.Dial(ctx, app)
			},
			DisableKeepAlives:      true,
			ResponseHeaderTimeout:  30 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
		},
		FlushInterval:  -1,
		ModifyResponse: hardenSchedulerResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.Log.Warn("scheduler gateway", "err", err)
			dbadminText(w, http.StatusBadGateway, "scheduler unavailable: the app must be running")
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		if m := schedulerTicketPath.FindStringSubmatch(r.URL.Path); m != nil {
			s.redeemSchedulerTicket(w, r, m[1])
			return
		}
		m := schedulerAppPath.FindStringSubmatch(r.URL.Path)
		if m == nil || strings.Contains(r.URL.Path, "/../") || strings.HasSuffix(r.URL.Path, "/..") {
			dbadminText(w, http.StatusNotFound, "not found")
			return
		}
		prefix := runtime.SchedulerBasePath(m[1])
		if m[2] == "" {
			http.Redirect(w, r, prefix, http.StatusFound)
			return
		}
		c, err := r.Cookie(schedulerCookie)
		if err != nil || len(c.Value) > 128 {
			dbadminText(w, http.StatusUnauthorized, "open the scheduler from the Bento UI")
			return
		}
		pass, ok := s.scheduler.grant(c.Value)
		if !ok || pass.bindingID != m[1] {
			dbadminText(w, http.StatusUnauthorized, "this scheduler link expired; open it again from the Bento UI")
			return
		}
		if _, err := store.GetLiveSession(r.Context(), s.Store.DB(), pass.sessionHash, time.Now()); err != nil {
			s.scheduler.revoke(c.Value)
			dbadminText(w, http.StatusUnauthorized, "your Bento session ended; sign in and open the scheduler again")
			return
		}
		if unsafeMethod(r.Method) && !sameOriginWrite(r) {
			dbadminText(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		app, err := store.GetApp(r.Context(), s.Store.DB(), pass.appID)
		if err != nil || app.Slug != m[1] {
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				dbadminText(w, http.StatusInternalServerError, "internal error")
				return
			}
			dbadminText(w, http.StatusNotFound, "app not found")
			return
		}
		if app.DesiredRuntime != domain.DesiredRunning {
			dbadminText(w, http.StatusServiceUnavailable, "the app is stopped; its scheduler is not running")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, schedulerMaxBody)
		ctx := context.WithValue(r.Context(), gatewayAppKey{}, app)
		proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) redeemSchedulerTicket(w http.ResponseWriter, r *http.Request, ticket string) {
	if r.Method != http.MethodGet {
		dbadminText(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	pass, grant, ok := s.scheduler.redeem(ticket)
	if !ok {
		dbadminText(w, http.StatusUnauthorized, "this scheduler link expired or was already used; open it again from the Bento UI")
		return
	}
	if _, err := store.GetLiveSession(r.Context(), s.Store.DB(), pass.sessionHash, time.Now()); err != nil {
		s.scheduler.revoke(grant)
		if !errors.Is(err, store.ErrNotFound) {
			s.Log.Warn("scheduler session check", "err", err)
		}
		dbadminText(w, http.StatusUnauthorized, "your Bento session ended; sign in and open the scheduler again")
		return
	}
	s.Log.Info("scheduler opened", "app", pass.appID)
	prefix := runtime.SchedulerBasePath(pass.bindingID)
	http.SetCookie(w, &http.Cookie{Name: schedulerCookie, Value: grant, Path: prefix, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: requestHTTPS(r)})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, prefix, http.StatusSeeOther)
}

// hardenSchedulerResponse strips app-controlled cookies and pins the headers
// Bento owns on every scheduler response. The scheduler opens in its own tab
// on the utils origin and is never framed.
func hardenSchedulerResponse(resp *http.Response) error {
	h := resp.Header
	h.Del("Set-Cookie")
	h.Del("Referrer-Policy")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if resp.Request != nil && strings.Contains(resp.Request.URL.Path, "/api/") && h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	return nil
}
