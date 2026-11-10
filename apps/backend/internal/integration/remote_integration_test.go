package integration

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/logger"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/remoteaccess"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestMain(m *testing.M) {
	// The controller's Tailcat children are this test binary running the
	// real child (remoteaccess.RunChild).
	remoteaccess.MaybeRunTestChild()
	os.Exit(m.Run())
}

// TestIntegrationRemoteAccessPostgres turns remote access on for a real
// PostgreSQL service, connects through the public Tailcat relays with the
// tailcat client library, and logs in as the app's binding user. It needs
// internet access (HTTPS and UDP to the relays).
func TestIntegrationRemoteAccessPostgres(t *testing.T) {
	e := setup(t)
	e.c.Remote.Args = []string{remoteaccess.TestRealChildArg}
	t.Cleanup(e.c.Remote.StopAll)
	ctx := context.Background()
	svc, op, err := e.c.CreateService(ctx, domain.EnginePostgres, "17", "")
	e.wait(op, err)
	rt := domain.Runtime{Kind: domain.RuntimeHTTP,
		HTTP: &domain.HTTPRuntime{Toolchain: "node", Version: "24", Argv: []string{"node", "s.js"}, Port: 3000}}
	app, op, err := e.c.CreateApp(ctx, operations.CreateAppInput{Slug: "remote", Runtime: rt,
		Bindings: []operations.BindingRequest{{Engine: domain.EnginePostgres, Service: svc.Name}}}, "")
	e.wait(op, err)
	app, _ = store.GetApp(ctx, e.s.DB(), app.ID)
	b := app.Bindings[0]
	if len(b.Databases) == 0 || b.Password == "" {
		t.Fatalf("binding not provisioned: %+v", b.Databases)
	}

	e.wait(e.c.SetServiceRemote(ctx, svc.Name, operations.RemoteOn, ""))
	st := e.c.Remote.Status(svc.Name)
	if st.State != remoteaccess.StateListening || !strings.HasPrefix(st.Address, "tc") {
		t.Fatalf("remote access: %+v", st)
	}
	cl := &tailcat.Client{Server: tailcat.Addr(st.Address), Logf: logger.Discard}
	defer cl.Close()
	dial := func() net.Conn {
		t.Helper()
		dctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		c, err := cl.DialTCPPort(dctx, 5432)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(time.Minute))
		return c
	}

	c := dial()
	user, err := pgLogin(c, b.Username, b.Password, b.Databases[0], "SELECT current_user")
	c.Close()
	if err != nil || user != b.Username {
		t.Fatalf("login through the tunnel: %q %v", user, err)
	}
	c = dial()
	if _, err := pgLogin(c, b.Username, "not-the-password", b.Databases[0], "SELECT 1"); err == nil {
		t.Fatal("a wrong password logged in")
	}
	c.Close()
	if n := e.c.Remote.Status(svc.Name).Connections; n > 1 {
		t.Fatalf("closed tunnels still counted: %d", n)
	}

	// Rotate: the old address stops working, the new one works.
	e.wait(e.c.SetServiceRemote(ctx, svc.Name, operations.RemoteRotate, ""))
	rotated := e.c.Remote.Status(svc.Name)
	if rotated.State != remoteaccess.StateListening || rotated.Address == st.Address {
		t.Fatalf("rotate: %s, same address %v", rotated.State, rotated.Address == st.Address)
	}
	old := &tailcat.Client{Server: tailcat.Addr(st.Address), Logf: logger.Discard}
	defer old.Close()
	octx, ocancel := context.WithTimeout(ctx, 20*time.Second)
	defer ocancel()
	if oc, err := old.DialTCPPort(octx, 5432); err == nil {
		oc.SetDeadline(time.Now().Add(15 * time.Second))
		if _, err := pgLogin(oc, b.Username, b.Password, b.Databases[0], "SELECT 1"); err == nil {
			t.Fatal("the rotated-away address still reaches the database")
		}
		oc.Close()
	}
	fresh := &tailcat.Client{Server: tailcat.Addr(rotated.Address), Logf: logger.Discard}
	defer fresh.Close()
	fctx, fcancel := context.WithTimeout(ctx, time.Minute)
	defer fcancel()
	fc, err := fresh.DialTCPPort(fctx, 5432)
	if err != nil {
		t.Fatal(err)
	}
	fc.SetDeadline(time.Now().Add(time.Minute))
	if user, err := pgLogin(fc, b.Username, b.Password, b.Databases[0], "SELECT current_user"); err != nil || user != b.Username {
		t.Fatalf("login on the new address: %q %v", user, err)
	}
	fc.Close()

	e.wait(e.c.SetServiceRemote(ctx, svc.Name, operations.RemoteOff, ""))
	if off := e.c.Remote.Status(svc.Name); off.State != remoteaccess.StateOff {
		t.Fatalf("off: %+v", off)
	}
}

