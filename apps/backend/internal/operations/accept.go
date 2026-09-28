package operations

import (
	"context"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// ErrPrecondition reports a stale target generation or a state conflict.
var ErrPrecondition = errors.New("precondition failed")

// ErrConfirmation reports a missing or inexact destructive confirmation.
var ErrConfirmation = errors.New("confirmation required")

type BindingRequest struct {
	Engine  domain.Engine
	Service string
}

type CreateAppInput struct {
	Slug      string
	Runtime   domain.Runtime
	Resources domain.Resources
	Ingress   domain.IngressMode
	Domains   []string
	Route     domain.Route
	Bindings  []BindingRequest
}

func normalizeDomains(in []string, errs *domain.ValidationErrors) []domain.DomainLink {
	seen := map[string]bool{}
	var out []domain.DomainLink
	if len(in) > 50 {
		errs.Add("domains", "at most 50 domains")
		return nil
	}
	for i, d := range in {
		n, err := domain.NormalizeDomain(d)
		if err != nil {
			errs.Add(fmt.Sprintf("domains[%d]", i), "%s", err)
			continue
		}
		if seen[n] {
			errs.Add(fmt.Sprintf("domains[%d]", i), "duplicate domain")
			continue
		}
		seen[n] = true
		out = append(out, domain.DomainLink{Name: n, Primary: len(out) == 0})
	}
	return out
}

func dbIdent(slug string) string { return strings.ReplaceAll(slug, "-", "_") }

func newVacuumSlot() *domain.VacuumSlot {
	return &domain.VacuumSlot{DayOfWeek: mrand.IntN(7), Hour: mrand.IntN(5), Minute: mrand.IntN(60)}
}

func (c *Controller) newBinding(ctx context.Context, q store.Q, app domain.App, req BindingRequest, field string, errs *domain.ValidationErrors) (domain.Binding, bool) {
	b := domain.Binding{ID: "b" + platform.RandomHex(6), AppID: app.ID, Engine: req.Engine, CreatedAt: time.Now().UTC()}
	switch req.Engine {
	case domain.EngineMySQL, domain.EnginePostgres:
		svc, err := store.GetService(ctx, q, req.Service)
		if err != nil {
			errs.Add(field+".service", "unknown data service %q", req.Service)
			return b, false
		}
		if svc.Engine != req.Engine {
			errs.Add(field+".service", "service %s is %s, not %s", svc.Name, svc.Engine, req.Engine)
			return b, false
		}
		b.Service = svc.Name
		b.Username = "u" + app.ID
		b.Password = platform.RandomPassword(32)
		b.Databases = []string{dbIdent(app.Slug)}
	case domain.EngineSQLite:
		if req.Service != "" {
			errs.Add(field+".service", "must be omitted for sqlite")
			return b, false
		}
		b.SQLiteFileID = app.Slug + "_" + platform.RandomHex(5)
		b.Vacuum = newVacuumSlot()
	default:
		errs.Add(field+".engine", "must be mysql, postgres, or sqlite")
		return b, false
	}
	return b, true
}

// CreateApp allocates a new incarnation, persists it desired stopped and
// unpublished, and queues provisioning. It never starts the app.
func (c *Controller) CreateApp(ctx context.Context, in CreateAppInput, idem string) (domain.App, store.Operation, error) {
	var errs domain.ValidationErrors
	if err := domain.ValidateSlug(in.Slug); err != nil {
		errs.Add("slug", "%s", err)
	}
	domain.ValidateRuntime(&in.Runtime, &errs)
	domain.ValidateResources(&in.Resources, in.Runtime.Kind, &errs)
	if in.Ingress == "" {
		in.Ingress = domain.IngressManaged
	}
	if err := domain.ValidateIngress(in.Ingress); err != nil {
		errs.Add("ingress", "%s", err)
	}
	domain.ValidateRoute(&in.Route, "route", &errs)
	links := normalizeDomains(in.Domains, &errs)
	if len(in.Bindings) > 16 {
		errs.Add("bindings", "at most 16 bindings")
	}
	if err := errs.Err(); err != nil {
		return domain.App{}, store.Operation{}, err
	}
	// A retained home from an earlier incarnation is never adopted.
	if _, err := os.Lstat(c.Layout.AppHome(in.Slug)); err == nil {
		if idem != "" {
			if op, err := store.FindByIdempotencyKey(ctx, c.Store.DB(), idem); err == nil {
				app, _ := store.GetApp(ctx, c.Store.DB(), op.TargetID)
				return app, op, nil
			}
		}
		return domain.App{}, store.Operation{}, fmt.Errorf("%w: a retained home for %q exists from an earlier app; prune it or restore explicitly", store.ErrConflict, in.Slug)
	}
	rng := domain.DefaultUIDRange()
	_, _ = store.GetSetting(ctx, c.Store.DB(), "uid_range", &rng)
	now := time.Now().UTC()
	app := domain.App{
		ID: platform.NewAppID(), Slug: in.Slug, Runtime: in.Runtime, Resources: in.Resources,
		DesiredRuntime: domain.DesiredStopped, Ingress: in.Ingress, Publication: domain.Unpublished, Route: in.Route,
		ConfigGeneration: 1, CredentialsGeneration: 1, CreatedAt: now, UpdatedAt: now, Domains: links,
	}
	app.Redis = domain.RedisIdentity{Mode: "acl", Prefix: in.Slug + ":", Username: "app-" + app.ID, Password: platform.RandomPassword(32)}
	op, existed, err := c.Submit(ctx, Submission{
		Kind: KindAppProvision, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem, Generation: 1,
		Request: map[string]any{"slug": in.Slug},
		Mutate: func(ctx context.Context, q store.Q) error {
			if _, err := store.GetApp(ctx, q, in.Slug); err == nil {
				return fmt.Errorf("%w: app %q already exists", store.ErrConflict, in.Slug)
			}
			uid, err := store.AllocateUID(ctx, q, rng, c.HostIDs, app.ID, app.Slug)
			if err != nil {
				return err
			}
			app.UID, app.GID = uid, uid
			if err := store.InsertApp(ctx, q, app); err != nil {
				return err
			}
			if err := store.ReplaceDomains(ctx, q, "app", app.ID, links); err != nil {
				return err
			}
			var berrs domain.ValidationErrors
			for i, br := range in.Bindings {
				b, ok := c.newBinding(ctx, q, app, br, fmt.Sprintf("bindings[%d]", i), &berrs)
				if !ok {
					continue
				}
				if err := store.InsertBinding(ctx, q, b); err != nil {
					return err
				}
				for _, name := range b.Databases {
					if err := store.AddBindingDatabase(ctx, q, b.ID, name); err != nil {
						return err
					}
				}
			}
			return berrs.Err()
		},
	})
	if err != nil {
		return domain.App{}, op, err
	}
	if existed {
		app, err = store.GetApp(ctx, c.Store.DB(), op.TargetID)
		return app, op, err
	}
	app, err = store.GetApp(ctx, c.Store.DB(), app.ID)
	return app, op, err
}

type UpdateAppInput struct {
	ExpectedGeneration int64
	Runtime            *domain.Runtime
	Resources          *domain.Resources
	Ingress            *domain.IngressMode
	Domains            *[]string
	Route              *domain.Route
	Env                *[]domain.EnvVar
}

// UpdateApp persists a configuration change and queues its scoped
// application. The runtime kind is immutable per incarnation.
func (c *Controller) UpdateApp(ctx context.Context, id string, in UpdateAppInput, idem string) (domain.App, store.Operation, error) {
	app, err := store.GetApp(ctx, c.Store.DB(), id)
	if err != nil {
		return app, store.Operation{}, err
	}
	var errs domain.ValidationErrors
	if in.Runtime != nil {
		if in.Runtime.Kind != app.Runtime.Kind {
			errs.Add("runtime.kind", "cannot change runtime kind of an existing app")
		}
		domain.ValidateRuntime(in.Runtime, &errs)
	}
	if in.Resources != nil {
		domain.ValidateResources(in.Resources, app.Runtime.Kind, &errs)
	}
	if in.Ingress != nil {
		if err := domain.ValidateIngress(*in.Ingress); err != nil {
			errs.Add("ingress", "%s", err)
		}
	}
	if in.Route != nil {
		domain.ValidateRoute(in.Route, "route", &errs)
	}
	if in.Env != nil {
		domain.ValidateEnv(*in.Env, &errs)
	}
	var links []domain.DomainLink
	if in.Domains != nil {
		links = normalizeDomains(*in.Domains, &errs)
	}
	if err := errs.Err(); err != nil {
		return app, store.Operation{}, err
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindAppUpdate, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem, Request: in,
		Mutate: func(ctx context.Context, q store.Q) error {
			cur, err := store.GetApp(ctx, q, app.ID)
			if err != nil {
				return err
			}
			if in.ExpectedGeneration != 0 && in.ExpectedGeneration != cur.ConfigGeneration {
				return fmt.Errorf("%w: app configuration generation is %d, not %d", ErrPrecondition, cur.ConfigGeneration, in.ExpectedGeneration)
			}
			if in.Runtime != nil {
				env := cur.Runtime.Env
				cur.Runtime = *in.Runtime
				cur.Runtime.Env = env
			}
			if in.Env != nil {
				cur.Runtime.Env = *in.Env
			}
			if in.Resources != nil {
				cur.Resources = *in.Resources
			}
			if in.Route != nil {
				cur.Route = *in.Route
			}
			if in.Ingress != nil {
				cur.Ingress = *in.Ingress
				if cur.Ingress != domain.IngressManaged {
					cur.Publication = domain.Unpublished
				}
			}
			cur.ConfigGeneration++
			if err := store.UpdateApp(ctx, q, cur); err != nil {
				return err
			}
			if in.Domains != nil {
				return store.ReplaceDomains(ctx, q, "app", cur.ID, links)
			}
			return nil
		},
	})
	if err != nil {
		return app, op, err
	}
	app, err = store.GetApp(ctx, c.Store.DB(), app.ID)
	return app, op, err
}

