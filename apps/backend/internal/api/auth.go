package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

const (
	SessionCookie = "bento_session"
	SessionTTL    = 12 * time.Hour
	authSetting   = "auth"
	csrfHeader    = "X-CSRF-Token"
)

// PasswordRecord is the stored operator credential (argon2id).
type PasswordRecord struct {
	Hash string `json:"hash"`
}

// HashPassword derives an encoded argon2id hash.
func HashPassword(password string) string {
	salt := []byte(platform.RandomToken(16))
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return fmt.Sprintf("argon2id$3$65536$2$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false
	}
	var t, m, p uint32
	if _, err := fmt.Sscanf(parts[1]+" "+parts[2]+" "+parts[3], "%d %d %d", &t, &m, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ValidatePassword enforces a minimum operator password strength.
func ValidatePassword(pw string) error {
	if len(pw) < 12 || len(pw) > 1024 {
		return fmt.Errorf("password must be 12-1024 characters")
	}
	return nil
}

// SetOperatorPassword stores a new password and revokes all sessions.
func SetOperatorPassword(ctx context.Context, s *store.Store, pw string) error {
	if err := ValidatePassword(pw); err != nil {
		return err
	}
	return s.Tx(ctx, func(q store.Q) error {
		if err := store.PutSetting(ctx, q, authSetting, PasswordRecord{Hash: HashPassword(pw)}); err != nil {
			return err
		}
		return store.RevokeAllSessions(ctx, q)
	})
}

type principalKey struct{}

type principal struct {
	Kind    string // "session" | "local"
	Session store.Session
}

func principalFrom(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(principalKey{}).(principal)
	return p, ok
}

// loginLimiter slows password guessing.
type loginLimiter struct {
	mu       sync.Mutex
	failures []time.Time
}

func (l *loginLimiter) allowed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-5 * time.Minute)
	kept := l.failures[:0]
	for _, t := range l.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.failures = kept
	return len(l.failures) < 10
}

func (l *loginLimiter) fail() {
	l.mu.Lock()
	l.failures = append(l.failures, time.Now())
	l.mu.Unlock()
}

func tokenHash(token string) string { return platform.SHA256Hex([]byte(token)) }

func (s *Server) sessionFromRequest(r *http.Request) (store.Session, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" || len(c.Value) > 128 {
		return store.Session{}, false
	}
	sess, err := store.GetLiveSession(r.Context(), s.Store.DB(), tokenHash(c.Value), time.Now())
	if err != nil {
		return store.Session{}, false
	}
	return sess, true
}

// originAllowed requires an exact configured origin.
func (s *Server) originAllowed(origin string) bool {
	for _, o := range s.AllowedOrigins {
		if origin == o {
			return true
		}
	}
	return false
}

// checkWrite enforces exact Origin, same-origin fetch metadata, and the
// session CSRF token for state-changing browser requests.
func (s *Server) checkWrite(r *http.Request, sess store.Session) error {
	if !s.originAllowed(r.Header.Get("Origin")) {
		return &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden, msg: "origin not allowed"}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden, msg: "cross-site request refused"}
	}
	got := r.Header.Get(csrfHeader)
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
		return &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden, msg: "missing or invalid CSRF token"}
	}
	return nil
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// requireAuth wraps browser-facing handlers. Local CLI requests arrive on a
// separate listener already marked with a local principal.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := principalFrom(r.Context()); ok && p.Kind == "local" {
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := s.sessionFromRequest(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, dto.ErrorResponse{Error: dto.ErrorBody{Code: dto.ErrorCodeUnauthorized, Message: "authentication required"}})
			return
		}
		if unsafeMethod(r.Method) {
			if err := s.checkWrite(r, sess); err != nil {
				writeError(w, s.Log, err)
				return
			}
		}
		ctx := context.WithValue(r.Context(), principalKey{}, principal{Kind: "session", Session: sess})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	if p, ok := principalFrom(r.Context()); ok && p.Kind == "local" {
		writeJSON(w, http.StatusOK, dto.Session{Authenticated: true})
		return
	}
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusOK, dto.Session{Authenticated: false})
		return
	}
	writeJSON(w, http.StatusOK, dto.Session{Authenticated: true, CSRFToken: sess.CSRFToken, ExpiresAt: platform.FormatTime(sess.ExpiresAt)})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r.Header.Get("Origin")) {
		writeError(w, s.Log, &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden, msg: "origin not allowed"})
		return
	}
	if !s.limiter.allowed() {
		writeError(w, s.Log, &apiError{status: http.StatusTooManyRequests, code: dto.ErrorCodeRateLimited, msg: "too many failed logins; wait and retry"})
		return
	}
	var req dto.LoginRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	var rec PasswordRecord
	found, err := store.GetSetting(r.Context(), s.Store.DB(), authSetting, &rec)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	if !found || rec.Hash == "" {
		writeError(w, s.Log, &apiError{status: http.StatusServiceUnavailable, code: dto.ErrorCodeUnavailable,
			msg: "no operator password is configured; run `bento auth set-password` on the host"})
		return
	}
	if !verifyPassword(rec.Hash, req.Password) {
		s.limiter.fail()
		time.Sleep(300 * time.Millisecond)
		writeError(w, s.Log, &apiError{status: http.StatusUnauthorized, code: dto.ErrorCodeUnauthorized, msg: "invalid password"})
		return
	}
	token := platform.RandomToken(32)
	sess := store.Session{TokenHash: tokenHash(token), CSRFToken: platform.RandomToken(24), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(SessionTTL)}
	if err := store.InsertSession(r.Context(), s.Store.DB(), sess); err != nil {
		writeError(w, s.Log, err)
		return
	}
	_ = store.PruneSessions(r.Context(), s.Store.DB(), time.Now())
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil, Expires: sess.ExpiresAt,
	})
	writeJSON(w, http.StatusOK, dto.Session{Authenticated: true, CSRFToken: sess.CSRFToken, ExpiresAt: platform.FormatTime(sess.ExpiresAt)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		_ = store.RevokeSession(r.Context(), s.Store.DB(), tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

// ---- local CLI transport ----

type connKey struct{}

// PeerUID returns the uid of the process on the other end of a Unix socket.
func PeerUID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *syscall.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if serr != nil {
		return -1, serr
	}
	return int(cred.Uid), nil
}

// ConnContext stores the connection for peer-credential checks.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, c)
}

// LocalOnly admits only root or the backend's own uid over the control
// socket; credentials never travel in argv.
func (s *Server) LocalOnly(next http.Handler) http.Handler {
	own := syscall.Geteuid()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Context().Value(connKey{}).(net.Conn)
		uid, err := PeerUID(c)
		if err != nil || (uid != 0 && uid != own) {
			writeJSON(w, http.StatusForbidden, dto.ErrorResponse{Error: dto.ErrorBody{Code: dto.ErrorCodeForbidden, Message: "control socket requires root or the backend user"}})
			return
		}
		ctx := context.WithValue(r.Context(), principalKey{}, principal{Kind: "local"})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