// pgLogin speaks just enough of the PostgreSQL protocol to authenticate
// (SCRAM-SHA-256, MD5 is refused, cleartext for completeness) and run one
// query, returning its first column.
func pgLogin(c net.Conn, user, password, database, query string) (string, error) {
	r := bufio.NewReader(c)
	var startup []byte
	startup = binary.BigEndian.AppendUint32(startup, 196608)
	for _, kv := range []string{"user", user, "database", database} {
		startup = append(append(startup, kv...), 0)
	}
	startup = append(startup, 0)
	if _, err := c.Write(binary.BigEndian.AppendUint32(nil, uint32(len(startup)+4))); err != nil {
		return "", err
	}
	if _, err := c.Write(startup); err != nil {
		return "", err
	}
	send := func(typ byte, body []byte) error {
		msg := append([]byte{typ}, binary.BigEndian.AppendUint32(nil, uint32(len(body)+4))...)
		_, err := c.Write(append(msg, body...))
		return err
	}
	var nonce, clientFirstBare, serverSig string
	for {
		typ, body, err := pgRead(r)
		if err != nil {
			return "", err
		}
		switch typ {
		case 'E':
			return "", fmt.Errorf("server error: %s", pgErrorText(body))
		case 'R':
			code := binary.BigEndian.Uint32(body)
			rest := body[4:]
			switch code {
			case 0: // ok
			case 3:
				if err := send('p', append([]byte(password), 0)); err != nil {
					return "", err
				}
			case 10: // SASL
				if !strings.Contains(string(rest), "SCRAM-SHA-256") {
					return "", errors.New("no SCRAM-SHA-256")
				}
				nb := make([]byte, 18)
				rand.Read(nb)
				nonce = base64.StdEncoding.EncodeToString(nb)
				clientFirstBare = "n=,r=" + nonce
				first := "n,," + clientFirstBare
				msg := append([]byte("SCRAM-SHA-256\x00"), binary.BigEndian.AppendUint32(nil, uint32(len(first)))...)
				if err := send('p', append(msg, first...)); err != nil {
					return "", err
				}
			case 11: // SASL continue
				serverFirst := string(rest)
				attrs := scramAttrs(serverFirst)
				if !strings.HasPrefix(attrs["r"], nonce) {
					return "", errors.New("bad server nonce")
				}
				salt, _ := base64.StdEncoding.DecodeString(attrs["s"])
				var iter int
				fmt.Sscanf(attrs["i"], "%d", &iter)
				salted, err := pbkdf2.Key(sha256.New, password, salt, iter, 32)
				if err != nil {
					return "", err
				}
				clientKey := hmacSum(salted, "Client Key")
				stored := sha256.Sum256(clientKey)
				finalNoProof := "c=biws,r=" + attrs["r"]
				authMsg := clientFirstBare + "," + serverFirst + "," + finalNoProof
				sig := hmacSum(stored[:], authMsg)
				proof := make([]byte, len(clientKey))
				for i := range clientKey {
					proof[i] = clientKey[i] ^ sig[i]
				}
				serverSig = base64.StdEncoding.EncodeToString(hmacSum(hmacSum(salted, "Server Key"), authMsg))
				if err := send('p', []byte(finalNoProof+",p="+base64.StdEncoding.EncodeToString(proof))); err != nil {
					return "", err
				}
			case 12: // SASL final
				if scramAttrs(string(rest))["v"] != serverSig {
					return "", errors.New("server signature mismatch")
				}
			default:
				return "", fmt.Errorf("unsupported authentication %d", code)
			}
		case 'Z':
			if query == "" {
				return "", nil
			}
			if err := send('Q', append([]byte(query), 0)); err != nil {
				return "", err
			}
			var value string
			for {
				typ, body, err := pgRead(r)
				if err != nil {
					return "", err
				}
				switch typ {
				case 'E':
					return "", fmt.Errorf("query error: %s", pgErrorText(body))
				case 'D':
					if binary.BigEndian.Uint16(body) > 0 {
						n := int32(binary.BigEndian.Uint32(body[2:]))
						if n > 0 {
							value = string(body[6 : 6+n])
						}
					}
				case 'Z':
					return value, nil
				}
			}
		}
	}
}

func pgRead(r *bufio.Reader) (byte, []byte, error) {
	typ, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return 0, nil, err
	}
	if n < 4 || n > 1<<20 {
		return 0, nil, fmt.Errorf("bad message length %d", n)
	}
	body := make([]byte, n-4)
	_, err = io.ReadFull(r, body)
	return typ, body, err
}

func pgErrorText(body []byte) string {
	for _, f := range strings.Split(string(body), "\x00") {
		if strings.HasPrefix(f, "M") {
			return f[1:]
		}
	}
	return "unknown"
}

func scramAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(part, "="); ok {
			out[k] = v
		}
	}
	return out
}

func hmacSum(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}