// setIntent is a helper for simple lifecycle submissions.
func (c *Controller) lifecycle(ctx context.Context, id, kind, idem string, mutate func(a *domain.App) error) (store.Operation, error) {
	app, err := store.GetApp(ctx, c.Store.DB(), id)
	if err != nil {
		return store.Operation{}, err
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: kind, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem, Generation: app.ConfigGeneration,
		Request: map[string]any{"slug": app.Slug},
		Mutate: func(ctx context.Context, q store.Q) error {
			cur, err := store.GetApp(ctx, q, app.ID)
			if err != nil {
				return err
			}
			if mutate == nil {
				return nil
			}
			if err := mutate(&cur); err != nil {
				return err
			}
			return store.UpdateApp(ctx, q, cur)
		},
	})
	return op, err
}

func (c *Controller) StartApp(ctx context.Context, id, idem string) (store.Operation, error) {
	return c.lifecycle(ctx, id, KindAppStart, idem, func(a *domain.App) error {
		a.DesiredRuntime = domain.DesiredRunning
		return nil
	})
}

// StopApp persists stopped and unpublished intent before any effect.
func (c *Controller) StopApp(ctx context.Context, id, idem string) (store.Operation, error) {
	return c.lifecycle(ctx, id, KindAppStop, idem, func(a *domain.App) error {
		a.DesiredRuntime = domain.DesiredStopped
		a.Publication = domain.Unpublished
		return nil
	})
}

