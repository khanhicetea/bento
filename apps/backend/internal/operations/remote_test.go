package operations

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/remoteaccess"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

func TestMain(m *testing.M) {
	remoteaccess.MaybeRunTestChild()
	os.Exit(m.Run())
}

// newRemoteHarness runs remote-access children as the network-free test
// child of this binary.
func newRemoteHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarnessWith(t, func(d *Deps) {
		m := remoteaccess.NewManager(d.Layout, "/proc/self/exe", domain.TailcatUID, domain.TailcatUID, nil)
		m.Args = []string{remoteaccess.TestChildArg}
		d.Remote = m
	})
	t.Cleanup(h.c.Remote.StopAll)
	return h
}

func TestServiceRemoteOnRotateOff(t *testing.T) {
	h := newRemoteHarness(t)
	ctx := t.Context()
	svc := newMySQLService(t, h)

	op := h.mustSucceed(h.c.SetServiceRemote(ctx, svc.Name, RemoteOn, ""))
	if strings.Contains(string(op.Result), "tc") && strings.Contains(string(op.Result), "address") {
		t.Fatalf("the operation result carries the address: %s", op.Result)
	}
	st := h.c.Remote.Status(svc.Name)
	if st.State != remoteaccess.StateListening || st.Address == "" {
		t.Fatalf("after on: %+v", st)
	}
	key, err := remoteaccess.ReadKey(h.layout, svc.Name)
	if err != nil || key.Generation != 0 || key.RegionID != remoteaccess.StubRegion {
		t.Fatalf("key %+v %v", key, err)
	}
	// The target is the engine port of the service.
	h.c.Remote.Stop(svc.Name)
	h.mustSucceed(h.c.SetServiceRemote(ctx, svc.Name, RemoteOn, ""))
	if again := h.c.Remote.Status(svc.Name); again.Address != st.Address {
		t.Fatal("turning remote access on again changed the address")
	}

	h.mustSucceed(h.c.SetServiceRemote(ctx, svc.Name, RemoteRotate, ""))
	rotated := h.c.Remote.Status(svc.Name)
	if rotated.State != remoteaccess.StateListening || rotated.Address == st.Address {
		t.Fatalf("after rotate: %s, same address %v", rotated.State, rotated.Address == st.Address)
	}
	row, _ := store.GetService(ctx, h.store.DB(), svc.Name)
	if !row.Remote.Enabled || row.Remote.Generation != 1 {
		t.Fatalf("intent after rotate %+v", row.Remote)
	}

	h.mustSucceed(h.c.SetServiceRemote(ctx, svc.Name, RemoteOff, ""))
	if off := h.c.Remote.Status(svc.Name); off.State != remoteaccess.StateOff {
		t.Fatalf("after off: %+v", off)
	}
	if _, err := os.Stat(h.layout.RemoteSocket(svc.Name)); !os.IsNotExist(err) {
		t.Fatalf("socket left after off: %v", err)
	}
	// Off keeps the key: turning on again restores the same address.
	if _, err := remoteaccess.ReadKey(h.layout, svc.Name); err != nil {
		t.Fatalf("off removed the key: %v", err)
	}
	if _, err := h.c.SetServiceRemote(ctx, svc.Name, RemoteRotate, ""); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("rotate while off: %v", err)
	}
	var verr domain.ValidationErrors
	if _, err := h.c.SetServiceRemote(ctx, svc.Name, "open", ""); !errors.As(err, &verr) {
		t.Fatalf("unknown action: %v", err)
	}
	if _, err := h.c.SetServiceRemote(ctx, "nosuch", RemoteOn, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown service: %v", err)
	}
}

