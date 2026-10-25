package api

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/mcp"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Token management is scopeFull only (not in routeScopes): a token can
// never mint, list or revoke tokens.

var tokenNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,47}$`)

func tokenToDTO(t store.APIToken) dto.APIToken {
	return dto.APIToken{ID: t.ID, Name: t.Name, Scope: dto.TokenScope(t.Scope),
		CreatedAt: platform.FormatTime(t.CreatedAt), ExpiresAt: platform.FormatTime(t.ExpiresAt),
		LastUsedAt: platform.FormatTime(t.LastUsedAt)}
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	ts, err := store.ListAPITokens(r.Context(), s.Store.DB())
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out := dto.APITokenList{Tokens: []dto.APIToken{}}
	for _, t := range ts {
		out.Tokens = append(out.Tokens, tokenToDTO(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateAPITokenRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	var fields []dto.FieldError
	if !tokenNamePattern.MatchString(req.Name) {
		fields = append(fields, dto.FieldError{Field: "name",
			Message: "1-48 characters: lowercase letters, digits, '.', '_' or '-'"})
	}
	if _, ok := parseScope(req.Scope); !ok {
		fields = append(fields, dto.FieldError{Field: "scope", Message: "must be read, operate or admin"})
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 365 {
		fields = append(fields, dto.FieldError{Field: "expiresInDays", Message: "must be 0 (never) to 365"})
	}
	if len(fields) > 0 {
		writeError(w, s.Log, &apiError{status: http.StatusUnprocessableEntity, code: dto.ErrorCodeValidation,
			msg: "validation failed", fields: fields})
		return
	}
	secret := TokenPrefix + platform.RandomToken(32)
	now := time.Now().UTC()
	t := store.APIToken{ID: "tok_" + platform.RandomHex(8), Name: req.Name, Scope: string(req.Scope),
		TokenHash: tokenHash(secret), CreatedAt: now}
	if req.ExpiresInDays > 0 {
		t.ExpiresAt = now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
	}
	if err := store.InsertAPIToken(r.Context(), s.Store.DB(), t); err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.Log.Info("api token created", "token", t.Name, "scope", t.Scope)
	writeJSON(w, http.StatusCreated, dto.CreatedAPIToken{APIToken: tokenToDTO(t), Token: secret})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("id")
	if !tokenNamePattern.MatchString(ref) && !idPattern.MatchString(ref) {
		writeError(w, s.Log, badRequest("invalid token id"))
		return
	}
	if err := store.DeleteAPIToken(r.Context(), s.Store.DB(), ref); err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.Log.Info("api token revoked", "token", ref)
	w.WriteHeader(http.StatusNoContent)
}

// ---- MCP over HTTP ----

// mcpHandler serves POST /mcp for bearer tokens and scoped socket clients.
// Browser sessions are refused: the UI has no use for it, and refusing keeps
// cookie-authenticated cross-site requests out of tool calls.
func (s *Server) mcpHandler(api http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := principalFrom(r.Context())
		if p.Kind == "session" {
			writeJSON(w, http.StatusUnauthorized, dto.ErrorResponse{Error: dto.ErrorBody{
				Code: dto.ErrorCodeUnauthorized, Message: "MCP needs an API token (bento token create)"}})
			return
		}
		// MCP requires Origin validation against DNS rebinding; clients
		// outside a browser send none.
		if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
			writeError(w, s.Log, &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden,
				msg: "origin not allowed"})
			return
		}
		sc := dto.TokenScopeAdmin
		switch p.Scope {
		case scopeRead:
			sc = dto.TokenScopeRead
		case scopeOperate:
			sc = dto.TokenScopeOperate
		}
		b := &mcp.Backend{HTTP: &http.Client{Transport: inProcess{api}}}
		mcp.NewServer(b, sc, s.Version).ServeHTTP(w, r)
	})
}

// inProcess serves a request through the API mux without a network hop.
// The request context carries the caller's principal, so requireAuth checks
// each route's scope exactly as for a direct call.
type inProcess struct{ h http.Handler }

func (t inProcess) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	t.h.ServeHTTP(rec, req)
	return &http.Response{
		StatusCode: rec.status, Status: http.StatusText(rec.status), Header: rec.header,
		Body: io.NopCloser(bytes.NewReader(rec.body.Bytes())), Request: req, ContentLength: int64(rec.body.Len()),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}, nil
}

// recorder buffers a response. Flush is a no-op so streaming handlers (SSE
// logs without follow) run to completion into the buffer.
type recorder struct {
	header http.Header
	status int
	wrote  bool
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
}
func (r *recorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(b)
}
func (r *recorder) Flush() {}