func (c *Controller) RestartApp(ctx context.Context, id, idem string) (store.Operation, error) {
	return c.lifecycle(ctx, id, KindAppRestart, idem, func(a *domain.App) error {
		if a.DesiredRuntime != domain.DesiredRunning {
			return fmt.Errorf("%w: app is stopped; start it instead", ErrPrecondition)
		}
		return nil
	})
}

func (c *Controller) PublishApp(ctx context.Context, id, idem string) (store.Operation, error) {
	return c.lifecycle(ctx, id, KindAppPublish, idem, func(a *domain.App) error {
		if a.Ingress != domain.IngressManaged {
			return fmt.Errorf("%w: publication applies only to managed ingress; this app uses %s ingress, whose routes are operator-owned", ErrPrecondition, a.Ingress)
		}
		if a.DesiredRuntime != domain.DesiredRunning {
			return fmt.Errorf("%w: start the app first; publish never starts an app", ErrPrecondition)
		}
		return nil
	})
}

func (c *Controller) UnpublishApp(ctx context.Context, id, idem string) (store.Operation, error) {
	return c.lifecycle(ctx, id, KindAppUnpublish, idem, func(a *domain.App) error {
		if a.Ingress != domain.IngressManaged {
			return fmt.Errorf("%w: this app's routes are operator-owned (%s ingress); remove them in the external router", ErrPrecondition, a.Ingress)
		}
		a.Publication = domain.Unpublished
		return nil
	})
}

