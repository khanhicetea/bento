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
	for range maxClientHashes {
		done, ok := l.begin("uid:0")
		if !ok {
			t.Fatal("an attempt under the concurrency cap was refused")
		}
		dones = append(dones, done)
	}
	if _, ok := l.begin("uid:0"); ok {
		t.Fatal("an attempt beyond maxClientHashes was admitted")
	}
	dones[0](true)
	dones[0](false) // a second call is ignored
	if b := l.clients["uid:0"]; l.pending != maxClientHashes-1 || b.pending != maxClientHashes-1 || len(b.failures) != 0 {
		t.Fatalf("after one success: pending=%d client=%+v", l.pending, b)
	}
	dones[1](false)
	for i := 1; i < maxLoginFailures; i++ {
		done, ok := l.begin("uid:0")
		if !ok {
			t.Fatalf("attempt %d refused before the budget was spent", i+1)
		}
		done(false)
	}
	if _, ok := l.begin("uid:0"); ok {
		t.Fatal("attempt admitted after maxLoginFailures failures")
	}
}

func TestLoginLimiterReservesBudgetForInFlightAttempts(t *testing.T) {
	var l loginLimiter
	for range maxLoginFailures - 1 {
		done, _ := l.begin("uid:0")
		done(false)
	}
	done, ok := l.begin("uid:0")
	if !ok {
		t.Fatal("last attempt of the budget refused")
	}
	if _, ok := l.begin("uid:0"); ok {
		t.Fatal("an attempt was admitted while the last budget slot was in flight")
	}
	done(true)
	if _, ok := l.begin("uid:0"); !ok {
		t.Fatal("a successful attempt did not return its budget slot")
	}
}

// One local user spending its budget does not rate limit another (the
// operator's browser behind an SSH tunnel); hashes in flight stay bounded in
// all.
func TestLoginLimiterBudgetsArePerClient(t *testing.T) {
	var l loginLimiter
	for range maxLoginFailures {
		done, ok := l.begin("uid:1001")
		if !ok {
			t.Fatal("refused before the budget was spent")
		}
		done(false)
	}
	if _, ok := l.begin("uid:1001"); ok {
		t.Fatal("the guessing client must be refused")
	}
	done, ok := l.begin("uid:0")
	if !ok {
		t.Fatal("another client must keep its own budget")
	}
	done(true)
	var dones []func(bool)
	for _, c := range []string{"a", "a", "b", "b"} {
		d, ok := l.begin(c)
		if !ok {
			t.Fatalf("client %s refused under the caps", c)
		}
		dones = append(dones, d)
	}
	if _, ok := l.begin("c"); ok {
		t.Fatal("more than maxConcurrentHashes checks in flight")
	}
	for _, d := range dones {
		d(true)
	}
}

func TestTCPOwnerMatchesTheClientEnd(t *testing.T) {
	table := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1E64 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1001 1 0
   1: 0100007F:1E64 0100007F:D431 01 00000000:00000000 00:00000000 00000000     0        0 1002 1 0
   2: 0100007F:D431 0100007F:1E64 01 00000000:00000000 00:00000000 00000000  1000        0 1003 1 0
`
	// 0x1E64 = 7780 (the listener), 0xD431 = 54321 (the client's port).
	if uid, ok := tcpOwner(table, 54321, 7780); !ok || uid != "1000" {
		t.Fatalf("owner %q %v", uid, ok)
	}
	if _, ok := tcpOwner(table, 40000, 7780); ok {
		t.Fatal("an unknown port matched")
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
