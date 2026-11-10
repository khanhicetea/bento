package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/remoteaccess"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// KindServiceRemote turns a data service's remote access over Tailcat on or
// off, or rotates its address.
const KindServiceRemote = "service.remote"

// KindBindingLogin records that a binding's login was revealed. It is never
// executed: the record is written as already succeeded, so the journal shows
// who asked, and never holds the password.
const KindBindingLogin = "binding.login"

// Remote access actions.
const (
	RemoteOn     = "on"
	RemoteOff    = "off"
	RemoteRotate = "rotate"
)

// remoteSettleTimeout bounds how long an operation waits for the Tailcat
// server to report its address; after it the server keeps trying in the
// background.
const remoteSettleTimeout = 75 * time.Second

// EnginePort is the port a MySQL or PostgreSQL service listens on.
func EnginePort(e domain.Engine) int {
	switch e {
	case domain.EngineMySQL:
		return runtime.MySQLPort
	case domain.EnginePostgres:
		return runtime.PostgresPort
	}
	return 0
}

// SetServiceRemote validates and persists a remote-access change, then
// applies it. A rotate requires remote access to be on.
func (c *Controller) SetServiceRemote(ctx context.Context, name, action, idem string) (store.Operation, error) {
	if action != RemoteOn && action != RemoteOff && action != RemoteRotate {
		var errs domain.ValidationErrors
		errs.Add("action", "must be on, off or rotate")
		return store.Operation{}, errs
	}
	svc, err := store.GetService(ctx, c.Store.DB(), name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Operation{}, fmt.Errorf("%w: service %s", store.ErrNotFound, name)
	} else if err != nil {
		return store.Operation{}, err
	}
	if EnginePort(svc.Engine) == 0 {
		return store.Operation{}, fmt.Errorf("%w: remote access supports MySQL and PostgreSQL services only", ErrPrecondition)
	}
	if action == RemoteRotate && !svc.Remote.Enabled {
		return store.Operation{}, fmt.Errorf("%w: remote access of %s is off; turn it on first", ErrPrecondition, name)
	}
	op, _, err := c.Submit(ctx, Submission{
		Kind: KindServiceRemote, TargetKind: "service", TargetID: name, IdempotencyKey: idem,
		Request: map[string]any{"action": action},
		Mutate: func(ctx context.Context, q store.Q) error {
			_, err := store.SetServiceRemote(ctx, q, name, action != RemoteOff, action == RemoteRotate)
			return err
		},
	})
	return op, err
}

func (c *Controller) handleServiceRemote(ctx context.Context, r *Run) (any, error) {
	var req struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(r.Op.Request, &req)
	svc, err := store.GetService(ctx, c.Store.DB(), r.Op.TargetID)
	if err != nil {
		return nil, err
	}
	// The latest intent decides: a later operation may already have changed it.
	if !svc.Remote.Enabled {
		if err := r.Phase(ctx, "stop-remote"); err != nil {
			return nil, err
		}
		c.Remote.Stop(svc.Name)
		return map[string]any{"enabled": false}, nil
	}
	if req.Action == RemoteRotate {
		// Cut every tunnel of the old address before its key is replaced.
		if err := r.Phase(ctx, "stop-remote"); err != nil {
			return nil, err
		}
		c.Remote.Stop(svc.Name)
	}
	if err := r.Phase(ctx, "remote-key"); err != nil {
		return nil, err
	}
	if _, created, err := remoteaccess.EnsureKey(c.Layout, svc.Name, svc.Remote.Generation, true); err != nil {
		return nil, Fail("remote-key-invalid",
			"Inspect tailcat/"+svc.Name+".key under the stack root; Bento never overwrites a key it cannot read.",
			"%v", err)
	} else if created {
		r.Info(ctx, "new Tailcat key for %s (generation %d); its address is new", svc.Name, svc.Remote.Generation)
	}
	if err := r.Phase(ctx, "start-remote"); err != nil {
		return nil, err
	}
	if err := c.Remote.Start(c.remoteTarget(svc.DataService)); err != nil {
		return nil, Fail("remote-access-failed", "Fix the cause, then turn remote access on again.", "%v", err)
	}
	wctx, cancel := context.WithTimeout(ctx, remoteSettleTimeout)
	defer cancel()
	st := c.Remote.WaitSettled(wctx, svc.Name)
	switch st.State {
	case remoteaccess.StateFailed:
		return nil, Fail("remote-access-failed",
			"Bento keeps retrying in the background. The host needs outbound HTTPS and UDP to the Tailcat relays.",
			"%s", st.Message)
	case remoteaccess.StateListening:
		r.Info(ctx, "remote access of %s is listening", svc.Name)
	default:
		r.Warn(ctx, "the Tailcat server of %s has not reported its address yet; it keeps trying", svc.Name)
	}
	// The address is a credential: it is shown by GET /services only.
	return map[string]any{"enabled": true, "state": string(st.State)}, nil
}