// RemoveApp requires the exact confirmation "delete <slug>".
func (c *Controller) RemoveApp(ctx context.Context, id, confirm, idem string) (store.Operation, error) {
	app, err := store.GetApp(ctx, c.Store.DB(), id)
	if err != nil {
		return store.Operation{}, err
	}
	if confirm != "delete "+app.Slug {
		return store.Operation{}, fmt.Errorf("%w: type exactly %q to remove this app (durable data is retained)", ErrConfirmation, "delete "+app.Slug)
	}
	return c.lifecycle(ctx, id, KindAppRemove, idem, func(a *domain.App) error {
		a.DesiredRuntime = domain.DesiredStopped
		a.Publication = domain.Unpublished
		return nil
	})
}

// PruneRetired permanently deletes retained data of a removed incarnation.
// It requires the literal confirmation "delete" and never reclaims the UID.
func (c *Controller) PruneRetired(ctx context.Context, appID, confirm, idem string) (store.Operation, error) {
	ret, err := store.GetRetired(ctx, c.Store.DB(), appID)
	if err != nil {
		return store.Operation{}, err
	}
	if ret.PrunedAt != "" {
		return store.Operation{}, fmt.Errorf("%w: already pruned", store.ErrConflict)
	}
	if confirm != "delete" {
		return store.Operation{}, fmt.Errorf("%w: type exactly \"delete\" to permanently delete the retained data listed in the prune plan", ErrConfirmation)
	}
	op, _, err := c.Submit(ctx, Submission{Kind: KindAppPrune, TargetKind: "retired-app", TargetID: ret.AppID, IdempotencyKey: idem,
		Request: map[string]any{"slug": ret.Slug}})
	return op, err
}

// AddBinding appends an add-only data binding.
func (c *Controller) AddBinding(ctx context.Context, id string, req BindingRequest, idem string) (store.Operation, error) {
	app, err := store.GetApp(ctx, c.Store.DB(), id)
	if err != nil {
		return store.Operation{}, err
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindBindingAdd, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem, Request: req,
		Mutate: func(ctx context.Context, q store.Q) error {
			cur, err := store.GetApp(ctx, q, app.ID)
			if err != nil {
				return err
			}
			var errs domain.ValidationErrors
			b, ok := c.newBinding(ctx, q, cur, req, "binding", &errs)
			if !ok {
				return errs.Err()
			}
			for _, existing := range cur.Bindings {
				if existing.Engine == b.Engine && b.Service != "" && existing.Service == b.Service {
					return fmt.Errorf("%w: the app already has a binding to %s; add a database to it instead", store.ErrConflict, b.Service)
				}
			}
			if err := store.InsertBinding(ctx, q, b); err != nil {
				return err
			}
			for _, name := range b.Databases {
				if err := store.AddBindingDatabase(ctx, q, b.ID, name); err != nil {
					return err
				}
			}
			cur.CredentialsGeneration++
			cur.ConfigGeneration++
			return store.UpdateApp(ctx, q, cur)
		},
	})
	return op, err
}

// AddDatabase adds an app-namespaced database to a relational binding.
func (c *Controller) AddDatabase(ctx context.Context, id, bindingID, name, idem string) (store.Operation, error) {
	app, err := store.GetApp(ctx, c.Store.DB(), id)
	if err != nil {
		return store.Operation{}, err
	}
	full := dbIdent(app.Slug) + "_" + name
	if err := domain.ValidateDatabaseName(full); err != nil {
		return store.Operation{}, domain.ValidationErrors{{Field: "name", Message: err.Error()}}
	}
	var binding *domain.Binding
	for i := range app.Bindings {
		if app.Bindings[i].ID == bindingID {
			binding = &app.Bindings[i]
		}
	}
	if binding == nil || binding.Engine == domain.EngineSQLite {
		return store.Operation{}, fmt.Errorf("%w: relational binding %s", store.ErrNotFound, bindingID)
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindDatabaseAdd, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem,
		Request: map[string]any{"binding": bindingID, "database": full},
		Mutate: func(ctx context.Context, q store.Q) error {
			return store.AddBindingDatabase(ctx, q, bindingID, full)
		},
	})
	return op, err
}

