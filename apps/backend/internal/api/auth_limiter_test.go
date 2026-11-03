package api

import (
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestLoginLimiterCountsAttemptsBeforeHashing(t *testing.T) {
	var l loginLimiter
	var dones []func(bool)
	for range maxConcurrentHashes {
		done, ok := l.begin()
		if !ok {
			t.Fatal("an attempt under the concurrency cap was refused")
		}
		dones = append(dones, done)
	}
	if _, ok := l.begin(); ok {
		t.Fatal("an attempt beyond maxConcurrentHashes was admitted")
	}
	dones[0](true)
	dones[0](false) // a second call is ignored
	if l.pending != maxConcurrentHashes-1 || len(l.failures) != 0 {
		t.Fatalf("after one success: pending=%d failures=%d", l.pending, len(l.failures))
	}
	dones[1](false)
	for i := 1; i < maxLoginFailures; i++ {
		done, ok := l.begin()
		if !ok {
			t.Fatalf("attempt %d refused before the budget was spent", i+1)
		}
		done(false)
	}
	if _, ok := l.begin(); ok {
		t.Fatal("attempt admitted after maxLoginFailures failures")
	}
}

func TestLoginLimiterReservesBudgetForInFlightAttempts(t *testing.T) {
	var l loginLimiter
	for range maxLoginFailures - 1 {
		done, _ := l.begin()
		done(false)
	}
	done, ok := l.begin()
	if !ok {
		t.Fatal("last attempt of the budget refused")
	}
	if _, ok := l.begin(); ok {
		t.Fatal("an attempt was admitted while the last budget slot was in flight")
	}
	done(true)
	if _, ok := l.begin(); !ok {
		t.Fatal("a successful attempt did not return its budget slot")
	}
}

func TestLoginBurstIsBoundedAndCounted(t *testing.T) {
	c, _ := newServer(t)
	const n = 24
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			resp, _ := c.do("POST", "/api/v1/session", `{"password":"wrong password here"}`, map[string]string{"Origin": origin})
			codes[i] = resp.StatusCode
		})
	}
	wg.Wait()
	var unauthorized, limited int
	for _, code := range codes {
		switch code {
		case http.StatusUnauthorized:
			unauthorized++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("unexpected status %d", code)
		}
	}
	if unauthorized > maxLoginFailures || limited == 0 {
		t.Fatalf("burst of %d: %d hashed and rejected, %d rate limited", n, unauthorized, limited)
	}
	// The budget is spent: even the right password waits.
	resp, _ := c.do("POST", "/api/v1/session", `{"password":"`+password+`"}`, map[string]string{"Origin": origin})
	if unauthorized == maxLoginFailures && resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("login after a spent budget -> %d", resp.StatusCode)
	}
}

func TestPasswordHashUsesOWASPParametersAndUpgradesOldHashes(t *testing.T) {
	h := HashPassword("a long enough password")
	if !strings.HasPrefix(h, "argon2id$2$19456$1$") || needsRehash(h) || !verifyPassword(h, "a long enough password") {
		t.Fatalf("new hash: %s", h)
	}
	// A hash made with the parameters used before 2026-11-03.
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	old := "argon2id$3$65536$2$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
	if !verifyPassword(old, password) || !needsRehash(old) {
		t.Fatal("an old hash must verify and be marked for upgrade")
	}
	for _, bad := range []string{"argon2id$2$9999999$1$c2FsdA$a2V5", "argon2id$2$19456$0$c2FsdA$a2V5", "bcrypt$x"} {
		if verifyPassword(bad, password) {
			t.Errorf("%q verified", bad)
		}
	}

	c, hs := newServer(t)
	if err := store.PutSetting(t.Context(), hs.Store.DB(), authSetting, PasswordRecord{Hash: old}); err != nil {
		t.Fatal(err)
	}
	c.login()
	var rec PasswordRecord
	if _, err := store.GetSetting(t.Context(), hs.Store.DB(), authSetting, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Hash == old || needsRehash(rec.Hash) || !verifyPassword(rec.Hash, password) {
		t.Fatalf("login did not upgrade the hash: %s", rec.Hash)
	}
	resp, _ := c.do("GET", "/api/v1/apps", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal("upgrading the hash must keep the session")
	}
}
