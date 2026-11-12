package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/reconcile"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// UpgradePlan is what this binary would do to a stack it is installed for:
// the state schema migration, and what the reconciler would build, replace
// and reload once it serves the stack. It is computed by this build's own
// code against a migrated copy of the state and a read-only Docker engine.
type UpgradePlan struct {
	Version string `json:"version"`
	Stack   string `json:"stack"`
	Root    string `json:"root"`
	Schema  struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
		// Migrated is true when the copy was migrated (From < To).
		Migrated bool `json:"migrated"`
	} `json:"schema"`
	Docker   string              `json:"docker"`
	Changes  []reconcile.Change  `json:"changes"`
	Problems []reconcile.Problem `json:"problems"`
	Notes    []string            `json:"notes"`
}

// ErrUpgradeBlocked means this binary must not be installed for the stack.
var ErrUpgradeBlocked = errors.New("upgrade blocked")

// PlanUpgrade computes the UpgradePlan of this build for the stack at
// layout. It never writes the stack root, its state database or Docker: the
// database is copied (consistently, even while a backend serves it) into a
// private temporary directory, migrated there, and read with a controller
// that is never started and whose engine refuses every mutation. It takes no
// stack lock, so the running backend keeps serving.
func PlanUpgrade(ctx context.Context, layout platform.Layout, version string, utilsAppsPort int) (UpgradePlan, error) {
	plan := UpgradePlan{Version: version, Root: layout.Root, Changes: []reconcile.Change{},
		Problems: []reconcile.Problem{}, Notes: []string{}}
	if _, err := os.Stat(layout.Database()); err != nil {
		return plan, fmt.Errorf("no Bento state at %s: %w", layout.Database(), err)
	}
	tmp, err := os.MkdirTemp("", "bento-upgrade-plan-")
	if err != nil {
		return plan, err
	}
	defer os.RemoveAll(tmp)
	cp := filepath.Join(tmp, "bento.db")
	appID, from, err := store.CopyFile(ctx, layout.Database(), cp)
	if err != nil {
		return plan, err
	}
	plan.Schema.From, plan.Schema.To = from, store.SchemaVersion
	switch {
	case appID != store.ApplicationID:
		return plan, fmt.Errorf("%w: %s is not a Bento state database", ErrUpgradeBlocked, layout.Database())
	case from > store.SchemaVersion:
		return plan, fmt.Errorf("%w: the stack is on state schema %d, newer than this build's %d; this binary is "+
			"older than the one that last served the stack and would refuse to open it", ErrUpgradeBlocked, from,
			store.SchemaVersion)
	case from < store.MigratableFromVersion:
		return plan, fmt.Errorf("%w: the stack is on state schema %d; this build upgrades schema %d and later only",
			ErrUpgradeBlocked, from, store.MigratableFromVersion)
	}
	st, err := store.Open(cp)
	if err != nil {
		return plan, fmt.Errorf("%w: migrating a copy of the state failed, so the real one would too: %v",
			ErrUpgradeBlocked, err)
	}
	defer st.Close()
	plan.Schema.Migrated = from < store.SchemaVersion
	sdk, err := docker.NewSDK()
	if err != nil {
		return plan, err
	}
	defer sdk.Close()
	engine := docker.ReadOnly{E: sdk}
	v, err := engine.Version(ctx)
	if err != nil {
		return plan, fmt.Errorf("%w: docker: %v", ErrUpgradeBlocked, err)
	}
	plan.Docker = fmt.Sprintf("Engine %s, API %s (needs %s+)", v.ServerVersion, v.APIVersion, docker.MinAPIVersion)
	ctrl, err := operations.NewController(operations.Deps{
		Store:         st,
		Engine:        engine,
		Layout:        layout,
		HostIDs:       platform.FileHostIDs{},
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		UtilsAppsPort: utilsAppsPort,
	})
	if err != nil {
		return plan, unreadable(err)
	}
	plan.Stack = ctrl.Stack.Name
	p, err := reconcile.PlanPass(ctx, ctrl)
	if err != nil {
		return plan, unreadable(err)
	}
	plan.Changes, plan.Problems = p.Changes, p.Problems
	active, err := store.ListOperations(ctx, st.DB(), store.OpFilter{States: []store.OpState{store.OpQueued,
		store.OpRunning}})
	if err != nil {
		return plan, err
	}
	if len(active) > 0 {
		plan.Notes = append(plan.Notes, fmt.Sprintf("%d operation(s) queued or running; a restart drains the running "+
			"one for up to 90 s and marks anything cut short as interrupted", len(active)))
	}
	return plan, nil
}

