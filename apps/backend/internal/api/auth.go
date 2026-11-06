package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

const (
	SessionCookie = "bento_session"
	SessionTTL    = 12 * time.Hour
	authSetting   = "auth"
	csrfHeader    = "X-CSRF-Token"
	// ScopeHeader lets a control-socket client (bento mcp) drop to a token
	// scope. It can only narrow access, never widen it.
	ScopeHeader = "X-Bento-Scope"
	// TokenPrefix marks Bento API tokens so leaked ones are recognizable.
	TokenPrefix = "bento_pat_"
)

// PasswordRecord is the stored operator credential (argon2id).
type PasswordRecord struct {
	Hash string `json:"hash"`
}

// argon2id parameters for new hashes: OWASP's recommended minimum (19 MiB, 2
// iterations, 1 lane). Each stored hash carries its own parameters, so older
// hashes keep verifying and are upgraded at the next successful login.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
)

// HashPassword derives an encoded argon2id hash.
func HashPassword(password string) string {
	salt := []byte(platform.RandomToken(16))
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"argon2id$%d$%d$%d$%s$%s",
		argonTime, argonMemory, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

// hashParams parses an encoded hash's argon2id parameters.
func hashParams(encoded string) (parts []string, t, m, p uint32, ok bool) {
	parts = strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return nil, 0, 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[1]+" "+parts[2]+" "+parts[3], "%d %d %d", &t, &m, &p); err != nil {
		return nil, 0, 0, 0, false
	}
	return parts, t, m, p, true
}

// needsRehash reports whether a hash was made with other parameters than
// HashPassword uses now.
func needsRehash(encoded string) bool {
	_, t, m, p, ok := hashParams(encoded)
	return !ok || t != argonTime || m != argonMemory || p != argonThreads
}

