package operations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// EnableWebhook creates the app's deploy webhook, or rotates its secret while
// keeping the URL. The returned secret is the only time it is disclosed.
func (c *Controller) EnableWebhook(ctx context.Context, id string) (domain.Webhook, error) {
	var out domain.Webhook
	err := c.Store.Tx(ctx, func(q store.Q) error {
		app, err := store.GetApp(ctx, q, id)
		if err != nil {
			return err
		}
		if _, ok, err := store.GetGitSource(ctx, q, app.ID); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: configure a git source for %s first", ErrPrecondition, app.Slug)
		}
		w, ok, err := store.GetWebhook(ctx, q, app.ID)
		if err != nil {
			return err
		}
		if !ok || w.HookID == "" {
			w = domain.Webhook{HookID: platform.RandomHex(16)}
		}
		w.Secret, w.SecretCreatedAt = platform.RandomHex(32), time.Now().UTC()
		out = w
		return store.PutWebhook(ctx, q, app.ID, w)
	})
	return out, err
}

// DisableWebhook destroys the webhook; its URL stops working immediately.
func (c *Controller) DisableWebhook(ctx context.Context, id string) error {
	return c.Store.Tx(ctx, func(q store.Q) error {
		app, err := store.GetApp(ctx, q, id)
		if err != nil {
			return err
		}
		if _, ok, err := store.GetWebhook(ctx, q, app.ID); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: app %s has no webhook", store.ErrNotFound, app.Slug)
		}
		return store.DeleteWebhook(ctx, q, app.ID)
	})
}

// WebhookOutcome is the HTTP answer to a delivery.
type WebhookOutcome struct {
	Status      int    `json:"-"`
	Result      string `json:"result"`
	Detail      string `json:"detail,omitempty"`
	OperationID string `json:"operationId,omitempty"`
}

// webhookMu serializes deliveries so coalescing sees queued deploys.
var webhookMu sync.Mutex

// HandleWebhook authenticates a delivery for hookID and, for a push to the
// configured branch, submits a deploy. It performs no effects: like any
// handler it only records intent. An unknown hook and a bad credential are
// indistinguishable to the caller and are not recorded.
func (c *Controller) HandleWebhook(ctx context.Context, hookID string, h domain.Headers, body []byte) (WebhookOutcome, error) {
	denied := WebhookOutcome{Status: http.StatusNotFound, Result: "not-found"}
	appID, w, err := store.FindWebhook(ctx, c.Store.DB(), hookID)
	if errors.Is(err, store.ErrNotFound) {
		return denied, nil
	}
	if err != nil {
		return WebhookOutcome{}, err
	}
	if !domain.VerifyWebhook(h, body, w.Secret) {
		return denied, nil
	}
	app, err := store.GetApp(ctx, c.Store.DB(), appID)
	if errors.Is(err, store.ErrNotFound) {
		return denied, nil
	}
	if err != nil {
		return WebhookOutcome{}, err
	}
	g, ok, err := store.GetGitSource(ctx, c.Store.DB(), app.ID)
	if err != nil {
		return WebhookOutcome{}, err
	}
	if !ok {
		return denied, nil
	}

	webhookMu.Lock()
	defer webhookMu.Unlock()
	ev := domain.ParseWebhook(h, body, g.Branch)
	d := domain.WebhookDelivery{At: time.Now().UTC(), Provider: ev.Provider, Event: ev.Event, DeliveryID: ev.DeliveryID, Ref: ev.Ref, Commit: ev.Commit}
	out := WebhookOutcome{Status: http.StatusAccepted}
	switch {
	case ev.Kind == domain.WebhookPing:
		out.Status, out.Result = http.StatusOK, "ping"
	case ev.Kind != domain.WebhookPush:
		out.Result, out.Detail = "ignored-event", "only push events deploy"
	case ev.Provider != domain.ProviderGeneric && ev.Branch != g.Branch:
		out.Result, out.Detail = "ignored-ref", fmt.Sprintf("configured branch is %s", g.Branch)
	case ev.Deleted:
		out.Result, out.Detail = "ignored-ref", "branch deleted"
	case !app.Provisioned:
		out.Status, out.Result, out.Detail = http.StatusConflict, "refused", "app is not provisioned yet"
	default:
		op, result, err := c.submitWebhookDeploy(ctx, app, ev)
		if err != nil {
			var oe *OpError
			if !errors.As(err, &oe) {
				return WebhookOutcome{}, err
			}
			out.Status, out.Result, out.Detail = http.StatusServiceUnavailable, "refused", oe.Message
			break
		}
		out.Result, out.OperationID = result, op.ID
		switch result {
		case "coalesced":
			out.Detail = "a deploy is already queued; it will fetch the branch head"
		case "duplicate":
			out.Detail = "this delivery was already accepted"
		}
	}
	d.Result, d.Detail, d.OperationID = out.Result, out.Detail, out.OperationID
	err = c.Store.Tx(ctx, func(q store.Q) error {
		cur, ok, err := store.GetWebhook(ctx, q, app.ID)
		if err != nil || !ok || cur.HookID != hookID {
			return err // disabled or replaced meanwhile
		}
		cur.Record(d)
		return store.PutWebhook(ctx, q, app.ID, cur)
	})
	if err != nil {
		c.Log.Warn("record webhook delivery", "app", app.Slug, "err", err)
	}
	return out, nil
}

// submitWebhookDeploy submits a deploy, or returns the already queued one:
// every deploy fetches the branch head, so a queued deploy covers this push.
// A provider redelivery maps to its original operation.
func (c *Controller) submitWebhookDeploy(ctx context.Context, app domain.App, ev domain.WebhookEvent) (store.Operation, string, error) {
	queued, err := store.ListOperations(ctx, c.Store.DB(), store.OpFilter{TargetID: app.ID, States: []store.OpState{store.OpQueued}})
	if err != nil {
		return store.Operation{}, "", err
	}
	for _, o := range queued {
		if o.Kind == KindAppDeploy {
			return o, "coalesced", nil
		}
	}
	idem := ""
	if ev.DeliveryID != "" {
		idem = "webhook:" + app.ID + ":" + ev.Provider + ":" + ev.DeliveryID
	}
	op, existed, err := c.Submit(ctx, Submission{Kind: KindAppDeploy, TargetKind: "app", TargetID: app.ID, IdempotencyKey: idem,
		Origin: DeployTriggerWebhook, Request: DeployRequest{
			Trigger: DeployTriggerWebhook, Provider: ev.Provider, Event: ev.Event, DeliveryID: ev.DeliveryID,
			Ref: ev.Ref, Commit: ev.Commit, Pusher: ev.Pusher,
		}})
	if existed {
		return op, "duplicate", err
	}
	return op, "deployed", err
}
