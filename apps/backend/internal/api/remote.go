package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/remoteaccess"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// Remote access to data services over Tailcat (experimental).
//
// The address and a binding's password together grant access to a database
// from anywhere; that is the product decision. The address is therefore
// shown only to full-scope callers (a browser session or the unscoped CLI),
// and the binding login is the single API response that carries a database
// password: full scope only, a POST (so browser sessions pass the CSRF
// check), never cached, and journaled as a binding.login operation without
// the value.

// remoteAccessDTO describes a service's remote access to the caller.
func (s *Server) remoteAccessDTO(ctx context.Context, row store.ServiceRow) dto.RemoteAccess {
	port := operations.EnginePort(row.Engine)
	out := dto.RemoteAccess{Enabled: row.Remote.Enabled, State: dto.RemoteAccessStateOff, EnginePort: port,
		LocalPort: remoteaccess.LocalPort(port), MaxConnections: remoteaccess.DefaultMaxConns}
	if s.C.Remote != nil && s.C.Remote.MaxConns > 0 {
		out.MaxConnections = s.C.Remote.MaxConns
	}
	if !row.Remote.Enabled || s.C.Remote == nil {
		return out
	}
	st := s.C.Remote.Status(row.Name)
	out.Connections, out.Message = st.Connections, st.Message
	switch st.State {
	case remoteaccess.StateListening:
		out.State = dto.RemoteAccessStateListening
	case remoteaccess.StateFailed:
		out.State = dto.RemoteAccessStateFailed
	default:
		// Enabled but not (yet) supervised, or starting.
		out.State = dto.RemoteAccessStateStarting
	}
	if st.Address != "" && callerScope(ctx) == scopeFull {
		out.Address = st.Address
		out.Command = remoteaccess.ForwardCommand(st.Address, port)
	}
	return out
}

func (s *Server) handlePutServiceRemote(w http.ResponseWriter, r *http.Request) {
	var req dto.RemoteAccessRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	idem, err := idempotencyKey(r)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	op, err := s.C.SetServiceRemote(r.Context(), r.PathValue("name"), string(req.Action), idem)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	s.accepted(w, op, nil)
}

// handleBindingLogin reveals one MySQL/PostgreSQL binding's own login. See
// the comment at the top of this file for why this endpoint exists.
func (s *Server) handleBindingLogin(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if (p.Kind != "session" && p.Kind != "local") || p.Scope != scopeFull {
		writeError(w, s.Log, &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden,
			msg: "a binding login is shown only to a browser session or the local CLI"})
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
	b, err := loginBinding(app, bid)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	if err := s.C.RecordLoginReveal(r.Context(), app, b, p.Kind); err != nil {
		// No journal entry, no password.
		writeError(w, s.Log, err)
		return
	}
	s.Log.Info("binding login revealed", "app", app.Slug, "binding", b.ID, "by", p.Kind)
	port := remoteaccess.LocalPort(operations.EnginePort(b.Engine))
	out := dto.BindingLogin{Engine: dto.Engine(b.Engine), Service: b.Service, Host: "127.0.0.1", Port: port,
		Username: b.Username, Password: b.Password, Databases: nonNil(b.Databases), URLs: []dto.BindingLoginURL{}}
	scheme := "postgresql"
	if b.Engine == domain.EngineMySQL {
		scheme = "mysql"
	}
	for _, db := range b.Databases {
		u := url.URL{Scheme: scheme, User: url.UserPassword(b.Username, b.Password),
			Host: "127.0.0.1:" + strconv.Itoa(port), Path: "/" + db}
		out.URLs = append(out.URLs, dto.BindingLoginURL{Database: db, URL: u.String()})
	}
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, out) // Cache-Control: no-store
}

// loginBinding returns an app's MySQL/PostgreSQL binding that has a login.
func loginBinding(app domain.App, id string) (domain.Binding, error) {
	for _, b := range app.Bindings {
		if b.ID != id {
			continue
		}
		if b.Engine != domain.EngineMySQL && b.Engine != domain.EnginePostgres {
			return b, badRequest("only MySQL and PostgreSQL bindings have a login")
		}
		if b.Service == "" || b.Username == "" || b.Password == "" {
			return b, &apiError{status: http.StatusConflict, code: dto.ErrorCodePrecondition,
				msg: fmt.Sprintf("binding %s is not provisioned yet", b.ID)}
		}
		return b, nil
	}
	return domain.Binding{}, notFound("binding not found")
}
