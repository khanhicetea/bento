package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

const hookSecret = "s3cr3t-s3cr3t-s3cr3t"

func sign(body string) string {
	m := hmac.New(sha256.New, []byte(hookSecret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestVerifyWebhookSchemes(t *testing.T) {
	body := `{"ref":"refs/heads/main"}`
	cases := []struct {
		name string
		h    http.Header
		ok   bool
	}{
		{"github", hdr("X-Hub-Signature-256", "sha256="+sign(body)), true},
		{"github wrong", hdr("X-Hub-Signature-256", "sha256="+sign(body+" ")), false},
		{"gitea", hdr("X-Gitea-Signature", sign(body)), true},
		{"forgejo", hdr("X-Forgejo-Signature", sign(body)), true},
		{"bitbucket", hdr("X-Hub-Signature", "sha256="+sign(body)), true},
		{"bitbucket sha1 refused", hdr("X-Hub-Signature", "sha1=abc"), false},
		{"gitlab", hdr("X-Gitlab-Token", hookSecret), true},
		{"gitlab wrong", hdr("X-Gitlab-Token", hookSecret+"x"), false},
		{"bearer", hdr("Authorization", "Bearer "+hookSecret), true},
		{"basic refused", hdr("Authorization", "Basic "+hookSecret), false},
		{"none", hdr(), false},
		// A present-but-wrong signature is not rescued by a valid token.
		{"signature wins", hdr("X-Hub-Signature-256", "sha256=00", "X-Gitlab-Token", hookSecret), false},
	}
	for _, tc := range cases {
		if got := VerifyWebhook(tc.h, []byte(body), hookSecret) != ""; got != tc.ok {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
	if got := VerifyWebhook(hdr("X-Gitlab-Token", hookSecret), []byte(body), hookSecret); got != "X-Gitlab-Token" {
		t.Fatalf("the verifying credential must be reported, got %q", got)
	}
	if VerifyWebhook(hdr("Authorization", "Bearer "), []byte(body), "") != "" {
		t.Fatal("an empty secret must never verify")
	}
}

func TestParseWebhookProviders(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	gh := `{"ref":"refs/heads/main","after":"` + sha + `","pusher":{"name":"kit"}}`
	cases := []struct {
		name     string
		h        http.Header
		body     string
		provider string
		kind     WebhookEventKind
		branch   string
		deleted  bool
	}{
		{"github push", hdr("X-GitHub-Event", "push", "X-GitHub-Delivery", "d-1"), gh, ProviderGitHub, WebhookPush, "main", false},
		{"github ping", hdr("X-GitHub-Event", "ping"), `{}`, ProviderGitHub, WebhookPing, "", false},
		{"github tag", hdr("X-GitHub-Event", "push"), `{"ref":"refs/tags/v1","after":"` + sha + `"}`, ProviderGitHub, WebhookPush, "", false},
		{"github delete", hdr("X-GitHub-Event", "push"), `{"ref":"refs/heads/main","after":"0000000000000000000000000000000000000000","deleted":true}`, ProviderGitHub, WebhookPush, "main", true},
		{"github issue", hdr("X-GitHub-Event", "issues"), `{}`, ProviderGitHub, WebhookOther, "", false},
		// Gitea also sends GitHub-compatible headers; its own header wins.
		{"gitea push", hdr("X-Gitea-Event", "push", "X-GitHub-Event", "push"), gh, ProviderGitea, WebhookPush, "main", false},
		{"forgejo push", hdr("X-Forgejo-Event", "push", "X-Gitea-Event", "push"), gh, ProviderForgejo, WebhookPush, "main", false},
		{"gitlab push", hdr("X-Gitlab-Event", "Push Hook"), `{"ref":"refs/heads/main","after":"` + sha + `","checkout_sha":"` + sha + `","user_username":"kit"}`, ProviderGitLab, WebhookPush, "main", false},
		{"gitlab delete", hdr("X-Gitlab-Event", "Push Hook"), `{"ref":"refs/heads/main","after":"0000000000000000000000000000000000000000","checkout_sha":null}`, ProviderGitLab, WebhookPush, "main", true},
		{"gitlab tag", hdr("X-Gitlab-Event", "Tag Push Hook"), `{}`, ProviderGitLab, WebhookOther, "", false},
		{"bitbucket batched", hdr("X-Event-Key", "repo:push"), `{"push":{"changes":[{"new":{"type":"branch","name":"dev","target":{"hash":"` + sha + `"}}},{"new":{"type":"branch","name":"main","target":{"hash":"` + sha + `"}}}]}}`, ProviderBitbucket, WebhookPush, "main", false},
		{"bitbucket ping", hdr("X-Event-Key", "diagnostics:ping"), `{}`, ProviderBitbucket, WebhookPing, "", false},
		{"generic", hdr("Authorization", "Bearer x"), ``, ProviderGeneric, WebhookPush, "", false},
		{"malformed", hdr("X-GitHub-Event", "push"), `not json`, ProviderGitHub, WebhookOther, "", false},
	}
	for _, tc := range cases {
		ev := ParseWebhook(tc.h, []byte(tc.body), "main")
		if ev.Provider != tc.provider || ev.Kind != tc.kind || ev.Branch != tc.branch || ev.Deleted != tc.deleted {
			t.Errorf("%s: got %+v", tc.name, ev)
		}
	}
	ev := ParseWebhook(hdr("X-GitHub-Event", "push", "X-GitHub-Delivery", "$(touch /tmp/x)"), []byte(`{"ref":"refs/heads/main; rm -rf /","after":"zz","pusher":{"name":"a b"}}`), "main")
	if ev.DeliveryID != "" || ev.Ref != "" || ev.Commit != "" || ev.Pusher != "" {
		t.Fatalf("unsafe provider strings must be dropped: %+v", ev)
	}
}

func TestWebhookRecordKeepsRecentDeliveries(t *testing.T) {
	var w Webhook
	for i := range MaxWebhookDeliveries + 5 {
		w.Record(WebhookDelivery{Result: "deployed", Detail: string(rune('a' + i%26))})
	}
	if len(w.Deliveries) != MaxWebhookDeliveries || w.Deliveries[0].Detail != string(rune('a'+(MaxWebhookDeliveries+4)%26)) {
		t.Fatalf("history must keep the newest %d first: %d", MaxWebhookDeliveries, len(w.Deliveries))
	}
}
