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

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Scheduler gateway.
//
// minicrond's UI is served on the management origin under
// runtime.SchedulerBasePath (/apps/<slug>/scheduler/), so it sits beside the
// Bento UI and needs no separate listener. minicrond escapes and locks down
// its own UI (strict CSP, framing only when MINICRON_ALLOW_IFRAME is set), and the gateway adds the parts only it
// can enforce: an operator session, an exact Origin on writes, the app's slug
// and desired-running state, and stripping every credential before the request
// crosses the app's UID-matched relay. The CSRF token is not required: the UI
// is app-served and must never be handed the session's token.
const schedulerMaxBody = 10 << 20

var schedulerSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)

type gatewayAppKey struct{}

// schedulerGateway serves /apps/{slug}/scheduler/… on the management listener.
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
		w.Header().Set("Cache-Control", "no-store")
		// Only the scheduler page may be framed, and only by this origin: the
		// Scheduler tab embeds it. Everything else keeps DENY / 'none'.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", strings.Replace(
			w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
		slug := r.PathValue("slug")
		if !schedulerSlugPattern.MatchString(slug) || strings.Contains(r.URL.Path, "/../") ||
			strings.HasSuffix(r.URL.Path, "/..") {
			dbadminText(w, http.StatusNotFound, "not found")
			return
		}
		if _, ok := s.sessionFromRequest(r); !ok {
			dbadminText(w, http.StatusUnauthorized, "sign in to Bento to use the scheduler")
			return
		}
		if unsafeMethod(r.Method) {
			if !s.originAllowed(r.Header.Get("Origin")) {
				dbadminText(w, http.StatusForbidden, "origin not allowed")
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
				dbadminText(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		}
		app, err := store.GetApp(r.Context(), s.Store.DB(), slug)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.Log.Warn("scheduler gateway app lookup", "err", err)
			dbadminText(w, http.StatusInternalServerError, "internal error")
			return
		}
		// GetApp also matches ids; minicrond serves under the slug only.
		if err != nil || app.Slug != slug {
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

// hardenSchedulerResponse strips app-controlled cookies and duplicates of the
// headers the management listener already sets. minicrond's own
// Content-Security-Policy is kept: a second policy only narrows the first.
func hardenSchedulerResponse(resp *http.Response) error {
	h := resp.Header
	h.Del("Set-Cookie")
	h.Del("Referrer-Policy")
	h.Del("X-Frame-Options")
	h.Del("X-Content-Type-Options")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
	return nil
}
