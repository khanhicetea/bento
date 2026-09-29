package operations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

func TestDBAdminLifecycleAndHardening(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.mustSucceed(h.c.SetDBAdmin(ctx, true, "dbadmin-on-1"))
	ins, err := h.c.Engine.Inspect(ctx, h.c.Names.DBAdminContainer())
	if err != nil || ins == nil || !ins.State.Running || !h.c.Names.OwnedBy(ins.Config.Labels, runtime.RoleDBAdmin, "") {
		t.Fatalf("dbadmin container: %v %+v", err, ins)
	}
	hc := ins.HostConfig
	if !hc.ReadonlyRootfs || len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" || len(hc.PortBindings) != 0 || hc.Privileged {
		t.Fatalf("container is not hardened: %+v", hc)
	}
	if ins.Config.User != "100:101" || len(ins.Config.Env) != 1 {
		t.Fatalf("user/env: %q %v", ins.Config.User, ins.Config.Env)
	}
	if len(hc.Mounts) != 1 || !hc.Mounts[0].ReadOnly || hc.Mounts[0].Source != h.layout.DBAdminDir() {
		t.Fatalf("mounts: %+v", hc.Mounts)
	}
	nets := ins.NetworkSettings.Networks
	if len(nets) != 1 || nets[h.c.Names.DataNetwork()] == nil {
		t.Fatalf("dbadmin must join only the data network: %v", nets)
	}
	tok, err := h.c.DBAdminToken()
	if err != nil || len(tok) < 32 {
		t.Fatalf("gateway token: %q %v", tok, err)
	}
	for _, name := range []string{"gateway-token", "router.php"} {
		fi, err := os.Stat(filepath.Join(h.layout.DBAdminDir(), name))
		if err != nil || fi.Mode().Perm() != 0o440 {
			t.Fatalf("%s: %v %v", name, fi.Mode(), err)
		}
	}
	if ep, err := h.c.DBAdminEndpoint(ctx); err != nil || ep == "" {
		t.Fatalf("endpoint: %q %v", ep, err)
	}
	if drift, err := h.c.DBAdminDrift(ctx); err != nil || drift {
		t.Fatalf("fresh container drifted: %v %v", drift, err)
	}

	// Re-applying keeps the container and the token.
	h.mustSucceed(h.c.SetDBAdmin(ctx, true, "dbadmin-on-2"))
	if again, _ := h.c.Engine.Inspect(ctx, h.c.Names.DBAdminContainer()); again == nil || again.ID != ins.ID {
		t.Fatal("unchanged dbadmin was recreated")
	}
	if tok2, _ := h.c.DBAdminToken(); tok2 != tok {
		t.Fatal("gateway token rotated on re-apply")
	}

	h.mustSucceed(h.c.SetDBAdmin(ctx, false, "dbadmin-off-1"))
	if gone, _ := h.c.Engine.Inspect(ctx, h.c.Names.DBAdminContainer()); gone != nil {
		t.Fatal("disabled dbadmin container still exists")
	}
}