// CreateService adds a managed MySQL/PostgreSQL version (add-only).
func (c *Controller) CreateService(ctx context.Context, engine domain.Engine, version, idem string) (domain.DataService, store.Operation, error) {
	var image string
	var ok bool
	switch engine {
	case domain.EngineMySQL:
		image, ok = domain.MySQLVersions[version]
	case domain.EnginePostgres:
		image, ok = domain.PostgresVersions[version]
	case domain.EngineRedis:
		image, ok, version = domain.RedisImage, true, domain.RedisVersion
	}
	if !ok {
		return domain.DataService{}, store.Operation{}, domain.ValidationErrors{{Field: "version", Message: "unsupported engine or version"}}
	}
	name := string(engine) + strings.ReplaceAll(version, ".", "")
	if engine == domain.EngineRedis {
		name = "redis"
	}
	svc := domain.DataService{Name: name, Engine: engine, Version: version, Image: image, Volume: c.Names.ServiceVolume(name), CreatedAt: time.Now().UTC()}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindServiceCreate, TargetKind: "service", TargetID: name, IdempotencyKey: idem, Request: svc,
		Mutate: func(ctx context.Context, q store.Q) error { return store.InsertService(ctx, q, svc) },
	})
	return svc, op, err
}

// ConfigureEdge validates and persists edge settings, then applies them.
func (c *Controller) ConfigureEdge(ctx context.Context, s domain.EdgeSettings, idem string) (store.Operation, error) {
	var errs domain.ValidationErrors
	if s.HTTPPort < 1 || s.HTTPPort > 65535 {
		errs.Add("httpPort", "must be a port")
	}
	if s.HTTPSPort < 1 || s.HTTPSPort > 65535 || s.HTTPSPort == s.HTTPPort {
		errs.Add("httpsPort", "must be a port different from httpPort")
	}
	if s.Bind == "" {
		s.Bind = "0.0.0.0"
	}
	if _, err := parseEdgeBind(s.Bind); err != nil {
		errs.Add("bind", "must be an IPv4 address")
	}
	if s.ACMEURL == "" {
		s.ACMEURL = domain.DefaultEdgeSettings().ACMEURL
	}
	if !strings.HasPrefix(s.ACMEURL, "https://") && !strings.HasPrefix(s.ACMEURL, "http://") {
		errs.Add("acmeUrl", "must be an http(s) URL")
	}
	if strings.ContainsAny(s.ACMEEmail, " \"'\n;{}") {
		errs.Add("acmeEmail", "invalid email")
	}
	if err := errs.Err(); err != nil {
		return store.Operation{}, err
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindEdgeApply, TargetKind: "edge", TargetID: "edge", IdempotencyKey: idem, Request: s,
		Mutate: func(ctx context.Context, q store.Q) error { return store.PutSetting(ctx, q, edgeSettingKey, s) },
	})
	return op, err
}

// parseEdgeBind accepts only a canonical dotted-quad IPv4 address (no
// trailing garbage, no leading zeros, no zone, no IPv6).
func parseEdgeBind(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, err
	}
	if !a.Is4() {
		return netip.Addr{}, fmt.Errorf("%q is not an IPv4 address", s)
	}
	return a, nil
}

// SetTunnelToken stores a token in a private file (never state, API output,
// argv, or env) and recreates only the tunnel container. An empty token
// disables the tunnel.
func (c *Controller) SetTunnelToken(ctx context.Context, token, idem string) (store.Operation, error) {
	token = strings.TrimSpace(token)
	enabled := token != ""
	if enabled && (len(token) < 32 || len(token) > 4096 || strings.ContainsAny(token, " \n\r\t\"'")) {
		return store.Operation{}, domain.ValidationErrors{{Field: "token", Message: "does not look like a Cloudflare tunnel token"}}
	}
	if err := platform.EnsureDir(c.Layout.TunnelDir(), 0o750, platform.Owner{UID: 0, GID: TunnelUID}); err != nil {
		return store.Operation{}, err
	}
	path := c.Layout.TunnelDir() + "/token"
	if enabled {
		if err := platform.AtomicWrite(path, []byte(token), 0o440, platform.Owner{UID: 0, GID: TunnelUID}); err != nil {
			return store.Operation{}, err
		}
	} else {
		_ = os.Remove(path)
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindTunnelApply, TargetKind: "tunnel", TargetID: "tunnel", IdempotencyKey: idem,
		Request: map[string]any{"enabled": enabled},
		Mutate: func(ctx context.Context, q store.Q) error {
			var s domain.TunnelSettings
			if _, err := store.GetSetting(ctx, q, tunnelSettingKey, &s); err != nil {
				return err
			}
			s.Enabled = enabled
			s.TokenGeneration++
			return store.PutSetting(ctx, q, tunnelSettingKey, s)
		},
	})
	return op, err
}

