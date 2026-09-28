package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func runtimeFromDTO(r dto.RuntimeSpec) domain.Runtime {
	out := domain.Runtime{Kind: domain.RuntimeKind(r.Kind)}
	if r.PHP != nil {
		p := domain.PHPRuntime(*r.PHP)
		out.PHP = &p
	}
	if r.HTTP != nil {
		h := domain.HTTPRuntime{Toolchain: r.HTTP.Toolchain, Version: r.HTTP.Version, Argv: append([]string(nil), r.HTTP.Argv...),
			Workdir: r.HTTP.Workdir, Port: r.HTTP.Port, ReadyPath: r.HTTP.ReadyPath}
		out.HTTP = &h
	}
	return out
}

func runtimeToDTO(r domain.Runtime) dto.RuntimeSpec {
	out := dto.RuntimeSpec{Kind: dto.RuntimeKind(r.Kind)}
	if r.PHP != nil {
		p := dto.PHPRuntime(*r.PHP)
		out.PHP = &p
	}
	if r.HTTP != nil {
		out.HTTP = &dto.HTTPRuntime{Toolchain: r.HTTP.Toolchain, Version: r.HTTP.Version, Argv: nonNil(r.HTTP.Argv),
			Workdir: r.HTTP.Workdir, Port: r.HTTP.Port, ReadyPath: r.HTTP.ReadyPath}
	}
	return out
}

func routeFromDTO(r *dto.Route) domain.Route {
	if r == nil {
		return domain.Route{TLS: domain.TLSNone}
	}
	return domain.Route{TLS: domain.TLSMode(r.TLS), CertName: r.CertName, RedirectHTTPS: r.RedirectHTTPS, AccessLog: r.AccessLog, StaticCache: r.StaticCache}
}

func routeToDTO(r domain.Route) dto.Route {
	return dto.Route{TLS: dto.TLSMode(r.TLS), CertName: r.CertName, RedirectHTTPS: r.RedirectHTTPS, AccessLog: r.AccessLog, StaticCache: r.StaticCache}
}

func domainsToDTO(ds []domain.DomainLink) []dto.Domain {
	out := []dto.Domain{}
	for _, d := range ds {
		out = append(out, dto.Domain{Name: d.Name, Primary: d.Primary})
	}
	return out
}