func verifyPassword(encoded, password string) bool {
	parts, t, m, p, ok := hashParams(encoded)
	if !ok || m > 256*1024 || t > 16 || p == 0 || p > 16 {
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

// upgradeHash re-hashes a correct password whose stored hash uses older
// parameters. It replaces the hash only if it is still the one that was
// checked, so a concurrent password change wins, and it keeps every session.
// A failure leaves the old hash, which still verifies.
func (s *Server) upgradeHash(ctx context.Context, old, password string) {
	err := s.Store.Tx(ctx, func(q store.Q) error {
		var cur PasswordRecord
		if _, err := store.GetSetting(ctx, q, authSetting, &cur); err != nil || cur.Hash != old {
			return err
		}
		return store.PutSetting(ctx, q, authSetting, PasswordRecord{Hash: HashPassword(password)})
	})
	if err != nil {
		s.Log.Warn("upgrade operator password hash", "err", err)
	}
}

// ValidatePassword enforces a minimum operator password strength.
func ValidatePassword(pw string) error {
	if len(pw) < 12 || len(pw) > 1024 {
		return errors.New("password must be 12-1024 characters")
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

// confirmCurrentPassword makes a browser session prove the operator password
// before an action that would outlive the session (a new password, an API
// token). Root on the control socket is exempt: that is how a locked-out
// operator recovers. Wrong guesses count against the login limiter. It writes
// the error response and returns false when the caller must stop.
func (s *Server) confirmCurrentPassword(w http.ResponseWriter, r *http.Request, current string) bool {
	if p, ok := principalFrom(r.Context()); ok && p.Kind == "local" {
		return true
	}
	var rec PasswordRecord
	if _, err := store.GetSetting(r.Context(), s.Store.DB(), authSetting, &rec); err != nil {
		writeError(w, s.Log, err)
		return false
	}
	if current != "" && rec.Hash != "" {
		match, admitted := s.limiter.checkPassword(loginClient(r), rec.Hash, current)
		if !admitted {
			writeError(w, s.Log, &apiError{status: http.StatusTooManyRequests, code: dto.ErrorCodeRateLimited,
				msg: "too many password attempts; wait and retry"})
			return false
		}
		if match {
			return true
		}
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}
	writeError(w, s.Log, &apiError{status: http.StatusUnprocessableEntity, code: dto.ErrorCodeValidation,
		msg: "current password required", fields: []dto.FieldError{{Field: "currentPassword",
			Message: "enter the current operator password"}}})
	return false
}

type principalKey struct{}

type principal struct {
	Kind    string // "session" | "local" | "token"
	Session store.Session
	Token   store.APIToken
	Scope   scope
}

func principalFrom(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(principalKey{}).(principal)
	return p, ok
}

const (
	// maxLoginFailures failed password checks within loginWindow refuse
	// further attempts from the same client until the oldest one ages out.
	maxLoginFailures = 10
	loginWindow      = 5 * time.Minute
	// maxConcurrentHashes bounds password checks in flight. Each one is an
	// argon2id derivation holding 19 MiB (64 MiB for a hash from before
	// 2026-11-03 until it is upgraded), however many requests arrive; one
	// client gets at most maxClientHashes of them.
	maxConcurrentHashes = 4
	maxClientHashes     = 2
)

// loginLimiter slows password guessing and bounds the memory password checks
// use. Budgets are per client (the local user that opened the connection, see
// loginClient), so one local process cannot keep the operator's browser
// logins rate limited. An attempt counts against its client's budget from the
// moment it is admitted, before its password is hashed, so parallel guesses
// cannot all pass the check while none has failed yet.
type loginLimiter struct {
	mu      sync.Mutex
	clients map[string]*loginBudget
	pending int // admitted attempts of every client whose check has not finished
}

type loginBudget struct {
	failures []time.Time
	pending  int
}

// begin admits one password check for client, or reports false when the
// client's failure budget (counting its attempts in flight) is spent, it has
// maxClientHashes checks running, or maxConcurrentHashes run in all. An
// admitted attempt must end with exactly one call of the returned function:
// true for a correct password, false for a wrong one.
func (l *loginLimiter) begin(client string) (func(ok bool), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.clients == nil {
		l.clients = map[string]*loginBudget{}
	}
	cutoff := time.Now().Add(-loginWindow)
	for k, b := range l.clients {
		kept := b.failures[:0]
		for _, t := range b.failures {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		b.failures = kept
		if len(b.failures) == 0 && b.pending == 0 && k != client {
			delete(l.clients, k)
		}
	}
	b := l.clients[client]
	if b == nil {
		b = &loginBudget{}
		l.clients[client] = b
	}
	if len(b.failures)+b.pending >= maxLoginFailures || b.pending >= maxClientHashes || l.pending >= maxConcurrentHashes {
		return nil, false
	}
	b.pending++
	l.pending++
	var once sync.Once
	return func(ok bool) {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			b.pending--
			l.pending--
			if !ok {
				b.failures = append(b.failures, time.Now())
			}
		})
	}, true
}

// checkPassword verifies a password under client's budget. It reports
// (match, admitted); a refused attempt is never hashed.
func (l *loginLimiter) checkPassword(client, encoded, password string) (bool, bool) {
	done, ok := l.begin(client)
	if !ok {
		return false, false
	}
	match := verifyPassword(encoded, password)
	done(match)
	return match, true
}

// loginClient names the client a login attempt counts against: the local
// user (uid) owning the other end of a loopback TCP connection, read from
// /proc/net/tcp{,6}. Every browser reaches the loopback listener from
// 127.0.0.1 (an SSH tunnel's sshd runs as the operator), so addresses would
// not tell an operator from another local user. "unknown" when the owner
// cannot be read.
func loginClient(r *http.Request) string {
	c, _ := r.Context().Value(connKey{}).(net.Conn)
	if c == nil {
		return "unknown"
	}
	local, ok1 := c.LocalAddr().(*net.TCPAddr)
	remote, ok2 := c.RemoteAddr().(*net.TCPAddr)
	if !ok1 || !ok2 {
		return "unknown"
	}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if uid, ok := tcpOwner(string(b), remote.Port, local.Port); ok {
			return "uid:" + uid
		}
	}
	return "unknown"
}

// tcpOwner finds, in /proc/net/tcp text, the socket whose local port is
// localPort and whose peer port is peerPort, and returns its owner uid.
func tcpOwner(table string, localPort, peerPort int) (string, bool) {
	want := func(addr string, port int) bool {
		_, hexPort, ok := strings.Cut(addr, ":")
		p, err := strconv.ParseUint(hexPort, 16, 16)
		return ok && err == nil && int(p) == port
	}
	for line := range strings.SplitSeq(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || f[0] == "sl" {
			continue
		}
		if want(f[1], localPort) && want(f[2], peerPort) {
			return f[7], true
		}
	}
	return "", false
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
	// An opaque origin (sandboxed frame, data: URL) is never trusted, even if
	// misconfigured into AllowedOrigins.
	if origin == "" || origin == "null" {
		return false
	}
	return slices.Contains(s.AllowedOrigins, origin)
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

// requireAuth admits a caller whose scope covers need. A principal already
// in the context (the control socket, or an MCP tool call re-entering the
// API in-process) is checked as is; otherwise a bearer token or a browser
// session authenticates the request.
func (s *Server) requireAuth(need scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := principalFrom(r.Context()); ok {
			if p.Scope < need {
				writeError(w, s.Log, scopeDenied(need, p.Scope))
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" {
			p, ok := s.tokenPrincipal(r)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, dto.ErrorResponse{Error: dto.ErrorBody{
					Code: dto.ErrorCodeUnauthorized, Message: "invalid or expired API token"}})
				return
			}
			if p.Scope < need {
				writeError(w, s.Log, scopeDenied(need, p.Scope))
				return
			}
			// A bearer token is never sent by a browser on its own, so token
			// writes need no Origin or CSRF check.
			ctx := context.WithValue(r.Context(), principalKey{}, p)
			ctx = operations.WithOrigin(ctx, "token:"+p.Token.Name)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		sess, ok := s.sessionFromRequest(r)
		if !ok {
			writeJSON(
				w,
				http.StatusUnauthorized,
				dto.ErrorResponse{Error: dto.ErrorBody{Code: dto.ErrorCodeUnauthorized, Message: "authentication required"}},
			)
			return
		}
		if unsafeMethod(r.Method) {
			if err := s.checkWrite(r, sess); err != nil {
				writeError(w, s.Log, err)
				return
			}
		}
		ctx := context.WithValue(r.Context(), principalKey{}, principal{Kind: "session", Session: sess, Scope: scopeFull})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// tokenPrincipal authenticates "Authorization: Bearer bento_pat_…".
func (s *Server) tokenPrincipal(r *http.Request) (principal, bool) {
	scheme, tok, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !strings.HasPrefix(tok, TokenPrefix) || len(tok) > 128 {
		return principal{}, false
	}
	now := time.Now()
	t, err := store.GetLiveAPIToken(r.Context(), s.Store.DB(), tokenHash(tok), now)
	if err != nil {
		return principal{}, false
	}
	sc, ok := parseScope(dto.TokenScope(t.Scope))
	if !ok {
		return principal{}, false
	}
	if err := store.TouchAPIToken(r.Context(), s.Store.DB(), t.ID, now); err != nil {
		s.Log.Warn("record token use", "token", t.Name, "err", err)
	}
	return principal{Kind: "token", Token: t, Scope: sc}, true
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
	writeJSON(
		w,
		http.StatusOK,
		dto.Session{Authenticated: true, CSRFToken: sess.CSRFToken, ExpiresAt: platform.FormatTime(sess.ExpiresAt)},
	)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r.Header.Get("Origin")) {
		writeError(w, s.Log, &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden, msg: "origin not allowed"})
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
	match, admitted := s.limiter.checkPassword(loginClient(r), rec.Hash, req.Password)
	if !admitted {
		writeError(
			w,
			s.Log,
			&apiError{
				status: http.StatusTooManyRequests,
				code:   dto.ErrorCodeRateLimited,
				msg:    "too many login attempts; wait and retry",
			},
		)
		return
	}
	if !match {
		select { // slow guessing, but not past a client that already left
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
		writeError(
			w,
			s.Log,
			&apiError{status: http.StatusUnauthorized, code: dto.ErrorCodeUnauthorized, msg: "invalid password"},
		)
		return
	}
	if needsRehash(rec.Hash) {
		s.upgradeHash(r.Context(), rec.Hash, req.Password)
	}
	token := platform.RandomToken(32)
	sess := store.Session{
		TokenHash: tokenHash(token),
		CSRFToken: platform.RandomToken(24),
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(SessionTTL),
	}
	if err := store.InsertSession(r.Context(), s.Store.DB(), sess); err != nil {
		writeError(w, s.Log, err)
		return
	}
	if err := store.PruneSessions(r.Context(), s.Store.DB(), time.Now()); err != nil {
		// Expired sessions are rejected on use; pruning is housekeeping.
		s.Log.Warn("prune expired sessions", "err", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil, Expires: sess.ExpiresAt,
	})
	writeJSON(
		w,
		http.StatusOK,
		dto.Session{Authenticated: true, CSRFToken: sess.CSRFToken, ExpiresAt: platform.FormatTime(sess.ExpiresAt)},
	)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var revokeErr error
	if c, err := r.Cookie(SessionCookie); err == nil {
		revokeErr = store.RevokeSession(r.Context(), s.Store.DB(), tokenHash(c.Value))
	}
	http.SetCookie(
		w,
		&http.Cookie{
			Name:     SessionCookie,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		},
	)
	if revokeErr != nil {
		// The cookie is cleared, but the server-side session is still valid:
		// report the failure instead of claiming a completed logout.
		writeError(w, s.Log, fmt.Errorf("revoke session: %w", revokeErr))
		return
	}
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
			writeJSON(
				w,
				http.StatusForbidden,
				dto.ErrorResponse{Error: dto.ErrorBody{
					Code:    dto.ErrorCodeForbidden,
					Message: "control socket requires root or the backend user",
				}},
			)
			return
		}
		p := principal{Kind: "local", Scope: scopeFull}
		ctx := r.Context()
		if v := r.Header.Get(ScopeHeader); v != "" {
			sc, ok := parseScope(dto.TokenScope(v))
			if !ok {
				writeError(w, s.Log, badRequest(ScopeHeader+" must be read, operate or admin"))
				return
			}
			p.Scope = sc
			ctx = operations.WithOrigin(ctx, "local:"+v)
		}
		ctx = context.WithValue(ctx, principalKey{}, p)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