type ProxyInput struct {
	Name      string
	Upstreams []string
	Domains   []string
	Route     domain.Route
	Enabled   bool
}

// UpsertProxy manages an edge reverse-proxy route to an external upstream.
func (c *Controller) UpsertProxy(ctx context.Context, in ProxyInput, idem string) (domain.Proxy, store.Operation, error) {
	var errs domain.ValidationErrors
	if err := domain.ValidateSlug(in.Name); err != nil {
		errs.Add("name", "%s", err)
	}
	if len(in.Upstreams) == 0 || len(in.Upstreams) > 16 {
		errs.Add("upstreams", "1-16 upstream URLs required")
	}
	for i, u := range in.Upstreams {
		if err := domain.ValidateUpstream(u); err != nil {
			errs.Add(fmt.Sprintf("upstreams[%d]", i), "%s", err)
		}
	}
	domain.ValidateRoute(&in.Route, "route", &errs)
	links := normalizeDomains(in.Domains, &errs)
	if len(links) == 0 {
		errs.Add("domains", "at least one domain is required")
	}
	if err := errs.Err(); err != nil {
		return domain.Proxy{}, store.Operation{}, err
	}
	p, err := store.GetProxy(ctx, c.Store.DB(), in.Name)
	if errors.Is(err, store.ErrNotFound) {
		p = domain.Proxy{ID: "p" + platform.RandomHex(6), Name: in.Name, CreatedAt: time.Now().UTC()}
	} else if err != nil {
		return p, store.Operation{}, err
	}
	p.Upstreams, p.Route, p.Enabled = in.Upstreams, in.Route, in.Enabled
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindEdgeApply, TargetKind: "proxy", TargetID: p.ID, IdempotencyKey: idem, Request: in,
		Mutate: func(ctx context.Context, q store.Q) error {
			if err := store.UpsertProxy(ctx, q, p); err != nil {
				return err
			}
			return store.ReplaceDomains(ctx, q, "proxy", p.ID, links)
		},
	})
	if err != nil {
		return p, op, err
	}
	p, err = store.GetProxy(ctx, c.Store.DB(), p.ID)
	return p, op, err
}

func (c *Controller) DeleteProxy(ctx context.Context, name, confirm, idem string) (store.Operation, error) {
	p, err := store.GetProxy(ctx, c.Store.DB(), name)
	if err != nil {
		return store.Operation{}, err
	}
	if confirm != "delete "+p.Name {
		return store.Operation{}, fmt.Errorf("%w: type exactly %q", ErrConfirmation, "delete "+p.Name)
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindEdgeApply, TargetKind: "proxy", TargetID: p.ID, IdempotencyKey: idem, Request: map[string]any{"delete": p.Name},
		Mutate: func(ctx context.Context, q store.Q) error { return store.DeleteProxy(ctx, q, p.ID) },
	})
	return op, err
}

func (c *Controller) RepairPermissions(ctx context.Context, id, mode, idem string) (store.Operation, error) {
	switch mode {
	case "check", "dry-run", "shallow", "recursive":
	default:
		return store.Operation{}, domain.ValidationErrors{{Field: "mode", Message: "must be check, dry-run, shallow, or recursive"}}
	}
	app, err := store.GetApp(ctx, c.Store.DB(), id)
	if err != nil {
		return store.Operation{}, err
	}
	op, _, err := c.Submit(ctx, Submission{Kind: KindPermissions, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem, Request: map[string]any{"mode": mode}})
	return op, err
}
