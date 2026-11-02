package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/schedulerui"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Scheduler gateway.
//
// minicrond's UI is served on the management origin under
// runtime.SchedulerBasePath (/apps/<slug>/scheduler/), so it sits beside the
// Bento UI and needs no separate listener. The app's UID owns the minicrond
// socket, so whatever answers there is only as trustworthy as the app: a
// compromised app can replace minicrond. Therefore the page, scripts and
// stylesheets come from the minicrond bundle vendored into this binary
// (internal/schedulerui), and only /api/… crosses the UID-matched relay. Its
// answers must be data (JSON, plain text, event streams, TOML exports); they
// carry a sandbox CSP, and anything else, including redirects, is refused.
//
// The gateway also enforces an operator session, an exact Origin on writes,
// the app's slug and desired-running state, and strips every credential before
// the request crosses the relay. The CSRF token is not required: minicrond's UI
// is never handed the session's token.
const schedulerMaxBody = 10 << 20

var schedulerSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)

// schedulerShellCSP is minicrond's own policy for its UI, with framing allowed
// for this origin only (the Scheduler tab embeds it).
const schedulerShellCSP = "default-src 'self'; script-src 'self'; script-src-attr 'none'; " +
	"style-src 'self'; style-src-attr 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'self'; object-src 'none'; frame-src 'none'; frame-ancestors 'self'; " +
	"form-action 'none'; worker-src 'none'"

// schedulerAPICSP goes on every relayed answer: even if one is opened as a
// document, it gets an opaque origin and can run nothing.
const schedulerAPICSP = "sandbox; default-src 'none'; frame-ancestors 'none'"

// schedulerAPITypes are the media types minicrond's API answers with.
var schedulerAPITypes = map[string]bool{
	"application/json":  true,
	"text/plain":        true,
	"text/event-stream": true,
	"application/toml":  true,
}

var errSchedulerResponse = errors.New("scheduler answered with a response that is not API data")

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
				return s.dialScheduler(ctx, app)
			},
			DisableKeepAlives:      true,
			ResponseHeaderTimeout:  30 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
		},
		FlushInterval:  -1,
		ModifyResponse: hardenSchedulerResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.Log.Warn("scheduler gateway", "err", err)
			if errors.Is(err, errSchedulerResponse) {
				dbadminText(w, http.StatusBadGateway, "scheduler answered with an unexpected response")
				return
			}
			dbadminText(w, http.StatusBadGateway, "scheduler unavailable: the app must be running")
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
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
		if !app.Minicrond {
			dbadminText(w, http.StatusServiceUnavailable, "minicrond is off for this app; turn it on in the app settings")
			return
		}
		if app.DesiredRuntime != domain.DesiredRunning {
			dbadminText(w, http.StatusServiceUnavailable, "the app is stopped; its scheduler is not running")
			return
		}
		base := runtime.SchedulerBasePath(slug)
		rest := strings.TrimPrefix(r.URL.Path, base)
		if strings.HasPrefix(rest, "/api/") {
			r.Body = http.MaxBytesReader(w, r.Body, schedulerMaxBody)
			ctx := context.WithValue(r.Context(), gatewayAppKey{}, app)
			proxy.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		s.serveSchedulerUI(w, r, base, rest)
	})
}

// serveSchedulerUI answers every non-API path from the vendored bundle:
// assets/<name> are the scripts and stylesheets, anything else is a client
// route of the single-page app and gets its shell. Only this page may be
// framed, and only by this origin.
func (s *Server) serveSchedulerUI(w http.ResponseWriter, r *http.Request, base, rest string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		dbadminText(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if rest == "/api" {
		dbadminText(w, http.StatusNotFound, "not found")
		return
	}
	h := w.Header()
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Security-Policy", schedulerShellCSP)
	if name, ok := strings.CutPrefix(rest, "/assets/"); ok {
		asset, ok := schedulerui.Lookup(name)
		if !ok {
			dbadminText(w, http.StatusNotFound, "not found")
			return
		}
		h.Set("Content-Type", asset.ContentType)
		// Content-hashed names: a fetched file never changes.
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
		h.Set("ETag", asset.ETag)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(asset.Body))
		return
	}
	page, err := schedulerui.Index(base + "/")
	if err != nil {
		s.Log.Error("scheduler UI", "err", err)
		dbadminText(w, http.StatusInternalServerError, "scheduler UI unavailable")
		return
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(page))
}

// hardenSchedulerResponse admits only API data from the relay. Redirects and
// any media type minicrond's API does not use (HTML above all) are refused;
// what passes is sandboxed, loses app-controlled cookies, and drops duplicates
// of the headers the management listener already sets.
func hardenSchedulerResponse(resp *http.Response) error {
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != http.StatusNotModified {
		return fmt.Errorf("%w: status %d", errSchedulerResponse, resp.StatusCode)
	}
	h := resp.Header
	if ct := h.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || !schedulerAPITypes[mt] {
			return fmt.Errorf("%w: content type %q", errSchedulerResponse, ct)
		}
	} else if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified && resp.ContentLength != 0 {
		return fmt.Errorf("%w: no content type", errSchedulerResponse)
	}
	for _, k := range []string{"Set-Cookie", "Referrer-Policy", "X-Frame-Options", "X-Content-Type-Options",
		"Content-Security-Policy", "Content-Security-Policy-Report-Only", "Location", "Refresh", "Link"} {
		h.Del(k)
	}
	h.Set("Content-Security-Policy", schedulerAPICSP)
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
	return nil
}
