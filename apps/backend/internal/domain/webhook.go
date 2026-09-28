package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// WebhookPathPrefix is the edge path reserved on every managed route for
// inbound webhooks; the edge forwards it to the backend's hooks socket.
const WebhookPathPrefix = "/_webhook/"

// WebhookDeployPath is the deploy webhook path for a hook id.
func WebhookDeployPath(hookID string) string { return WebhookPathPrefix + "deploy/" + hookID }

// MaxWebhookDeliveries bounds the recorded delivery history per app.
const MaxWebhookDeliveries = 20

// Webhook is an app's deploy webhook. HookID only routes a request and is not
// a credential (it appears in URLs and access logs); every request must prove
// knowledge of Secret. Secret never leaves the backend except in the response
// to the mutation that generated it.
type Webhook struct {
	HookID          string            `json:"hookId"`
	Secret          string            `json:"secret"`
	SecretCreatedAt time.Time         `json:"secretCreatedAt"`
	Deliveries      []WebhookDelivery `json:"deliveries,omitempty"`
}

// WebhookDelivery is one authenticated delivery and what Bento did with it.
type WebhookDelivery struct {
	At          time.Time `json:"at"`
	Provider    string    `json:"provider"`
	Event       string    `json:"event"`
	DeliveryID  string    `json:"deliveryId,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	Commit      string    `json:"commit,omitempty"`
	Result      string    `json:"result"` // deployed, coalesced, ignored-ref, ignored-event, ping, refused
	Detail      string    `json:"detail,omitempty"`
	OperationID string    `json:"operationId,omitempty"`
}

// Record prepends a delivery and trims the history.
func (w *Webhook) Record(d WebhookDelivery) {
	w.Deliveries = append([]WebhookDelivery{d}, w.Deliveries...)
	if len(w.Deliveries) > MaxWebhookDeliveries {
		w.Deliveries = w.Deliveries[:MaxWebhookDeliveries]
	}
}

// Headers reads request headers case-insensitively (http.Header satisfies it).
type Headers interface{ Get(key string) string }

// Webhook providers.
const (
	ProviderGitHub    = "github"
	ProviderGitea     = "gitea"
	ProviderForgejo   = "forgejo"
	ProviderGitLab    = "gitlab"
	ProviderBitbucket = "bitbucket"
	ProviderGeneric   = "generic"
)

// VerifyWebhook authenticates a request against secret using the first
// credential header present: an HMAC-SHA256 body signature (GitHub, Gitea,
// Forgejo, Bitbucket), a shared token (GitLab), or a bearer token (generic).
// The secret is never accepted in the URL.
func VerifyWebhook(h Headers, body []byte, secret string) bool {
	if secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	sig := func(v string) bool {
		got, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(v), "sha256="))
		return err == nil && hmac.Equal(got, want)
	}
	token := func(v string) bool {
		return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(v)), []byte(secret)) == 1
	}
	switch {
	case h.Get("X-Hub-Signature-256") != "":
		return sig(h.Get("X-Hub-Signature-256"))
	case h.Get("X-Forgejo-Signature") != "":
		return sig(h.Get("X-Forgejo-Signature"))
	case h.Get("X-Gitea-Signature") != "":
		return sig(h.Get("X-Gitea-Signature"))
	case strings.HasPrefix(h.Get("X-Hub-Signature"), "sha256="):
		return sig(h.Get("X-Hub-Signature"))
	case h.Get("X-Gitlab-Token") != "":
		return token(h.Get("X-Gitlab-Token"))
	case strings.HasPrefix(h.Get("Authorization"), "Bearer "):
		return token(strings.TrimPrefix(h.Get("Authorization"), "Bearer "))
	}
	return false
}

// WebhookEventKind classifies a delivery.
type WebhookEventKind int

const (
	WebhookOther WebhookEventKind = iota
	WebhookPing
	WebhookPush
)

// WebhookEvent is the provider-neutral part of a delivery that Bento uses.
// The payload never chooses what is fetched: a push only triggers a deploy of
// the configured branch head.
type WebhookEvent struct {
	Provider   string
	Kind       WebhookEventKind
	Event      string
	DeliveryID string
	// Branch is the pushed branch; empty for tags or when unknown.
	Branch string
	Ref    string
	// Commit is the pushed head as reported by the provider (informational).
	Commit  string
	Deleted bool
	Pusher  string
}

var (
	safeToken    = regexp.MustCompile(`^[A-Za-z0-9._:@/+-]{1,200}$`)
	commitHexRef = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
)

// clean keeps provider-supplied strings safe to log and pass as env.
func clean(s string) string {
	s = strings.TrimSpace(s)
	if !safeToken.MatchString(s) {
		return ""
	}
	return s
}

func cleanCommit(s string) string {
	if commitHexRef.MatchString(s) {
		return s
	}
	return ""
}

func branchOf(ref string) string {
	if b, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return b
	}
	return ""
}

// ParseWebhook identifies the provider from its event headers and extracts
// the push details. A request without provider headers is a generic trigger
// (for CI or curl) and deploys regardless of its body. branch is the
// configured branch; it selects the relevant change when a provider batches
// several refs in one delivery.
func ParseWebhook(h Headers, body []byte, branch string) WebhookEvent {
	ev := WebhookEvent{Provider: ProviderGeneric, Kind: WebhookPush, Event: "trigger"}
	switch {
	case h.Get("X-Forgejo-Event") != "":
		ev.Provider, ev.Event, ev.DeliveryID = ProviderForgejo, h.Get("X-Forgejo-Event"), h.Get("X-Forgejo-Delivery")
	case h.Get("X-Gitea-Event") != "":
		ev.Provider, ev.Event, ev.DeliveryID = ProviderGitea, h.Get("X-Gitea-Event"), h.Get("X-Gitea-Delivery")
	case h.Get("X-GitHub-Event") != "":
		ev.Provider, ev.Event, ev.DeliveryID = ProviderGitHub, h.Get("X-GitHub-Event"), h.Get("X-GitHub-Delivery")
	case h.Get("X-Gitlab-Event") != "":
		ev.Provider, ev.Event, ev.DeliveryID = ProviderGitLab, h.Get("X-Gitlab-Event"), h.Get("X-Gitlab-Event-UUID")
	case h.Get("X-Event-Key") != "":
		ev.Provider, ev.Event, ev.DeliveryID = ProviderBitbucket, h.Get("X-Event-Key"), h.Get("X-Request-UUID")
	default:
		ev.DeliveryID = h.Get("X-Request-Id")
	}
	ev.Event, ev.DeliveryID = clean(ev.Event), clean(ev.DeliveryID)
	if ev.Provider == ProviderGeneric {
		return ev
	}
	ev.Kind = WebhookOther
	switch ev.Provider {
	case ProviderGitHub, ProviderGitea, ProviderForgejo:
		switch ev.Event {
		case "ping":
			ev.Kind = WebhookPing
		case "push":
			var p struct {
				Ref     string `json:"ref"`
				After   string `json:"after"`
				Deleted bool   `json:"deleted"`
				Pusher  struct {
					Name     string `json:"name"`
					Login    string `json:"login"`
					Username string `json:"username"`
				} `json:"pusher"`
			}
			if json.Unmarshal(body, &p) != nil {
				return ev
			}
			ev.Kind, ev.Ref, ev.Commit = WebhookPush, clean(p.Ref), cleanCommit(p.After)
			ev.Deleted = p.Deleted || strings.Trim(p.After, "0") == "" && p.After != ""
			ev.Pusher = clean(firstNonEmpty(p.Pusher.Login, p.Pusher.Username, p.Pusher.Name))
		}
	case ProviderGitLab:
		// GitLab event names contain spaces, which clean() drops.
		ev.Event = strings.ToLower(strings.ReplaceAll(h.Get("X-Gitlab-Event"), " ", "-"))
		if !safeToken.MatchString(ev.Event) {
			ev.Event = ""
		}
		if ev.Event == "push-hook" {
			ev.Event = "push"
			var p struct {
				Ref          string  `json:"ref"`
				After        string  `json:"after"`
				CheckoutSHA  *string `json:"checkout_sha"`
				UserUsername string  `json:"user_username"`
			}
			if json.Unmarshal(body, &p) != nil {
				return ev
			}
			ev.Kind, ev.Ref, ev.Commit = WebhookPush, clean(p.Ref), cleanCommit(p.After)
			ev.Deleted = p.CheckoutSHA == nil || strings.Trim(p.After, "0") == ""
			ev.Pusher = clean(p.UserUsername)
		}
	case ProviderBitbucket:
		switch ev.Event {
		case "diagnostics:ping":
			ev.Kind = WebhookPing
		case "repo:push":
			var p struct {
				Actor struct {
					Nickname string `json:"nickname"`
				} `json:"actor"`
				Push struct {
					Changes []struct {
						New *struct {
							Type   string `json:"type"`
							Name   string `json:"name"`
							Target struct {
								Hash string `json:"hash"`
							} `json:"target"`
						} `json:"new"`
						Old *struct {
							Type string `json:"type"`
							Name string `json:"name"`
						} `json:"old"`
					} `json:"changes"`
				} `json:"push"`
			}
			if json.Unmarshal(body, &p) != nil {
				return ev
			}
			ev.Kind, ev.Pusher = WebhookPush, clean(p.Actor.Nickname)
			// Bitbucket batches changes; the configured branch wins, otherwise
			// the first branch change is reported.
			for _, ch := range p.Push.Changes {
				ref, commit, deleted := "", "", false
				switch {
				case ch.New != nil && ch.New.Type == "branch":
					ref, commit = clean("refs/heads/"+ch.New.Name), cleanCommit(ch.New.Target.Hash)
				case ch.New == nil && ch.Old != nil && ch.Old.Type == "branch":
					ref, deleted = clean("refs/heads/"+ch.Old.Name), true
				default:
					continue
				}
				if ev.Ref == "" || branchOf(ref) == branch {
					ev.Ref, ev.Commit, ev.Deleted = ref, commit, deleted
				}
				if branchOf(ref) == branch {
					break
				}
			}
		}
	}
	ev.Branch = branchOf(ev.Ref)
	return ev
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