// unreadable reports a failure to read the migrated copy. PlanPass reports
// Docker failures per target as problems, so what reaches here is the state
// itself: this build could not serve it.
func unreadable(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: this build could not read the stack's state (%v); it would fail the same way serving it",
		ErrUpgradeBlocked, err)
}

func printUpgradePlan(w io.Writer, p UpgradePlan) {
	fmt.Fprintf(w, "bento %s upgrade plan for stack %s (%s)\n", p.Version, p.Stack, p.Root)
	fmt.Fprintln(w, "Nothing was changed: this ran on a copy of the state and a read-only Docker connection.")
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	switch {
	case p.Schema.Migrated:
		fmt.Fprintf(tw, "SCHEMA\t%d → %d\tmigration ran on a copy: ok; the first start copies the state to "+
			"state-backups/ before migrating, and an older binary cannot open the result\n", p.Schema.From, p.Schema.To)
	default:
		fmt.Fprintf(tw, "SCHEMA\t%d\tno change; the previous binary can still open the state\n", p.Schema.From)
	}
	fmt.Fprintf(tw, "DOCKER\tok\t%s\n", p.Docker)
	label := map[string]string{"image": "IMAGES", "app": "APPS", "service": "SERVICES", "edge": "EDGE",
		"tunnel": "TUNNEL", "dbadmin": "DB ADMIN"}
	for _, kind := range []string{"image", "app", "service", "edge", "tunnel", "dbadmin"} {
		first := true
		for _, c := range p.Changes {
			if c.Kind != kind {
				continue
			}
			head := ""
			if first {
				head, first = label[kind], false
			}
			fmt.Fprintf(tw, "%s\t%s %s\t%s\n", head, c.Action, c.Target, c.Detail)
		}
	}
	for i, pr := range p.Problems {
		head := ""
		if i == 0 {
			head = "UNKNOWN"
		}
		fmt.Fprintf(tw, "%s\t%s %s\t%s\n", head, pr.Kind, pr.Target, pr.Error)
	}
	for i, n := range p.Notes {
		head := ""
		if i == 0 {
			head = "NOTE"
		}
		fmt.Fprintf(tw, "%s\t\t%s\n", head, n)
	}
	tw.Flush()
	fmt.Fprintln(w)
	if len(p.Changes) == 0 && len(p.Problems) == 0 {
		fmt.Fprintln(w, "Installing this binary only restarts the backend; apps, the edge and databases keep running.")
		return
	}
	var replaced, builds int
	for _, c := range p.Changes {
		switch {
		case c.Kind == "image":
			builds++
		case c.Kind == "app" && (c.Action == reconcile.ActionReplace || c.Action == reconcile.ActionReload):
			replaced++
		}
	}
	if builds > 0 {
		fmt.Fprintf(w, "After the restart, %d image(s) build first; running apps keep serving on their current images.\n",
			builds)
	}
	if replaced > 0 {
		fmt.Fprintf(w, "Then %d app(s) change one at a time: a replacement costs a few seconds of 502, a reload none.\n",
			replaced)
	}
	if len(p.Problems) > 0 {
		fmt.Fprintln(w, "Some targets could not be checked (UNKNOWN); the reconciler decides them after the restart.")
	}
	fmt.Fprintln(w, "This is a forecast: the backend decides again when it starts.")
}

func (r *runner) upgrade(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "plan" {
		return errors.New("usage: bento upgrade plan [--utils-listen ADDR|apps:PORT|off]... [--json]")
	}
	fs, rest := sub("upgrade plan", args[1:])
	var utils multiFlag
	fs.Var(&utils, "utils-listen", "the backend's --utils-listen values, if its unit sets them (repeatable)")
	asJSON := fs.Bool("json", r.json, "machine-readable output")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("upgrade plan: unexpected argument %q", fs.Arg(0))
	}
	if os.Geteuid() != 0 {
		return errors.New("upgrade plan reads the stack state and Docker: run it as root")
	}
	if len(utils) == 0 {
		utils = DefaultUtilsListen
	}
	addrs, err := ParseUtilsListen(utils)
	if err != nil {
		return err
	}
	p, err := PlanUpgrade(ctx, r.layout, r.version, appsPort(addrs))
	if err != nil {
		return err
	}
	if *asJSON {
		r.printJSON(p)
		return nil
	}
	printUpgradePlan(r.out, p)
	return nil
}
