package api

import (
	"context"
	"net/http"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
)

// scope orders what a caller may do. Browser sessions and the plain CLI are
// scopeFull; API tokens and scoped control-socket clients (`bento mcp`)
// carry read, operate, or admin.
type scope int

const (
	scopeRead scope = iota + 1
	scopeOperate
	scopeAdmin
	scopeFull
)

func parseScope(v dto.TokenScope) (scope, bool) {
	switch v {
	case dto.TokenScopeRead:
		return scopeRead, true
	case dto.TokenScopeOperate:
		return scopeOperate, true
	case dto.TokenScopeAdmin:
		return scopeAdmin, true
	}
	return 0, false
}

func (s scope) String() string {
	switch s {
	case scopeRead:
		return "read"
	case scopeOperate:
		return "operate"
	case scopeAdmin:
		return "admin"
	case scopeFull:
		return "full"
	}
	return "none"
}

// routeScopes is the least scope each route needs. A route that is not
// listed needs scopeFull, so a new route is closed to tokens until it is
// classified here.
//
// Never listed (session or unscoped CLI only): removing apps, hosts,
// schedules or backups; restores and clones; prune; export; exec, terminals
// and raw scheduler commands (importing or deleting app jobs; admin may
// create and edit jobs through the typed routes); database-browser tickets; credentials (password,
// tunnel token, restic settings and keys, webhook secrets); rclone; backup
// downloads; image prune; and token management itself.
var routeScopes = map[string]scope{
	"GET /api/v1/system":                             scopeRead,
	"GET /api/v1/system/docker":                      scopeRead,
	"GET /api/v1/system/runtime-images":              scopeRead,
	"GET /api/v1/catalog":                            scopeRead,
	"GET /api/v1/apps":                               scopeRead,
	"GET /api/v1/apps/{id}":                          scopeRead,
	"GET /api/v1/apps/{id}/git":                      scopeRead,
	"GET /api/v1/apps/{id}/restic":                   scopeRead,
	"GET /api/v1/apps/{id}/readiness":                scopeRead,
	"GET /api/v1/apps/{id}/metrics":                  scopeRead,
	"GET /api/v1/apps/{id}/logs":                     scopeRead,
	"GET /api/v1/apps/{id}/scheduler/jobs":           scopeRead,
	"GET /api/v1/apps/{id}/scheduler/runs":           scopeRead,
	"GET /api/v1/apps/{id}/scheduler/runs/{run}/log": scopeRead,
	"GET /api/v1/operations":                         scopeRead,
	"GET /api/v1/operations/{id}":                    scopeRead,
	"GET /api/v1/operations/{id}/events":             scopeRead,
	"GET /api/v1/services":                           scopeRead,
	"GET /api/v1/reconcile":                          scopeRead,
	"GET /api/v1/edge":                               scopeRead,
	"GET /api/v1/edge/metrics":                       scopeRead,
	"GET /api/v1/tunnel":                             scopeRead,
	"GET /api/v1/utils":                              scopeRead,
	"GET /api/v1/dbadmin":                            scopeRead,
	"GET /api/v1/hosts":                              scopeRead,
	"GET /api/v1/retired":                            scopeRead,
	"GET /api/v1/backups/artifacts":                  scopeRead,
	"GET /api/v1/backups/runs":                       scopeRead,
	"GET /api/v1/backups/apps":                       scopeRead,
	"GET /api/v1/backups/schedules":                  scopeRead,
	"GET /api/v1/backups/schedules/{id}":             scopeRead,
	"GET /api/v1/backups/rclone":                     scopeRead,

	"POST /api/v1/apps/{id}/start":                             scopeOperate,
	"POST /api/v1/apps/{id}/stop":                              scopeOperate,
	"POST /api/v1/apps/{id}/restart":                           scopeOperate,
	"POST /api/v1/apps/{id}/deploy":                            scopeOperate,
	"POST /api/v1/apps/{id}/restic/backup":                     scopeOperate,
	"POST /api/v1/apps/{id}/restic/refresh":                    scopeOperate,
	"POST /api/v1/apps/{id}/restic/check":                      scopeOperate,
	"POST /api/v1/operations/{id}/cancel":                      scopeOperate,
	"POST /api/v1/services/{name}/restart":                     scopeOperate,
	"POST /api/v1/backups":                                     scopeOperate,
	"POST /api/v1/apps/{id}/scheduler/jobs/{name}/run":         scopeOperate,
	"POST /api/v1/apps/{id}/scheduler/workers/{name}/{action}": scopeOperate,
	"POST /api/v1/apps/{id}/scheduler/runs/{run}/stop":         scopeOperate,
	"POST /api/v1/system/runtime-images/{key}/rebuild":         scopeOperate,

	"POST /api/v1/apps":                                    scopeAdmin,
	"PATCH /api/v1/apps/{id}":                              scopeAdmin,
	"POST /api/v1/apps/{id}/publish":                       scopeAdmin,
	"POST /api/v1/apps/{id}/unpublish":                     scopeAdmin,
	"POST /api/v1/apps/{id}/bindings":                      scopeAdmin,
	"POST /api/v1/apps/{id}/bindings/{bid}/databases":      scopeAdmin,
	"POST /api/v1/apps/{id}/permissions":                   scopeAdmin,
	"PUT /api/v1/apps/{id}/git":                            scopeAdmin,
	"POST /api/v1/services":                                scopeAdmin,
	"PUT /api/v1/services/{name}/settings":                 scopeAdmin,
	"PUT /api/v1/edge":                                     scopeAdmin,
	"POST /api/v1/hosts":                                   scopeAdmin,
	"PUT /api/v1/hosts/{name}":                             scopeAdmin,
	"POST /api/v1/backups/schedules":                       scopeAdmin,
	"PUT /api/v1/backups/schedules/{id}":                   scopeAdmin,
	"POST /api/v1/backups/schedules/{id}/enabled":          scopeAdmin,
	"POST /api/v1/apps/{id}/scheduler/jobs/{name}/enabled": scopeAdmin,
	"POST /api/v1/apps/{id}/scheduler/jobs":                scopeAdmin,
	// Moves apps onto other PHP/toolchain releases.
	"POST /api/v1/system/runtime-images/{key}/rollout": scopeAdmin,
	"PATCH /api/v1/apps/{id}/scheduler/jobs/{name}":    scopeAdmin,
	// App code files (~/app only, no hidden paths). Admin because the code
	// directory can hold credentials and an edit runs as the app's code.
	"GET /api/v1/apps/{id}/files":           scopeAdmin,
	"GET /api/v1/apps/{id}/files/content":   scopeAdmin,
	"PUT /api/v1/apps/{id}/files/content":   scopeAdmin,
	"PATCH /api/v1/apps/{id}/files/content": scopeAdmin,
}

func routeScope(pattern string) scope {
	if sc, ok := routeScopes[pattern]; ok {
		return sc
	}
	return scopeFull
}

// callerScope is the scope of the authenticated caller.
func callerScope(ctx context.Context) scope {
	p, ok := principalFrom(ctx)
	if !ok {
		return 0
	}
	return p.Scope
}

// redactedValue replaces env values shown to restricted callers.
const redactedValue = "[redacted]"

// restricted reports a token or scoped-socket caller. Such callers never see
// operator env values and cannot change them.
func restricted(ctx context.Context) bool { return callerScope(ctx) < scopeFull }

func scopeDenied(need, have scope) *apiError {
	return &apiError{status: http.StatusForbidden, code: dto.ErrorCodeForbidden,
		msg: "this action needs " + need.String() + " access; the caller has " + have.String()}
}