func observedToDTO(app domain.App, obs operations.Observation, planned string, rec dto.Reconcile) dto.Observed {
	o := dto.Observed{ContainerID: short(obs.ContainerID), Health: obs.Health, StartedAt: obs.StartedAt, ExitCode: obs.ExitCode,
		GenerationCurrent: obs.Exists && planned != "" && obs.Generation == planned}
	switch {
	case !app.Provisioned:
		o.State, o.Message = dto.ObservedStateAbsent, "provisioning pending or failed; see operations"
	case len(obs.Duplicates) > 0 || (obs.Exists && !obs.Owned):
		o.State, o.Message = dto.ObservedStateBlocked, "conflicting or duplicate containers"
	case rec.Blocked:
		o.State, o.Message = dto.ObservedStateBlocked, "reconciliation retry budget exhausted: "+rec.LastError
	case !obs.Exists:
		o.State = dto.ObservedStateAbsent
	case obs.Running:
		switch obs.Health {
		case "healthy":
			o.State = dto.ObservedStateHealthy
		case "unhealthy":
			o.State = dto.ObservedStateUnhealthy
		default:
			o.State = dto.ObservedStateStarting
		}
	case obs.Status == "restarting":
		o.State = dto.ObservedStateStarting
	case app.DesiredRuntime == domain.DesiredRunning && obs.ExitCode != 0:
		o.State, o.Message = dto.ObservedStateFailed, fmt.Sprintf("exited with code %d", obs.ExitCode)
	default:
		o.State = dto.ObservedStateStopped
	}
	if o.Message == "" && rec.LastError != "" && o.State != dto.ObservedStateHealthy {
		o.Message = "last reconciliation failed: " + rec.LastError
	}
	if app.DesiredRuntime == domain.DesiredRunning && app.Publication == domain.Published && o.State != dto.ObservedStateHealthy {
		if o.Message == "" {
			o.Message = "published route is pending activation or currently unavailable"
		}
	}
	return o
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func ingressInfo(app domain.App) dto.IngressInfo {
	info := dto.IngressInfo{Mode: dto.IngressMode(app.Ingress), InternalURL: fmt.Sprintf("http://%s:%d", runtime.AppAlias(app.ID), app.HTTPPort())}
	switch app.Ingress {
	case domain.IngressManaged:
		info.BentoControls = true
		info.Note = "Bento's edge owns this route; publish/unpublish and stop control it."
	case domain.IngressExternal:
		info.Note = "The public route is operator-owned (cloudflared or an external proxy targeting the internal URL). " +
			"Bento cannot remove it: stop makes the app unavailable, but the route may remain and return errors. " +
			"Bypassing the edge also bypasses its redirects, TLS policy, limits, and logs."
	default:
		info.Note = "No public ingress; the app is reachable only on the private app network."
	}
	return info
}

func (s *Server) appToDTO(ctx context.Context, app domain.App, detail bool) (dto.App, error) {
	obs, err := s.C.Observe(ctx, app)
	if err != nil {
		return dto.App{}, err
	}
	planned, _, _ := s.C.PlannedGeneration(ctx, app)
	rs := s.R.Status(app.ID)
	rec := dto.Reconcile{Failures: rs.Failures, Blocked: rs.Blocked, LastError: rs.LastError}
	if !rs.NextAttempt.IsZero() {
		rec.NextAttempt = platform.FormatTime(rs.NextAttempt)
	}
	key := app.Runtime.ImageKey()
	sum := dto.AppSummary{
		ID: app.ID, Slug: app.Slug, UID: app.UID, Kind: dto.RuntimeKind(app.Runtime.Kind), Toolchain: key.Toolchain, Version: key.Version,
		DesiredRuntime: dto.DesiredRuntime(app.DesiredRuntime), Ingress: dto.IngressMode(app.Ingress), Publication: dto.Publication(app.Publication),
		PrimaryDomain: app.PrimaryDomain(), Provisioned: app.Provisioned, ConfigGeneration: int(app.ConfigGeneration),
		Observed: observedToDTO(app, obs, planned, rec), BindingSummary: []dto.BindingSummary{}, Resources: dto.Resources(app.Resources),
	}
	for _, b := range app.Bindings {
		sum.BindingSummary = append(sum.BindingSummary, dto.BindingSummary{Engine: dto.Engine(b.Engine), Service: b.Service, Databases: len(b.Databases)})
	}
	out := dto.App{AppSummary: sum}
	if !detail {
		return out, nil
	}
	out.GID = app.GID
	out.Home = app.ContainerHome()
	out.Runtime = runtimeToDTO(app.Runtime)
	out.Route = routeToDTO(app.Route)
	out.Domains = domainsToDTO(app.Domains)
	out.Bindings = []dto.Binding{}
	for _, b := range app.Bindings {
		db := dto.Binding{ID: b.ID, Engine: dto.Engine(b.Engine), Service: b.Service, Username: b.Username, Databases: nonNil(b.Databases),
			CreatedAt: platform.FormatTime(b.CreatedAt)}
		if b.Engine == domain.EngineSQLite {
			db.SQLitePath = runtime.SQLiteFile(b, app.Slug)
		}
		out.Bindings = append(out.Bindings, db)
	}
	out.RedisPrefix = app.Redis.Prefix
	out.RedisUser = app.Redis.Username
	out.IngressInfo = ingressInfo(app)
	out.Reconcile = rec
	out.CreatedAt = platform.FormatTime(app.CreatedAt)
	out.UpdatedAt = platform.FormatTime(app.UpdatedAt)
	return out, nil
}

func opToDTO(o store.Operation, events []store.OpEvent) dto.Operation {
	out := dto.Operation{
		ID: o.ID, Kind: o.Kind, TargetKind: o.TargetKind, TargetID: o.TargetID, State: dto.OperationState(o.State), Phase: o.Phase,
		Origin: o.Origin, ErrorCode: o.ErrorCode, ErrorMessage: o.ErrorMessage, Guidance: o.Guidance,
		CreatedAt: o.CreatedAt, StartedAt: o.StartedAt, FinishedAt: o.FinishedAt,
	}
	if len(o.Result) > 2 {
		_ = json.Unmarshal(o.Result, &out.Result)
	}
	for _, e := range events {
		out.Events = append(out.Events, dto.OperationEvent{Seq: e.Seq, At: e.At, Level: e.Level, Message: e.Message})
	}
	return out
}

func proxyToDTO(p domain.Proxy) dto.Proxy {
	return dto.Proxy{ID: p.ID, Name: p.Name, Upstreams: nonNil(p.Upstreams), Domains: domainsToDTO(p.Domains), Route: routeToDTO(p.Route),
		Enabled: p.Enabled, CreatedAt: platform.FormatTime(p.CreatedAt), UpdatedAt: platform.FormatTime(p.UpdatedAt)}
}

func gitSourceToDTO(g domain.GitSource, configured bool) dto.GitSource {
	if !configured {
		return dto.GitSource{}
	}
	return dto.GitSource{
		Configured: true, RepoURL: g.RepoURL, Branch: g.Branch, UsesSSH: g.UsesSSH(),
		PublicKey: g.PublicKey, Fingerprint: g.Fingerprint, KeyCreatedAt: platform.FormatTime(g.KeyCreatedAt),
		DeployedCommit: g.DeployedCommit, DeployedAt: platform.FormatTime(g.DeployedAt),
	}
}

func webhookToDTO(w domain.Webhook, enabled bool, url string, targets []string) dto.Webhook {
	if !enabled {
		return dto.Webhook{Targets: nonNil(targets), Deliveries: []dto.WebhookDelivery{}}
	}
	out := dto.Webhook{Enabled: true, Path: domain.WebhookDeployPath(w.HookID), SecretCreatedAt: platform.FormatTime(w.SecretCreatedAt),
		Targets: nonNil(targets), Deliveries: []dto.WebhookDelivery{}}
	if url != "" {
		out.URL = url + out.Path
	}
	for _, d := range w.Deliveries {
		out.Deliveries = append(out.Deliveries, dto.WebhookDelivery{At: platform.FormatTime(d.At), Provider: d.Provider, Event: d.Event,
			DeliveryID: d.DeliveryID, Ref: d.Ref, Commit: d.Commit, Pusher: d.Pusher, Auth: d.Auth, Result: d.Result, Detail: d.Detail, OperationID: d.OperationID})
	}
	return out
}