// Each external effect fails cleanly: an unreadable key is never replaced,
// and a child that cannot start fails the operation while intent stays on.
func TestServiceRemoteFaults(t *testing.T) {
	h := newRemoteHarness(t)
	ctx := t.Context()
	svc := newMySQLService(t, h)
	h.mustSucceed(h.c.SetServiceRemote(ctx, svc.Name, RemoteOn, ""))
	h.c.Remote.Stop(svc.Name)

	path := h.layout.RemoteKey(svc.Name)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	got := h.wait(mustOp(h.c.SetServiceRemote(ctx, svc.Name, RemoteOn, "")))
	if got.State != store.OpFailed || got.ErrorCode != "remote-key-invalid" {
		t.Fatalf("readable key: %s %s %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Fatal("the refused key file was touched")
	}
	os.Chmod(path, 0o600)

	h.c.Remote.Executable = "/nonexistent/bento"
	got = h.wait(mustOp(h.c.SetServiceRemote(ctx, svc.Name, RemoteOn, "")))
	if got.State != store.OpFailed || got.ErrorCode != "remote-access-failed" {
		t.Fatalf("child start failure: %s %s %s", got.State, got.ErrorCode, got.ErrorMessage)
	}
	if st := h.c.Remote.Status(svc.Name); st.State != remoteaccess.StateFailed && st.State != remoteaccess.StateStarting {
		t.Fatalf("a failing server must stay supervised: %+v", st)
	}
	row, _ := store.GetService(ctx, h.store.DB(), svc.Name)
	if !row.Remote.Enabled {
		t.Fatal("a failed start turned the intent off")
	}
}

// At backend start a missing key is not replaced (the address would move
// silently), while a persisted rotate whose operation never ran is honored.
func TestStartRemoteAccessAtBoot(t *testing.T) {
	h := newRemoteHarness(t)
	ctx := t.Context()
	svc := newMySQLService(t, h)
	if _, err := store.SetServiceRemote(ctx, h.store.DB(), svc.Name, true, false); err != nil {
		t.Fatal(err)
	}
	h.c.StartRemoteAccess(ctx)
	if st := h.c.Remote.Status(svc.Name); st.State != remoteaccess.StateFailed ||
		!strings.Contains(st.Message, "missing") {
		t.Fatalf("missing key: %+v", st)
	}
	if _, err := os.Stat(h.layout.RemoteKey(svc.Name)); !os.IsNotExist(err) {
		t.Fatal("boot created a key")
	}

	h.mustSucceed(h.c.SetServiceRemote(ctx, svc.Name, RemoteOn, ""))
	first := h.c.Remote.Status(svc.Name).Address
	h.c.Remote.StopAll()
	// A rotate accepted just before a crash: intent is generation 1, the
	// key is still generation 0.
	if _, err := store.SetServiceRemote(ctx, h.store.DB(), svc.Name, true, true); err != nil {
		t.Fatal(err)
	}
	h.c.StartRemoteAccess(ctx)
	st := h.c.Remote.WaitSettled(ctx, svc.Name)
	if st.State != remoteaccess.StateListening || st.Address == first {
		t.Fatalf("pending rotate at boot: %s, same address %v", st.State, st.Address == first)
	}
}

func TestServiceEndpointChecksOwnership(t *testing.T) {
	h := newRemoteHarness(t)
	ctx := t.Context()
	svc := newMySQLService(t, h)
	if _, err := h.c.ServiceEndpoint(ctx, svc.Name); err == nil {
		t.Fatal("an endpoint was returned while remote access is off")
	}
	if _, err := store.SetServiceRemote(ctx, h.store.DB(), svc.Name, true, false); err != nil {
		t.Fatal(err)
	}
	ep, err := h.c.ServiceEndpoint(ctx, svc.Name)
	if err != nil || !strings.HasSuffix(ep, ":3306") {
		t.Fatalf("endpoint %q %v", ep, err)
	}
	ins, _ := h.c.Engine.Inspect(ctx, h.c.Names.ServiceContainer(svc.Name))
	labels := map[string]string{}
	for k, v := range ins.Config.Labels {
		labels[k] = v
	}
	labels[runtime.LabelStackID] = "sother"
	h.fake.Containers[ins.ID].Spec.Config.Labels = labels
	if _, err := h.c.ServiceEndpoint(ctx, svc.Name); err == nil {
		t.Fatal("a container of another stack was used")
	}
}

func TestBindingLoginIsJournaledWithoutPassword(t *testing.T) {
	h := newRemoteHarness(t)
	ctx := t.Context()
	app := h.createApp("shop")
	b := domain.Binding{ID: "bpg", AppID: app.ID, Engine: domain.EnginePostgres, Service: "pg17", Username: "ushop",
		Password: "Sup3rSecretBindingPw"}
	if err := h.c.RecordLoginReveal(ctx, app, b, "session"); err != nil {
		t.Fatal(err)
	}
	ops, err := store.ListOperations(ctx, h.store.DB(), store.OpFilter{TargetID: app.ID})
	if err != nil {
		t.Fatal(err)
	}
	var found *store.Operation
	for i := range ops {
		if ops[i].Kind == KindBindingLogin {
			found = &ops[i]
		}
	}
	if found == nil || found.State != store.OpSucceeded {
		t.Fatalf("no finished %s record: %+v", KindBindingLogin, ops)
	}
	events, _ := store.ListEvents(ctx, h.store.DB(), found.ID, 0)
	all := string(found.Request) + string(found.Result)
	for _, e := range events {
		all += e.Message
	}
	if strings.Contains(all, b.Password) || len(events) != 1 {
		t.Fatalf("journal %q (%d events)", all, len(events))
	}
}

func mustOp(op store.Operation, err error) store.Operation {
	if err != nil {
		panic(err)
	}
	return op
}