func (c *Controller) remoteTarget(s domain.DataService) remoteaccess.Target {
	return remoteaccess.Target{Service: s.Name, Port: EnginePort(s.Engine), Generation: s.Remote.Generation}
}

// StartRemoteAccess starts the Tailcat servers of every service with remote
// access on (backend start). A missing key is not replaced; the service
// shows failed until its address is rotated.
func (c *Controller) StartRemoteAccess(ctx context.Context) {
	rows, err := store.ListServices(ctx, c.Store.DB())
	if err != nil {
		c.Log.Warn("remote access: list services", "err", err)
		return
	}
	for _, row := range rows {
		if !row.Remote.Enabled || EnginePort(row.Engine) == 0 {
			continue
		}
		if err := c.Remote.Start(c.remoteTarget(row.DataService)); err != nil {
			c.Log.Warn("remote access not started", "service", row.Name, "err", err)
		}
	}
}

// ServiceEndpoint returns host:port of a running, Bento-owned data service on
// the data network (the host reaches the bridge). Remote access proxies each
// tunnel stream to it.
func (c *Controller) ServiceEndpoint(ctx context.Context, name string) (string, error) {
	svc, err := store.GetService(ctx, c.Store.DB(), name)
	if err != nil {
		return "", err
	}
	if !svc.Remote.Enabled {
		return "", errors.New("remote access is off")
	}
	port := EnginePort(svc.Engine)
	ins, err := c.Engine.Inspect(ctx, c.Names.ServiceContainer(name))
	if err != nil {
		return "", err
	}
	if ins == nil || ins.Config == nil || !c.Names.OwnedBy(ins.Config.Labels, runtime.RoleDatabase, "") ||
		ins.Config.Labels[runtime.LabelService] != name {
		return "", fmt.Errorf("service %s has no container of this stack", name)
	}
	if ins.State == nil || !ins.State.Running {
		return "", fmt.Errorf("service %s is not running", name)
	}
	if ins.NetworkSettings != nil {
		if ep, ok := ins.NetworkSettings.Networks[c.Names.DataNetwork()]; ok && ep != nil && ep.IPAddress.IsValid() {
			return net.JoinHostPort(ep.IPAddress.String(), strconv.Itoa(port)), nil
		}
	}
	return "", fmt.Errorf("service %s has no data network address", name)
}

// RecordLoginReveal journals that a binding's login was shown, as an
// operation that is already finished. Only who asked and which binding are
// recorded, never the password.
func (c *Controller) RecordLoginReveal(ctx context.Context, app domain.App, b domain.Binding, by string) error {
	origin, _ := ctx.Value(originKey{}).(string)
	req, _ := json.Marshal(map[string]any{"binding": b.ID, "service": b.Service, "username": b.Username})
	return c.Store.Tx(ctx, func(q store.Q) error {
		op, _, err := store.InsertOperation(ctx, q, store.Operation{
			ID: platform.NewOperationID(), Kind: KindBindingLogin, TargetKind: "app", TargetID: app.ID,
			Request: req, Origin: origin,
		})
		if err != nil {
			return err
		}
		if err := store.AppendEvent(ctx, q, op.ID, "info",
			fmt.Sprintf("login of binding %s (%s on %s) revealed to a %s caller", b.ID, b.Username, b.Service, by)); err != nil {
			return err
		}
		return store.FinishOperation(ctx, q, op.ID, store.OpSucceeded, json.RawMessage(`{"revealed":true}`), "", "", "")
	})
}
