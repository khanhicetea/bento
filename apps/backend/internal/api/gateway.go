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

var gatewayPath = regexp.MustCompile(`^/scheduler/apps/([a-z][a-z0-9-]{1,30}[a-z0-9])(/.*)?$`)

type gatewayAppKey struct{}

// schedulerGateway authenticates and authorizes every scheduler asset, API,
// SSE, and download request to exactly the selected app, then forwards the
// full prefixed path over that app's UID-matched relay. This is a
// same-origin trusted-content mode: it provides no XSS isolation between
// scheduler UIs and Bento.
func (s *Server) schedulerGateway() http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "minicrond"
			pr.Out.Host = "minicrond"
			for _, h := range []string{"Cookie", "Authorization", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "X-CSRF-Token"} {
				pr.Out.Header.Del(h)
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
			http.Error(w, "scheduler unavailable: the app must be running", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gatewayPath.FindStringSubmatch(r.URL.Path)
		if m == nil || strings.Contains(r.URL.Path, "/../") || strings.HasSuffix(r.URL.Path, "/..") {
			http.NotFound(w, r)
			return
		}
		if m[2] == "" {
			http.Redirect(w, r, "/scheduler/apps/"+m[1]+"/", http.StatusFound)
			return
		}
		sess, ok := s.sessionFromRequest(r)
		if !ok {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		_ = sess
		if unsafeMethod(r.Method) {
			if !s.originAllowed(r.Header.Get("Origin")) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
				http.Error(w, "cross-site request refused", http.StatusForbidden)
				return
			}
		}
		app, err := store.GetApp(r.Context(), s.Store.DB(), m[1])
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if app.DesiredRuntime != domain.DesiredRunning {
			http.Error(w, "the app is stopped; its scheduler is not running", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
		ctx := context.WithValue(r.Context(), gatewayAppKey{}, app)
		proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}

// hardenSchedulerResponse strips app-controlled cookies and framing headers
// and pins the headers Bento owns on every scheduler response. The content
// still runs on the management origin: a CSP sandbox would give it an opaque
// origin, which drops the SameSite=Strict session cookie from the scheduler
// UI's own API calls and breaks it (see docs/architecture.md, audit C2).
func hardenSchedulerResponse(resp *http.Response) error {
	h := resp.Header
	h.Del("Set-Cookie")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Security-Policy", "frame-ancestors 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if resp.Request != nil && strings.Contains(resp.Request.URL.Path, "/api/") && h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	return nil
}
